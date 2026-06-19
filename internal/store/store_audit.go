// Persistentes Audit-Log administrativer Aktionen (ADR-032).
//
// Das Audit-Log lebt in einem eigenen, append-only Bucket (audit_log) — bewusst
// getrennt vom Event-Strom (events): Audit-Einträge sollen Fach-Events nicht
// stören (nicht in read-events/run-query/observe auftauchen, nicht die globale
// Event-Sequenz oder die Hash-Kette prägen) und nicht über die normale Write-API
// erreichbar/fälschbar sein. Geschrieben wird nur aus server-internen
// Admin-Codepfaden und der Offline-CLI; nie landet ein Geheimnis darin.
package store

import (
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Audit-Ergebniswerte.
const (
	AuditSuccess = "success"
	AuditFailure = "failure"
)

// Audit-Aktionsnamen (ADR-032), bewusst stabil und punktiert (Bereich.Verb),
// damit sie sich gut filtern lassen. Zentral hier, damit HTTP-Layer und CLI/
// Maintenance dieselbe Vokabel verwenden.
const (
	AuditActionKeyCreate      = "key.create"
	AuditActionKeyRotate      = "key.rotate"
	AuditActionKeyRevoke      = "key.revoke"
	AuditActionSchemaRegister = "schema.register"
	AuditActionBackup         = "backup"
	AuditActionDevReset       = "dev.reset"
	AuditActionCompaction     = "compaction"
)

// AuditEntry ist ein einzelner Audit-Eintrag. Seq ist die eigene monotone
// Sequenz des Audit-Logs (unabhängig von der Event-Sequenz). ActorKID/ActorName
// identifizieren den Auslöser (leer bei system/CLI). Es wird NIE ein Geheimnis
// gespeichert.
type AuditEntry struct {
	Seq       uint64    `json:"seq"`
	Time      time.Time `json:"time"`
	ActorKID  string    `json:"actorKid,omitempty"`
	ActorName string    `json:"actorName,omitempty"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	Target    string    `json:"target,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// AppendAudit hängt einen Audit-Eintrag append-only an das Audit-Log an. Seq und
// Time werden serverseitig vergeben/gesetzt (vom Aufrufer übergebene Werte werden
// überschrieben). Ein leeres Result wird auf AuditSuccess normalisiert.
func (s *Store) AppendAudit(e AuditEntry) error {
	if e.Result == "" {
		e.Result = AuditSuccess
	}
	if e.Time.IsZero() {
		e.Time = s.now().UTC()
	} else {
		e.Time = e.Time.UTC()
	}
	return s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAuditLog)
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		e.Seq = seq
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("audit-eintrag serialisieren: %w", err)
		}
		return b.Put(seqKey(seq), data)
	})
}

// AuditEntries liefert die jüngsten Audit-Einträge in absteigender Reihenfolge
// (neueste zuerst). limit <= 0 liefert alle. beforeSeq > 0 blättert: nur Einträge
// mit Seq < beforeSeq (für Cursor-Pagination); 0 = von der Spitze.
func (s *Store) AuditEntries(limit int, beforeSeq uint64) ([]AuditEntry, error) {
	var out []AuditEntry
	err := s.view(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketAuditLog).Cursor()
		// Rückwärts iterieren (neueste zuerst). Bei beforeSeq vor diesem Schlüssel
		// einsteigen, sonst am Ende.
		var k, v []byte
		if beforeSeq > 0 {
			c.Seek(seqKey(beforeSeq)) // erster Schlüssel >= beforeSeq
			k, v = c.Prev()           // strikt davor
		} else {
			k, v = c.Last()
		}
		for ; k != nil; k, v = c.Prev() {
			if limit > 0 && len(out) >= limit {
				break
			}
			var e AuditEntry
			if err := json.Unmarshal(v, &e); err != nil {
				return fmt.Errorf("audit-eintrag dekodieren: %w", err)
			}
			out = append(out, e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CountAudit liefert die Anzahl der Audit-Einträge (O(1) über die bbolt-Sequenz).
func (s *Store) CountAudit() (uint64, error) {
	var n uint64
	err := s.view(func(tx *bolt.Tx) error {
		n = tx.Bucket(bucketAuditLog).Sequence()
		return nil
	})
	return n, err
}
