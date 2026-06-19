# Deploy — Beispielkonfigurationen & Betriebsprofile

Fertige Vorlagen für den Betrieb von **cliostore** als Single-Instance-Dienst.
Grundlagen und Garantien: [`docs/production-readiness.md`](../docs/production-readiness.md).
Empfohlene Env-Vars je Profil: [`docs/operations-profiles.md`](../docs/operations-profiles.md).

| Verzeichnis | Plattform | Enthält |
|---|---|---|
| [`systemd/`](systemd/) | Linux (systemd) | Service-Unit (gehärtet) + Hot-Backup-Service/Timer/Skript |
| [`docker-compose/`](docker-compose/) | Docker | Compose-Stack + Backup-Profil + Env-Beispiel |
| [`windows/`](windows/) | Windows Server | Hot-Backup-PowerShell + Verweis auf die volle Anleitung |
| [`kubernetes-single-instance/`](kubernetes-single-instance/) | Kubernetes | StatefulSet (`replicas: 1`) + Service + PVCs + Backup-CronJob — **mit Warnung** |

## Betriebsprofile

| Profil | Wofür | Kurz |
|---|---|---|
| **dev** | lokal | Dev-Mode an, `CLIO_SYNC=off`, schnell |
| **lab** | interne Tools/Demos | Dev-Mode aus, Backup + Compaction, moderate Timeouts |
| **small-production** | echter Single-Node-Betrieb | voll durabel, Query-Timeout gesetzt, Mmap vorbelegt, Backup + Monitoring, hinter Reverse Proxy |

Die konkreten Env-Werte je Profil stehen in
[`docs/operations-profiles.md`](../docs/operations-profiles.md); die Beispiel-Env-
Dateien hier (`*.env.example`) entsprechen **small-production**.

## Wiederkehrende Prinzipien

- **Single-Instance bleibt Pflicht** (ADR-002) — nie zwei Prozesse auf derselben
  DB-Datei (Lock-Konflikt/Korruption). In Kubernetes heißt das `replicas: 1`.
- **Hot-Backup über HTTP**, nicht über das CLI gegen die gehaltene Datei — die
  laufende Instanz hält den exklusiven bbolt-Lock (ADR-030).
- **TLS/öffentlicher Zugang über einen Reverse Proxy** — clio bindet nur lokal
  und terminiert kein TLS (siehe Reverse-Proxy-Hinweise in der Production-Readiness-Doku).
- **Auth-Bootstrap nur einmal** — `CLIO_BOOTSTRAP_ADMIN_KEY` für den Erststart,
  danach entfernen; benannte Keys mit minimalem Scope (ADR-025, `docs/security.md`).
