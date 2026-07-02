# Projekt-Analyse clio — harte Probe, Maßnahmenplan & Roadmap

> **Status:** Analyse (2026-07-02) · **Commit:** `50b7697` · **Version:** v0.3.0
> **Autor:** Code-Review + Betriebs-/Härtetest auf einer laufenden Instanz
> **Zweck:** Ehrliche Standortbestimmung als Grundlage für die Planung nach v0.3.0.
> Bewusst kritisch gehalten — die Stärken sind real, die Lücken auch.

Dieses Dokument ist eine **Analyse**, keine ADR und kein Umsetzungsplan. Es
benennt, wo gearbeitet werden sollte, priorisiert nach Risiko, und schlägt eine
Roadmap mit Work-Packages (WPs) vor. Umsetzungsdetails gehören in die jeweiligen
Pläne unter `docs/plans/` bzw. neue ADRs.

---

## 1. Gesamturteil

clio ist ein **überdurchschnittlich sorgfältig gebauter Single-Instance-Event-Store**.
Der Code ist konsequent streaming-orientiert, die Fehlerbehandlung ist typisiert,
Kommentare verweisen auf ADRs, die Testbasis ist breit (84,4 % Coverage, Race-Detector
in CI, Smoke-E2E, Tamper-Detection-Tests) und die Betriebs-Doku ist bemerkenswert
ehrlich über die eigenen Grenzen.

Die harte Probe hat das im **n=1-Pfad weitgehend bestätigt**, aber zwei Klassen von
Problemen aufgedeckt:

1. **Zwei kritische Bugs im Kern-Streaming-Pfad** (K1, K2), die genau die Garantien
   verletzen, die der Code selbst in Kommentaren verspricht — beide klein zu fixen,
   aber wirkungsvoll.
2. **Die Partitionierung (`CLIO_PARTITIONS>1`) ist ein Prototyp**, dessen Semantik bei
   N>1 an mehreren Stellen *still falsch* wird (Preconditions, State-Cache, Cursor,
   Backup, Verify). Die ADRs 034–038 sind „akzeptiert", aber real umgesetzt ist nur
   das Fundament (Hash-Ring + file-per-partition). Als „Grundlagen für horizontale
   Skalierung" ist das ehrlich etikettiert — gefährlich wird es nur, weil N>1 heute
   ohne Warnung *scharf* geschaltet werden kann.

**Kernbotschaft:** Der n=1-Store ist nah an „solide betreibbar" — es fehlen vor allem
zwei Bugfixes, ein Request-Limit und ein paar Betriebs-Härtungen. Die horizontale
Skalierung dagegen ist Baustelle und sollte bis zur Behebung der N>1-Korrektheitsfehler
**explizit verriegelt oder als experimentell gekennzeichnet** werden.

### Reifegrad-Ampel

| Bereich | Ampel | Begründung |
|---|---|---|
| Kern-Schreibpfad (n=1) | 🟢 | atomar, Hash-Kette, Crash-Recovery im Test bestanden |
| Lesen / Query / CEL | 🟢 | funktioniert, Kostenmodell, Index-Wahl; Timeout default aus |
| Live-Streaming (observe) | 🔴 | K1 trennt Streams nach 30 s; K2 verliert Events bei Nebenläufigkeit |
| Auth / Scopes | 🟢 | zeitkonstanter Vergleich, saubere Grants, XSS-sicher |
| Betriebshärtung (DoS) | 🟡 | kein Request-Limit, kein Default-Query-Timeout, keine Body-Limits |
| Backup / Restore (n=1) | 🟢 | E2E getestet, verify OK |
| Partitionierung (N>1) | 🔴 | Preconditions/State/Cursor/Backup/Verify bei N>1 still falsch |
| Tests / CI | 🟡 | gute Basis, aber kein Fuzzing, keine Crash-Tests, kein Lint-Gate |
| Doku / ADRs | 🟢 | umfangreich, ehrlich; kleinere Status-Drifts |

---

## 2. Was die harte Probe ergeben hat

Durchgeführt auf einer frisch gebauten Instanz (`50b7697`), Linux, localhost.

### Grün (bestätigt funktionsfähig)

