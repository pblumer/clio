# Backup & Restore — Betriebsanleitung

> Praktische Anleitung für den Betrieb. Das **Warum** und die
> Architekturentscheidung stehen in [`backup-restore-dr-concept.md`](backup-restore-dr-concept.md)
> und **ADR-031** ([`ARCHITECTURE.md`](../ARCHITECTURE.md) §7). Diese Seite zeigt
> die Kommandos, die Garantien und die Fehlerfälle.

Clio sichert sich über **konsistente Snapshots** der bbolt-Datei — nicht über
Replikation. Jeder Snapshot ist selbst eine gültige, eigenständig öffenbare und
per Hash-Kette prüfbare clio-Datei (`.clio`).

---

## 1. Die Kommandos

```
cliostore backup  --output snap.clio [--db clio.db] [--force] [--verify] [--json]
cliostore restore --input  snap.clio  --db /data/clio.db [--force] [--json]
cliostore verify  [--db clio.db] [--json]
```

| Kommando | Zweck | Online/Offline |
|---|---|---|
| `backup` | konsistenter Snapshot einer DB-Datei in eine `.clio`-Datei | **offline** (CLI) / **online** (HTTP) |
| `restore` | spielt einen Snapshot atomar an einen Zielpfad ein | **offline** (keine laufende Instanz auf `--db`) |
| `verify` | rechnet die Hash-Kette einer DB/`.clio` nach (Tamper-Evidence) | read-only, jederzeit |

- `--db` ohne Wert nutzt `CLIO_DB_PATH` bzw. `clio.db`.
- `--force` erlaubt das Überschreiben einer vorhandenen Zieldatei (sonst Fehler
  `ziel existiert bereits`, **kein stiller Verlust**).
- `--verify` (nur `backup`) prüft das frische Backup direkt nach dem Schreiben.
- `--json` gibt das Ergebnis maschinenlesbar aus (für Skripte/CI).
- Ist `CLIO_SIGNING_KEY` gesetzt, prüfen `verify` / `backup --verify` auch die
  **Event-Signaturen** mit, nicht nur die Hash-Kette.

### Exit-Codes

- `verify`: **0** = Kette intakt, **1** = Bruch (`brokenAt`/`reason` werden
  ausgegeben). Direkt skriptbar für Backup-Audits.
- `backup`/`restore`: **0** = Erfolg, **1** = Fehler (Meldung auf stderr).

---

## 2. Hot-Backup vs. Cold-Backup — die wichtige Einschränkung

bbolt hält im Schreib-Modus einen **exklusiven Datei-Lock**. Daraus folgt:

- **`cliostore backup --db …` (CLI)** kann eine **laufende** Instanz **nicht**
  öffnen. Es ist der **Cold/Offline-Pfad**: Server gestoppt, oder eine Kopie der
  Datei. Der Snapshot ist trotzdem konsistent.
- **`GET /api/v1/backup` (HTTP, admin-scoped)** läuft **in-Process** im
  laufenden Server und liefert ein echtes **Hot-Backup** ohne Stopp — der
  Snapshot läuft in einer Read-Transaktion und **blockiert keine Schreiber**.

> **Faustregel:** Server läuft → HTTP-Endpunkt. Server gestoppt / DB-Datei-Kopie
> → CLI. Beide erzeugen dasselbe, verify-grüne `.clio`-Artefakt.

### Hot-Backup über HTTP

```bash
curl -fsS -H "Authorization: Bearer <kid>.<secret>" \
  http://127.0.0.1:3000/api/v1/backup -o clio-$(date +%F).clio
cliostore verify --db clio-$(date +%F).clio
```

### Cold-Backup über CLI (Server gestoppt)

```bash
cliostore backup --db /var/lib/clio/clio.db --output /var/backups/clio/clio-$(date +%F).clio --verify
```

---

## 3. Restore in 3 Schritten

Restore ist **offline** auszuführen (keine Instanz darf auf `--db` zugreifen):

```bash
# 1. Server stoppen (falls er läuft)
systemctl stop cliostore        # bzw. docker stop clio

# 2. Snapshot einspielen (an einen Zielpfad; --force überschreibt)
cliostore restore --input /var/backups/clio/clio-2026-06-18.clio --db /var/lib/clio/clio.db

# 3. Wiederhergestellten Stand verifizieren, DANN starten
cliostore verify --db /var/lib/clio/clio.db
systemctl start cliostore
```

Nach `restore` + `verify` gilt: `head` und `count` entsprechen exakt dem Backup,
ein Event-für-Event-Replay liefert bit-identische Hashes, und der nächste
`write-events` schließt **lückenlos** an die wiederhergestellte Kette an.

---

## 4. Garantien — und was *nicht* garantiert ist

**Garantiert:**

