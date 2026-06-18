# M09 — Observability

> **Tracks:** Betrieb · **Dauer:** ~20 Min

## Lernziele

- Das strukturierte Request-Logging verstehen.
- Die Prometheus-Metriken unter `/metrics` scrapen und interpretieren.
- Sinnvolle Alarme ableiten.

## Voraussetzungen

- [M08](M08-betrieb-und-durability.md). Idealerweise eine Prometheus-Instanz.

## Inhalt

### Strukturierte Logs

Jede Anfrage wird strukturiert geloggt (slog): Methode, Route, Status, Dauer.
Als Route-Label dient das **gematchte Mux-Pattern** (`r.Pattern`) — dadurch
bleibt die Kardinalität niedrig, obwohl Pfade variabel sind
([ADR-013](../../../ARCHITECTURE.md#adr-013-eigene-abhängigkeitsfreie-metriken-statt-prometheus-client)).

### Metriken: /metrics

Clio rendert Prometheus-Textformat **ohne** externe Client-Bibliothek (passt zum
abhängigkeitsfreien Binary). `/metrics` ist **ohne Auth** erreichbar — im
Betrieb per Netz/Proxy absichern.

```bash
curl http://127.0.0.1:3000/metrics
```

### Die wichtigsten Metriken

| Metrik | Typ | Aussage |
|---|---|---|
| `clio_http_requests_total{method,route,status}` | Counter | Request-Volumen & Fehlerquote (Status `5xx`/`4xx`) |
| `clio_http_request_duration_seconds` | Histogramm | Latenzverteilung |
| `clio_events_written_total` | Counter | Schreibdurchsatz |
| `clio_precondition_failures_total` | Counter | 409-Konflikte (Optimistic-Concurrency-Druck) |
| `clio_active_observers` | Gauge | aktuell offene Observe-Verbindungen |
| `clio_events_total` | Gauge | Gesamtzahl Events |
| `clio_db_size_bytes` | Gauge | DB-Dateigröße (→ wann `compact`?) |
| `clio_db_data_bytes` | Gauge | tatsächlich genutzter Umfang (Highwater-Mark); bei vorbelegter Datei kleiner als `clio_db_size_bytes` |
| `clio_db_initial_bytes` | Gauge | vorbelegte Grenze (`CLIO_DB_INITIAL_MB`); nur gesetzt, wenn vorbelegt |

### Woraus du Alarme baust

- **Fehlerrate:** Anstieg von `clio_http_requests_total{status=~"5.."}`.
- **Latenz:** hohe Quantile aus `clio_http_request_duration_seconds`.
- **Konfliktdruck:** stark steigende `clio_precondition_failures_total` deutet
  auf heiße Streams / zu grobe Aggregat-Grenzen hin.
- **Plattenwachstum:** `clio_db_size_bytes` als Kapazitäts- und
  `compact`-Indikator.
- **Remap-Headroom:** Nähert sich `clio_db_data_bytes` der Grenze
  `clio_db_initial_bytes`, drohen Schreib-Latenzspitzen — `CLIO_DB_INITIAL_MB`
  erhöhen und neu starten (der Server warnt zusätzlich im Log).
- **Observer-Lecks:** dauerhaft wachsende `clio_active_observers`.

### Health/Deploy-Check

`/api/v1/info` (Auth nötig) liefert `version`, `uptimeSeconds`, `eventsTotal`,
`syncMode` — ideal für Deploy-Verifikation und einen leichten Health-Check.

## Hands-on

1. Scrape `/metrics`, schreibe Events und beobachte `clio_events_written_total`
   und `clio_events_total` steigen.
2. Provoziere einen 409 (zweites `opened` aus [M04](M04-optimistic-concurrency.md))
   und sieh `clio_precondition_failures_total` steigen.

## Checkpoint

1. Warum ist das Route-Label das *Mux-Pattern* und nicht der konkrete Pfad?
2. Welche Metrik nutzt du als Trigger für `compact`?
3. Was könnte eine dauerhaft steigende `clio_active_observers` bedeuten?

→ [Lösungen](../uebungen/loesungen.md#m09)

---

**Weiter:** [M07 — Integrität & Signaturen](M07-integritaet-und-signaturen.md)
(verify als Betriebs-Check) oder zurück zum
[Betrieb-Track](../rollen/betrieb.md).