- **Build & statische Prüfung:** `go build ./...`, `go vet ./...` sauber; gofmt sauber.
- **Testlauf mit Race-Detector:** `go test -race -count=1 ./...` — **alle 16 Pakete grün**
  (`internal/store` ~26 s, sonst < 12 s). Kein `TODO`/`FIXME` im produktiven Code.
- **Funktions-Smoke:** ping/write/read OK, Hash-Kette + `predecessorhash` in der Antwort;
  unauthentifizierter Write → 401; `isSubjectPristine`-Konflikt → 409.
- **Lasttest (8 Worker, Batch 10, localhost):** **~4.600 Events/s, ~460 req/s, 0 Fehler**
  über 15 s (68.880 Events). Für einen Single-Writer mit voller Durability solide.
- **Crash-Test:** `kill -9` mitten unter Volllast → sauberer Neustart, keine Fehler im Log,
  Offline-`verify`: **OK — 99.602 Events, Kette intakt.** Die Crash-Recovery-Kernzusage hält.

### Rot / Gelb (Befunde aus der Probe, unten im Detail belegt)

- **K1 — `observe-events` stirbt nach 30 s.** Selbst reproduziert: Der Server trennt den
  Stream nach exakt 30 Sekunden (curl exit 18, partieller Transfer), obwohl er unendlich
  offen bleiben soll. Ursache: der Middleware-Wrapper verhindert das Aufheben der
  Write-Deadline. **Das entwertet das Live-Streaming-Feature.**
- **Kein Request-Size-Limit:** Ein 60-MB-Payload wurde mit HTTP 200 akzeptiert (RSS-Spitze
  640 MB). Die REST-API liest `r.Body` ohne `http.MaxBytesReader`.
- **`verify` ist partitionsblind:** Auf einer partitionierten DB mit 48.000 Events (alle in
  `.p3`) meldete `cliostore verify` **„OK — 0 events"** — ein stiller Fehlalarm von
  Integrität. Nur die Basisdatei wird geprüft.
- **Partitionierung bringt bei realistischer Last keinen Gewinn**, wenn wie üblich wenige
  Sources dominieren (Hot-Partition — ADR-034 warnt davor, aber es gibt keine Mitigation).

---

## 3. Befunde nach Priorität

Schweregrade: 🔴 kritisch · 🟠 hoch · 🟡 mittel · ⚪ niedrig.
Quellen sind `Datei:Zeile` im Repo.

### 🔴 P0 — Kritisch (Kernzusagen verletzt, n=1 betroffen)

**K1 · Streaming-Deadline durch Middleware wirkungslos — alle Streams sterben nach 30 s.**
`internal/httpapi/middleware.go:10-36`, Handler `handlers.go:519,773,988,1279`,
Server `cmd/cliostore/main.go:200`.
Jeder Handler läuft hinter `instrument()` und bekommt den `statusRecorder`-Wrapper. Der
implementiert **kein `Unwrap() http.ResponseWriter`**, deshalb liefert
`http.NewResponseController(w).SetWriteDeadline(time.Time{})` ein `ErrNotSupported`, das
mit `_ =` verschluckt wird. Der Server hat `WriteTimeout: 30s`. Folge: `observe-events`,
lange `read-events`/`run-query`-Scans und `GET /backup` > 30 s werden hart getrennt —
**genau das, was der Code laut Kommentar verhindern soll.** In der Probe selbst
reproduziert (Trennung nach 30 s). **Fix (1 Zeile):**
`func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }`.
Danach Regressionstest: observe-Stream > 30 s offen halten.

**K2 · Publish-Ordering-Race — stiller, permanenter Event-Verlust im Live-Stream.**
Publish `internal/httpapi/handlers.go:634`, Dedup `handlers.go:1085-1103`
(`seq <= delivered[partition]` → verwerfen).
Zwei nebenläufige Writer: bbolt committet Tx1 (Seq 1–5) vor Tx2 (Seq 6–10), aber die
Handler rufen `broker.Publish` unsynchronisiert — der zweite kann zuerst publizieren. Der
Observer sieht 6–10, setzt `delivered=10`; die danach ankommenden 1–5 werden als
„Duplikate" verworfen. Kein `Lost`-Signal, der Cursor steht auf 10 → **auch der Reconnect
holt 1–5 nie nach.** Group Commit macht dieses Fenster wahrscheinlicher. **Fix:** Publish in
Commit-/Sequenzordnung serialisieren (Publish im Store nach Commit unter Ordnungs-Lock),
oder Lücken-Erkennung statt „≤ delivered"-Verwerfen. Regressionstest mit nebenläufigen
Writern + Observer.

