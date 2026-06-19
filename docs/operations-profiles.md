# Betriebsprofile — empfohlene Einstellungen

> Konkrete Umgebungsvariablen je Einsatzprofil. Hintergrund & Eignung:
> [`production-readiness.md`](production-readiness.md). Fertige Vorlagen:
> [`deploy/`](../deploy/).

clio wird vollständig über **Umgebungsvariablen** konfiguriert (keine Config-
Datei). Diese Seite bündelt sinnvolle Werte für drei Profile.

## Übersicht aller Variablen

| Variable | Default | Bedeutung |
|---|---|---|
| `CLIO_ADDR` | `:3000` | HTTP-Listen-Adresse |
| `CLIO_DB_PATH` | `clio.db` | Pfad zur bbolt-Datei |
| `CLIO_SYNC` | `group` | Durability: `group` \| `always` \| `off` |
| `CLIO_QUERY_TIMEOUT` | `0` (aus) | Scan-Deadline für `run-query` |
| `CLIO_DB_INITIAL_MB` | `0` | Mmap/Datei vorbelegen (verhindert Remap-Latenzspitzen) |
| `CLIO_DB_GROW_THRESHOLD_PCT` | `80` | Warnschwelle des Headroom-Monitors |
| `CLIO_DB_MONITOR_INTERVAL` | `60s` | Intervall des Headroom-Monitors |
| `CLIO_DB_COMPACT_ENABLED` | `false` | Online-Compaction-Scheduler |
| `CLIO_DB_COMPACT_INTERVAL_H` | `6` | Compaction-Intervall (Stunden) |
| `CLIO_COMPRESS` | `false` | DEFLATE-Kompression der Event-Werte |
| `CLIO_SIGNING_KEY` | — | Ed25519-Seed (Event-Signaturen) |
| `CLIO_EVENT_AUTHORSHIP` | `false` | `kid` des Schreibers als `clioauthkid` ins Event |
| `CLIO_OBSERVE_PREAMBLE_BYTES` | `4096` | Anti-Buffering-Preamble für Streams |
| `CLIO_DATA_INDEX_FIELDS` | — | Sekundärindex auf `event.data`-Felder (`typ:feld,…`) |
| `CLIO_BOOTSTRAP_ADMIN_KEY` | — | Erststart-Admin-Geheimnis (danach entfernen) |
| `CLIO_API_TOKEN` | — | **deprecated** Legacy-Bootstrap (siehe `security.md`) |
| `CLIO_DEV_MODE` | `false` | **destruktive** Dev-Routen (nur Entwicklung!) |

> Feste Server-Timeouts (nicht konfigurierbar): `ReadHeaderTimeout` 5 s
> (Slowloris-Schutz), `WriteTimeout` 30 s (für Streams bewusst aufgehoben),
> `IdleTimeout` 120 s.

---

## Profil: `dev`

Lokal, schnelle Iteration. Durchsatz vor Crash-Durability; destruktive Helfer an.

```bash
CLIO_DEV_MODE=true
CLIO_SYNC=off
CLIO_BOOTSTRAP_ADMIN_KEY=dev-admin-secret-change-me
# optional: CLIO_QUERY_TIMEOUT, CLIO_SIGNING_KEY
```

- **Backup:** optional. **Reverse Proxy:** nicht nötig. **Monitoring:** nicht nötig.

## Profil: `lab`

Interne Tools, Demos, Schulungen. Echte Durability, aber entspannter Betrieb.

```bash
CLIO_SYNC=group
CLIO_QUERY_TIMEOUT=10s
CLIO_DB_COMPACT_ENABLED=true
CLIO_DB_COMPACT_INTERVAL_H=24
CLIO_BOOTSTRAP_ADMIN_KEY=...    # nach Erststart entfernen
# CLIO_DEV_MODE bleibt AUS
```

- **Backup:** täglich + `verify` (Drill empfohlen). **Reverse Proxy:** empfohlen,
  sobald nicht nur localhost. **Monitoring:** `/metrics` scrapen.

## Profil: `small-production`

Echter Single-Node-Betrieb mit Backup, Monitoring, Reverse Proxy, Disk-Headroom.

```bash
CLIO_ADDR=127.0.0.1:3000          # TLS/öffentlich über Reverse Proxy
CLIO_DB_PATH=/var/lib/clio/clio.db
CLIO_SYNC=group                   # voll durabel bei hohem Durchsatz
CLIO_QUERY_TIMEOUT=15s            # breite Scans begrenzen (Default aus!)
CLIO_DB_INITIAL_MB=1024           # Mmap vorbelegen
CLIO_DB_GROW_THRESHOLD_PCT=80     # Headroom-Warnung
CLIO_DB_COMPACT_ENABLED=true
CLIO_DB_COMPACT_INTERVAL_H=24     # ruhiges Fenster
CLIO_SIGNING_KEY=...              # optional: Event-Authentizität
CLIO_DEV_MODE=false               # NIEMALS true in Produktion
# CLIO_BOOTSTRAP_ADMIN_KEY=...    # nur Erststart, danach entfernen
```

**Betriebspflichten** (Checkliste in [`production-readiness.md`](production-readiness.md) §4):
Hot-Backup nächtlich + `verify`, regelmäßiger Restore-Drill, `/metrics` mit Alarm
auf Disk-Headroom, Reverse Proxy mit Buffering aus und großzügigen Timeouts für
`observe`/`run-query`, Audit-Log off-host exportieren.

---

## Dimensionierung (`CLIO_DB_INITIAL_MB`)

bbolt mappt die Datei beim Überschreiten der Mmap-Grenze neu — unter Leselast
erzeugt das kurze Schreib-Latenzspitzen. Eine vorab große Mmap verschiebt diese
Remaps nach hinten. Faustregel: erwartetes Datenvolumen der nächsten Monate als
`CLIO_DB_INITIAL_MB` setzen; der Headroom-Monitor warnt rechtzeitig
(`CLIO_DB_GROW_THRESHOLD_PCT`), wenn der genutzte Umfang sich der Grenze nähert —
dann den Wert erhöhen und neu starten.
