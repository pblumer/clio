# Security — API-Keys, Scopes & Lebenszyklus

> Praxisleitfaden zum Authentifizierungs- und Berechtigungsmodell von cliostore
> (ADR-025). Architekturhintergrund steht in [`ARCHITECTURE.md`](../ARCHITECTURE.md);
> die Bedrohungsbetrachtung folgt separat in `docs/threat-model.md`.

clio authentifiziert jeden API-Zugriff über **benannte API-Keys mit Scopes**.
Ein Key besteht aus einer öffentlichen `kid` und einem geheimen `secret`; auf der
Leitung steht der zusammengesetzte Wert im Bearer-Header:

```
Authorization: Bearer <kid>.<secret>
```

Persistiert wird **ausschließlich der SHA-256-Hash** des Geheimnisses, nie der
Klartext. Der Vergleich beim Login läuft **zeitkonstant** (kein Timing-Orakel
über die Existenz eines `kid`).

---

## 1. Scopes

| Scope | Erlaubt |
|---|---|
| `read` | lesende Routen: `read-events`, `observe-events`, `run-query`, `verify`, `info`, `read-subjects`, … |
| `write` | schreibende Datenrouten: `write-events`, `register-event-schema` |
| `admin` | Schlüsselverwaltung (`keys` …), `backup`, Dev-Routen; liest auch das Audit-Log |
| `audit` | **nur** read-only Lesen des Audit-Logs (`GET /api/v1/audit`) — siehe [`audit.md`](audit.md) |

Ein Key trägt einen oder mehrere Scopes. Routen verlangen je **genau einen**
Scope. Fehlt er, antwortet der Server mit **403** (klar getrennt vom **401** bei
fehlender/ungültiger Authentifizierung).

> **Prinzip der geringsten Rechte.** Vergib pro Anwendungsfall einen eigenen Key
> mit minimalem Scope: ein Reporting-Job bekommt `read`, ein Producer `write`,
> nur die Betriebsverwaltung `admin`.

### Subject-/Prefix-gebundene Scopes (ADR-033)

`read` und `write` lassen sich auf einen **Subject-Teilbaum** einschränken — für
Mehrparteien-Szenarien, in denen ein Producer/Consumer nur einen Bereich sehen
oder beschreiben darf:

| Scope-String | Bedeutung |
|---|---|
| `read` | global lesen (alle Subjects) — wie bisher |
| `read:/orders/*` | nur den Teilbaum unter `/orders` lesen (rekursiv) |
| `read:/orders` | nur das **exakte** Subject `/orders` (ohne Nachfahren) |
| `read:/*` | gesamter Baum (= effektiv global) |
| `write:/orders/*` | nur in den Teilbaum `/orders` schreiben |

```bash
# Ein Producer, der ausschließlich /orders/* schreiben und lesen darf:
cliostore keys create --db clio.db --name orders-svc \
  --scopes 'read:/orders/*,write:/orders/*'
```

Durchsetzung:

- **Datenrouten** (`read-events`, `observe-events`, `run-query`, `events`-Pfad,
  `write-events`): der angefragte Subject-Zugriff muss vollständig in einem Grant
  liegen, sonst **403**. Bei `write-events` wird **jedes** Event geprüft (eine
  Abweisung schreibt atomar **nichts**).
- **Aggregat-/globale Routen** (`info`, `event-stats`, `read-event-types`,
  `read-event-schema`, `verify`, `read-subjects` ohne Prefix; `register-event-schema`
  als globaler `write`) verlangen einen **globalen** Grant — ein rein
  prefix-beschränkter Key bekommt dort 403. `read-subjects?prefix=/orders` ist
  erlaubt, wenn der Prefix im Grant liegt.
- `admin`/`audit` sind **immer global** (Schlüssel-/Auditverwaltung ist nicht
  subjektgebunden).

**Bewusste Grenzen:** keine Deny-Regeln (nur additive Grants), keine Wildcards in
der Mitte (`/orders/*/items`), und Subjects bleiben in **einer** DB — das ist
**keine** Mandantentrennung (siehe [`threat-model.md`](threat-model.md)).

---

## 2. Lebenszyklus

Keys lassen sich **über die HTTP-Admin-API** (laufender Server, Scope `admin`)
**oder über die CLI** (offline, Server gestoppt) verwalten. Die CLI ist der
Bootstrap- und **Notfall-/Recovery-Pfad** (siehe §5).

### Erstellen

```bash
# HTTP (laufender Server)
curl -fsS -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"nightly-export","scopes":["read"],"owner":"team-data","expiresAt":"2026-12-31T00:00:00Z"}' \
  http://127.0.0.1:3000/api/v1/keys

# CLI (Server gestoppt)
cliostore keys create --db clio.db --name nightly-export --scopes read \
  --owner team-data --expires 720h
```

Optional: `expiresAt`/`--expires` (Ablauf), `owner`, `purpose`, `description`.
`--expires` akzeptiert eine Dauer (`720h`) **oder** einen RFC3339-Zeitstempel.
Das Geheimnis (`kid.secret`) wird **nur einmal** ausgegeben.

### Auflisten (ohne Geheimnisse)

```bash
cliostore keys list --db clio.db          # oder GET /api/v1/keys
```

Die Liste enthält **nie** Hash oder Klartext — nur `kid`, Name, Scopes, Status,
Ablauf/`expired`, Owner/Purpose und die Zahl nutzbarer Admin-Keys.

### Rotieren (Geheimnis erneuern, kid bleibt)

```bash
cliostore keys rotate --db clio.db --kid kid_abcd1234   # oder POST …/keys/{kid}/rotate
```

