# Implementierungsplan: Aktivität & Presence — wer ist online, wer tut was

**Projekt:** `github.com/pblumer/clio`
**Status:** PLANUNG — bereit zur Umsetzung durch einen AI-Coding-Agenten
**Bezug:** ADR-030 (in `ARCHITECTURE.md`); baut auf dem Schlüsselbund ADR-025 (`kid`-Identität, Audit) und der opt-in Event-Urheberschaft (`CLIO_EVENT_AUTHORSHIP`) auf.
**Vorbild-Doku:** `docs/plans/security-api-keys-plan.md` (in sich geschlossene Work-Packages mit Akzeptanzkriterien).

---

## 0. Zusammenfassung

Mit ADR-025 trägt jede authentifizierte Anfrage eine Identität (`kid`, `name`,
Scopes), und jede Autorisierungsentscheidung landet als `slog`-Audit-Zeile im
Log. Was fehlt, ist die **Sicht darauf**: Es gibt keine Möglichkeit zu sehen,
**wer gerade online ist**, und ein Admin kann **nicht zusammengefasst erkennen**,
wer was tut (erste/letzte Aktivität, gelesen/geschrieben, offene
Live-Verbindungen). Das Audit-Log ist flüchtig, nicht aggregiert und nicht
abfragbar.

Dieser Plan ergänzt drei aufeinander aufbauende Schichten — bewusst getrennt
nach **Last-Profil**:

1. **Presence & Aktivitäts-Zähler (in-memory).** Eine schlanke, prozesslokale
   Registry hält pro `kid`: erste/letzte Aktivität, Zähler nach Kategorie
   (read/write/admin), abgelehnte Zugriffe und die Anzahl **offener
   Observe-Verbindungen**. „Online" = offene Live-Verbindung **oder** letzte
   Aktivität innerhalb eines gleitenden Fensters. Kein Persistenz-Eingriff,
   keine Event-Last — gespeist aus der bestehenden Auth-/Instrument-Middleware.

2. **Auth-Lifecycle als CloudEvents (eat your own dog food).** Die *seltenen,
   wertvollen* Ereignisse — Session-Start/-Ende (das „Login"-Äquivalent eines
   tokenbasierten, sessionlosen Systems), Key angelegt/widerrufen — werden als
   echte Events in clio selbst geschrieben (reservierter Subject-Namespace
   `/_clio/auth/…`). Damit sind Login-Zeiten und Schlüssel-Lebenszyklus
   **mit dem vorhandenen Werkzeug abfragbar und live beobachtbar**
   (`run-query`, `observe`, `/ui`-Explorer) — ohne neuen Lese-Endpunkt.
   **Bewusst KEIN Event pro Read/Write** (Event-Amplifikation, siehe
   Nicht-Ziele).

3. **Admin-Sichten.** Ein neuer Endpunkt `GET /api/v1/activity` (Scope `admin`)
   liefert den Presence-/Aktivitäts-Snapshot; ein neuer **UI-Tab „Aktivität"**
   zeigt online-Status live, eine Aktivitätstabelle je `kid` und einen
   Live-Feed der Auth-Lifecycle-Events. Historie kommt aus den
   `/_clio/auth/`-Events (Dogfooding), nicht aus einem neuen Speicher.

Das Single-Binary-/Stdlib-Prinzip (ADR-001) bleibt erhalten: **keine neue
externe Abhängigkeit**. Presence ist prozesslokal (Single-Instance, ADR-002) und
nach Neustart bewusst leer — die *dauerhafte* Spur sind die Lifecycle-Events.

### Designentscheidungen, die alles Weitere prägen

