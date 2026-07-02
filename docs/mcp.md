# MCP-Server — clio als Werkzeug für KI-Agenten

clio stellt seinen Event-Store über das **Model Context Protocol (MCP)** als
native, entdeckbare Werkzeugfläche bereit. Ein Agent (Claude Code o. ä.) kann
damit Events schreiben und lesen, CEL-Queries stellen, die gefaltete
Zustandssicht eines Subjects abrufen und Event-Schemas verwalten — ohne selbst
HTTP-Aufrufe zu bauen.

Die Entscheidung und ihre Begründung stehen in
[`adr/0042-mcp-server.md`](./adr/0042-mcp-server.md).

## Architektur in einem Absatz

Der MCP-Server ist ein **dünner Adapter über clios öffentliche HTTP-API**: jeder
Tool-Call wird in genau einen clio-HTTP-Request übersetzt. Die Geschäftslogik
(Auth-Scopes ADR-025/033, Preconditions, Zustandsfaltung ADR-039, Schema-
Validierung ADR-014) bleibt unverändert in den bestehenden Handlern — nichts
wird dupliziert. Das Protokoll (JSON-RPC 2.0) ist allein mit der
Standardbibliothek implementiert (kein SDK, ADR-001).

## Zwei Betriebsmodi

### 1. Eigenständiges Binary `clio-mcp`

Spricht MCP über **stdin/stdout** (Default, für lokale Agenten) oder über
**HTTP** (`-http`). Übersetzt Tool-Calls in HTTP-Requests gegen eine — auch
entfernte — clio-Instanz.

```
clio-mcp [-http host:port] [-base-url URL] [-token kid.secret]
```

| Flag / Env | Default | Bedeutung |
|---|---|---|
| `-http` | `""` (stdio) | `host:port` für den HTTP-Transport unter `POST /mcp` |
| `-base-url` / `CLIO_BASE_URL` | `http://127.0.0.1:3000` | Basis-URL der clio-Instanz |
| `-token` / `CLIO_API_TOKEN` | `""` | clio-API-Key `kid.secret` als Fallback-Identität |
| `-version` | — | Version ausgeben und beenden |

Beispiel (stdio) — Handshake und ein Query:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_query","arguments":{"subject":"/","recursive":true,"where":"event.type == \"order.placed\""}}}' \
  | clio-mcp -base-url http://127.0.0.1:3000 -token kid_xxx.secret
```

### 2. Eingebettet in cliostore unter `POST /mcp`

Opt-in per `CLIO_MCP=1`. cliostore mountet den MCP-Server auf demselben Port wie
die API; die Requests laufen in-process an den eigenen Handler (kein Loopback-
Socket).

```bash
CLIO_MCP=1 CLIO_BOOTSTRAP_ADMIN_KEY=… cliostore
# → POST http://127.0.0.1:3000/mcp
```

## Beispiel-Client-Konfiguration (Claude Code / Claude Desktop)

stdio-Variante, die gegen eine lokale clio-Instanz spricht:

```json
{
  "mcpServers": {
    "clio": {
      "command": "clio-mcp",
      "env": {
        "CLIO_BASE_URL": "http://127.0.0.1:3000",
        "CLIO_API_TOKEN": "kid_xxx.secret"
      }
    }
  }
}
```

## Authentifizierung & Sicherheit

Der MCP-Layer **authentifiziert nicht selbst**. Das `Authorization: Bearer
kid.secret` (ADR-025) des Aufrufers wird an clios API durchgereicht — es gelten
exakt dieselben Scopes wie für die REST-Fläche (`read`/`write`/`admin`, auch
Subject-/Prefix-Scopes ADR-033). Ein `write_events` schlägt fehl, wenn der
Schlüssel keinen `write`-Grant auf das Subject hat; clios Fehlermeldung (z. B.
`403`, `409` bei Precondition) kommt als Tool-Ergebnis mit `isError=true` zurück.

- **Eingebettet (`POST /mcp`):** jede MCP-Anfrage trägt den API-Key des
  Aufrufers im `Authorization`-Header.
- **Eigenständig (stdio/HTTP):** ohne durchgereichten Bearer greift das feste
  `-token`/`CLIO_API_TOKEN`.

> **Betriebshinweis:** Der `POST /mcp`-Endpunkt selbst ist ungeschützt (der Gate
> ist der API-Key). Betreibe ihn hinter denselben Netz-/Proxy-Schutzmaßnahmen wie
> die übrige API. Der eingebettete Mount ist per Default **aus**.

## Tool-Referenz

Jedes Tool bildet auf genau einen clio-Endpunkt ab.

| Tool | Endpunkt | Scope | Zweck |
|---|---|---|---|
| `ping` | `GET /api/v1/ping` | — | Liveness |
| `info` | `GET /api/v1/info` | read | Version, Uptime, DB-Belegung |
| `event_stats` | `GET /api/v1/event-stats` | read | Eventmengen über die Zeit (`by=source`) |
| `read_events` | `POST /api/v1/read-events` | read (Subject) | Events lesen (NDJSON) |
| `run_query` | `POST /api/v1/run-query` | read (Scope) | CEL-Filter + Projektion |
| `write_events` | `POST /api/v1/write-events` | write (Subject) | Events atomar anhängen (+ Preconditions) |
| `read_subjects` | `GET /api/v1/read-subjects` | read | Subjects auflisten (`prefix`, `tree`) |
| `read_event_types` | `GET /api/v1/read-event-types` | read | Event-Typen auflisten |
| `get_state` | `GET /api/v1/state/<subject>` | read (Subject) | Gefaltete Zustandssicht (ADR-039) |
| `register_event_schema` | `POST /api/v1/register-event-schema` | write (global) | JSON-Schema je Typ registrieren |
| `read_event_schema` | `GET /api/v1/read-event-schema` | read | Registriertes Schema lesen |
| `register_reduce_spec` | `POST /api/v1/register-reduce-spec` | write (global) | Reduce-Spec je Prefix (ADR-041) |
| `read_reduce_spec` | `GET /api/v1/read-reduce-spec` | read | Reduce-Specs lesen |
| `delete_reduce_spec` | `DELETE /api/v1/reduce-spec` | write (global) | Reduce-Spec entfernen |

**Bewusst nicht enthalten:** ein streamendes `observe`-Tool. MCP-Tool-Calls sind
Request/Response; wer Live-Streams braucht, nutzt `observe-events` bzw.
`GET /api/v1/events/<subject>?watch=true` direkt über die HTTP-API.

## MCP-Protokoll

Unterstützte Methoden: `initialize`, `notifications/initialized`, `ping`,
`tools/list`, `tools/call`. Erfolgreiche Antworten kommen als Text-Content
(clios JSON/NDJSON unverändert durchgereicht); Fehler als Content mit
`isError=true`, damit der Agent die Meldung lesen und reagieren kann.
