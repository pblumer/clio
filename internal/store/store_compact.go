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

// Size liefert die Summe der Dateigrößen aller Partitionen in Bytes. Bei n=1 ist
// das genau die eine Datei (identisch zum bisherigen Verhalten).
func (s *Store) Size() (int64, error) {
	var total int64
	for _, sh := range s.shards {
		n, err := sh.size()
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
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
	// Über alle Partitionen aggregieren (Bytes/Seiten summieren, Füllgrad neu
	// berechnen). Bei n=1 ist das exakt die Statistik der einen Datei. PageSize ist
	// für alle Partitionen identisch (zentrale Partition als Referenz).
	var agg DBStats
	for _, sh := range s.shards {
		st, err := sh.stats()
		if err != nil {
			return DBStats{}, err
		}
		agg.FileBytes += st.FileBytes
		agg.DataBytes += st.DataBytes
		agg.UsedBytes += st.UsedBytes
		agg.FreeBytes += st.FreeBytes
		agg.FreePages += st.FreePages
		// PageSize aus der zentralen Partition (für alle identisch) — aus dem unter
		// RLock gelesenen Ergebnis, nicht über einen ungeschützten Feldzugriff.
		if sh == s.central {
			agg.PageSize = st.PageSize
		}
	}
	if agg.DataBytes > 0 {
		agg.FillPercent = float64(agg.UsedBytes) / float64(agg.DataBytes) * 100
	}
	return agg, nil
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
	// Jede Partition einzeln online kompaktieren (eigener Reopen-Guard je Datei,
	// ADR-037). Die gemeldeten Größen sind die Summen über alle Partitionen; bei
	// n=1 ist es genau die eine Datei. Der letzte Compact-Zeitpunkt wird je Partition
	// vermerkt; LastCompaction meldet den jüngsten.
	at := s.now().UTC()
	for _, sh := range s.shards {
		var o, n int64
		rerr := sh.reopen(func(path string) error {
			oo, nn, cerr := Compact(path)
			if cerr != nil {
				return cerr
			}
			o, n = oo, nn
			return nil
		})
		if rerr != nil {
			return 0, 0, rerr
		}
		oldSize += o
		newSize += n
		sh.lastCompactMu.Lock()
		sh.lastCompact = &CompactionInfo{At: at, OldBytes: o, NewBytes: n}
		sh.lastCompactMu.Unlock()
	}
	return oldSize, newSize, nil
}

// CompactionInfo beschreibt einen im laufenden Betrieb durchgeführten
// Online-Compact (CompactInPlace). OldBytes/NewBytes sind die Dateigrößen vor
// und nach der Defragmentierung; bei vorbelegter Datei wird NewBytes anschließend
// wieder auf die reservierte Größe gebracht (NewBytes spiegelt die echte
// Rückgewinnung, nicht die finale Dateigröße).
type CompactionInfo struct {
	At       time.Time `json:"at"`
	OldBytes int64     `json:"oldBytes"`
	NewBytes int64     `json:"newBytes"`
}

// LastCompaction liefert den letzten Online-Compact dieser Laufzeit. ok ist
// false, wenn in dieser Laufzeit noch keiner lief (offline-Compacts laufen in
// einem separaten Prozess und sind hier nicht sichtbar).
func (s *Store) LastCompaction() (CompactionInfo, bool) {
	var latest CompactionInfo
	found := false
	for _, sh := range s.shards {
		sh.lastCompactMu.Lock()
		lc := sh.lastCompact
		sh.lastCompactMu.Unlock()
		if lc != nil && (!found || lc.At.After(latest.At)) {
			latest = *lc
			found = true
		}
	}
	return latest, found
}
