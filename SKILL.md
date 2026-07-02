---
name: clio-event-store
version: 1.0.0
description: >
  Betrieb und Deployment von cliostore — dem Go-basierten Event Store.
  Build, Deploy, Health-Checks, Event Import/Export, DB-Wartung.
triggers:
  - "clio deploy"
  - "clio health"
  - "clio events"
  - "event store"
  - "cliostore"
---

# clio Event Store — Operations Skill

## Quick Reference

| Ressource | URL |
|---|---|
| Repo | `https://github.com/pblumer/clio.git` |
| Prod | `https://clio.blumer.cloud` (server01) |
| Dev  | `http://pi-server-03:3000` (lokaler dev) |
| API  | OpenAPI/Swagger UI unter `/docs` |
| MCP  | `https://clio-mcp.blumer.cloud/mcp` |

## Auth

ADR-025 Keyring: `kid.secret`-Format. Agent-Token liegt unter
`/home/pi/clio_token.txt`. Im Docker-Container via `CLIO_API_KEY`.

## Build

```bash
make build        # Binary bauen
make test         # Tests
make race         # Race-Condition-Check
make lint         # golangci-lint
```

**Qualitätstor:** `make lint && make test && make race`

## Deploy (Ansible)

Aus `cloud.blumer.home` Repo:

```bash
ansible-playbook -i inventory.yaml ansible/playbooks/vps.yml \
  --limit server01 --tags clio --diff --ask-vault-pass
```

## Health

```bash
# HTTP API
curl -s https://clio.blumer.cloud/api/v1/ping

# MCP (JSON-RPC 2.0)
curl -s -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"info","arguments":{}}}' \
  https://clio-mcp.blumer.cloud/mcp
```

## Events schreiben (via MCP)

```bash
TOKEN=$(cat /home/pi/clio_token.txt)
curl -s -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "write_events",
      "arguments": {
        "events": [
          {"source": "/hermes", "subject": "/test/1", "type": "demo", "data": {"msg": "hi"}}
        ]
      }
    }
  }' https://clio-mcp.blumer.cloud/mcp
```

## Events lesen (via MCP)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "read_events",
      "arguments": {"subject": "/test/1"}
    }
  }' https://clio-mcp.blumer.cloud/mcp
```

## DB-Kapazität checken

```bash
# Prometheus-metrics
curl -s https://clio.blumer.cloud/metrics | grep clio_db_

# Via MCP info — beachten:
# databaseFillPercent = used/data_bytes (interner Compaction-Indikator)
# NICHT der Nutzungsgrad der 4GB-Datei.
```

## Troubleshooting

| Problem | Lösung |
|---|---|
| `ContainerConfig` beim Deploy | docker-compose V1 Bug; Rescue-Block in Rolle |
| DB voll | `cliostore compact` oder `CLIO_DB_INITIAL_MB` erhöhen |
| 409 Precondition Failed | Neue Event-ID prüfen |
| Auth denied | Token-Format `kid.secret` prüfen |

## Links

- [ARCHITECTURE.md](./ARCHITECTURE.md) — SSoT
- [docs/adr/](./docs/adr/) — Entscheidungsarchiv
- [AGENTS.md](./AGENTS.md) — Ablageregeln
