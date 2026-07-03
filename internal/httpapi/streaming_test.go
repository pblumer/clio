package httpapi

import (
	"net/http"
	"testing"
	"time"
)

// deadlineRecorder ist ein http.ResponseWriter, der SetWriteDeadline-Aufrufe und
// geschriebene Bytes protokolliert — so lässt sich das Fortschritts-Timeout des
// streamWriter deterministisch prüfen (ohne echte Verbindung).
type deadlineRecorder struct {
	header    http.Header
	deadlines []time.Time
	writes    int
}

func (d *deadlineRecorder) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}

func (d *deadlineRecorder) Write(p []byte) (int, error) {
	d.writes++
	return len(p), nil
}

func (d *deadlineRecorder) WriteHeader(int) {}

// SetWriteDeadline macht den Recorder für http.ResponseController auffindbar.
func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

// TestStreamWriterSetsProgressDeadline prüft B3/WP-4.3: bei aktivem Timeout setzt
// der streamWriter vor JEDEM Write eine frische Schreib-Deadline in die Zukunft.
func TestStreamWriterSetsProgressDeadline(t *testing.T) {
	rec := &deadlineRecorder{}
	const to = 60 * time.Second
	sw := newStreamWriter(rec, to)

	before := time.Now()
	if _, err := sw.Write([]byte("a")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := sw.Write([]byte("b")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if len(rec.deadlines) != 2 {
		t.Fatalf("SetWriteDeadline-Aufrufe = %d, want 2 (einer je Write)", len(rec.deadlines))
	}
	for i, d := range rec.deadlines {
		if !d.After(before) {
			t.Fatalf("Deadline[%d] = %v liegt nicht in der Zukunft", i, d)
		}
		// Grobes oberes Fenster: Deadline ~ now+timeout, sicher unter now+2*timeout.
		if d.After(before.Add(2 * to)) {
			t.Fatalf("Deadline[%d] = %v unerwartet weit in der Zukunft", i, d)
		}
	}
	if rec.writes != 2 {
		t.Fatalf("Writes = %d, want 2", rec.writes)
	}
}

// TestStreamWriterDisabledUnsetsDeadline prüft, dass timeout<=0 die Deadline EINMAL
// aufhebt (Nullzeit) und danach keine Fortschritts-Deadlines mehr setzt — das alte,
// unbegrenzte Streaming-Verhalten.
func TestStreamWriterDisabledUnsetsDeadline(t *testing.T) {
	rec := &deadlineRecorder{}
	sw := newStreamWriter(rec, 0)

	if len(rec.deadlines) != 1 || !rec.deadlines[0].IsZero() {
		t.Fatalf("erwartete genau eine Nullzeit-Deadline bei Konstruktion, bekam %v", rec.deadlines)
	}
	if _, err := sw.Write([]byte("a")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(rec.deadlines) != 1 {
		t.Fatalf("nach Write: Deadline-Aufrufe = %d, want 1 (kein Fortschritts-Timeout)", len(rec.deadlines))
	}
}
