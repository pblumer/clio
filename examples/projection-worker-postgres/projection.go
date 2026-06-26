package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// checkpointName ist der Schlüssel des Projektions-Checkpoints in der DB. Mehrere
// Projektionen könnten unterschiedliche Namen verwenden.
const checkpointName = "orders"

// schemaSQL legt Read-Model- und Checkpoint-Tabellen idempotent an. Das Read Model
// (orders) ist eine *abgeleitete* Sicht — der Event Store bleibt Source of Truth.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS orders (
    order_id          TEXT PRIMARY KEY,
    customer          TEXT,
    total_cents       BIGINT,
    status            TEXT NOT NULL,
    carrier           TEXT,
    tracking_id       TEXT,
    cancel_reason     TEXT,
    updated_event_id  BIGINT NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS projection_checkpoint (
    name       TEXT   NOT NULL,
    partition  INT    NOT NULL,
    last_seq   BIGINT NOT NULL,
    PRIMARY KEY (name, partition)
);
`

// projection kapselt die *.DB und die Projektionslogik.
type projection struct {
	db *sql.DB
}

// ensureSchema legt die Tabellen an. Der per-Partition-Checkpoint braucht keine
// Initialzeile: eine fehlende Partition bedeutet „ab Sequenz 0".
func (p *projection) ensureSchema(ctx context.Context) error {
	if _, err := p.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("schema anlegen: %w", err)
	}
	return nil
}

// rebuild verwirft das Read Model vollständig und setzt den Checkpoint zurück, um
// es ab Sequenz 0 neu aufzubauen (Replay). Möglich, *weil* der Event-Strom
// unveränderlich und vollständig ist — die zentrale Eigenschaft von Event
// Sourcing.
func (p *projection) rebuild(ctx context.Context) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `TRUNCATE orders`); err != nil {
		return err
	}
	// Alle Partitions-Checkpoints löschen → fehlende Partition = „ab Sequenz 0".
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM projection_checkpoint WHERE name = $1`, checkpointName); err != nil {
		return err
	}
	return tx.Commit()
}

// checkpoint liest die zuletzt verarbeitete Sequenz JE Partition (ADR-036). Das
// Ergebnis ist der per-Partition-Cursor: partition → letzte Sequenz; fehlende
// Partitionen bedeuten „noch nichts verarbeitet" (Sequenz 0). Bei n=1 enthält die
// Map genau Partition 0.
func (p *projection) checkpoint(ctx context.Context) (map[int]uint64, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT partition, last_seq FROM projection_checkpoint WHERE name = $1`, checkpointName)
	if err != nil {
		return nil, fmt.Errorf("checkpoint lesen: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cur := make(map[int]uint64)
	for rows.Next() {
		var part int
		var seq int64
		if err := rows.Scan(&part, &seq); err != nil {
			return nil, fmt.Errorf("checkpoint-zeile: %w", err)
		}
		cur[part] = uint64(seq)
	}
	return cur, rows.Err()
}

// apply wendet ein Event in EINER Transaktion an und schreibt den Checkpoint der
// **Partition des Events** gemeinsam mit der Read-Model-Änderung fort. Das macht die
// Projektion **exactly-once** auf dem Read Model: Stürzt der Worker mitten im Stream
// ab, werden bei Reconnect ggf. Events erneut geliefert — der Guard (seq <=
// Partitions-Checkpoint → skip) verwirft sie, und weil Checkpoint und Daten atomar
// zusammen committen, gibt es keine Teilanwendung. Die Idempotenz ist **pro
// Partition**, weil Sequenzen nach ADR-034 nur innerhalb einer Partition monoton und
// eindeutig sind (über Partitionen hinweg können sich IDs wiederholen).
func (p *projection) apply(ctx context.Context, ev Event) error {
	seq, err := strconv.ParseUint(ev.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("event-id %q ungültig: %w", ev.ID, err)
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Guard: bereits verarbeitete Sequenzen DIESER Partition überspringen (Idempotenz
	// bei Replay/Re-Delivery). Der Partitions-Checkpoint wird unter FOR UPDATE
	// gelesen; eine fehlende Zeile bedeutet „ab 0".
	var last int64
	switch err := tx.QueryRowContext(ctx,
		`SELECT last_seq FROM projection_checkpoint WHERE name = $1 AND partition = $2 FOR UPDATE`,
		checkpointName, ev.Partition).Scan(&last); err {
	case nil, sql.ErrNoRows:
		// last bleibt 0 bei ErrNoRows
	default:
		return err
	}
	if seq <= uint64(last) {
		return tx.Commit() // schon verarbeitet — no-op
	}

	if err := applyToReadModel(ctx, tx, ev, seq); err != nil {
		return err
	}

	// Partitions-Checkpoint atomar fortschreiben (anlegen, falls erste Sequenz).
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projection_checkpoint(name, partition, last_seq) VALUES ($1, $2, $3)
		 ON CONFLICT (name, partition) DO UPDATE SET last_seq = EXCLUDED.last_seq`,
		checkpointName, ev.Partition, int64(seq)); err != nil {
		return err
	}
	return tx.Commit()
}