### 🟠 P1 — Hoch (DoS-Fläche & N>1-Korrektheit)

**S-H1 · Kein Request-Body-Limit auf der REST-Fläche (Speicher-DoS).**
`internal/httpapi/respond.go:13-20` (`decodeJSON` ohne `http.MaxBytesReader`), kein
`MaxHeaderBytes` am Server (`main.go:190-203`). Ein Client mit `write`-Scope treibt mit
einem großen `events`-Array den Prozess in den OOM (Decode + `canonicalData`-Kopie +
Marshal, alles im RAM). Der MCP-Pfad hat ein 8-MiB-Limit (`mcp/http.go:27`) — die
eigentliche API nicht. In der Probe mit 60 MB bestätigt. **Fix:** `http.MaxBytesReader`
mit konfigurierbarem Limit (`CLIO_MAX_BODY_BYTES`) + `MaxHeaderBytes`.

**S-H2 · API-Key-Secrets nur mit ungesalzenem SHA-256 (kein KDF).**
`internal/auth/keyring.go:275-278` (`HashSecret`), `store_authkeys.go:19`.
Serverseitig generierte Secrets haben 160 Bit Entropie (unkritisch), aber vom Betreiber
gewählte Bootstrap-/Legacy-Secrets (`CLIO_BOOTSTRAP_ADMIN_KEY`, `CLIO_API_TOKEN`) sind
angreifbar: Wer Lese-Zugriff auf DB **oder Backup** hat (das Backup enthält den Keyring),
kann sie offline per Wörterbuch/Rainbow-Table angreifen — kein Salt, kein rechenintensiver
KDF. **Fix:** salted KDF (argon2id/scrypt) für vom Menschen gewählte Secrets; per-Key-Salt.

**C-H1 · Preconditions greifen bei N>1 nur in der Ziel-Partition.**
`internal/store/store.go:1294-1300,1325,1552-1660`.
Subject ≠ Source: dasselbe Subject kann von verschiedenen Sources in verschiedenen
Partitionen liegen. `isSubjectPristine` meldet „leer", obwohl das Subject anderswo Events
hat. **Optimistic Concurrency ist bei N>1 faktisch außer Kraft.**

**C-H2 · Partitionsanzahl wird nicht persistiert/validiert — `CLIO_PARTITIONS` ändern
korrumpiert still.** `store.go:295-338`, `shard.go:60-108`. Kein Meta-Eintrag, kein
Abgleich mit vorhandenen `.pN`-Dateien. Wer N ändert (oder vergisst), routet Sources auf
andere Partitionen: alte Events werden unsichtbar, Streams zerreißen, `Verify` bleibt grün.
**Fix (Muss vor jedem ernsthaften N>1):** N + vnodes in der DB verankern, beim Open hart prüfen.

**C-H3 · State-Cache: inkrementeller Fold bei N>1 falsch.**
`internal/httpapi/state.go:136-160`, `store.go:133-138`. `lastSeq` (per-Partition-Sequenz)
wird als globaler Cursor benutzt → beim Nachfalten werden Events anderer Partitionen mit
Seq ≤ lastSeq übersprungen → dauerhaft falscher Zustand und `revision`. `?at=` (skalar) hat
bei N>1 keine wohldefinierte Semantik.

**C-H4 · `handleBackup` liefert bei N>1 ein leeres 200-„Backup".**
`handlers.go:518-538`, `store_backup.go:83-97`. `Backup()` scheitert bei `partitions()>1`
mit `ErrBackupMultiPartition` **vor dem ersten Byte**; der Handler loggt nur und returned →
Go schreibt 200 mit Download-Headern und **leerem Body**. Der Operator hält eine 0-Byte-Datei
für ein Backup. **Fix:** 5xx/501 senden, solange keine Bytes flossen (und N>1-Backup lösen).

