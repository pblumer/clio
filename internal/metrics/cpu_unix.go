//go:build unix

package metrics

import (
	"syscall"
	"time"
)

// processCPUSeconds liefert die bisher verbrauchte CPU-Zeit (user+sys) des
// Prozesses in Sekunden via getrusage(RUSAGE_SELF). Verfügbar auf
// Linux/macOS/BSD (der Standard-Deploy-Fall, inkl. Docker).
func processCPUSeconds() (float64, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	usr := time.Duration(int64(ru.Utime.Sec))*time.Second + time.Duration(int64(ru.Utime.Usec))*time.Microsecond
	sys := time.Duration(int64(ru.Stime.Sec))*time.Second + time.Duration(int64(ru.Stime.Usec))*time.Microsecond
	return (usr + sys).Seconds(), true
}
