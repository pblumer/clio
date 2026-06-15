//go:build windows

package store

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DiskUsage liefert freien und gesamten Speicher (Bytes) des Dateisystems,
// auf dem die Datenbankdatei liegt. Windows-Variante über GetDiskFreeSpaceEx;
// der Aufruf erwartet ein Verzeichnis, daher das Verzeichnis der DB-Datei.
func (s *Store) DiskUsage() (freeBytes, totalBytes int64, err error) {
	dir, err := windows.UTF16PtrFromString(filepath.Dir(s.db.Path()))
	if err != nil {
		return 0, 0, err
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(dir, &freeToCaller, &total, &totalFree); err != nil {
		return 0, 0, err
	}
	return int64(freeToCaller), int64(total), nil
}