| Entscheidung | Gewählt | Begründung |
|---|---|---|
| Presence-Speicherort | **In-memory**, prozesslokal | Presence ist flüchtig; Single-Instance (ADR-002); kein DB-Wachstum, keine Write-Last pro Request |
| „Online"-Definition | Offene Observe-Verbindung **oder** letzte Aktivität < `CLIO_PRESENCE_WINDOW` | Deckt Live-Beobachter (echt online) und kurzlebige Request-Clients (heuristisch online) ab |
| Was wird zum Event? | **Nur Lifecycle** (Session-Start/-Ende, Key angelegt/widerrufen) | Geringe, wertvolle Volumina; kein Event pro Read/Write → keine Event-Amplifikation |
| Event-Namespace | Reservierter Subject-Prefix `/_clio/auth/…` | Klar von Fachdaten getrennt; server-only beschreibbar; per Prefix filter-/ausblendbar |
| Default für Lifecycle-Events | `CLIO_AUTH_EVENTS` **opt-in (aus)** | Konsistent mit `CLIO_EVENT_AUTHORSHIP`: ein Eingriff in den Event-Strom des Betreibers ist eine bewusste Entscheidung; rückwärtskompatibel |
| Session-Definition | Übergang *offline → online* eines `kid` startet eine Session | Ein sessionloses Token-System hat kein „Login" — die Presence-Lücke *ist* die Session-Grenze; debounced über das Fenster |
| Abgelehnte Zugriffe als Event | **Opt-in, rate-limitiert** (Default: nur Zähler + bestehendes slog-Audit) | Ein Angreifer könnte sonst den Event-Strom fluten (Amplifikation/DoS) |
| Remote-IP / PII | **Nicht** in Events/Snapshot (Default) | Datensparsamkeit; IP ist personenbeziehbar. Optionales späteres Feld, bewusst zurückgestellt |

### Abgrenzung zum Append-only-Prinzip

Die `/_clio/auth/`-Events sind **echte, unveränderliche Events** im
Haupt-Event-Strom — sie gehen in Hash-Kette (ADR-012) und Signatur (ADR-016) ein
wie jedes andere Event und löschen nichts (ADR-015). Das ist **kein** Bruch des
Append-only-Versprechens, sondern dessen Anwendung auf die eigene Auth-Historie.
Die **Presence-Registry** dagegen ist flüchtiger Laufzeit-State (wie Metriken),
kein Event und keine Quelle der Wahrheit über die Vergangenheit.

---

## 1. Zielarchitektur

### 1.1 Presence-/Aktivitäts-Registry (`internal/activity`)

Reines, prozesslokales Domänenpaket ohne Storage-/HTTP-Abhängigkeit (analog
`internal/auth`). Mutex-geschützte Map `kid → *entry`.

```go
type Snapshot struct {
    KID            string    // identisch zum Keyring-kid (ADR-025)
    Name           string    // letzter bekannter Name des Keys
    Scopes         []string  // letzte bekannte Scopes
    FirstSeen      time.Time // erste Aktivität dieser Laufzeit
    LastSeen       time.Time // letzte (erfolgreiche) Aktivität
    Reads          uint64    // erfolgreiche read-Scope-Requests
    Writes         uint64    // erfolgreiche write-Scope-Requests
    AdminOps       uint64    // erfolgreiche admin-Scope-Requests
    Denied         uint64    // 401/403 unter diesem kid (sofern zuordenbar)
    OpenObserves   int       // aktuell offene Observe-Verbindungen
    Online         bool      // OpenObserves>0 ODER now-LastSeen < window
    SessionStarted time.Time // Beginn der aktuellen Presence-Session (0 wenn offline)
}
```

API (knapp):
- `Record(kid, name string, scopes []auth.Scope, cat Category, allowed bool, now time.Time) (sessionStarted bool)`
  — verbucht eine Anfrage; meldet `true`, wenn dadurch ein Übergang
  offline→online stattfand (Trigger fürs Session-Start-Event).
- `OpenObserve(kid…) / CloseObserve(kid…)` — Live-Verbindungs-Zähler.
- `Snapshot(window, now) []Snapshot` — sortierte Momentaufnahme (online zuerst).
- `Sweep(window, now) []endedSession` — markiert abgelaufene Sessions als
  offline und liefert sie (Trigger fürs Session-Ende-Event).

`Category` ist `read|write|admin` (abgeleitet aus dem Routen-Scope).

### 1.2 Auth-Lifecycle-Events (`internal/httpapi`, opt-in)

Geschrieben **direkt über den Store** (nicht über die HTTP-Schicht → kein
Auth-Pfad, keine Rekursion, kein Henne-Ei-Problem). Format: reguläre
CloudEvents.

| Event-Typ | Subject | Auslöser |
|---|---|---|
| `dev.clio.auth.session-started` | `/_clio/auth/sessions/{kid}` | Übergang offline→online (1.1 meldet `true`) |
| `dev.clio.auth.session-ended` | `/_clio/auth/sessions/{kid}` | Sweeper: Fenster abgelaufen / letzte Observe-Verbindung zu |
| `dev.clio.auth.key-created` | `/_clio/auth/keys/{kid}` | `POST /api/v1/keys` **und** Bootstrap |
| `dev.clio.auth.key-revoked` | `/_clio/auth/keys/{kid}` | `POST /api/v1/keys/{kid}/revoke` |
| `dev.clio.auth.access-denied` | `/_clio/auth/denied/{kid}` | **opt-in**, rate-limitiert — wiederholte 401/403 |

