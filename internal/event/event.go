// Package event definiert die CloudEvents-Domänentypen von cliostore sowie
// die Validierung von Event-Candidates (siehe ADR-004, ADR-005).
package event

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SpecVersion ist die unterstützte CloudEvents-Spezifikationsversion.
const SpecVersion = "1.0"

// GenesisHash ist der predecessorhash des allerersten Events (64 Null-Hex).
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// JSONContentType ist der content-type strukturierter JSON-Events.
const JSONContentType = "application/json"

// Candidate ist ein vom Client gesendeter Event-Vorschlag, bevor er
// gespeichert wurde. Die Felder ID, Time und SpecVersion werden
// serverseitig ergänzt und dürfen vom Client nicht gesetzt werden.
type Candidate struct {
	Source  string          `json:"source"`
	Subject string          `json:"subject"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Event ist ein gespeichertes, unveränderliches Faktum im CloudEvents-Format.
// Über PredecessorHash und Hash bilden die Events eine fortlaufende Hash-Kette
// (Tamper-Evidence): jede nachträgliche Änderung bricht die Kette nachweisbar.
type Event struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Time            string          `json:"time"`
	Source          string          `json:"source"`
	Subject         string          `json:"subject"`
	Type            string          `json:"type"`
	DataContentType string          `json:"datacontenttype,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
	// AuthKID ist eine optionale CloudEvents-Extension: der kid des
	// authentifizierten Schlüssels, der das Event geschrieben hat (Urheberschaft,
	// ADR-025). Wird nur gesetzt, wenn die Event-Urheberschaft aktiviert ist
	// (CLIO_EVENT_AUTHORSHIP); sonst leer und damit abwärtskompatibel. Geht in den
	// Hash ein (nur wenn nicht-leer), bindet die Urheberschaft also an die
	// Hash-Kette/Signatur (ADR-012/016).
	AuthKID         string  `json:"clioauthkid,omitempty"`
	PredecessorHash string  `json:"predecessorhash"`
	Hash            string  `json:"hash"`
	Signature       *string `json:"signature"`

	// Partition ist die Partition, in der dieses Event liegt (ADR-034/036). Es ist
	// ein **serverseitig abgeleitetes Sicht-Attribut**, das NUR beim Lesen/Streamen
	// gesetzt wird — es wird NICHT gespeichert und geht NICHT in den Hash ein
	// (ComputeHash ignoriert es). `omitempty` lässt es bei Partition 0 weg, sodass
	// die nicht-partitionierte Ablage (n=1) byte-identisch bleibt. Konsumenten bilden
	// daraus zusammen mit `id` (der per-Partition-Sequenz) den per-Partition-Cursor
	// für Reconnect/Replay (INV-P3).
	Partition int `json:"partition,omitempty"`
}

// ComputeHash berechnet den SHA-256-Hash des Events über seinen Inhalt und den
// predecessorhash. Die Felder Hash und Signature gehen NICHT ein. Jedes Feld
// wird längenpräfigiert serialisiert, damit Feldgrenzen eindeutig sind (keine
// Verwechslung zwischen z. B. ("ab","c") und ("a","bc")).
//
// Voraussetzung: Data liegt in kanonischer (kompakter) Form vor, damit die
// Verifikation reproduzierbar ist.
func ComputeHash(ev Event) string {
	h := sha256.New()
	for _, f := range []string{
		ev.PredecessorHash,
		ev.SpecVersion,
		ev.ID,
		ev.Time,
		ev.Source,
		ev.Subject,
		ev.Type,
		ev.DataContentType,
	} {
		writeField(h, []byte(f))
	}
	writeField(h, ev.Data)
	// Urheberschaft (ADR-025) geht NUR ein, wenn gesetzt. So bleiben Events ohne
	// Urheberschaft (Bestand sowie Feature-aus) byte-identisch zum bisherigen Hash
	// — /verify bleibt für die gesamte vorhandene Historie grün.
	if ev.AuthKID != "" {
		writeField(h, []byte(ev.AuthKID))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeField(h io.Writer, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
}

// Validate prüft, ob ein Candidate die Pflichtfelder korrekt setzt.
// Ein Subject muss mit "/" beginnen (ADR-005).
func (c Candidate) Validate() error {
	if strings.TrimSpace(c.Source) == "" {
		return fmt.Errorf("source ist pflicht")
	}
	if c.Subject == "" || !strings.HasPrefix(c.Subject, "/") {
		return fmt.Errorf("subject muss mit %q beginnen", "/")
	}
	if strings.TrimSpace(c.Type) == "" {
		return fmt.Errorf("type ist pflicht")
	}
	if len(c.Data) > 0 && !json.Valid(c.Data) {
		return fmt.Errorf("data ist kein gültiges JSON")
	}
	return nil
}
