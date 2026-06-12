//go:build !unix

package metrics

// processCPUSeconds ist auf Plattformen ohne getrusage (z. B. Windows) nicht
// verfügbar; die CPU-Serie entfällt dann.
func processCPUSeconds() (float64, bool) { return 0, false }