`source` = serverseitig fix (z. B. `clio://auth`), `data` trägt nur
nicht-geheime Felder (`kid`, `name`, `scopes`, `reason`); **niemals** ein Secret
oder Hash, **keine** Remote-IP per Default.

**Reservierter Namespace.** Der Subject-Prefix `/_clio/` ist **server-only**:
der `write-events`-Pfad lehnt Client-Writes auf `/_clio/…` hart ab (analog dazu,
dass `id`/`time` serverseitig gesetzt sind). Sonst könnte ein `write`-Client
Login-Events fälschen.

**Sichtbarkeit (bewusst dokumentiert).** Die Events liegen im
Haupt-Event-Strom; ein rekursives `read`/`observe` auf `/` sieht sie. Das ist
für Transparenz erwünscht (jeder `read`-Client sieht die Auth-Historie). Wird
das je als Rauschen empfunden, ist ein optionaler Ausschluss des `/_clio/`-Prefix
aus dem Wurzel-Scan additiv nachrüstbar (als Nicht-Ziel hier vermerkt).

### 1.3 Endpunkt & Scope-Mapping

| Route | Scope | Zweck |
|---|---|---|
| `GET /api/v1/activity` | `admin` | Presence-/Aktivitäts-Snapshot (1.1) als JSON |

Login-/Lifecycle-**Historie** braucht keinen neuen Endpunkt — sie kommt über die
bestehenden `read`-Routen auf `/_clio/auth/` (Dogfooding):
`observe`, `run-query`, `GET /api/v1/events/_clio/auth/...`.

### 1.4 Konfiguration

| ENV | Default | Wirkung |
|---|---|---|
| `CLIO_PRESENCE_WINDOW` | `60s` | Gleitendes „Online"-Fenster (Go-Dauer) |
| `CLIO_AUTH_EVENTS` | `off` | Schaltet die Lifecycle-Events (1.2) ein |
| `CLIO_AUTH_DENIED_EVENTS` | `off` | Schaltet zusätzlich `access-denied`-Events ein (rate-limitiert) |

Presence/Zähler/Endpoint/UI sind **immer aktiv** (kein Persistenz-/Event-Eingriff).

---

## 2. Work-Packages

Reihenfolge ist die empfohlene Implementierungssequenz. Jedes WP ist isoliert
testbar und endet in grünem `go test ./... && go vet ./...`.

### WP-01 — Presence-/Aktivitäts-Registry (`internal/activity/registry.go`)

**Ziel:** Reines, nebenläufigkeitssicheres In-memory-Domänenpaket.

**Inhalt:** Typen `Category`, `Snapshot` (siehe 1.1); `Registry` mit
`Record`/`OpenObserve`/`CloseObserve`/`Snapshot`/`Sweep`. Mutex-geschützt; keine
Importe von `store`/`net/http`. `Record` liefert den offline→online-Übergang.

**Akzeptanzkriterien:**
- Tabellengetriebene Tests: erster Record setzt `FirstSeen`/`SessionStarted` und
  meldet `sessionStarted=true`; Folge-Record im Fenster meldet `false`; Record
  nach Fenster-Ablauf meldet wieder `true` (neue Session).
- `OpenObserve`/`CloseObserve` zählen korrekt; `Online` ist true bei
  `OpenObserves>0` auch ohne jüngste Aktivität.
- `Sweep` liefert genau die Sessions, deren Fenster ablief und die keine offene
  Observe-Verbindung haben.
- Nebenläufigkeits-Test (`-race`) über viele Goroutinen.
- Kein Import von `store` oder `net/http`.

---

### WP-02 — Verdrahtung in der Middleware (`internal/httpapi`)

**Ziel:** Die Registry aus dem bestehenden Auth-/Request-Pfad speisen.

**Inhalt:**
- Server-Feld `activity *activity.Registry` + Verdrahtung in `New`; Config-Feld
  `PresenceWindow` (WP-03/Config).
- In `requireScope` (`auth_middleware.go`): nach der Entscheidung
  `activity.Record(kid, name, scopes, cat, allowed, now)` aufrufen. `cat` ergibt
  sich aus dem Scope der Route. Bei `allow` zählt es read/write/admin hoch, bei
  `deny` `Denied`. Liefert `Record` einen Session-Start, wird (falls
  `CLIO_AUTH_EVENTS`) WP-04s Emitter angestoßen.