**C-H5 · Langsame Streaming-Clients halten bbolt-Read-Tx + Shard-RLock unbegrenzt.**
`handlers.go:762-798,1059,1418`, `shard.go:112-116,207`, `store_compact.go:166-192`.
Ein hängender Reader hält die Read-Tx (Freelist wächst → DB-Wachstum) und blockiert mit
Auto-Compaction (die `dbMu.Lock` exklusiv nimmt) **alle** neuen Reads/Writes des Shards.
`CLIO_QUERY_TIMEOUT` ist default aus und deckt nur run-query. **Fix:** serverseitiges
Zeit-/Fortschrittslimit für **alle** Streaming-Reads, mit sinnvollem Default.

### 🟡 P2 — Mittel (Härtung, N>1-Semantik, Betrieb)

- **Q-M1 · CEL ohne Kostenlimit, Query-Timeout default aus.** `query/query.go:81-93`
  (kein `cel.CostLimit`), `config.go:121/251` (Timeout 0). Ein read-Nutzer kann mit einer
  teuren Query CPU binden. **Fix:** natives CEL-Kostenlimit setzen **und** einen
  konservativen Default-Timeout aktivieren.
- **SEC-M2 · Keine Security-Header** (CSP, `X-Frame-Options`, `X-Content-Type-Options`,
  HSTS). `middleware.go:39-64` setzt nur `Cache-Control`. Dashboard `/ui` ohne
  Clickjacking-/XSS-Tiefenschutz. **Fix:** Header-Middleware; CSP für `/ui`.
- **SEC-M3 · Bearer-Token im `sessionStorage`** (`webui/static/js/dashboard.js`). Mit M2
  kombiniert würde ein XSS das (Admin-)Token exfiltrieren.
- **C-M5 · Skalare Grenzen (`lowerBound`/`upperBound`/`?at=`) haben bei N>1
  per-Partition-Semantik** (`store.go:133-138`). Paging über `lowerBound` ist bei N>1
  semantisch kaputt; per-Partition-Cursor existiert nur für observe, nicht für
  read-events/run-query/state.
- **M-M2 · Multi-Typ-Query puffert alle Treffer-Sequenzen im RAM** (`store.go:955-965`) —
  kein Gegenstück zum vorhandenen `maxRecursiveSeqBuffer`.
- **M-M3 · `read-subjects` (flach & `tree=true`) materialisiert alle Subjects**
  (`store.go:658-711`, `handlers.go:209-224`) — OOM-Pfad bei hoher Kardinalität.
- **MCP-M4 · In-Process-Transport puffert die ganze Tool-Antwort**, Limit greift erst
  danach, Truncation ist still (`mcp/client.go:101,112-118`).
- **MCP-M5 · `POST /mcp` reicht die volle Key-Fläche durch** (write/register/delete). Für
  Agent-Kontexte (Prompt-Injection) erhöht riskant; eine engere MCP-Scope-Ebene fehlt.
- **SEC-M6 · `clio-mcp -token` sichtbar in der Prozessliste** (`cmd/clio-mcp/main.go:32`).
- **M-M1 · TOCTOU zwischen Schema-Validierung und Write** (`store.go:1306-1315`,
  `store_schema.go:53-79`) kann die „alle Events erfüllen ihr Schema"-Invariante verletzen.
- **M-M6 · Dev-Bulk-Import umgeht `/_clio/`-Schutz und Subject-Grants** (`dev.go:83-88`) —
  nur Dev-Mode + Admin, daher mittel.
- **M-M7 · Backup hält die Read-Tx für die gesamte Client-Download-Dauer**
  (`store_backup.go:83-97`) → Slowloris-mit-Admin-Key kann Freelist blockieren.

### ⚪ P3 — Niedrig (Informationspreisgabe, Aufräumen, Struktur)

- **Fehler-Leaks:** `decodeJSON` gibt Parser-Fehlertexte (Feldnamen) an den Client
  (`respond.go:13-19`); `/api/v1/info` verrät `databaseFilePath`/Listen-Adresse
  (`handlers.go:84-86`); `/metrics` ohne Auth (`routes.go:69`).
- **Audit-Abdeckung:** abgelehnte Zugriffe (401/403) landen nur im flüchtigen slog, nicht
  im persistenten Audit-Log; `recordAudit` ist „best effort" (Aktion läuft auch bei
  Audit-Schreibfehler; `audit.go:47-62`).
- **Duplikation/Tot:** `lookupPath`/`setPath` doppelt (`query.go:412` vs `reduce.go:200`),
  Bound-Parsing 3×, Fehler-Mapping write vs. bulk kopiert; tot: `Store.batch`
  (`store.go:360`), `frameRaw` (`codec.go:34`).
