//go:build !windows

package store

import "syscall"

// DiskUsage liefert freien und gesamten Speicher (Bytes) des Dateisystems,
// auf dem die Datenbankdatei liegt.
func (s *Store) DiskUsage() (freeBytes, totalBytes int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.path(), &st); err != nil {
		return 0, 0, err
	}
	free := int64(st.Bavail) * int64(st.Bsize)
	total := int64(st.Blocks) * int64(st.Bsize)
	return free, total, nil
}
