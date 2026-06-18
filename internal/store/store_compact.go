package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ensureFileSize belegt die Datei unter path auf mindestens size Bytes vor.
// Strikt grow-only: ist die Datei bereits >= size, passiert nichts (eine schon
// größere Datenbank darf niemals verkleinert werden — das würde bbolt-Seiten
// abschneiden und die DB zerstören). os.Truncate erzeugt eine sparse-Datei;
// echte Blöcke werden erst beim Schreiben belegt — für das Ziel (große Mmap,
// keine Remaps) genügt das, da bbolt die Mmap unabhängig von belegten Blöcken
// dimensioniert.
func ensureFileSize(path string, size int64) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() >= size {
		return nil
	}
	return os.Truncate(path, size)
}

// Size liefert die aktuelle Größe der Datenbankdatei in Bytes.
func (s *Store) Size() (int64, error) {
	info, err := os.Stat(s.path())
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// DBStats beschreibt die Speicherbelegung der bbolt-Datei. Wichtig bei
// vorbelegter Datei (CLIO_DB_INITIAL_MB): FileBytes ist dann die – ggf. weit
// größere – Datei auf der Platte, während DataBytes (die bbolt-High-Water-Mark)
// den tatsächlich in Benutzung genommenen Umfang angibt. Der Füllgrad bezieht
// sich auf diese genutzte Region (Live-Daten vs. wiederverwendbare freie Seiten
// aus der Freelist), nicht auf die ggf. vorbelegte Datei — sonst läse er bei
// frisch vorbelegter, leerer DB fälschlich ~100 %. bbolt gibt belegten Platz nie
// von selbst frei; echtes Verkleinern geschieht nur via `cliostore compact`
// (ADR-015). Der Abstand DataBytes ↔ FileBytes ist der Remap-Headroom.
type DBStats struct {
	FileBytes   int64   `json:"fileBytes"`   // Dateigröße auf der Platte (os.Stat)
	DataBytes   int64   `json:"dataBytes"`   // tatsächlich genutzter Umfang (High-Water-Mark = pgid*pageSize)
	UsedBytes   int64   `json:"usedBytes"`   // DataBytes - FreeBytes (Nutzdaten + Strukturen)
	FreeBytes   int64   `json:"freeBytes"`   // in freien Seiten gebundener, wiederverwendbarer Platz
	FillPercent float64 `json:"fillPercent"` // UsedBytes / DataBytes * 100 (Live-Anteil der genutzten Region)
	FreePages   int     `json:"freePages"`   // Anzahl freier (+ pending) Seiten
	PageSize    int     `json:"pageSize"`    // Seitengröße in Bytes
}

// Stats liefert die Speicherbelegung der Datenbankdatei (siehe DBStats). Der
// genutzte Umfang ist die High-Water-Mark (tx.Size = pgid*pageSize), der freie
// Anteil stammt aus der bbolt-Freelist (FreeAlloc).
func (s *Store) Stats() (DBStats, error) {
	// Eine RLock über die ganze Berechnung: hält den db-Pointer stabil und
	// vermeidet verschachteltes RLock (daher os.Stat hier inline statt via Size()).
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()

	info, err := os.Stat(s.db.Path())
	if err != nil {
		return DBStats{}, err
	}
	fileBytes := info.Size()

	var dataBytes int64
	if err := s.db.View(func(tx *bolt.Tx) error {
		dataBytes = tx.Size()
		return nil
	}); err != nil {
		return DBStats{}, err
	}
	st := s.db.Stats()
	freeBytes := int64(st.FreeAlloc)
	if freeBytes > dataBytes {
		freeBytes = dataBytes
	}
	used := dataBytes - freeBytes
	var fill float64
	if dataBytes > 0 {
		fill = float64(used) / float64(dataBytes) * 100
	}
	return DBStats{
		FileBytes:   fileBytes,
		DataBytes:   dataBytes,
		UsedBytes:   used,
		FreeBytes:   freeBytes,
		FillPercent: fill,
		FreePages:   st.FreePageN + st.PendingPageN,
		PageSize:    s.pageSize,
	}, nil
}

// Compact schreibt die Datenbank unter path defragmentiert in eine temporäre
// Datei und ersetzt das Original atomar. Da die DB dabei exklusiv gelesen wird,
// schlägt der Aufruf fehl, wenn eine laufende Instanz die Datei hält
// (Datei-Lock) — Kompaktierung ist offline auszuführen.
//
// Events bleiben unverändert: Kompaktierung defragmentiert nur die bbolt-Datei,
// sie löscht oder ändert keine Events (die Hash-Kette bleibt gültig).
func Compact(path string) (oldSize, newSize int64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, fmt.Errorf("db finden: %w", err)
	}
	oldSize = info.Size()

	src, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, ReadOnly: true})
	if err != nil {
		return 0, 0, fmt.Errorf("db öffnen (läuft eine instanz?): %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".clio-compact-*")
	if err != nil {
		_ = src.Close()
		return 0, 0, err
	}
	tmpName := tmp.Name()
	_ = tmp.Close() // bbolt öffnet die Datei selbst

	cleanup := func() { _ = os.Remove(tmpName) }

	dst, err := bolt.Open(tmpName, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		_ = src.Close()
		cleanup()
		return 0, 0, err
	}

	if err := bolt.Compact(dst, src, 0); err != nil {
		_ = dst.Close()
		_ = src.Close()
		cleanup()
		return 0, 0, fmt.Errorf("kompaktieren: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = src.Close()
		cleanup()
		return 0, 0, err
	}
	if err := src.Close(); err != nil {
		cleanup()
		return 0, 0, err
	}

	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return 0, 0, fmt.Errorf("ersetzen: %w", err)
	}

	ni, err := os.Stat(path)
	if err != nil {
		return oldSize, 0, err
	}
	return oldSize, ni.Size(), nil
}

// CompactInPlace kompaktiert die laufende Datenbank online (ADR-015): unter dem
// exklusiven Reopen-Guard wird die DB geschlossen, mit Compact() defragmentiert
// (neue Datei, atomarer Rename) und unter denselben Optionen neu geöffnet. Für
// die Dauer (grob 1–2 s pro GB) blockieren Lese-/Schreibzugriffe — eine kurze,
// bewusste "Downtime". Events bleiben unverändert (die Hash-Kette gilt weiter).
//
// Die zurückgegebenen Größen entsprechen Compact(); newSize ist die Größe NACH
// dem Wiederöffnen, falls eine Vorbelegung (CLIO_DB_INITIAL_MB) die Datei direkt
// wieder auf die Mindestgröße bringt — daher wird sie zusätzlich gemeldet.
func (s *Store) CompactInPlace() (oldSize, newSize int64, err error) {
	rerr := s.reopen(func(path string) error {
		o, n, cerr := Compact(path)
		if cerr != nil {
			return cerr
		}
		oldSize, newSize = o, n
		return nil
	})
	if rerr != nil {
		return 0, 0, rerr
	}
	return oldSize, newSize, nil
}
