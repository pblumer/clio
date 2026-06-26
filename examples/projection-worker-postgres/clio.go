package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// clioClient kapselt den Zugriff auf die clio-HTTP-API (observe + info). Bewusst
// nur net/http + encoding/json — keine SDK-Abhängigkeit.
type clioClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newClioClient(baseURL, token string) *clioClient {
	return &clioClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		// Kein Client-Timeout: observe ist ein langlebiger Stream. Die Lebensdauer
		// steuert der übergebene Context (Shutdown/Reconnect).
		http: &http.Client{},
	}
}

// observe öffnet den observe-Stream für subject (rekursiv) ab dem per-Partition-
// Cursor und ruft fn für jedes empfangene Event auf. cursor ist partition → zuletzt
// verarbeitete Sequenz; der Server resümiert je Partition ab Sequenz+1 (ADR-036).
// Ein leerer Cursor liefert die gesamte History. Leerzeilen sind Heartbeats
// (ADR-028) und werden übersprungen. Der Aufruf kehrt zurück, wenn der Context
// abgebrochen wird, der Server die Verbindung schließt oder fn einen Fehler liefert
// (dann wird neu verbunden — siehe runWorker).
func (c *clioClient) observe(ctx context.Context, subject string, cursor map[int]uint64, fn func(Event) error) error {
	body := map[string]any{"subject": subject, "recursive": true}
	if len(cursor) > 0 {
		// Per-Partition-Cursor: der Server liest je Partition ab cursor[p]+1.
		body["cursor"] = cursor
	}
	buf, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/observe-events", strings.NewReader(string(buf)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("observe verbinden: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("observe HTTP %d (token/scope prüfen)", resp.StatusCode)
	}

	sc := bufio.NewScanner(resp.Body)
	// NDJSON-Zeilen können groß sein (große Payloads) — Puffer erhöhen.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue // Heartbeat
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return fmt.Errorf("event-zeile dekodieren: %w", err)
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("observe-stream lesen: %w", err)
	}
	return nil // Server hat den Stream beendet → Aufrufer verbindet neu
}

// totalEvents liefert die Gesamtzahl der Events im Store (aus /api/v1/info) —
// Grundlage für die Lag-Berechnung (Store-Spitze minus Checkpoint).
func (c *clioClient) totalEvents(ctx context.Context) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/info", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("info HTTP %d", resp.StatusCode)
	}
	var info struct {
		EventsTotal uint64 `json:"eventsTotal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, err
	}
	return info.EventsTotal, nil
}