// applyToReadModel bildet ein einzelnes Event auf das orders-Read-Model ab. Nur
// bekannte Typen verändern den Zustand; unbekannte werden bewusst ignoriert
// (vorwärtskompatibel — neue Event-Typen brechen die Projektion nicht).
func applyToReadModel(ctx context.Context, tx *sql.Tx, ev Event, id uint64) error {
	orderID := orderIDFromSubject(ev.Subject)
	if orderID == "" {
		return nil // kein Order-Subject — ignorieren
	}
	switch ev.Type {
	case "order.placed":
		var d orderPlaced
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return fmt.Errorf("order.placed payload: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO orders (order_id, customer, total_cents, status, updated_event_id, updated_at)
			VALUES ($1, $2, $3, 'placed', $4, now())
			ON CONFLICT (order_id) DO UPDATE
			  SET customer = EXCLUDED.customer,
			      total_cents = EXCLUDED.total_cents,
			      status = 'placed',
			      updated_event_id = EXCLUDED.updated_event_id,
			      updated_at = now()`,
			orderID, d.Customer, d.TotalCents, int64(id))
		return err

	case "order.paid":
		_, err := tx.ExecContext(ctx,
			`UPDATE orders SET status = 'paid', updated_event_id = $1, updated_at = now()
			 WHERE order_id = $2`, int64(id), orderID)
		return err

	case "order.shipped":
		var d orderShipped
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return fmt.Errorf("order.shipped payload: %w", err)
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE orders SET status = 'shipped', carrier = $1, tracking_id = $2,
			        updated_event_id = $3, updated_at = now()
			 WHERE order_id = $4`, d.Carrier, d.TrackingID, int64(id), orderID)
		return err

	case "order.cancelled":
		var d orderCancelled
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return fmt.Errorf("order.cancelled payload: %w", err)
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE orders SET status = 'cancelled', cancel_reason = $1,
			        updated_event_id = $2, updated_at = now()
			 WHERE order_id = $3`, d.Reason, int64(id), orderID)
		return err

	default:
		return nil // unbekannter Typ — ignorieren
	}
}

// orderIDFromSubject zieht die Order-ID aus dem Subject /orders/<id>.
func orderIDFromSubject(subject string) string {
	const prefix = "/orders/"
	if !strings.HasPrefix(subject, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(subject, prefix)
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

// snapshotLog ist nur für die Demo: gibt eine kurze Read-Model-Statistik aus.
func (p *projection) snapshotLog(ctx context.Context) string {
	rows, err := p.db.QueryContext(ctx,
		`SELECT status, count(*) FROM orders GROUP BY status ORDER BY status`)
	if err != nil {
		return "read-model: (Fehler: " + err.Error() + ")"
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err == nil {
			parts = append(parts, fmt.Sprintf("%s=%d", status, n))
		}
	}
	if len(parts) == 0 {
		return "read-model: leer"
	}
	return "read-model: " + strings.Join(parts, " ")
}