- **Gott-Dateien:** `store.go` (1990 Z.) und `httpapi/handlers.go` (1437 Z.) gehören
  zerlegt (append/read/index/verify bzw. eine Datei je Ressourcengruppe).
- **Shutdown:** `srv.Shutdown` bricht offene observe-Streams nicht ab (kein `BaseContext`)
  → läuft mit aktiven Observern in den 10-s-Timeout (`main.go:215-223`).

---

## 4. Test- & CI-Lücken

Coverage gesamt **84,4 %** (Self), Race-Detector + Smoke laufen bereits in CI (die
`docs/testing.md` untertreibt das). Schwachpunkte:

- **`cmd/clio-mcp` nur 40,5 %** — der MCP-Server-Einstieg ist praktisch ungetestet und
  auch nicht im Smoke-Test. `internal/metrics` 79 % (`ObserveAuthDecision` 0 %),
  `auth.Allows` 0 % (öffentliche Autz-Fassade), `store.shardForSource` 0 % (Multi-Partition-
  Routing wird nicht end-to-end getestet — passt zum Prototyp-Status).
- **Kein Fuzzing** — obwohl an mehreren Grenzen untrusted Input geparst wird (CloudEvents,
  JSON-Schema-Kompilierung, Query-Requests, Scope-Grammatik, JSON-RPC). Go-natives Fuzzing
  wäre billig und würde genau die Panic-/DoS-Klassen finden.
- **Keine Crash-Consistency-Tests** (torn writes, ENOSPC, partieller fsync) — für einen
  Event-Store mit Durability-Zusage die Kern-Eigenschaft. `make chaos-smoke` ist in der Doku
  notiert, aber nicht umgesetzt.
- **Keine Property-Based-Tests** für Hash-Ring (Balance, minimale Umverteilung N→N+1),
  Reduce-Semantik (LWW-Assoziativität) und Hash-Ketten-Invarianten.
- **CI-Lücken:** kein `golangci-lint`/`staticcheck` (nur gofmt+vet), **kein Coverage-Gate**
  (Zahl wird nur gedruckt), **kein `govulncheck`/Dependabot** (auffällig für ein Projekt mit
  `security.md`), **Release ohne Test-Gate** (`release.yml` published bei Tag ohne
  Test-`needs:`), keine OS-Matrix (nur ubuntu, obwohl Windows dokumentiert ist).

---

## 5. Doku- & ADR-Abgleich (SOLL/IST)

- **Erledigt & solide:** Stufen 0–3 (MVP, Concurrency, Observe, Robustheit), v0.2.0
  (benannte Keys/Scopes ADR-025, Kompression ADR-024, Data-Index ADR-029), v0.3.0
  (Activity/Presence ADR-030, Audit ADR-032, Backup/Restore Stufe 1 ADR-031,
  Subject-Scopes ADR-033, Zustandssicht + Reduce-Specs + Snapshot-Cache ADR-039/040/041,
  MCP ADR-042). ADR-042 ist die einzige mit Zusatz „umgesetzt" — der Code bestätigt das.
- **„Akzeptiert" ≠ „umgesetzt":** Die ADRs 034–038 (Partitionierung) sind akzeptiert, real
  umgesetzt ist **nur WP-1 (Hash-Ring) + file-per-partition (ADR-037)**. Es fehlen:
  Cross-Partition-Anchoring (**ADR-035, im Code explizit als offen markiert**,
  `store.go:1461-1465`), Write-Leases/Coordinator/Raft (ADR-038), der Scatter-Gather-
  Read-Path mit Cursor-Vektor (ADR-036 nur teilweise), Rebalancing mit Datentransfer.
- **Status-Drift:** `docs/plans/README.md` führt `security-api-keys-plan` und
  `activity-presence-plan` als „Planung (umgesetzt)", die Plandokumente selbst tragen noch
  „PLANUNG" ohne WP-Abhakung. Die Coverage-Tabelle in `docs/testing.md` ist veraltet
  (`metrics` 93 % → real 79 %). **Empfehlung:** einen Sync-Durchlauf über die Status-Felder.
