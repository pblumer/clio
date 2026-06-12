# M10 — Tour durch die Codebasis

> **Tracks:** Contributor · **Dauer:** ~30 Min

## Lernziele

- Die Paketstruktur von Clio kennen und wissen, wo was lebt.
- Den **Datenfluss** eines Writes von HTTP bis Storage nachzeichnen.
- Die Test- und Build-Konventionen verstehen.

## Voraussetzungen

- Solide Go-Kenntnisse. [`ARCHITECTURE.md`](../../../ARCHITECTURE.md) gelesen
  (Roadmap §6, ADRs §7).

## Inhalt

### Paketüberblick

```
cmd/cliostore/main.go     ← Einstiegspunkt: CLI-Subcommands (version, gen-key,
                             compact), Config laden, Server starten
internal/
  config/   config.go     ← Env-Variablen einlesen & validieren (CLIO_*)
  event/    event.go      ← CloudEvents-Typen: Candidate & Event, Felder
  store/    store.go      ← Kern: bbolt, Write/Read/Observe-Anbindung,
                             monotone IDs, Hash-Kette, Preconditions
            store_schema.go   ← JSON-Schema-Registry & Validierung (ADR-014)
            store_signing.go  ← Ed25519-Signaturen (ADR-016)
            store_compact.go  ← Offline-Defragmentierung (ADR-015)
  pubsub/   broker.go     ← Channels für observe-events; langsame Subscriber
                             werden abgehängt (Stufe 2)
  query/    query.go      ← CEL-Prädikat-Layer: kompilieren & auswerten (ADR-017)
  httpapi/  server.go     ← HTTP-Mux, Auth-Middleware, alle Handler, Logging/
                             Metrics-Middleware
  metrics/  metrics.go    ← abhängigkeitsfreie Counter/Gauge/Histogramme,
                             Prometheus-Textformat (ADR-013)
  apidocs/  apidocs.go    ← eingebettete OpenAPI-Spec + Swagger UI (ADR-011)
            openapi.yaml      ← handgepflegte API-Spec (bei Änderungen mitziehen!)
```

### Routing als Landkarte

Der schnellste Einstieg ist die Routen-Registrierung in
`internal/httpapi/server.go`. Dort siehst du auf einen Blick, welcher Pfad zu
welchem Handler führt — `requireAuth(...)` markiert die geschützten Datenrouten,
`ping`/`metrics`/`docs` laufen ohne Auth.

### Datenfluss eines Writes (nachzeichnen)

1. **HTTP-Handler** (`handleWriteEvents` in `server.go`): Request-Body parsen
   (`events` + optionale `preconditions`), Auth ist via Middleware schon
   passiert.
2. **Store** (`internal/store/store.go`): in **einer** bbolt-Transaktion —
   Preconditions prüfen, monotone IDs vergeben, ggf. Schema validieren
   (`store_schema.go`), Hash-Kette fortschreiben, ggf. signieren
   (`store_signing.go`), Events persistieren.
3. **Pub/Sub** (`internal/pubsub/broker.go`): neue Events an offene
   Observer-Verbindungen verteilen.
4. **Antwort**: gespeicherte Events zurück, Metriken aktualisiert.

Diese **eine serialisierte Schreibstelle** ist der Grund für strikte Ordnung und
Atomarität „gratis"
([ADR-003](../../../ARCHITECTURE.md#adr-003-serialisierte-schreibvorgänge-für-ordnung--atomarität)).

### Tests & Build

- **Tests neben dem Code:** `*_test.go` je Paket (z. B. `store_test.go`,
  `store_signing_test.go`, `server_test.go`). Benchmarks in
  `store_bench_test.go`.
- **Grün halten:**
  ```bash
  go test ./...
  go test -race ./...   # Nebenläufigkeit: Observe, Group Commit
  ```
- **Build/Dist:** `make build`, `make dist`, `make docker` (siehe `Makefile`);
  Version via `-ldflags "-X main.version=..."`.
- **Format:** `gofmt` vor dem Commit.

## Hands-on

1. Öffne `internal/httpapi/server.go` und finde die Routen-Registrierung. Liste
   für dich auf, welche Routen `requireAuth` haben und welche nicht.
2. Folge `handleWriteEvents` → in den Store. Wo genau werden die Preconditions
   geprüft, und warum *innerhalb* der Transaktion?

## Checkpoint

1. In welchem Paket lebt die Hash-Ketten-Logik, in welchem die
   CEL-Auswertung?
2. Warum müssen Preconditions in derselben Transaktion wie der Write geprüft
   werden?
3. Welche Datei musst du **zusätzlich** anfassen, wenn du eine neue Route
   hinzufügst — über den Handler hinaus?

→ [Lösungen](../uebungen/loesungen.md#m10)

---

**Weiter:** [M11 — Ein Feature mit Test & ADR beitragen](M11-feature-mit-adr.md)
