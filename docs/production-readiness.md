# Production Readiness

> Ehrliche Aussage, **wofür clio geeignet ist und wofür nicht** — plus konkrete
> Betriebseinstellungen je Einsatzprofil. Verwandt: [`threat-model.md`](threat-model.md),
> [`backup-restore.md`](backup-restore.md), [`security.md`](security.md),
> [`audit.md`](audit.md).

clio ist bewusst ein **kleiner, nachvollziehbarer Single-Instance Event Store**.
Diese Seite hilft zu entscheiden, ob das zu deinem Einsatz passt.

---

## 1. Einsatzprofile

| Profil | Eignung | Kurz |
|---|---|---|
| **Dev** | ✅ ideal | lokal, schnelle Iteration, Dev-Mode erlaubt |
| **Lab / Demo / Internal Tooling** | ✅ gut | interne Tools, PoCs, Schulungen, kleine Teams |
| **Small Production** | ✅ mit Betrieb | echter Betrieb mit Backup, Monitoring, Reverse Proxy, Disk-Headroom |
| **Critical Production** | ⚠️ nicht empfohlen | wo Single-Node-Ausfall = inakzeptabel: clio hat **kein Failover** |
| **HA-Plattform / Multi-Tenant SaaS** | ❌ nicht unterstützt | kein Clustering, keine horizontale Skalierung, keine Mandantenisolation |

> **Faustregel:** clio glänzt dort, wo ein **append-only Source-of-Truth-Log mit
> sauberer Operability** gebraucht wird und ein **kurzer, geplanter Ausfall mit
> schnellem Restore** akzeptabel ist (RTO Minuten, RPO = Backup-Intervall). Wo
> Null-Ausfall, Geo-Redundanz oder horizontale Schreib-Skalierung gefordert sind,
> ist clio die falsche Wahl.

---

## 2. Garantien

- **Append-only.** Events werden nie verändert oder gelöscht (Compaction
  defragmentiert nur; Dev-Reset ist explizit dev-only).
- **Atomare Writes.** Alle Events eines `write-events`-Aufrufs landen in **einer**
  Transaktion — alles-oder-nichts (ADR-003, bbolt-ACID).
- **Optimistic Concurrency.** Preconditions (`isSubjectOnEventId`,
  `isQueryResultEmpty/NonEmpty`) erlauben konfliktfreie nebenläufige Writes (409
  bei Verletzung, ADR-017).
- **Globale, monotone IDs.** Jede Event-`id` ist eine global monoton steigende
  Sequenz — stabile Ordnung und Cursor (ADR-006).
- **Tamper-Evidence** (sofern genutzt). Hash-Kette (`verify`, ADR-012) und
  optional Ed25519-Signaturen (`CLIO_SIGNING_KEY`, ADR-016) — siehe
  Vertrauensgrenzen im [`threat-model.md`](threat-model.md) §11.
- **Durability.** Mit `CLIO_SYNC=group` (Default) oder `always` sind committete
  Writes `fsync`-durabel.

## 3. Nicht-Garantien (bewusst)

- **Kein Clustering.** Eine Instanz, eine Datei.
- **Kein automatisches Failover.** Fällt der Node aus, steht der Dienst bis zum
  Neustart/Restore.
- **Keine horizontale Skalierung.** Schreiben ist serialisiert (ein Writer);
  Lesen skaliert nur vertikal.
- **Keine Mandantenplattform.** Scopes trennen Rechte, **nicht** Datenräume.
- **Kein Ersatz für Kafka.** Keine Consumer Groups, Partitionen, Rebalancing.
- **Kein Ersatz für Reporting/BI.** Keine Aggregation/Joins/Volltext im Kern —
  dafür ein **abgeleitetes Read Model** bauen (siehe
  [`examples/projection-worker-postgres`](../examples/projection-worker-postgres/)).

---

## 4. Betriebsanforderungen (Checkliste für „Small Production")

- [ ] **Backup** automatisiert (Hot-Backup via `GET /api/v1/backup` oder Cold-CLI),
      jedes Backup `verify`-t — [`backup-restore.md`](backup-restore.md).