- Im Observe-Handler (`handlers.go`): `OpenObserve`/`CloseObserve` um die
  offene Verbindung klammern (`defer`).
- Hintergrund-Sweeper (eine Goroutine, Ticker ~Fenster/2) ruft `Sweep` und
  stößt (falls aktiv) Session-Ende-Events an. Sauber per Server-Lifecycle
  beendet.

**Akzeptanzkriterien:**
- `server_test.go`: nach einem erfolgreichen read-Request taucht der `kid` im
  Snapshot mit `Reads==1`, `Online==true` auf; ein 403 erhöht `Denied`.
- Observe-Test: während offener Verbindung `OpenObserves==1`, danach `0`.
- Kein Verhaltensbruch der bestehenden Auth-Tests.

---

### WP-03 — Config-Erweiterung (`internal/config/config.go`)

**Ziel:** Neue ENV-Variablen aus 1.4.

**Inhalt:** Felder `PresenceWindow time.Duration` (Default 60s),
`AuthEvents bool`, `AuthDeniedEvents bool` aus den jeweiligen ENV-Variablen;
Parsing/Default analog zu `CLIO_QUERY_TIMEOUT` bzw. den bestehenden
Bool-Flags (`CLIO_EVENT_AUTHORSHIP`).

**Akzeptanzkriterien:**
- Tests: Default-Werte; gültige/ungültige Dauer; Bool-Parsing.
- Bestehende Config-Tests bleiben grün.

---

### WP-04 — Auth-Lifecycle-Events (`internal/httpapi/activity_events.go`, opt-in)

**Ziel:** Lifecycle als CloudEvents in `/_clio/auth/…`, server-only, opt-in.

**Inhalt:**
- Emitter, der bei `CLIO_AUTH_EVENTS` Events über den Store schreibt
  (Session-Start/-Ende aus WP-02; key-created bei `handleCreateKey` **und**
  Bootstrap; key-revoked bei `handleRevokeKey`). Schreibfehler werden geloggt,
  brechen aber den auslösenden Request **nicht** ab (best-effort, da Diagnose).
- **Reservierter-Namespace-Guard** im Write-Pfad: Client-`write-events` mit
  Subject unter `/_clio/` → harte Ablehnung (`400`/`403`, problem+json).
- `access-denied`-Events nur bei `CLIO_AUTH_DENIED_EVENTS`, mit einfacher
  Rate-Begrenzung je `kid`/Fenster gegen Flutung.
- Selbst-Rekursions-Sicherung dokumentieren: interne Writes laufen am
  HTTP-Auth-Pfad vorbei und erzeugen daher **keine** weiteren Aktivitäts-/
  Session-Events.

**Akzeptanzkriterien:**
- Mit `CLIO_AUTH_EVENTS=on`: ein erster authentifizierter Request erzeugt genau
  ein `session-started`-Event unter `/_clio/auth/sessions/{kid}`; Key anlegen/
  widerrufen erzeugt je ein Event. Inhalt enthält **kein** Secret/Hash.
- Mit Default (`off`): kein einziges `/_clio/`-Event wird geschrieben
  (Event-Count vorher/nachher identisch) — strikte Rückwärtskompatibilität.
- Client-Write auf `/_clio/foo` wird abgelehnt; Server-Write gelingt.
- Sweeper erzeugt `session-ended` nach Fensterablauf.

---

### WP-05 — Metriken (`internal/metrics`, ADR-013-Stil)

**Ziel:** Presence/Aktivität auch im Prometheus-Endpoint sichtbar — ohne
Client-Dependency.

**Inhalt:** Gauge `clio_online_keys`, Counter `clio_requests_by_scope_total{scope,decision}`
(oder Ableitung aus den vorhandenen Request-Metriken). Speisung aus WP-02.

**Akzeptanzkriterien:**
- `/metrics` exponiert die neuen Reihen im bestehenden Textformat; Test prüft
  Vorhandensein und Monotonie der Counter.

---

### WP-06 — UI-Tab „Aktivität" (`internal/webui`, admin, ADR-020-konform)

**Ziel:** Sichtbar machen, wer online ist und wer was tut. Vanilla JS, kein
Build-Step/CDN, keine neue Abhängigkeit.

