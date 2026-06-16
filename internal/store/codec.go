// Storage-Codec für Event-Werte (ADR-024).
//
// Events werden als JSON im `events`-Bucket abgelegt. Optional komprimiert dieser
// Codec den gespeicherten Wert transparent mit DEFLATE (`compress/flate`, reine
// Standardbibliothek — keine neue Abhängigkeit). Die Kompression ist eine reine
// Storage-Angelegenheit: Hash und Signatur werden weiterhin über die kanonischen
// Event-Felder berechnet, nicht über die Bytes auf Platte. `/verify` bleibt damit
// unberührt, und die HTTP-API liefert unverändertes JSON.
//
// Frame-Format (erstes Byte des gespeicherten Werts):
//
//	0x01  → DEFLATE-komprimiert mit Preset-Dictionary v1, danach der Stream
//	0x00  → roher JSON-Wert ohne Kompression, danach das JSON (reserviert)
//	sonst → Legacy: roher JSON-Wert wie bisher (beginnt stets mit '{', 0x7B)
//
// Damit sind bestehende Datenbanken vollständig abwärtskompatibel: alte Werte
// beginnen mit '{' und werden vom `default`-Zweig unverändert durchgereicht. Eine
// Datenbank kann beliebig gemischte Werte enthalten (z. B. nach dem Aktivieren der
// Kompression), denn jeder Wert trägt sein eigenes Frame-Byte.
package store

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"io"
	"sync"
)

// Frame-Bytes (siehe Paketkommentar). Beide liegen außerhalb des Bereichs des
// ersten Bytes von JSON-Objekten ('{' = 0x7B), sodass es keine Kollision mit
// Legacy-Werten gibt.
const (
	frameRaw         = 0x00 // roher JSON-Wert, explizit gerahmt (reserviert)
	frameFlateDictV1 = 0x01 // DEFLATE + dictV1
)

// compressLevel steuert die DEFLATE-Stufe. Event-Werte sind klein, daher kostet
// die beste Stufe kaum CPU, bringt aber die kleinste Ablage.
const compressLevel = flate.BestCompression

// dictV1 ist das Preset-Dictionary für frameFlateDictV1. Es enthält das
// CloudEvents-Gerüst in Marshal-Reihenfolge (siehe event.Event), sodass die in
// jedem Event identischen Feldnamen und konstanten Werte als Rückverweise ins
// Dictionary kodiert werden — das ist gerade bei kleinen Events der größte Hebel.
//
// Das Dictionary ist über das Frame-Byte versioniert: Ändert es sich, muss ein
// neues Frame-Byte (frameFlateDictV2 …) vergeben werden, damit bestehende Werte
// weiter mit dem alten Dictionary dekodiert werden.
var dictV1 = []byte(`{"specversion":"1.0","id":"","time":"","source":"","subject":"/","type":"","datacontenttype":"application/json","data":,"predecessorhash":"","hash":"","signature":null}`)

// encodeStored bereitet den JSON-Wert eines Events für die Ablage auf. Ist
// compress=false, wird der rohe JSON-Wert unverändert zurückgegeben (byte-identisch
// zum Verhalten vor der Kompression). Ist compress=true, wird DEFLATE versucht und
// nur dann verwendet, wenn das Ergebnis (inkl. Frame-Byte) echt kleiner ist —
// andernfalls bleibt der rohe Wert erhalten, sodass die Ablage nie wächst.
func encodeStored(jsonBytes []byte, compress bool) ([]byte, error) {
	if !compress {
		return jsonBytes, nil
	}
	comp, err := deflate(jsonBytes)
	if err != nil {
		return nil, err
	}
	if len(comp)+1 < len(jsonBytes) {
		out := make([]byte, 0, len(comp)+1)
		out = append(out, frameFlateDictV1)
		out = append(out, comp...)
		return out, nil
	}
	return jsonBytes, nil
}

// decodeStored macht die Ablage-Form wieder zu rohem Event-JSON. Es erkennt das
// Frame-Byte und reicht Legacy-Werte (rohes JSON) unverändert durch.
func decodeStored(stored []byte) ([]byte, error) {
	if len(stored) == 0 {
		return stored, nil
	}
	switch stored[0] {
	case frameFlateDictV1:
		return inflate(stored[1:])
	case frameRaw:
		return stored[1:], nil
	default:
		return stored, nil // Legacy: rohes JSON (beginnt mit '{')
	}
}

// unmarshalStored dekodiert die Ablage-Form und deserialisiert sie nach out.
// Ersetzt direkte json.Unmarshal-Aufrufe an Werten aus dem events-Bucket.
func unmarshalStored(stored []byte, out any) error {
	js, err := decodeStored(stored)
	if err != nil {
		return err
	}
	return json.Unmarshal(js, out)
}

// flateWriterPool/flateReaderPool halten wiederverwendbare DEFLATE-Coder mit
// gesetztem Dictionary. Reset() behält Level bzw. Dictionary bei.
var flateWriterPool = sync.Pool{New: func() any {
	w, _ := flate.NewWriterDict(io.Discard, compressLevel, dictV1)
	return w
}}

var flateReaderPool = sync.Pool{New: func() any {
	return flate.NewReaderDict(bytes.NewReader(nil), dictV1)
}}

func deflate(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := flateWriterPool.Get().(*flate.Writer)
	defer flateWriterPool.Put(w)
	w.Reset(&buf)
	if _, err := w.Write(src); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func inflate(src []byte) ([]byte, error) {
	r := flateReaderPool.Get().(io.ReadCloser)
	defer flateReaderPool.Put(r)
	if err := r.(flate.Resetter).Reset(bytes.NewReader(src), dictV1); err != nil {
		return nil, err
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return out, nil
}