- **Bewusste Nicht-Ziele** (kein Clustering-HA, kein RBAC, keine Projektionen, kein
  Failover) sind in `production-readiness.md` und `swiss-api-guidelines-gap.md` ehrlich
  dokumentiert — die bleiben Nicht-Ziele und sind **keine** Findings.

---

## 6. Maßnahmenplan (priorisiert, sofort umsetzbar)

Sortiert nach Wirkung/Aufwand. Größen: **S** ≤ ½ Tag, **M** ≈ 1–2 Tage, **L** > 2 Tage.

### Sofort (diese Woche) — Blocker für „n=1 solide"

| # | Maßnahme | Bezug | Größe |
|---|---|---|---|
| A1 | `Unwrap()` an `statusRecorder` ergänzen + observe->30 s-Regressionstest | K1 | **S** |
| A2 | Publish in Sequenzordnung serialisieren + Lücken-Erkennung; Test mit nebenläufigen Writern | K2 | **M** |
| A3 | `http.MaxBytesReader` + `MaxHeaderBytes` (`CLIO_MAX_BODY_BYTES`, sinnvoller Default) | S-H1 | **S** |
| A4 | Default-`CLIO_QUERY_TIMEOUT` aktivieren **und** natives CEL-Kostenlimit setzen | Q-M1, C-H5 | **S** |
| A5 | N>1 verriegeln oder als „experimentell" mit Warnung beim Start markieren | §5, C-H* | **S** |

### Kurzfristig (nächste 2–4 Wochen) — Betriebshärtung n=1

| # | Maßnahme | Bezug | Größe |
|---|---|---|---|
| B1 | Salted KDF (argon2id) für vom Betreiber gewählte Secrets; per-Key-Salt, Migrationspfad | S-H2 | **M** |
| B2 | Security-Header-Middleware (CSP für `/ui`, `X-Frame-Options`, `nosniff`, HSTS opt-in) | SEC-M2/M3 | **S** |
| B3 | Streaming-Read-Zeitlimit für **alle** Lesepfade (nicht nur run-query), Default an | C-H5 | **M** |
| B4 | `handleBackup` bei Fehler vor erstem Byte → 5xx/501 statt leerem 200 | C-H4 | **S** |
| B5 | Fehlertexte generisch halten; `databaseFilePath` aus `/info` entfernen; `/metrics` optional auth-/bind-gaten | P3 | **S** |
| B6 | CI: `golangci-lint` + `govulncheck` + Coverage-Gate + Test-`needs:` vor Release + Dependabot | §4 | **M** |
| B7 | Fuzz-Targets für CloudEvents-, Query-, Scope-, JSON-RPC-Parser (Kurzläufe in CI) | §4 | **M** |

### Mittelfristig — Struktur & Vertrauen