`kid`, Scopes, Status und Metadaten bleiben; der **alte Wert wird sofort
ungültig**. Anwendung: ein geleaktes oder turnusmäßig zu wechselndes Geheimnis
ersetzen, ohne überall den `kid` zu ändern.

### Deaktivieren / Widerrufen

```bash
cliostore keys revoke --db clio.db --kid kid_abcd1234   # oder POST …/keys/{kid}/revoke
```

Widerruf ist ein **Status-Wechsel, kein Löschen** — die `kid`-Zuordnung bleibt
fürs Audit dauerhaft erhalten. Ein widerrufener Key wird mit **401** abgewiesen.

### Ablauf (Expiry)

Ein Key mit gesetztem `expiresAt` wird ab diesem Zeitpunkt **wie ein
widerrufener** behandelt (401), ohne dass man ihn aktiv widerrufen muss — ideal
für temporäre/CI-Zugänge. Der Ablauf ist inklusiv (genau zum Zeitpunkt gilt der
Key als abgelaufen).

---

## 3. Was garantiert NICHT passiert

- **Secrets im Log:** nie. Geloggt werden nur `kid`/Name/Scopes und
  Autorisierungsentscheidungen (Audit), niemals Geheimnis oder Hash.
- **Secret-Rückgabe über die API:** nie. `kid.secret` erscheint **einmalig** bei
  `create`/`rotate`; `list` und jede andere Route geben es nicht zurück.
- **Timing-Orakel:** der Secret-Vergleich läuft auch bei unbekanntem `kid`
  zeitkonstant gegen einen Dummy-Hash.

---

## 4. Bootstrap-Regeln

Beim Start mit **leerem** Schlüsselbund legt clio genau einen Admin-Key an:

1. `CLIO_BOOTSTRAP_ADMIN_KEY` gesetzt → Admin-Key mit diesem Geheimnis (`kid`
   wird erzeugt und geloggt, **das Geheimnis nicht**).
2. sonst `CLIO_API_TOKEN` gesetzt (deprecated) → Legacy-Admin-Key (siehe §6).
3. sonst → **Start wird verweigert** (kein stiller, ungeschützter Betrieb).

Ist bereits mindestens ein Key vorhanden, passiert **kein** Bootstrap. Nach dem
ersten Start: einen benannten Admin-Key anlegen, mit ihm arbeiten und die
`CLIO_BOOTSTRAP_ADMIN_KEY`-Variable wieder **entfernen**.

```bash
export CLIO_BOOTSTRAP_ADMIN_KEY="$(openssl rand -base32 30 | tr -d '=')"
cliostore                       # legt 'bootstrap-admin' an, loggt den kid
# danach: benannte Keys anlegen, Bootstrap-Variable entfernen, neu starten
```

---

## 5. Notfall / Recovery (Lockout)

Wenn **kein nutzbarer Admin-Key** mehr existiert (verloren, alle abgelaufen/
widerrufen), ist die HTTP-Admin-API nicht mehr erreichbar. Weg zurück:

1. **Server stoppen** (CLI braucht den Datei-Lock exklusiv).
2. Über die **CLI** einen neuen Admin-Key anlegen **oder** einen bestehenden
   rotieren:
   ```bash
   cliostore keys list   --db /var/lib/clio/clio.db
   cliostore keys create --db /var/lib/clio/clio.db --name recovery-admin --scopes read,write,admin
   # oder, falls der kid bekannt ist:
   cliostore keys rotate --db /var/lib/clio/clio.db --kid kid_abcd1234
   ```
3. **Server starten** und mit dem neuen Wert arbeiten.

`cliostore keys list` warnt, wenn nur noch **ein** nutzbarer Admin-Key übrig ist
(Self-Lockout-Schutz). Ein letzter Admin-Key lässt sich bewusst widerrufen — der
Server blockt das nicht hart, meldet aber die Folge.

---

## 6. Migration von `CLIO_API_TOKEN` (deprecated)

Das frühere, geteilte `CLIO_API_TOKEN` ist **abgelöst** durch das Key-Modell.
Bei leerem Bund und gesetztem `CLIO_API_TOKEN` legt clio aus
Kompatibilitätsgründen einen Legacy-Admin-Key an — der Leitungswert ist dann
`kid.<CLIO_API_TOKEN>` (also **mit** generiertem `kid`-Präfix). Das alte Format
ohne `kid`-Präfix wird **nicht mehr akzeptiert** (401).

**Migrationsschritte:**

1. Mit dem Legacy-Wert (`<kid>.<CLIO_API_TOKEN>`, kid steht im Startlog) einloggen.
2. Pro Anwendungsfall **benannte Keys mit minimalem Scope** anlegen.
3. Clients/Jobs auf die neuen `kid.secret`-Werte umstellen.
4. Den Legacy-Key **widerrufen** und `CLIO_API_TOKEN` aus der Umgebung entfernen.

---

## 7. Betriebsempfehlungen

- **TLS verpflichtend.** `kid.secret` reist im Header — clio hinter einen
  TLS-terminierenden Reverse Proxy stellen (siehe Windows-/Deploy-Doku).
- **Ein Key je Konsument**, minimaler Scope, mit `owner`/`purpose` annotiert.
- **Rotation als Routine.** Geheimnisse turnusmäßig oder bei Verdacht rotieren;
  `expiresAt` für temporäre Zugänge nutzen.
- **Admin-Keys sparsam.** Wenige, gut geschützte Admin-Keys; einen separaten
  Recovery-Admin offline sicher hinterlegen.
- **Audit beobachten.** Die Autorisierungsentscheidungen stehen strukturiert im
  Log (`audit=true`); ungewöhnliche 401/403-Muster prüfen.
