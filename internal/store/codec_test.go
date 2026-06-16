package store

import (
	"bytes"
	"encoding/json"
	"testing"
)

// sampleEventJSON ist ein realistisches, vollständiges Event-JSON, wie es der
// Store ablegt — Grundlage für Round-Trip- und Größentests.
var sampleEventJSON = []byte(`{"specversion":"1.0","id":"4711","time":"2026-06-16T09:46:55.123456789Z","source":"https://erp.example.com/services/identity","subject":"/scenarios/identity_ops/employees/E-000042","type":"identity.employee.mailbox.attached","datacontenttype":"application/json","data":{"employeeId":"E-000042","mailbox":"e.musterfrau@example.com","quotaMb":51200},"predecessorhash":"9f2c1b7d4a6e8f0c3b5d7e9a1c2f4b6d8e0a2c4f6b8d0e2a4c6f8b0d2e4a6c8f","hash":"1a3c5e7902468ace1b3d5f7092b4d6f8a0c2e4061b3d5f7092b4d6f8a0c2e406","signature":null}`)

func TestCodecRoundTrip(t *testing.T) {
	for _, compress := range []bool{false, true} {
		stored, err := encodeStored(sampleEventJSON, compress)
		if err != nil {
			t.Fatalf("encodeStored(compress=%v): %v", compress, err)
		}
		got, err := decodeStored(stored)
		if err != nil {
			t.Fatalf("decodeStored(compress=%v): %v", compress, err)
		}
		if !bytes.Equal(got, sampleEventJSON) {
			t.Fatalf("round-trip (compress=%v) verändert: got %q", compress, got)
		}
	}
}

func TestEncodeUncompressedIsByteIdentical(t *testing.T) {
	stored, err := encodeStored(sampleEventJSON, false)
	if err != nil {
		t.Fatalf("encodeStored: %v", err)
	}
	// Ohne Kompression muss der gespeicherte Wert exakt das rohe JSON sein
	// (keine Rahmung), damit das Verhalten byte-identisch zu vor ADR-024 bleibt.
	if !bytes.Equal(stored, sampleEventJSON) {
		t.Fatalf("unkomprimiert nicht byte-identisch: %q", stored)
	}
}

func TestDecodeLegacyRawPassesThrough(t *testing.T) {
	// Werte aus Datenbanken von vor ADR-024 beginnen mit '{' und tragen kein
	// Frame-Byte — sie müssen unverändert dekodiert werden.
	legacy := []byte(`{"specversion":"1.0","id":"1","type":"x"}`)
	got, err := decodeStored(legacy)
	if err != nil {
		t.Fatalf("decodeStored(legacy): %v", err)
	}
	if !bytes.Equal(got, legacy) {
		t.Fatalf("legacy verändert: %q", got)
	}
}

func TestEncodeKeepsRawWhenCompressionDoesNotHelp(t *testing.T) {
	// Kurze, inkompressible Eingabe: das Frame + DEFLATE wäre nicht kleiner, also
	// muss der rohe Wert erhalten bleiben (Ablage darf nie wachsen).
	small := []byte(`{"a":1}`)
	stored, err := encodeStored(small, true)
	if err != nil {
		t.Fatalf("encodeStored: %v", err)
	}
	if len(stored) > len(small) {
		t.Fatalf("Ablage gewachsen: %d > %d (%q)", len(stored), len(small), stored)
	}
	got, err := decodeStored(stored)
	if err != nil {
		t.Fatalf("decodeStored: %v", err)
	}
	if !bytes.Equal(got, small) {
		t.Fatalf("round-trip verändert: %q", got)
	}
}

func TestUnmarshalStoredBothForms(t *testing.T) {
	raw := sampleEventJSON
	comp, err := encodeStored(sampleEventJSON, true)
	if err != nil {
		t.Fatalf("encodeStored: %v", err)
	}
	for name, stored := range map[string][]byte{"legacy": raw, "compressed": comp} {
		var m map[string]any
		if err := unmarshalStored(stored, &m); err != nil {
			t.Fatalf("unmarshalStored(%s): %v", name, err)
		}
		if m["type"] != "identity.employee.mailbox.attached" {
			t.Fatalf("%s: type falsch dekodiert: %v", name, m["type"])
		}
	}
}

func TestDecodeInvalidCompressedReturnsError(t *testing.T) {
	// Frame-Byte für DEFLATE, aber Müll dahinter → Fehler statt Stilldekodierung.
	bad := append([]byte{frameFlateDictV1}, 0xff, 0x00, 0x13, 0x37)
	if _, err := decodeStored(bad); err == nil {
		t.Fatal("erwartete einen Dekodierfehler für kaputten DEFLATE-Stream")
	}
}

// TestCompressionRatioDemo dokumentiert die Größenersparnis auf einem realistischen
// Event und schlägt nicht fehl — die Zahl erscheint mit `go test -v`.
func TestCompressionRatioDemo(t *testing.T) {
	comp, err := encodeStored(sampleEventJSON, true)
	if err != nil {
		t.Fatalf("encodeStored: %v", err)
	}
	raw := len(sampleEventJSON)
	got := len(comp)
	t.Logf("Event-Wert: roh %d B → komprimiert %d B (%.0f%% der Größe, -%.0f%%)",
		raw, got, 100*float64(got)/float64(raw), 100*(1-float64(got)/float64(raw)))
	if got >= raw {
		t.Fatalf("keine Ersparnis: %d >= %d", got, raw)
	}
}

func BenchmarkEncodeStoredCompress(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(sampleEventJSON)))
	for i := 0; i < b.N; i++ {
		if _, err := encodeStored(sampleEventJSON, true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeStoredCompress(b *testing.B) {
	comp, err := encodeStored(sampleEventJSON, true)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(sampleEventJSON)))
	for i := 0; i < b.N; i++ {
		out, err := decodeStored(comp)
		if err != nil {
			b.Fatal(err)
		}
		_ = out
	}
}

// Sanity: sampleEventJSON ist gültiges JSON (sonst sind die Tests wertlos).
func TestSampleIsValidJSON(t *testing.T) {
	if !json.Valid(sampleEventJSON) {
		t.Fatal("sampleEventJSON ist kein gültiges JSON")
	}
}
