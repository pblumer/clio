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
	info, err := os.Stat(s.db.Path())
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// DBStats beschreibt die Speicherbelegung der bbolt-Datei: ihre Größe auf der
// Platte und – aus der bbolt-Freelist – wie viel davon belegt bzw. freier,
// wiederverwendbarer Platz ist. bbolt vergrößert die Datei bei Bedarf, gibt sie
// aber nie von selbst frei (freie Seiten werden zuerst wiederverwendet); echtes
// Verkleinern geschieht nur offline via `cliostore compact` (ADR-015). Der
// Füllgrad macht sichtbar, wie viel der Datei tatsächlich genutzt wird.
type DBStats struct {
	FileBytes   int64   `json:"fileBytes"`   // Dateigröße auf der Platte (os.Stat)
	UsedBytes   int64   `json:"usedBytes"`   // FileBytes - FreeBytes (Nutzdaten + Strukturen)
	FreeBytes   int64   `json:"freeBytes"`   // in freien Seiten gebundener, wiederverwendbarer Platz
	FillPercent float64 `json:"fillPercent"` // UsedBytes / FileBytes * 100
	FreePages   int     `json:"freePages"`   // Anzahl freier (+ pending) Seiten
	PageSize    int     `json:"pageSize"`    // Seitengröße in Bytes
}

// Stats liefert die Speicherbelegung der Datenbankdatei (siehe DBStats). Der
// freie Anteil wird aus der bbolt-Freelist ermittelt (FreeAlloc).
func (s *Store) Stats() (DBStats, error) {
	fileBytes, err := s.Size()
	if err != nil {
		return DBStats{}, err
	}
	st := s.db.Stats()
	freeBytes := int64(st.FreeAlloc)
	if freeBytes > fileBytes {
		freeBytes = fileBytes
	}
	used := fileBytes - freeBytes
	var fill float64
	if fileBytes > 0 {
		fill = float64(used) / float64(fileBytes) * 100
	}
	return DBStats{
		FileBytes:   fileBytes,
		UsedBytes:   used,
		FreeBytes:   freeBytes,
		FillPercent: fill,
		FreePages:   st.FreePageN + st.PendingPageN,
		PageSize:    s.db.Info().PageSize,
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