- **Konsistenz (INV-B1):** Snapshot ist ein Punkt-in-der-Zeit-Zustand; `head`/
  `count` stammen aus derselben Transaktion wie der Snapshot.
- **Atomarität (INV-B2):** unter dem Zielnamen existiert nie ein halb
  geschriebenes Backup (temp + fsync + atomarer Rename).
- **Selbstvalidierung (INV-B3):** jedes `.clio` ist eigenständig öffenbar und
  besteht `verify`.
- **Kein stiller Verlust (INV-R1):** `restore`/`backup` überschreiben ein
  vorhandenes Ziel nur mit `--force`.
- **Identität nach Restore (INV-R2/R3):** `head`/`count` == Backup, Replay
  bit-identisch, nahtloser Folge-Append.
- **Read-only-Prüfung (INV-V1):** `verify` verändert die geprüfte Datei nicht.

**Nicht garantiert (bewusst außerhalb des Scopes):**

- **Kein automatisches Failover / keine Replikation.** Backup ≠ Hochverfügbarkeit.
- **Kein PITR out-of-the-box.** Der RPO ist das Backup-Intervall (siehe §6). Eine
  optionale PITR-Ausbaustufe ist in ADR-031 skizziert, aber zurückgestellt.
- **Keine Verschlüsselung des Artefakts.** Das `.clio` ist eine reine bbolt-Datei;
  Schutz im Ruhezustand/Transport (Volume-Encryption, GPG, Objektspeicher-SSE)
  liegt beim Betreiber — siehe [`threat-model.md`](threat-model.md) (Backup-Diebstahl).

---

## 5. Fehlerfälle (und was sie bedeuten)

| Situation | Verhalten |
|---|---|
| Ziel existiert schon | Fehler `ziel existiert bereits` (Exit 1) — mit `--force` überschreiben |
| Quelle fehlt (`restore`/CLI-`backup`) | Fehler beim Öffnen (Exit 1) |
| Quelle ist keine clio-DB / beschädigt | `keine gültige clio-datenbank` bzw. bbolt-Open-Fehler |
| Laufende Instanz hält die DB (CLI-`backup`) | Open-Timeout mit Hinweis: Server stoppen **oder** `GET /api/v1/backup` nutzen |
| Keine Schreibrechte im Zielverzeichnis | Fehler beim Anlegen der temp-Datei (Exit 1); Ziel bleibt unangetastet |
| Datenträger voll | Schreib-/fsync-Fehler (Exit 1); die temp-Datei wird entfernt, das Ziel bleibt unverändert |
| Manipulierte/korrupte Kette | `verify` meldet `KETTE GEBROCHEN` mit `brokenAt` und Exit 1 |

Weil alle schreibenden Operationen **temp + atomarer Rename** nutzen, hinterlässt
ein Fehler nie ein halb geschriebenes Ziel.

---

## 6. RPO / RTO und Backup-Strategie

| Kennzahl | Stufe-1-Snapshot (umgesetzt) |
|---|---|
| **RPO** (max. Datenverlust) | = Backup-Intervall (z. B. 24 h; bei kleinen Stores ruhig häufiger) |
| **RTO** (max. Ausfallzeit) | Minuten — eine Datei einspielen + `verify` + Start |

**Empfehlung:**

- **Frequenz/Retention:** täglich, 3-2-1 (z. B. 7 täglich, 4 wöchentlich, eine
  Off-site-Kopie). Häufiger senkt den RPO.
- **Immer verifizieren:** jedes frische Backup `verify`-en (`--verify` oder
  separater Schritt). *Ein Backup, das man nie geprüft/zurückgespielt hat, ist
  kein Backup.*
- **Restore-Drill:** das Einspielen periodisch üben, nicht nur im Notfall.
- **Headroom:** vor `restore` sicherstellen, dass das Zielverzeichnis genug
  Platz hat (Snapshot ist defragmentiert, also eher kleiner als die laufende DB).

---

## 7. Betriebsbeispiele

Fertige Units/Compose/Scripts liegen unter [`deploy/`](../deploy/) (sofern in
deiner Version vorhanden); das Konzeptdokument
[`backup-restore-dr-concept.md`](backup-restore-dr-concept.md) §4.4 zeigt
systemd-Timer-, Docker- und Kubernetes-CronJob-Beispiele im Detail.

Minimalbeispiel (systemd-Timer, Cold-Backup nachts mit Stop/Start oder
Hot-Backup über HTTP ohne Stop):

```ini
# /etc/systemd/system/clio-backup.service  (Type=oneshot)
[Service]
Type=oneshot
ExecStart=/usr/local/bin/cliostore backup \
  --db /var/lib/clio/clio.db \
  --output /var/backups/clio/clio-%i.clio --verify
```

```ini
# /etc/systemd/system/clio-backup.timer
[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=true
[Install]
WantedBy=timers.target
```
