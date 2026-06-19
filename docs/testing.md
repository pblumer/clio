# Tests & Qualitätsnachweise

> Was getestet ist, wie man es ausführt — und welche Lücken bewusst offen sind.
> Ehrlichkeit vor Vollständigkeit (vgl. [`production-readiness.md`](production-readiness.md)).

## Ausführen

```bash
make test     # alle Unit-/Integrationstests (go test ./...)
make race     # mit Race-Detector
make cover    # paketübergreifende Gesamt-Coverage
make bench    # Store-Benchmarks
make smoke    # Server starten + Postman-Collection (Newman) — braucht npx
make lint     # gofmt-Check + go vet
```

Das Beispielmodul hat eigene Tests: `cd examples/projection-worker-postgres && go test ./...`.

## Coverage (Stand dieser Arbeit)

**Paketübergreifend: ~87 %** (`go test -coverpkg=./...`). Per Paket (Self-Coverage):

| Paket | Coverage |
|---|---|
| `internal/eventstats` | 100 % |
| `internal/pubsub` | 100 % |
| `internal/config` | 95 % |
| `internal/event` | 95 % |
| `internal/query` | 96 % |
| `internal/metrics` | 93 % |
| `internal/webui` | 93 % |
| `internal/auth` | 90 % |
| `internal/httpapi` | 89 % |
| `internal/store` | 85 % |
| `cmd/cliostore` | 72 % |

`cmd/cliostore` liegt niedriger, weil der Server-Lauf (`run`, Signal-Handling,
`ListenAndServe`) bewusst nicht im Unit-Test gestartet wird — das deckt `make
smoke` (echter Prozess + HTTP-Roundtrips) ab.

## Abdeckung je Reifebereich

| Bereich | Tests (Auswahl) |
|---|---|
| **Backup/Restore/Verify** | `store_backup_test.go` (E2E: Backup→Löschen→Restore→Verify→Replay→Folge-Append, INV-R1/2/3), Offline-Backup, Lock-Einschränkung, korrupte/fehlende Quelle, Read-only-Verify; `backup_test.go` (CLI); `internal/httpapi/backup_test.go` (HTTP, 401/403) |
| **Key-Lifecycle** | `auth/keyring_lifecycle_test.go` (Expiry/Usable, Metadaten), `store_authkeys_rotate_test.go` (Rotation), `httpapi/keys_lifecycle_test.go` (Ablauf→401, Rotation, Scope-Trennung), `cmd/cliostore/keys_test.go` (CLI + Audit) |
| **Audit-Log** | `store_audit_test.go` (Append/List/Reihenfolge, failure, Überleben des Reset), `httpapi/audit_admin_test.go` (Admin-Aktion→Eintrag, fehlgeschlagene Aktion, Lese-Scopes, kein Secret, Auditor darf nicht schreiben) |
| **Query-Timeout** | `httpapi/runquery_resilience_test.go`: `TestRunQueryDeadlineAborts`, `TestRunQueryDeadlineDisabledByDefault` |
| **Heartbeat / Resilienz** | `TestRunQueryHeartbeatBeforeFirstHit`, `TestRunQueryUnderConcurrentWriteLoad`, Index-Warnung |
| **Observe Burst Flush (Regression)** | `TestObserveWatchKeepsUpWithBurst`; Broker: `TestOverflowMarksLost` |
| **Data-Index (ADR-029)** | `store/store_typeidx_test.go`, `store_*dataidx*`, `query/query_dataidx_test.go`, `httpapi/runquery_dataidx_test.go` |
| **Compaction** | `store_compress_test.go`/`store_reopen_test.go` u. a. (Reopen-Guard, Größen); Offline-`Compact`/`CompactInPlace` |
| **Hash-Kette/Signatur** | `store_hash_test.go`, `store_signing_test.go`, `event/event_test.go` |

## Bekannte Lücken (bewusst)

- **`cmd/cliostore` Serverlauf**: `run()`/Graceful-Shutdown nicht als Unit-Test —
  durch `make smoke` (Prozess-Ebene) abgedeckt, nicht durch Coverage gezählt.
- **`internal/store` 85 %**: einige Fehlerpfade (I/O-Fehler beim Reopen,
  seltene bbolt-Fehler) sind schwer deterministisch auszulösen und ungetestet.
- **Kein Chaos-/Fault-Injection-Test** (`kill -9` mitten im Write, voller
  Datenträger, partielle fsyncs). Ein `make chaos-smoke` ist als optionaler
  Folgeschritt notiert, aber nicht umgesetzt — solche Tests bräuchten eine
  kontrollierte Umgebung (cgroups/Loop-Device) außerhalb des reinen `go test`.
- **`examples/projection-worker-postgres`**: nur die reinen Hilfsfunktionen sind
  unit-getestet; der Postgres-/observe-Pfad wird über die Compose-Demo manuell
  verifiziert (kein DB-Integrationstest in CI).
- **Lastspitzen/Soak**: Benchmarks (`make bench`) existieren, aber kein
  langlaufender Soak-Test.

## CI

`make lint` (gofmt + vet) und `make test` laufen in der GitHub-Action; `make race`
und `make smoke` sind die empfohlenen zusätzlichen Gates vor einem Release.