**Inhalt:**
- Neuer Tab „Aktivität": (a) **Online-Liste** (poll `GET /api/v1/activity`,
  z. B. alle 5 s) mit `kid`, `name`, Scopes, online seit, offene Observes;
  (b) **Aktivitätstabelle** je `kid` (first/last seen, reads/writes/admin,
  denied); (c) **Live-Feed** der Lifecycle-Events über `observe` auf
  `/_clio/auth/` (recursive). Der Tab nutzt das im Dashboard bereits
  eingegebene Token; `activity` verlangt `admin`, daher Hinweis bei `403`.
- Aufteilung wie bestehend: Markup in `static/dashboard.html`, Logik in
  `static/js/dashboard.js`, Stil in `static/css/dashboard.css`.

**Akzeptanzkriterien:**
- `webui_test.go`/Asset-Test: der neue Tab ist eingebettet und wird ausgeliefert;
  ETag/Embed-Mechanik unverändert.
- Manuelle Akzeptanz (Doku im PR): mit admin-Token zeigt der Tab online-Keys und
  füllt den Feed, wenn `CLIO_AUTH_EVENTS=on`.

---

### WP-07 — Dokumentation: ADR-030 + ARCHITECTURE.md + README/OpenAPI

**Ziel:** Doku-First-Prinzip schließen.

**Inhalt:**
- **ADR-030** in `ARCHITECTURE.md` (nach ADR-029) — bereits in diesem PR
  angelegt (siehe ARCHITECTURE.md).
- Roadmap (Abschnitt 6, Stufe 5/UI) um den Tab „Aktivität" ergänzen;
  Versionsnummer/Datum oben nachziehen.
- `GET /api/v1/activity` in `internal/apidocs/openapi.yaml` ergänzen (ADR-011:
  handgepflegt).
- `README.md` und `examples/_env.sh`/`_env.ps1` um `CLIO_PRESENCE_WINDOW`,
  `CLIO_AUTH_EVENTS`, `CLIO_AUTH_DENIED_EVENTS` ergänzen; reservierten
  `/_clio/`-Namespace dokumentieren.

**Akzeptanzkriterien:**
- ADR-030 folgt exakt dem ADR-Format (Status/Kontext/Entscheidung/Konsequenzen).
- OpenAPI validiert; Beispielskripte erwähnen die neuen ENV-Variablen.

---

## 3. Review-Gate vor Merge (Sicherheit & Korrektheit)

- [ ] Kein Secret/Hash und keine Remote-IP in Snapshot, Events oder Logs.
- [ ] `/_clio/`-Namespace ist server-only: Client-Writes werden abgelehnt.
- [ ] `CLIO_AUTH_EVENTS=off` ⇒ **null** zusätzliche Events (Count-Regressionstest).
- [ ] Interne Auth-Writes erzeugen keine Folge-Aktivitäts-/Session-Events
      (keine Rekursion).
- [ ] `access-denied`-Events sind rate-limitiert (kein Amplifikations-/DoS-Hebel).
- [ ] `activity`-Endpoint ausschließlich unter Scope `admin`.
- [ ] Presence ist prozesslokal; Neustart-Verlust ist dokumentiert und gewollt.
- [ ] `-race`-sauber; Sweeper-Goroutine wird im Shutdown beendet.
- [ ] Keine neue externe Go-Abhängigkeit (go.mod unverändert).

## 4. Sequenz & Abhängigkeiten

```
WP-01 (Registry) ─┬─> WP-02 (Middleware) ─┬─> WP-03? (Config, parallel) 
                  │                       ├─> WP-05 (Metriken)
                  │                       └─> WP-04 (Lifecycle-Events) ─> WP-06 (UI)
                  │
WP-03 (Config) ───┘   (parallelisierbar mit WP-01)
WP-07 (Doku) ......................................... begleitend, vor Merge fertig
```

## 5. Nicht-Ziele (bewusst ausgeschlossen)

- **Kein Event pro Read/Write** — nur Lifecycle. (Event-Amplifikation; Reads
  sind der Hot-Path.)
- **Keine persistente Presence** über Neustarts — die dauerhafte Spur sind die
  Lifecycle-Events.
- **Keine Remote-IP/Geo/User-Agent-Erfassung** per Default (Datensparsamkeit;
  additiv nachrüstbar).
- **Kein Ausschluss von `/_clio/` aus rekursiven Reads** (vorerst sichtbar für
  Transparenz; additiv nachrüstbar, falls als Rauschen empfunden).
- **Kein RBAC/feingranulares Audit** über die bestehenden Scopes hinaus (ADR-025).
- **Keine echte Sitzungsverwaltung/Logout** — clio bleibt tokenbasiert und
  sessionlos; „Session" ist hier nur die Presence-Lücke.
