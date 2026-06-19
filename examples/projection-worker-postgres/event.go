package main

import "encoding/json"

// Event ist die für die Projektion relevante Teilmenge eines clio-Events
// (CloudEvents). Bewusst lokal definiert: clios internes event-Paket ist nicht
// importierbar (internal/), und ein Read-Model-Consumer soll ohnehin nur von der
// öffentlichen API/dem JSON-Format abhängen — nicht von clio-Interna.
type Event struct {
	ID      string          `json:"id"` // global monotone Sequenz (als String)
	Source  string          `json:"source"`
	Subject string          `json:"subject"` // z. B. /orders/o-42
	Type    string          `json:"type"`    // z. B. order.placed
	Time    string          `json:"time"`
	Data    json.RawMessage `json:"data"`
}

// orderPlaced ist die Payload von order.placed.
type orderPlaced struct {
	Customer   string `json:"customer"`
	TotalCents int64  `json:"totalCents"`
}

// orderShipped ist die Payload von order.shipped.
type orderShipped struct {
	Carrier    string `json:"carrier"`
	TrackingID string `json:"trackingId"`
}

// orderCancelled ist die Payload von order.cancelled.
type orderCancelled struct {
	Reason string `json:"reason"`
}
