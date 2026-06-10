package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Size liefert die aktuelle Größe der Datenbankdatei in Bytes.
func (s *Store) Size() (int64, error) {
	info, err := os.Stat(s.db.Path())
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
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
