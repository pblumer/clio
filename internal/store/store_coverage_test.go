package store

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/pblumer/clio/internal/event"
	"github.com/pblumer/clio/internal/query"
)

// TestCompactMissingFile deckt den frühen Fehlerpfad von Compact ab: Existiert
// die Datei nicht, scheitert bereits der Stat — kein Öffnen, kein Temp-File.
func TestCompactMissingFile(t *testing.T) {
	if _, _, err := Compact(filepath.Join(t.TempDir(), "gibts-nicht.db")); err == nil {
		t.Fatal("Compact auf fehlende Datei sollte fehlschlagen")
	}
}

// TestDecodeStoredFrames deckt die noch offenen Zweige des Ablage-Codecs ab:
// leerer Wert (unverändert), explizit gerahmtes rohes JSON (frameRaw) sowie der
// Fehler-Durchgriff von unmarshalStored bei kaputtem DEFLATE-Stream.
func TestDecodeStoredFrames(t *testing.T) {
	// Leerer Wert -> unverändert, kein Fehler.
	if got, err := decodeStored(nil); err != nil || len(got) != 0 {
		t.Fatalf("decodeStored(nil) = %q, %v; want leer, nil", got, err)
	}

	// frameRaw (0x00) + JSON -> das JSON ohne Frame-Byte.
	raw := []byte(`{"a":1}`)
	framed := append([]byte{frameRaw}, raw...)
	if got, err := decodeStored(framed); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("decodeStored(frameRaw) = %q, %v; want %q", got, err, raw)
	}

	// unmarshalStored muss den Dekodierfehler durchreichen (DEFLATE-Frame + Müll).
	bad := append([]byte{frameFlateDictV1}, 0xff, 0x00, 0x13, 0x37)
	var out map[string]any
	if err := unmarshalStored(bad, &out); err == nil {
		t.Fatal("unmarshalStored auf kaputten DEFLATE-Stream sollte fehlschlagen")
	}
}

// TestStoreSmallAccessors deckt ein paar schmale Zugriffsfunktionen ab:
// FirstEventTime auf leerem/gefülltem Store, DirectCount für vorhandene und
// fehlende Subjects sowie Size auf der offenen Datei.
func TestStoreSmallAccessors(t *testing.T) {
	st := openTemp(t)

	// Leerer Store: keine erste Event-Zeit.
	if _, ok, err := st.FirstEventTime(); err != nil || ok {
		t.Fatalf("FirstEventTime(leer) = ok=%v, err=%v; want ok=false", ok, err)
	}
	// DirectCount auf unbekanntem Subject -> 0.
	if n, err := st.DirectCount("/gibtsnicht"); err != nil || n != 0 {
		t.Fatalf("DirectCount(leer) = %d, %v; want 0", n, err)
	}

	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t"},
		event.Candidate{Source: "s", Subject: "/a", Type: "t"},
	)

	// Jetzt gibt es eine erste Event-Zeit.
	if _, ok, err := st.FirstEventTime(); err != nil || !ok {
		t.Fatalf("FirstEventTime(gefüllt) = ok=%v, err=%v; want ok=true", ok, err)
	}
	// Zwei Events exakt auf /a.
	if n, err := st.DirectCount("/a"); err != nil || n != 2 {
		t.Fatalf("DirectCount(/a) = %d, %v; want 2", n, err)
	}
	// Die Datei hat eine positive Größe.
	if sz, err := st.Size(); err != nil || sz <= 0 {
		t.Fatalf("Size = %d, %v; want > 0", sz, err)
	}
}

// TestQueryPreconditionRecursiveRoot deckt den rekursiven Wurzel-Scope von
// anyMatch ab (Subject "/", recursive): Dieser Pfad scannt das gesamte
// events-Bucket statt den Subject-Index — ein eigener Zweig, den die bisherigen
// Subject-spezifischen Tests nicht erreichen.
func TestQueryPreconditionRecursiveRoot(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/orders/1", Type: "placed"})

	c, err := query.NewCompiler()
	if err != nil {
		t.Fatalf("compiler: %v", err)
	}
	pred, err := c.Compile("event.type == 'placed'")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// Rekursiv über die Wurzel: irgendwo existiert ein 'placed' -> Empty verletzt.
	rootEmpty := []Precondition{{Type: PreconditionQueryResultEmpty, Subject: "/", Recursive: true, Predicate: pred}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/anything", Type: "x"}}, rootEmpty); !errorsIsPrecondition(err) {
		t.Fatalf("rekursiv ab Wurzel: erwartete ErrPreconditionFailed, bekam %v", err)
	}

	// NonEmpty über die Wurzel mit einem Prädikat ohne Treffer -> verletzt.
	noHit, err := c.Compile("event.type == 'gibtsnicht'")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rootNonEmpty := []Precondition{{Type: PreconditionQueryResultNonEmpty, Subject: "/", Recursive: true, Predicate: noHit}}
	if _, err := st.Append([]event.Candidate{{Source: "s", Subject: "/x", Type: "y"}}, rootNonEmpty); !errorsIsPrecondition(err) {
		t.Fatalf("nonEmpty ab Wurzel ohne Treffer: erwartete ErrPreconditionFailed, bekam %v", err)
	}
}

// TestForEachEventTimeSourceMaxSeq prüft, dass die Iteration bei Erreichen von
// maxSeq abbricht (nur Events bis zur Obergrenze besucht werden) und ohne Grenze
// (maxSeq==0) alle Events liefert.
func TestForEachEventTimeSourceMaxSeq(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s1", Subject: "/a", Type: "t"},
		event.Candidate{Source: "s2", Subject: "/b", Type: "t"},
		event.Candidate{Source: "s3", Subject: "/c", Type: "t"},
	)

	var bounded int
	if err := st.ForEachEventTimeSource(2, func(_ time.Time, _ string) { bounded++ }); err != nil {
		t.Fatalf("ForEachEventTimeSource(maxSeq=2): %v", err)
	}
	if bounded != 2 {
		t.Fatalf("maxSeq=2 besuchte %d Events, want 2", bounded)
	}

	var all int
	if err := st.ForEachEventTimeSource(0, func(_ time.Time, _ string) { all++ }); err != nil {
		t.Fatalf("ForEachEventTimeSource(0): %v", err)
	}
	if all != 3 {
		t.Fatalf("ohne Grenze %d Events, want 3", all)
	}
}