- [ ] **Restore-Test** als wiederkehrender Drill (nicht nur im Notfall).
- [ ] **Monitoring**: `/metrics` (Prometheus) scrapen — Events, DB-Füllgrad,
      Disk-Free, aktive Observer; Alarm auf Disk-Headroom.
- [ ] **Disk-Headroom**: genug Platz für Wachstum **+** Compaction (temporär
      ~Dateigröße) **+** Backups; bei Vorbelegung `CLIO_DB_INITIAL_MB` setzen.
- [ ] **Compaction-Fenster**: `CLIO_DB_COMPACT_ENABLED` mit Intervall in einer
      ruhigen Phase (kurze Online-Downtime pro Lauf) **oder** geplant offline.
- [ ] **Query-Timeout**: `CLIO_QUERY_TIMEOUT` **setzen** (Default aus!) gegen
      breite Scans.
- [ ] **Reverse Proxy**: TLS-Terminierung, Buffering aus, großzügige Timeouts für
      `observe`/`run-query` (siehe §6).
- [ ] **Auth**: Bootstrap-Variable nach Erststart entfernen, benannte Keys mit
      minimalem Scope, `CLIO_DEV_MODE` aus — [`security.md`](security.md).
- [ ] **Audit** off-host exportieren (`GET /api/v1/audit`) —
      [`audit.md`](audit.md).

---

## 5. Empfohlene Betriebsprofile (konkrete Settings)

> Vollständige Env-Werte je Profil: [`operations-profiles.md`](operations-profiles.md).
> Fertige Vorlagen (systemd/docker/windows/k8s): [`deploy/`](../deploy/).

### Dev
```bash
CLIO_DEV_MODE=true
CLIO_SYNC=off              # Durchsatz vor Crash-Durability — egal in Dev
CLIO_BOOTSTRAP_ADMIN_KEY=dev-admin-secret-please-change
# Query-Timeout, Signing, Backup: optional
```

### Lab / Internal
```bash
CLIO_SYNC=group
CLIO_QUERY_TIMEOUT=10s
CLIO_DB_COMPACT_ENABLED=true
CLIO_DB_COMPACT_INTERVAL_H=24
CLIO_BOOTSTRAP_ADMIN_KEY=...   # nach Erststart entfernen
# Dev-Mode AUS. Backup täglich + verify.
```

### Small Production
```bash
CLIO_ADDR=127.0.0.1:3000          # nur lokal; TLS/Public über Reverse Proxy
CLIO_SYNC=group                   # voll durabel bei hohem Durchsatz
CLIO_QUERY_TIMEOUT=15s            # breite Scans begrenzen
CLIO_DB_INITIAL_MB=1024           # Mmap vorbelegen → keine Remap-Latenzspitzen
CLIO_DB_GROW_THRESHOLD_PCT=80     # Headroom-Warnung
CLIO_DB_COMPACT_ENABLED=true
CLIO_DB_COMPACT_INTERVAL_H=24     # nachts/ruhig
CLIO_SIGNING_KEY=...              # optional: Event-Authentizität
CLIO_DEV_MODE=false               # NIEMALS true in Produktion
# Auth: CLIO_BOOTSTRAP_ADMIN_KEY nur zum Erststart, danach entfernen.
# Betrieb: Hot-Backup nächtlich + verify; /metrics scrapen; Audit off-host.
```

---

## 6. Reverse-Proxy-Hinweise (Kurzfassung)

clio terminiert kein TLS und sendet Heartbeats für lange Streams. Der Proxy muss
**Response-Buffering ausschalten** und für `observe`/`run-query` **großzügige
Timeouts** erlauben, sonst kappt er lange Streams.

**nginx:**
```nginx
location /api/v1/ {
    proxy_pass http://127.0.0.1:3000;
    proxy_buffering off;                 # Streams sofort durchreichen
    proxy_read_timeout 1h;               # observe/run-query laufen lange
    proxy_http_version 1.1;
    proxy_set_header Connection "";
}
```

Server-seitige Timeouts in clio (fix): `ReadHeaderTimeout` 5 s (Slowloris-Schutz),
`WriteTimeout` 30 s (für Streams bewusst aufgehoben), `IdleTimeout` 120 s. Der
Proxy sollte diese nicht unterschreiten.
