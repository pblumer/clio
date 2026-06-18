package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

// ErrSchemaExists wird zurückgegeben, wenn für einen Typ bereits ein Schema
// registriert ist (Schemas sind unveränderlich, um die Historie nicht
// nachträglich zu invalidieren).
var ErrSchemaExists = errors.New("für diesen typ ist bereits ein schema registriert")

// ErrSchemaValidation wird zurückgegeben, wenn Event-Daten nicht zum Schema
// ihres Typs passen (beim Write oder bei der Registrierung gegen die Historie).
var ErrSchemaValidation = errors.New("schema-validierung fehlgeschlagen")

// RegisterSchema registriert ein JSON Schema für einen Event-Typ. Die
// Registrierung schlägt fehl, wenn bereits ein Schema existiert (409) oder wenn
// schon gespeicherte Events dieses Typs das Schema verletzen würden (damit ist
// garantiert: hat ein Typ ein Schema, erfüllen es alle seine Events).
func (s *Store) RegisterSchema(typ string, schema json.RawMessage) error {
	compiled, err := compileSchema(schema)
	if err != nil {
		return fmt.Errorf("%w: ungültiges schema: %v", ErrSchemaValidation, err)
	}

	canonical, err := canonicalJSON(schema)
	if err != nil {
		return fmt.Errorf("%w: schema kompaktieren: %v", ErrSchemaValidation, err)
	}

	err = s.update(func(tx *bolt.Tx) error {
		schemas := tx.Bucket(bucketSchemas)
		if schemas.Get([]byte(typ)) != nil {
			return ErrSchemaExists
		}

		// Bestehende Events dieses Typs müssen dem Schema genügen.
		c := tx.Bucket(bucketEvents).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var ev event.Event
			if err := unmarshalStored(v, &ev); err != nil {
				return fmt.Errorf("event dekodieren: %w", err)
			}
			if ev.Type != typ {
				continue
			}
			if err := validateData(compiled, ev.Data); err != nil {
				return fmt.Errorf("%w: event %s verletzt das schema: %v", ErrSchemaValidation, ev.ID, err)
			}
		}

		return schemas.Put([]byte(typ), canonical)
	})
	if err != nil {
		return err
	}

	// Cache vorwärmen (geschlüsselt nach Inhalt).
	s.schemaMu.Lock()
	s.schemaCache[string(canonical)] = compiled
	s.schemaMu.Unlock()
	return nil
}

// SchemaFor liefert das (kanonische) Schema eines Typs, sofern registriert.
func (s *Store) SchemaFor(typ string) (json.RawMessage, bool, error) {
	var out json.RawMessage
	var found bool
	err := s.view(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bucketSchemas).Get([]byte(typ)); v != nil {
			out = append(json.RawMessage(nil), v...)
			found = true
		}
		return nil
	})
	return out, found, err
}

// validateAgainstSchema prüft die Daten eines Candidates gegen ein ggf.
// registriertes Schema. Läuft innerhalb der Schreibtransaktion.
func (s *Store) validateAgainstSchema(tx *bolt.Tx, typ string, data json.RawMessage) error {
	raw := tx.Bucket(bucketSchemas).Get([]byte(typ))
	if raw == nil {
		return nil // kein Schema -> keine Einschränkung
	}
	compiled, err := s.getCompiled(raw)
	if err != nil {
		return fmt.Errorf("schema kompilieren: %w", err)
	}
	if err := validateData(compiled, data); err != nil {
		return fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}
	return nil
}

// getCompiled liefert das kompilierte Schema zu rohen Schema-Bytes aus dem
// Cache (kompiliert bei Bedarf).
func (s *Store) getCompiled(raw []byte) (*jsonschema.Schema, error) {
	key := string(raw)
	s.schemaMu.RLock()
	sch, ok := s.schemaCache[key]
	s.schemaMu.RUnlock()
	if ok {
		return sch, nil
	}
	sch, err := compileSchema(raw)
	if err != nil {
		return nil, err
	}
	s.schemaMu.Lock()
	s.schemaCache[key] = sch
	s.schemaMu.Unlock()
	return sch, nil
}

// schemaResourceURI ist eine neutrale URI für das eingebettete Schema, damit
// Fehlermeldungen keinen lokalen Dateipfad verraten.
const schemaResourceURI = "urn:clio:event-schema"

func compileSchema(raw []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaResourceURI, doc); err != nil {
		return nil, err
	}
	return c.Compile(schemaResourceURI)
}

func validateData(sch *jsonschema.Schema, data json.RawMessage) error {
	var v any
	if len(data) > 0 {
		var err error
		if v, err = jsonschema.UnmarshalJSON(bytes.NewReader(data)); err != nil {
			return err
		}
	}
	return sch.Validate(v)
}

// canonicalJSON kompaktiert JSON (deterministische Speicherung).
func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