| # | Maßnahme | Bezug | Größe |
|---|---|---|---|
| C1 | `store.go` & `handlers.go` zerlegen (append/read/index/verify; Datei je Ressource) | P3 | **M** |
| C2 | Crash-Consistency-Harness (`make chaos-smoke`: kill-Loop + `verify`) | §4 | **M** |
| C3 | Property-Tests: Hash-Ring-Balance/Umverteilung, LWW-Assoziativität, Ketten-Invarianten | §4 | **M** |
| C4 | Doku-Status-Sync (`plans/README.md`, `testing.md`-Coverage, ADR-Status „umgesetzt"?) | §5 | **S** |
| C5 | Duplikation entfernen (`lookupPath`/`setPath`, Bound-Parsing, Fehler-Mapping); toten Code löschen | P3 | **S** |

---

## 7. Roadmap-Vorschlag (v0.3.0 → v1.0)

Drei Releases mit klarem Fokus. Jede Zeile ist ein WP; Größe wie oben.

### v0.4.0 — „n=1 produktionsreif härten" (Fokus: Korrektheit & DoS)

Ziel: Der Single-Instance-Pfad ist ohne Sternchen empfehlbar; N>1 ist sauber als
experimentell abgegrenzt.

| WP | Inhalt | Enthält | Größe |
|---|---|---|---|
| WP-4.1 | Streaming-Fix | A1 (K1) + Regressionstest | S |
| WP-4.2 | Verlustfreie Live-Ströme | A2 (K2) + Nebenläufigkeits-Test | M |
| WP-4.3 | DoS-Härtung | A3 Body-Limit, A4 Query-Timeout+CEL-Kosten, B3 Read-Timeout | M |
| WP-4.4 | Secret-Hardening | B1 KDF + Migration | M |
| WP-4.5 | HTTP-Härtung | B2 Security-Header, B4 Backup-Fehlercode, B5 Info-Leaks | M |
| WP-4.6 | CI-Gates | B6 (Lint/Vuln/Coverage/Release-Gate/Dependabot) | M |
| WP-4.7 | N>1-Verriegelung | A5 + ADR „N>1 experimentell" dokumentieren | S |

**Definition of Done v0.4.0:** K1/K2 gefixt und getestet; kein unbegrenzter Request-Body;
Query-Timeout + CEL-Kostenlimit default an; CI mit Lint+Vuln+Coverage-Gate; Release-Workflow
mit Test-Gate; N>1 nur mit expliziter „experimental"-Bestätigung startbar.

### v0.5.0 — „Vertrauen & Wartbarkeit" (Fokus: Tests & Struktur)

| WP | Inhalt | Enthält | Größe |
|---|---|---|---|
| WP-5.1 | Fuzzing | B7 (Parser-Fuzz-Targets, CI-Kurzläufe) | M |
| WP-5.2 | Crash-Consistency | C2 (`make chaos-smoke` + `verify`) | M |
| WP-5.3 | Property-Tests | C3 (Ring, Reduce, Ketten) | M |
| WP-5.4 | Refactor Gott-Dateien | C1 + C5 (Duplikation/Toter Code) | M |
| WP-5.5 | MCP härten | MCP-M4 Streaming statt Puffern, MCP-M5 engere Scopes, SEC-M6 kein Token-Flag | M |
| WP-5.6 | Doku-Sync | C4 | S |

**DoD v0.5.0:** Fuzz-Targets in CI; Crash-Test-Harness grün; Property-Tests für Kerninvarianten;
keine Datei > ~800 Zeilen im Kern; MCP mit dediziertem Scope-Konzept.

### v0.6.0 → v1.0 — „Horizontale Skalierung real machen" (nur wenn Bedarf besteht)

Reihenfolge folgt `docs/plans/partitioning-plan.md`. Diese Etappe ist groß und sollte erst
starten, wenn ein **konkreter Multi-Node-Bedarf** existiert (ADRs sagen das selbst).

| WP | Inhalt | Bezug | Größe |
|---|---|---|---|
| WP-P.1 | Ringkonfiguration persistieren & beim Open hart validieren | C-H2 | M |
| WP-P.2 | `verify`/Backup partitionsweit (alle `.pN`, konsistenter Multi-Datei-Snapshot) | Probe, C-H4 | L |
| WP-P.3 | Preconditions partitionsübergreifend korrekt (oder Subject→Partition binden) | C-H1 | L |
| WP-P.4 | Per-Partition-Cursor für read-events/run-query/state; State-Cache N>1-korrekt | C-H3, C-M5 | L |
| WP-P.5 | Cross-Partition-Anchoring / Merkle-Anker (ADR-035, WP-5) | §5 | L |
| WP-P.6 | Coordinator/Write-Leases, Rebalancing mit Datentransfer (ADR-038) | §5 | L |
| WP-P.7 | Hot-Partition-Mitigation & Skalierungs-Benchmark als CI-Bench | Probe (ADR-034) | M |

**DoD „N>1 nicht mehr experimentell":** WP-P.1–P.4 abgeschlossen (Ringkonfig verankert,
verify/Backup partitionsweit, Preconditions & Cursor bei N>1 korrekt). Erst dann darf die
„experimental"-Verriegelung aus WP-4.7 fallen. WP-P.5–P.7 sind der Weg zu echtem Cluster/HA.

---

## 8. Empfohlener nächster Schritt

**WP-4.1 und WP-4.2 zuerst** — beide sind kleine, klar abgegrenzte Patches, die die
schwerwiegendsten Befunde (K1, K2) beheben und je einen Regressionstest bekommen. Danach
WP-4.3 (Body-Limit + Timeouts) und WP-4.7 (N>1 verriegeln), weil sie mit minimalem Aufwand
die größte Risikoreduktion bringen. Das ergibt einen v0.4.0-Release-Kandidaten, der den
Single-Instance-Pfad ohne Vorbehalt empfehlbar macht.
