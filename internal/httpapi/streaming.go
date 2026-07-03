package httpapi

import (
	"net/http"
	"time"
)

// streamWriter kapselt den ResponseWriter einer streamenden Antwort (read-events,
// observe-events, run-query) und setzt vor JEDEM Write ein frisches Fortschritts-
// Timeout auf die Verbindung (WP-4.3/B3). Solange der Client Bytes abnimmt, wird die
// Deadline bei jedem Write neu in die Zukunft geschoben — der Stream läuft beliebig
// lange (Bulk-Read/Live-Tail). Nimmt der Client nichts mehr ab, läuft der nächste
// Write in die Deadline und liefert einen Fehler; der Handler bricht dann ab und gibt
// die gehaltene bbolt-Lesetransaktion frei (gegen die Read-Tx-/Compaction-Blockade
// durch langsame/tote Clients).
//
// Ersetzt das frühere pauschale Aufheben der Server-WriteTimeout
// (SetWriteDeadline(time.Time{})): dort blieb ein stehender Client unbegrenzt.
type streamWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
	wrote   bool // ob bereits Bytes geschrieben wurden (→ Status/Header committed)
}

// newStreamWriter richtet den Fortschritts-Deadline-Writer ein. timeout <= 0 hebt die
// Schreib-Deadline vollständig auf (unbegrenzt, altes Verhalten) — dann verhält sich
// der Writer wie ein direkter Passthrough ohne Deadline-Handling.
func newStreamWriter(w http.ResponseWriter, timeout time.Duration) *streamWriter {
	rc := http.NewResponseController(w)
	if timeout <= 0 {
		// Deadline unbegrenzt aufheben (Fehler ignorieren: nicht jeder Writer
		// unterstützt das, z. B. im Test).
		_ = rc.SetWriteDeadline(time.Time{})
	}
	return &streamWriter{w: w, rc: rc, timeout: timeout}
}

// Write setzt bei aktivem Fortschritts-Timeout vor dem Schreiben eine frische
// Deadline und schreibt dann durch.
func (s *streamWriter) Write(p []byte) (int, error) {
	if s.timeout > 0 {
		// Fehler bewusst ignorieren: unterstützt der Writer keine Deadline (Test),
		// bleibt es beim Verhalten ohne Deadline — der Write erfolgt trotzdem.
		_ = s.rc.SetWriteDeadline(time.Now().Add(s.timeout))
	}
	if len(p) > 0 {
		s.wrote = true
	}
	return s.w.Write(p)
}

// Wrote meldet, ob bereits Bytes geschrieben wurden. Solange false ist der
// 200-Header noch nicht committed, sodass der Handler bei einem frühen Fehler noch
// einen Fehlerstatus senden kann (statt eines leeren 200).
func (s *streamWriter) Wrote() bool { return s.wrote }

// Flush leitet an den ResponseController weiter (der intern den http.Flusher
// findet). Fehler werden ignoriert — Flushing ist best-effort.
func (s *streamWriter) Flush() {
	_ = s.rc.Flush()
}
