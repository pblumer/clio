package store

import "testing"

func TestDiskUsage(t *testing.T) {
	st := openTemp(t)
	free, total, err := st.DiskUsage()
	if err != nil {
		t.Fatalf("DiskUsage err: %v", err)
	}
	if free <= 0 {
		t.Fatalf("free <= 0: %d", free)
	}
	if total <= 0 {
		t.Fatalf("total <= 0: %d", total)
	}
	if free > total {
		t.Fatalf("free > total: free=%d total=%d", free, total)
	}
}
