package store

import (
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/auth"
)

// Schlüsselbund-Persistenz (ADR-025): der bucketAuthKeys bildet kid → JSON-Key
// ab. Anders als der Event-Strom sind das mutable Steuerungsdaten — Widerruf ist
// ein Status-Wechsel (kein Delete), damit die kid-Zuordnung im Audit dauerhaft
// bleibt. Persistiert wird nur der Hash des Geheimnisses (im auth.Key), nie der
// Klartext.

// PutKey legt einen Schlüssel an oder überschreibt ihn (idempotent über kid).
func (s *Store) PutKey(k auth.Key) error {
	data, err := json.Marshal(k)
	if err != nil {
		return fmt.Errorf("key serialisieren: %w", err)
	}
	return s.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAuthKeys).Put([]byte(k.KID), data)
	})
}

// GetKey liefert den Schlüssel zu einem kid. found ist false, wenn der kid
// unbekannt ist (kein Fehler).
func (s *Store) GetKey(kid string) (auth.Key, bool, error) {
	var k auth.Key
	var found bool
	err := s.view(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketAuthKeys).Get([]byte(kid))
		if v == nil {
			return nil
		}
		found = true
		return json.Unmarshal(v, &k)
	})
	if err != nil {
		return auth.Key{}, false, fmt.Errorf("key lesen: %w", err)
	}
	return k, found, nil
}

// ListKeys liefert alle Schlüssel (inkl. widerrufener), sortiert nach kid (die
// bbolt-Iteration läuft in Byte-Reihenfolge der Keys).
func (s *Store) ListKeys() ([]auth.Key, error) {
	var keys []auth.Key
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAuthKeys).ForEach(func(_, v []byte) error {
			var k auth.Key
			if err := json.Unmarshal(v, &k); err != nil {
				return err
			}
			keys = append(keys, k)
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("keys auflisten: %w", err)
	}
	return keys, nil
}

// RevokeKey widerruft einen Schlüssel: Status -> revoked, revokedAt -> jetzt.
// Bewusst KEIN Delete (die kid-Zuordnung bleibt fürs Audit erhalten). Liefert
// false, wenn der kid unbekannt ist. Bereits widerrufene Keys bleiben
// unverändert (idempotent, ok=true).
func (s *Store) RevokeKey(kid string) (bool, error) {
	var found bool
	err := s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuthKeys)
		v := b.Get([]byte(kid))
		if v == nil {
			return nil
		}
		found = true
		var k auth.Key
		if err := json.Unmarshal(v, &k); err != nil {
			return err
		}
		if k.Status == auth.StatusRevoked {
			return nil // schon widerrufen — revokedAt nicht überschreiben
		}
		now := s.now().UTC()
		k.Status = auth.StatusRevoked
		k.RevokedAt = &now
		data, err := json.Marshal(k)
		if err != nil {
			return err
		}
		return b.Put([]byte(kid), data)
	})
	if err != nil {
		return false, fmt.Errorf("key widerrufen: %w", err)
	}
	return found, nil
}

// CountKeys liefert die Anzahl der Schlüssel im Bund — Grundlage des
// Bootstrap-Checks (Bootstrap greift nur bei leerem Bund).
func (s *Store) CountKeys() (int, error) {
	var n int
	err := s.view(func(tx *bolt.Tx) error {
		n = tx.Bucket(bucketAuthKeys).Stats().KeyN
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("keys zählen: %w", err)
	}
	return n, nil
}
