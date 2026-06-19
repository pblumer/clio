# Audit-Log

> Praxisleitfaden zum persistenten Audit-Log administrativer Aktionen (ADR-032).
> Verwandt: [`security.md`](security.md) (Keys/Scopes) und das geplante
> `threat-model.md` (Vertrauensgrenzen).

clio führt **zwei** Audit-Spuren, mit unterschiedlichem Zweck:

| Spur | Wo | Inhalt | Beständigkeit |
|---|---|---|---|
| **Authz-Log** (ADR-025) | `slog` (stdout) | *jede* Autorisierungsentscheidung (allow/deny pro Request) | flüchtig, hochvolumig |
| **Audit-Log** (ADR-032) | bbolt-Bucket `audit_log` | *administrative Aktionen* (Mutationen) | dauerhaft, abfragbar |

Diese Seite beschreibt die zweite Spur — die durable, abfragbare.

---

## 1. Was wird auditiert

Jede dieser administrativen Aktionen erzeugt einen Audit-Eintrag:

| Aktion | `action` | Auslöser |
|---|---|---|
| Key angelegt | `key.create` | HTTP-Admin **oder** CLI |
| Key rotiert | `key.rotate` | HTTP-Admin **oder** CLI |
| Key widerrufen | `key.revoke` | HTTP-Admin **oder** CLI |
| Schema registriert | `schema.register` | HTTP-Admin |
| Backup gezogen | `backup` | HTTP `GET /api/v1/backup` |
| Dev-Reset | `dev.reset` | HTTP (nur Dev-Mode) |
| Online-Compaction | `compaction` | Hintergrund-Scheduler (Actor `system:scheduler`) |

Auch **fehlgeschlagene** Aktionen werden erfasst, wo sinnvoll (z. B. Rotation/
Widerruf eines unbekannten `kid` → `result: failure`).

> **Offline-Aktionen ohne Live-DB** (`cliostore backup`/`restore`/`verify`)
> erscheinen **nicht** im in-DB-Audit — sie schreiben die laufende DB nicht. Ihre
> Spur steht in der jeweiligen Prozessausgabe/den Logs.

---

## 2. Ein Eintrag

```json
{
  "seq": 7,
  "time": "2026-06-19T08:30:00Z",
  "actorKid": "kid_admin01",
  "actorName": "ci-admin",
  "action": "key.create",
  "result": "success",
  "target": "kid_ci01"
}
```

- `seq` — eigene monotone Sequenz des Audit-Logs (unabhängig von der Event-Sequenz).
- `actorKid`/`actorName` — der authentifizierte Auslöser; leer/`cli`/`system:*` bei
  nicht über die HTTP-API ausgelösten Aktionen.
- `result` — `success` oder `failure`; bei `failure` zusätzlich `error`.
- `target` — Zielobjekt (z. B. der betroffene `kid`, oder `events=12,bytes=…`).
- **Nie** ein Geheimnis (kein secret/hash).

---

## 3. Lesen

Read-only über `GET /api/v1/audit`, lesbar mit Scope **`audit`** oder **`admin`**:

```bash
curl -fsS -H "Authorization: Bearer $AUDITOR" \
  "http://127.0.0.1:3000/api/v1/audit?limit=50"
```

- `limit` (Default 100, max 1000), neueste zuerst.
- `before=<seq>` blättert: nur Einträge mit `seq < before` (Cursor-Pagination).
- Antwort: `{ "entries": [...], "total": <gesamtzahl> }`.

Ein dedizierter **Auditor-Key** trägt nur `audit` und kann damit das Log lesen,
**ohne** Schreib- oder Admin-Rechte (Prinzip der geringsten Rechte):

```bash
cliostore keys create --db clio.db --name auditor --scopes audit
```

---

## 4. Manipulationssicherheit — was garantiert ist, was nicht

**Garantiert:**

- **Append-only & isoliert.** Das Audit-Log lebt in einem eigenen bbolt-Bucket,
  getrennt vom Event-Strom. Es ist **nicht über die normale Write-API erreichbar**
  (`write-events` schreibt ausschließlich Events) und taucht **nicht** in
  `read-events`/`run-query`/`observe` auf — Fach-Events bleiben unberührt.
- **Reset-fest.** Der Dev-Reset (`dev.reset`) leert das Audit-Log **nicht** — die
  Spur des Resets selbst bleibt erhalten.
- **Keine Geheimnisse.** Secrets/Hashes landen nie im Audit-Log.

**Nicht garantiert (ehrliche Grenze, v1):**

- Das Audit-Log ist append-only **per Codepfad, nicht kryptografisch
  fälschungssicher.** Anders als die Event-Hash-Kette (ADR-012) sind die
  Audit-Einträge (noch) nicht verkettet. Wer **direkten Schreibzugriff auf die
  bbolt-Datei** oder einen **`admin`-Key** hat, kann Einträge verändern.
- Das Audit-Log schützt damit gegen *unbeabsichtigtes Vergessen* und
  *unprivilegierte* Manipulation — **nicht** gegen einen kompromittierten Host
  oder Admin (siehe Threat Model).

> **Folgeschritt (optional):** eine Hash-Verkettung der Audit-Einträge (analog
> ADR-012) würde das Log tamper-evident machen. Additiv nachrüstbar, in ADR-032
> als Option notiert.

---

## 5. Betriebsempfehlungen

- **Logs exportieren.** Für revisionssichere Aufbewahrung das Audit-Log
  periodisch abziehen (`GET /api/v1/audit`) und in ein WORM-/Append-only-Ziel
  außerhalb der clio-Instanz schreiben (z. B. ein zentrales Log-System).
- **Auditor von Admin trennen.** Wer prüft, braucht `audit`, nicht `admin`.
- **Auf `failure`-Einträge achten.** Häufige fehlgeschlagene Admin-Aktionen
  können auf Fehlkonfiguration oder einen Angriffsversuch hindeuten.
