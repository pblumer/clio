# ADR-042: MCP-Server als HTTP-Adapter (stdio + HTTP, eigenständig & eingebettet)

**Status:** Akzeptiert (2026-07-02)

**Datum:** 2026-07-02

**Kontext**

KI-Agenten (Claude Code u. ä.) sprechen zunehmend das **Model Context Protocol
(MCP)**, um externe Systeme als native, entdeckbare Werkzeuge einzubinden. clio
ist ein generischer Event Store mit einer vollständigen, scope-geschützten
HTTP-API (Schreiben, Lesen, CEL-Query ADR-017, gefaltete Zustandssicht ADR-039,
Event-Schemas ADR-014). Damit ein Agent clio direkt benutzen kann — Events
anhängen, Streams lesen, Zustände abfragen — ohne selbst HTTP-Aufrufe zu
konstruieren, fehlt eine MCP-Fläche.

Kräfte und Randbedingungen:

- **Abhängigkeitsarmut (ADR-001).** Kein MCP-SDK; das Protokoll (JSON-RPC 2.0)
  ist schlank genug, um mit der Standardbibliothek hand-gerollt zu werden — wie
  bei den eigenen Metriken (ADR-013) und der eingebetteten OpenAPI (ADR-011).
- **Keine Duplikation der Geschäftslogik.** Auth-Scopes (ADR-025/033),
  Preconditions (Optimistic Concurrency), die Zustandsfaltung (ADR-039/040/041)
  und die Schema-Validierung (ADR-014) leben in den `internal/httpapi`-Handlern.
  Ein MCP-Server darf diese Logik nicht ein zweites Mal implementieren.
- **Single-Instance (ADR-002).** Der Store liegt hinter dem HTTP-Server; die
  öffentliche Fläche ist die HTTP-API, nicht ein in-process-Store-Zugriff.
- **Zwei Nutzungsmodi.** Lokale Agenten erwarten den **stdio**-Transport; ein
  Betrieb hinter Reverse-Proxy erwartet **HTTP**. Beide sollen dieselbe
  Tool-Fläche bieten, eigenständig **und** in cliostore eingebettet.

**Entscheidung**

1. **Dünner Adapter über die HTTP-API, nicht über den Store.** Der MCP-Server
   (`internal/mcp`) übersetzt jeden Tool-Call in genau **einen** clio-HTTP-Request
   und setzt ihn über einen `http.RoundTripper` ab. Die Geschäftslogik bleibt
   unverändert in den bestehenden Handlern (Single Source of Truth).

2. **JSON-RPC 2.0 hand-gerollt, nur Standardbibliothek** (kein SDK, ADR-001), mit
   zwei Transports: **stdio** (`Serve`, zeilengetrennte Nachrichten) und **HTTP**
   (`ServeHTTP`, eine Nachricht je POST). MCP-Methoden: `initialize`,
   `notifications/initialized`, `ping`, `tools/list`, `tools/call`.

3. **Volle Tool-Fläche (lesen + schreiben)**, jedes Tool ↔ ein Endpunkt:
   `ping`, `info`, `event_stats`, `read_events`, `run_query`, `write_events`,
   `read_subjects`, `read_event_types`, `get_state`, `register_event_schema`,
   `read_event_schema`, `register_reduce_spec`, `read_reduce_spec`,
   `delete_reduce_spec`. Domänenfehler (clio-4xx/5xx) werden als Tool-Ergebnis mit
   `isError=true` gemeldet — der Agent liest die Meldung, statt einen abstrakten
   Protokollfehler zu bekommen.

4. **Auth wird durchgereicht, nicht neu erfunden.** Der MCP-Layer authentifiziert
   nicht selbst. Das `Authorization: Bearer kid.secret` (ADR-025) des Aufrufers
   wird pro Anfrage an clios API weitergereicht; damit greifen exakt dieselben
   Scopes (ADR-025/033) wie für die REST-Fläche. Fehlt ein Bearer, nutzt der
   eigenständige Client sein festes Fallback-Token (`-token`/`CLIO_API_TOKEN`).

5. **Zwei Modi, ein Paket.**
   - **Eigenständiges Binary** `cmd/clio-mcp`: `-http host:port` (leer = stdio),
     `-base-url` (`CLIO_BASE_URL`), `-token` (`CLIO_API_TOKEN`). Ein echter
     `*http.Client` gegen eine — auch entfernte — clio-Instanz.
   - **Eingebettet** in cliostore unter `POST /mcp`, opt-in per `CLIO_MCP`
     (Default aus). Ein **In-Process-`http.RoundTripper`** (`HandlerTransport`)
     verteilt die Requests direkt an den eigenen Mux — kein Loopback-Socket,
     dieselbe Fläche samt Auth ohne Duplikat.

**Konsequenzen**

- *Positiv:* Eine einzige Quelle der Wahrheit für die Geschäftslogik; ein Agent
  kann clio als natives Werkzeug entdecken und nutzen. Keine neue Abhängigkeit
  (ADR-001). Scopes gelten unverändert (ADR-025/033), da die echte API die
  Autorisierung macht. Der eingebettete Modus ist rückwärtskompatibel
  (Default aus), der eigenständige Modus funktioniert gegen jede clio-Instanz.
- *Negativ / Grenzen:* Der eingebettete Modus nimmt einen In-Process-HTTP-
  Roundtrip in Kauf (die Antwort wird gepuffert) — für die Tool-Semantik
  unkritisch. **Kein Streaming:** `observe-events` ist bewusst **nicht** Teil der
  Tool-Fläche, weil MCP-Tool-Calls Request/Response sind; wer Live-Streams
  braucht, nutzt die HTTP-API direkt. Der MCP-Endpunkt selbst ist ungeschützt
  (nur der Transport) — der Zugriffsschutz liegt vollständig im durchgereichten
  API-Key; der Endpunkt gehört hinter dieselben Netz-/Proxy-Schutzmaßnahmen wie
  die API. Anders als ein reiner In-Memory-Cache (Vergleich zu Werkzeugen mit
  zustandsloser Engine) ist clios Fläche zustandsbehaftet; der Adapter reicht
  daher an die laufende Instanz durch, statt Zustand zu teilen.

**Offene Punkte / Folge-ADRs**

- Optionaler eigener Gate-Token bzw. mTLS speziell für den `POST /mcp`-Endpunkt,
  falls ein Betrieb den MCP-Zugang zusätzlich zur API-Key-Prüfung einschränken
  will (heute bewusst nicht nötig, da der API-Key der Gate ist).
- Ein streaming-fähiges `observe`-Tool, falls MCP-Clients Server-initiierte
  Notifications zuverlässig konsumieren (heute zurückgestellt).
- MCP-**Resources**/**Prompts** (über Tools hinaus), z. B. Subjects als
  browsebare Ressourcen — additiv nachrüstbar.
