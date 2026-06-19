# Projekt-Architektur & Kontext: `cliostore` — ein Event Store in Go

> **Zweck dieses Dokuments**
> Dieses Dokument ist die *Single Source of Truth* für das Projekt. Es ist so geschrieben, dass eine KI oder eine Person ohne Vorwissen nach dem Lesen vollständig versteht: **Was** gebaut wird, **warum**, **welche Ziele** verfolgt werden, **welche Entscheidungen** getroffen wurden und **wo das Projekt aktuell steht**. Es kombiniert ein Kontextdokument mit eingebetteten Architecture Decision Records (ADRs).
>
> **Status des Gesamtprojekts:** `IN ENTWICKLUNG` — **Stufe 0–3 abgeschlossen** plus **Ed25519-Signaturen** (Authentizität). Write/Read/Observe, Optimistic Concurrency, Hash-Kette + Signaturen (`/verify`, `/public-key`), Event-Typen + JSON-Schemas, Group Commit (`CLIO_SYNC`), Observability (`/metrics`), Distribution (Cross-Builds/Docker/Release), Kompaktierung (`cliostore compact`), OpenAPI/Swagger UI, **CEL-Abfragen (`run-query`, ADR-017) mit Typ-Index (ADR-021)**, ein **eingebettetes Betriebs-Dashboard (`/ui`, ADR-020)** und **optionale transparente Wert-Kompression der Ablage (`CLIO_COMPRESS`, ADR-024)**. Offen (Stufe 4): Aggregation/Grouping und Snapshots.
> **Letzte Aktualisierung:** 2026-06-17
> **Dokumentversion:** 1.38

---

## 1. Worum geht es? (Elevator Pitch)

`cliostore` ist eine eigenständige, von Grund auf in Go geschriebene Neuimplementierung eines dedizierten **Event Stores**, funktional orientiert am Vorbild **EventSourcingDB** (von the native web GmbH). Es ist *kein* Fork und nutzt keinen Code des Originals — es ist eine unabhängige Implementierung, die denselben Funktionsumfang und dieselben API-Konzepte nachbaut, um Event-Sourcing-Systeme zu betreiben.

Der Name **cliostore** verbindet **Clio**, die griechische Muse der Geschichtsschreibung, mit **store** — passend für ein System, dessen einzige Aufgabe es ist, die vollständige, unveränderliche Geschichte aller Ereignisse zu bewahren und wieder erzählbar zu machen. (Kurzform im Code/Sprachgebrauch: „Clio".)

Ein Event Store speichert Zustandsänderungen einer Anwendung als unveränderliche, geordnete Ereignisse (Events) in einem Append-only-Log, statt nur den aktuellen Zustand zu überschreiben. Der aktuelle Zustand wird bei Bedarf durch erneutes Abspielen (Replay) der Events rekonstruiert.

**Kernidee in einem Satz:** Ein einzelnes, abhängigkeitsfreies Binary, das Events über eine einfache HTTP-API schreibt, liest und live beobachtbar macht — mit garantierter Unveränderlichkeit und strikter Ordnung.

---

## 2. Motivation & Ziele

### 2.1 Warum dieses Projekt?

- **Lernen & Verstehen:** Event Sourcing und der Aufbau eines Storage-Systems sollen durchdrungen werden, indem die Mechanik selbst gebaut wird.
- **Unabhängigkeit:** Das Original ist nur bis 25.000 Events kostenlos; darüber hinaus ist eine kommerzielle Lizenz nötig. Eine eigene Implementierung entfernt diese Grenze und schafft volle Kontrolle.
- **Anpassbarkeit:** Eine eigene Codebasis lässt sich auf die eigenen Bedürfnisse zuschneiden.

### 2.2 Projektziele (Goals)

1. Ein funktional brauchbarer Event Store, der die Kernoperationen **Write / Read / Observe** über HTTP beherrscht.
2. Garantien: **Append-only**, **strikte Ordnung**, **atomare Schreibvorgänge**, **Optimistic Concurrency**.
3. **CloudEvents** als Event-Format, kompatibel zur Spezifikation des Vorbilds.
4. Distribution als **einzelnes, statisch gelinktes Binary** für macOS/Linux/Windows auf x86 und ARM.
5. Schrittweiser Aufbau vom MVP zu höheren Reifegraden — jede Stufe für sich lauffähig und nützlich.

### 2.3 Nicht-Ziele (Non-Goals)

Diese Punkte sind **bewusst ausgeschlossen** (zumindest in absehbaren Stufen), genau wie beim Vorbild:

- **Keine Projektionen:** Die DB speichert und liefert Events. Das Ableiten von Lesemodellen (Projektionen) ist Aufgabe der Anwendung.
- **Keine Code-Ausführung:** Keine Handler, keine Workflows, keine serverseitige Geschäftslogik.
- **Kein Clustering / keine horizontale Skalierung (vorerst):** Das System läuft als **Single-Instance**. Dies ist eine bewusste, vereinfachende Designentscheidung (siehe ADR-002).
- **Kein RBAC:** Authentifizierung erfolgt über benannte API-Keys mit groben Scopes (`read`/`write`/`admin`, ADR-025). Keine feingranularen Rollen/Mandantentrennung. *(Historisch: anfangs ein einzelnes API-Token, ADR-008 — abgelöst durch ADR-025.)*
- **Keine GDPR-Spezialfunktionen** auf DB-Ebene (Verantwortung der Anwendung).
- **EventQL** (eigene Query-Sprache) ist explizit ein *spätes* Ziel und für den Kern nicht erforderlich (siehe ADR-007).

---

## 3. Fachliche Grundlagen & Domänenbegriffe

Damit jede KI dieselbe Sprache spricht:

| Begriff | Bedeutung |
|---|---|
| **Event** | Ein unveränderliches Faktum über eine Zustandsänderung. Im CloudEvents-Format. Erhält serverseitig eine ID, einen Zeitstempel und die Spec-Version. |
| **Event Candidate** | Ein vom Client gesendeter Event-Vorschlag, *bevor* er gespeichert wurde. Wird erst durch Annahme der DB zu einem echten Event. |
| **Subject** | Hierarchischer Pfad (z. B. `/books/42`), beginnt immer mit `/`. Identifiziert eindeutig einen **Event Stream**. Alle Events mit gleichem Subject gehören zum selben Stream. |
| **Stream** | Die geordnete Folge aller Events eines Subjects. |
| **Recursive** | Flag beim Lesen/Beobachten: bezieht alle untergeordneten Subjects mit ein. `/` + recursive = alle Events des Systems. |
| **Precondition** | Bedingung, die vor einem Write erfüllt sein muss (Optimistic Concurrency). |
| **Replay** | Erneutes Lesen historischer Events, optional gefiltert und paginiert. |
| **Observe** | Wie Read, aber die Verbindung bleibt offen und neue Events werden live nachgeliefert. |
| **Snapshot** | Gespeicherter aggregierter Zustand zu einem bestimmten Event, um Replay abzukürzen. |
| **Event-ID** | Global monoton steigende, eindeutige Kennung. Laut CloudEvents ein **String**, auch wenn numerisch aussehend. Grundlage der strikten Ordnung. |

---

## 4. Zielarchitektur (High Level)

```
                    ┌─────────────────────────────────────────┐
   HTTP-Clients ──▶ │  HTTP-API-Layer (POST-Routen, NDJSON)     │
   (curl, SDKs)     │  /write-events /read-events               │
                    │  /observe-events /ping                    │
                    └───────────────┬───────────────────────────┘
                                    │
                    ┌───────────────▼───────────────────────────┐
                    │  Core / Domain                              │
                    │  - CloudEvents-Validierung                  │
                    │  - Subject-Logik (Hierarchie, Prefix)       │
                    │  - Precondition-Auswertung                  │
                    │  - ID-Vergabe (monoton, serialisiert)       │
                    └───────────────┬───────────────────────────┘
                                    │
        ┌───────────────────────────┼───────────────────────────┐
        │                           │                            │
┌───────▼────────┐     ┌────────────▼─────────┐     ┌────────────▼─────────┐
│ Storage         │     │ Pub/Sub (Observe)    │     │ Index                │
│ Append-only Log │     │ Channels/Goroutinen  │     │ subject → seq        │
│ (bbolt)         │     │ pro Verbindung       │     │ persistent (bbolt)   │
└─────────────────┘     └──────────────────────┘     └──────────────────────┘
```

**Warum Go?** HTTP-Server, JSON, NDJSON-Streaming (`http.Flusher`) und Nebenläufigkeit (Goroutinen/Channels für Observe) sind alles Standardbibliothek. Cross-Compilation zu Single-Binaries via `GOOS`/`GOARCH` ist trivial. Statisches Linken ohne externe Abhängigkeiten passt exakt zum Distributionsziel.

---

## 5. HTTP-API-Kontrakt (Zielbild)

Alle Routen nutzen **POST** (außer ggf. `ping`), weil Parameter im Request-Body bequemer sind als in der Query-String-Kodierung. Antworten für Event-Listen erfolgen als **NDJSON** (ein JSON-Objekt pro Zeile).

| Route | Zweck | Stufe |
|---|---|---|
| `GET/POST /api/v1/ping` | Erreichbarkeitsprüfung | 0 |
| `POST /api/v1/write-events` | Ein oder mehrere Event-Candidates atomar schreiben, optional mit Preconditions | 0 → 1 |
| `POST /api/v1/read-events` | Events eines Subjects lesen; Optionen: `recursive`, `lowerBound`, `upperBound`, `types` (Filter nach Event-Typ) | 0 → 1 |
| `POST /api/v1/observe-events` | Wie read (inkl. `recursive`, `lowerBound`, `types`), aber Verbindung bleibt offen für Live-Updates; Reconnect via `lowerBound` | 2 |
| `GET /api/v1/verify` | Integrität der Hash-Kette (und ggf. Signaturen) prüfen | 3 |
| `GET /api/v1/public-key` | Öffentlicher Ed25519-Schlüssel (falls Signieren aktiv) | 3 |
| `GET /api/v1/read-event-types` | Alle bisher geschriebenen Event-Typen (Anzahl + `hasSchema`) | 3 |
| `GET /api/v1/read-subjects` | Alle bisher beschriebenen Subjects/Streams (Anzahl); optionaler `prefix`-Query für rekursiven Scope, `tree=true` für einen hierarchischen Baum (`count`/`total`) | 3 |
| `POST /api/v1/register-event-schema` · `GET /api/v1/read-event-schema` | JSON-Schema je Typ registrieren/lesen; Validierung beim Write (ADR-014) | 3 |
| `GET /api/v1/events/<subject>` | Komfort-Leseroute: Subject = URL-Pfad; Optionen als Query (`recursive` (Default true), `lowerBound`, `upperBound`, `type` (wiederholbar), `watch=true` für Live). `GET /api/v1/events` = Wurzel | 3 |
| `POST /api/v1/run-query` | CEL-basierte Abfrage (Scope + Prädikat), NDJSON (ADR-017) | 4 |
| `GET /api/v1/event-stats` | Histogramm der Eventmengen über die Zeit (nach Event-Zeit, beim Start aus der Historie aufgebaut; Start, Bucket-Breite, Zähler) — fürs `/ui`-Dashboard, ohne die Historie zu streamen | 3 |
| `GET /openapi.yaml` · `GET /docs` | OpenAPI-3-Spec bzw. interaktive Swagger UI (eingebettet, ohne Auth) | 3 |
| `GET /metrics` | Prometheus-Metriken (ohne Auth) | 3 |

**Auth:** Header `Authorization: Bearer kid.secret` gegen den Schlüsselbund (benannte API-Keys mit Scopes `read`/`write`/`admin`, ADR-025). Fehlender/ungültiger Schlüssel → 401, gültiger Schlüssel ohne nötigen Scope → 403. *(Historisch: ein einzelnes `Bearer <API_TOKEN>`, ADR-008 — abgelöst durch ADR-025; `CLIO_API_TOKEN` lebt nur noch als deprecated Bootstrap-Pfad fort.)*

### Beispiel Event-Candidate (Request-Body Auszug)

```json
{
  "source": "https://library.example.io",
  "subject": "/books/42",
  "type": "io.example.library.book-acquired",
  "data": { "title": "...", "author": "..." }
}
```

Felder `id`, `time`, `specversion` werden **serverseitig** ergänzt.

### Preconditions (Optimistic Concurrency)

- `isSubjectPristine(subject)` — schreibe nur, wenn der Stream noch leer ist.
- `isSubjectOnEventId(subject, id)` — schreibe nur, wenn das letzte Event des Streams diese ID hat.
- `isEventQlQueryTrue(query)` — schreibe nur, wenn eine EventQL-Abfrage true ergibt (spätes Ziel, Stufe 4).

---

## 6. Roadmap / Reifegrade (Stufenplan)

Jede Stufe ist für sich lauffähig. Statusmarkierungen: `⬜ offen` · `🟡 in Arbeit` · `✅ fertig`.

### Stufe 0 — MVP `✅`
*Schätzung: 1–2 Wochen (1 Person)*
- [x] Projekt-Skelett: Go-Modul `github.com/pblumer/clio`, HTTP-Server, Graceful Shutdown, Config via Env (`CLIO_ADDR`, `CLIO_API_TOKEN`, `CLIO_DB_PATH`)
- [x] `ping` (`GET`/`POST /api/v1/ping`)
- [x] Bearer-Token-Auth-Middleware (ein Token via Env-Var, konstante Vergleichszeit) — schützt die Datenrouten
- [x] `write-events`: Candidate validieren, CloudEvents-Felder (`id`/`time`/`specversion`) ergänzen, atomar (alles-oder-nichts) append-only schreiben
- [x] `read-events`: Events eines Subjects als NDJSON
- [x] Storage: `bbolt` — Bucket `events` (global, monotone Sequenz) + Subject-Index `subject → seq`
- **Ergebnis:** Events können geschrieben und gelesen werden. ✅

> **Hinweis:** Die atomare Mehrfach-Schreibung und die serialisierte, monotone ID-Vergabe (eigentlich Stufe-1-Punkte, ADR-003) ergeben sich aus der bbolt-Transaktion bereits hier „gratis" und sind umgesetzt. Verbleibend für Stufe 1: Preconditions sowie `lowerBound`/`upperBound` beim Lesen.

### Stufe 1 — Ordnung & Concurrency `✅`
*Schätzung: 1–2 Wochen*
- [x] Globale, monoton steigende Event-IDs (serialisiert) — via `bbolt`-Sequenz in der Schreibtransaktion
- [x] Atomares Schreiben mehrerer Events (alles-oder-nichts) — eine `bbolt`-Update-Transaktion pro Aufruf
- [x] Preconditions `isSubjectPristine`, `isSubjectOnEventId` — innerhalb der Schreibtransaktion ausgewertet; Verletzung → HTTP 409
- [x] `lowerBound` / `upperBound` beim Lesen — inklusive Event-ID-Grenzen
- [x] Serialisierte Write-Queue / einzelner Write-Mutex (siehe ADR-003) — durch bbolts Single-Writer-Transaktion erfüllt
- **Ergebnis:** Optimistic Concurrency und bereichsgefiltertes Lesen. ✅

### Stufe 2 — Observe / Live-Streaming `✅`
*Schätzung: 1–2 Wochen*
- [x] `observe-events`: erst History, dann offene Verbindung (Dedup neuer Events via ID)
- [x] Pub/Sub via Channels (`internal/pubsub`); der observe-Consumer fasst Bursts zu einem `http.Flusher`-Flush zusammen (hält ~1000 ev/s Schritt); langsame Subscriber werden abgehängt (→ Reconnect) statt den Schreibpfad zu blockieren
- [x] Reconnect via `lowerBound`
- [x] `recursive`-Flag + Subject-Prefix-Matching (`store.MatchSubject`) — auch für `read-events`; rekursive Reads laufen über den globalen `events`-Bucket und bewahren so die globale Ordnung
- **Ergebnis:** Live-Beobachtung von Streams inkl. rekursiver Subjects. ✅

### Stufe 3 — Robustheit & Betrieb `✅`
*Schätzung: 2–4 Wochen*
- [x] Crash-Recovery: durch bbolts ACID-Transaktionen gegeben — Index ist Teil derselben DB und damit immer konsistent; ein separater Rebuild entfällt (siehe ADR-006).
- [x] fsync-Strategie (Durability vs. Performance) → **Group Commit** als Default (ADR-009), umschaltbar via `CLIO_SYNC` (`group`/`always`/`off`). Benchmarks belegen den Effekt.
- [x] Kompaktierung — `cliostore compact` (offline, atomarer Swap) defragmentiert die bbolt-Datei ohne Events zu löschen; `clio_db_size_bytes`-Metrik (ADR-015). *Rotation/Archivierung bewusst nicht: widerspricht der Unveränderlichkeit (siehe ADR-015).*
- [x] Observability: strukturiertes Request-Logging (slog) + Prometheus-`/metrics` (Requests, Latenz-Histogramm, geschriebene Events, 409-Failures, aktive Observer, Event-Count, DB-Größe, Laufzeit: Speicher/Goroutinen/CPU) — ADR-013, ohne Prometheus-Client-Dependency
- [x] Single-Binary-Builds für alle Plattformen (`GOOS`/`GOARCH`) — `make dist` (linux/darwin/windows × amd64/arm64), Version via `-ldflags` eingebettet
- [x] Release-Artefakte — `make package` schnürt pro Plattform ein Archiv (`.tar.gz`/`.zip`, inkl. `LICENSE`/`README`) und eine `checksums.txt` (SHA-256); der Release-Workflow (Tags `v*`) hängt sie ans GitHub-Release. `DiskUsage` ist per Build-Constraints in Unix-/Windows-Varianten getrennt, damit der Windows-Cross-Build trägt.
- [x] Docker-Image — mehrstufig, `distroless/static`, nonroot, `/data`-Volume; cross-compile-fähig (`BUILDPLATFORM`/`TARGETOS`/`TARGETARCH`) und im Release-Workflow als Multi-Arch-Image (`linux/amd64`+`arm64`) nach `ghcr.io/pblumer/clio` gepusht
- **Ergebnis:** Betriebsreif — Durability-Tuning, Observability, Distribution, Wartung. **v0.1.0** als erstes getaggtes Release veröffentlicht. ✅

### Stufe 4 — Abfragen (CEL-basiert) & Snapshots `🟡`
*Schätzung: mehrere überschaubare PRs statt Parser-Marathon (siehe ADR-017)*

Statt EventQL syntaxgetreu nachzubauen (kein offener Parser verfügbar, eigener Lexer/Parser/Planner nötig) setzen wir auf **CEL** (`google/cel-go`) für die Prädikate und wiederverwenden unsere vorhandenen Scan-Primitive für die Struktur. Etappen, jede für sich lauffähig:

1. [x] **CEL-Prädikat-Layer** (`internal/query`): Ausdruck mit `event`-Variable kompilieren (Metadaten typisiert, `event.data` als dynamische Map), gegen ein Event auswerten → bool; Compile-Cache + Tests. *(Etappe 1 — umgesetzt.)*
2. [x] **`POST /api/v1/run-query`** (read-only): `{subject, recursive, where, lowerBound/upperBound, limit}` → CEL-Filter → NDJSON. *(Etappe 2 — umgesetzt.)* **Performance (ADR-021):** Bei einem zwingenden `event.type`-Constraint lädt die Abfrage nur die Treffer über den **Typ-Index** (Millisekunden statt Sekunden über große Scopes) statt `store.Read` über den ganzen Scope; sonst voller Scan. Zudem wird `event.data` nur geparst, wenn das Prädikat es referenziert.
3. [x] **Query-Precondition** `isQueryResultEmpty`/`isQueryResultNonEmpty`: Optimistic Concurrency auf einer CEL-Bedingung (unser `isEventQlQueryTrue`-Äquivalent), atomar im Write-Pfad ausgewertet. *(Etappe 3 — umgesetzt.)*
4. [x] **Projektion**: optionales `select` (punktseparierte Feldliste) in `run-query` — Ausgabe auf gewählte Felder reduzieren; Verschachtelung bleibt erhalten, fehlende Felder werden ausgelassen (kein `null`). *(Etappe 4 — umgesetzt, Feldliste; CEL-Projektion mit abgeleiteten Feldern bleibt eine spätere Option.)*
5. [ ] **Aggregation/Grouping** (später) sowie **Snapshots** (App-geliefert; semantisch optional, da wir bewusst keine Aggregate berechnen).

> **Bewusste Abweichung:** Der Endpoint heißt `run-query` (nicht `run-eventql-query`) — wir bauen *CEL*-basiert, nicht die EventQL-Syntax. Keine Byte-Kompatibilität zu EventSourcingDB; dafür ein Bruchteil des Aufwands. Strikte EventQL-Kompatibilität bliebe ein separater, großer Schritt.

**Gesamteinschätzung:** Funktional brauchbarer Klon (Stufen 0–3) ist erreicht. Die Abfrage-Schicht (Etappen 1–3) ist das „brauchbare 80 %" und besteht aus wenigen normalen PRs statt eines Monatsbrockens.

### Stufe 5 — Betriebs-Dashboard / Web-UI (`/ui`) `🟡`
*Ein optionaler, zusätzlicher Reifegrad über dem Kern: eine eingebettete Oberfläche (`go:embed`, Vanilla JS, **kein** Build-Step/CDN, keine neuen Fremd-Abhängigkeiten — ADR-020). Reine View-Schicht auf bestehende Endpunkte, gleiche Bearer-Token-Auth. Ausführlicher Scope, Sicherheitsbetrachtung und der schrittweise Verlauf: [`docs/web-ui-scope.md`](./docs/web-ui-scope.md).*

Unter `GET /ui`, sechs Tabs — jeder für sich nutzbar:

- [x] **Dashboard** — Health/Monitoring aus `/api/v1/info` + `/metrics`; **Live-Telemetrie** (CPU/Heap/Event-Durchsatz/Request-Rate, gespeist aus zusätzlichen Laufzeit-Metriken: `runtime/metrics` + plattformabhängig `getrusage`); ein **Eventstrom-Diagramm über die Zeit** über den neuen Endpunkt **`GET /api/v1/event-stats`** (serverseitiges Zeit-Histogramm, beim Start aus der Historie aufgebaut) mit **Maus-Box-Zoom**; dazu ein einklappbares Live-Events-Fenster.
- [x] **Live-Events** — `observe`-Stream auf `/` (nur neue Events ab Verbinden, Subject-/Typ-Filter, Pause).
- [x] **Explorer** — read-only: Subject-Baum (`read-subjects?tree=true`), Event-Typen & Schemas (`read-event-types`/`read-event-schema`), Integrität (`verify`/`public-key`).
- [x] **Query** & **Hilfe** — `run-query`-Konsole mit CEL-Editor (Highlighting, Autovervollständigung), Verlauf/Favoriten und NDJSON/CSV-Export; CEL-Referenz mit Beispielen.
- [x] **Erzeugen** — Onboarding: Events schreiben (`write-events`, Vorlagen, Beispiel-Szenarien) und Schemas registrieren (`register-event-schema`). Bewusst die **einzige schreibende** UI-Fläche — normale, token-gebundene Daten-Writes ohne neuen Endpunkt/Privileg.
- [ ] **Maintenance-Konsole** (Kompaktierung u. ä. anstoßen) — **bewusst zurückgestellt**, bis ein eigenes Auth-/Audit-Konzept für betriebskritische Schreibaktionen steht.

> **Server-Erweiterungen** für die UI bleiben minimal: zusätzliche Laufzeit-Metriken in `/metrics` und der read-only-Endpunkt `event-stats`. Alles andere ist Wiederverwendung bestehender Endpunkte. Die UI ist **optional** — der Kern (Stufen 0–4) funktioniert ohne sie.

---

## 7. Architecture Decision Records (ADRs)

> Jeder ADR dokumentiert genau eine Entscheidung mit Kontext, Entscheidung, Konsequenzen und Status.

### ADR-001: Implementierungssprache Go
- **Status:** Akzeptiert
- **Kontext:** Es wird ein abhängigkeitsfreies Single-Binary mit HTTP-API, JSON-Verarbeitung und Live-Streaming benötigt, lauffähig auf mehreren OS/Architekturen.
- **Entscheidung:** Implementierung in Go.
- **Konsequenzen:** HTTP/JSON/NDJSON/Nebenläufigkeit aus der Standardbibliothek; triviale Cross-Compilation; statisches Linken. Nachteil: Go ist nicht ideal für später evtl. gewünschte hochkomplexe Query-Optimierung, aber für den Kern unkritisch.

### ADR-002: Single-Instance-Architektur (vorerst kein Clustering)
- **Status:** Akzeptiert
- **Kontext:** Verteilte Systeme (Konsens, Sharding, Multi-Master) sind der mit Abstand größte Komplexitätstreiber. Das Vorbild ist ebenfalls Single-Instance.
- **Entscheidung:** Das System läuft als einzelne Instanz ohne Clustering.
- **Konsequenzen:** Drastisch vereinfachte Garantien für Ordnung und Atomarität (siehe ADR-003). Keine horizontale Skalierung; Verfügbarkeit an eine Instanz gebunden. Bewusst akzeptiert.

### ADR-003: Serialisierte Schreibvorgänge für Ordnung & Atomarität
- **Status:** Akzeptiert
- **Kontext:** „Strikte Ordnung" und „atomares Schreiben" müssen auch bei gleichzeitigen Schreibern garantiert sein.
- **Entscheidung:** Alle Writes laufen durch eine einzige serialisierte Stelle (Write-Mutex bzw. Write-Queue). IDs werden dort monoton vergeben.
- **Konsequenzen:** Einfache, korrekte Ordnungsgarantie ohne verteilte Konsensmechanismen. Schreibdurchsatz durch Serialisierung begrenzt — für die Single-Instance-Annahme akzeptabel.

### ADR-004: CloudEvents als Event-Format (strukturiertes JSON)
- **Status:** Akzeptiert
- **Kontext:** Interoperabilität und Kompatibilität zum Vorbild sind erwünscht.
- **Entscheidung:** Events folgen der CloudEvents-Spezifikation, ausschließlich strukturiertes JSON, content-type fix `application/json`. Event-IDs sind Strings.
- **Konsequenzen:** Bekanntes Format, einfache Tooling-Integration. Keine Binär-Serialisierung. Optionale Tracing-Felder (`traceparent`/`tracestate`) können später ergänzt werden.

### ADR-005: Subjects als hierarchische Stream-Identifier
- **Status:** Akzeptiert
- **Kontext:** Events müssen Streams zugeordnet und hierarchisch abfragbar sein.
- **Entscheidung:** Das CloudEvents-Feld `subject` ist Pflicht und identifiziert den Stream. Pfade beginnen mit `/`; `recursive` bezieht Unterpfade ein.
- **Konsequenzen:** Einfaches Prefix-Matching ermöglicht recursive Read/Observe und „alles ab `/`". Subject-Index nötig.

### ADR-006: Append-only Storage mit In-Memory-Index
- **Status:** Akzeptiert (für Stufe 0–1; überprüfbar in Stufe 3)
- **Kontext:** Immutability und schnelle Subject-Abfragen werden benötigt.
- **Entscheidung:** Start mit append-only Log (Datei) plus In-Memory-Index `subject → []offset`; alternativ embedded `bbolt`. Index wird beim Start aus dem Log rekonstruiert.
- **Konsequenzen:** Einfacher, korrekter Start. Index-Größe an RAM gebunden; Rebuild-Zeit skaliert mit Log-Größe — in Stufe 3 zu adressieren (Kompaktierung, persistenter Index).
- **Nachtrag (Performance):** Umgesetzt mit `bbolt` und einem persistenten Subject-Index (`subject → seq`). **Reads sind index-begrenzt:** nicht-rekursiv über den Subject-Index, rekursiv für einen Teilbaum über einen Index-Prefix-Scan (Treffer-Sequenzen sammeln, sortieren, gezielt laden) — Laufzeit ~O(Treffer) statt O(alle Events). Nur die echte Wurzel-Abfrage (`/`) scannt den global geordneten `events`-Bucket (Subtree = alles).

### ADR-007: EventQL als spätes, optionales Ziel
- **Status:** Akzeptiert
- **Kontext:** Eine eigene Query-Sprache (Lexer/Parser/Planner/Executor) kann den Aufwand des gesamten Restprojekts übersteigen.
- **Entscheidung:** Der Kern (Write/Read/Observe/Preconditions) funktioniert ohne EventQL. EventQL kommt erst in Stufe 4; bis dahin genügt eine einfachere Filter-API.
- **Konsequenzen:** Früher Nutzwert ohne Sprachimplementierung. Die „einfachere Filter-API" umfasst inzwischen `subject`, `recursive`, `lowerBound`/`upperBound` und `types` (Filter nach Event-Typ) — für viele Abfragen („alle Events vom Typ X") reicht das ohne EventQL. `isEventQlQueryTrue` erst ab Stufe 4 verfügbar.

### ADR-008: Authentifizierung über einzelnes API-Token
- **Status:** Akzeptiert (erweitert durch ADR-025, siehe Konsequenzen)
- **Kontext:** Zugriffsschutz wird benötigt, RBAC ist ein Non-Goal.
- **Entscheidung:** Ein konfiguriertes Bearer-Token schützt alle Routen.
- **Konsequenzen:** Minimaler Aufwand. Keine Mandantentrennung/Rollen — bewusst akzeptiert. **Nachtrag:** Das geteilte Single-Token ist nicht pro Beteiligtem widerrufbar, trennt Lesen/Schreiben nicht und ist nicht zuordenbar. Diese Grenzen adressiert **ADR-025** (mehrere benannte Keys mit Scopes, Widerruf und Audit); `CLIO_API_TOKEN` lebt dort nur noch als deprecated Bootstrap-Pfad fort.

### ADR-009: Group Commit als Default-Schreibstrategie
- **Status:** Akzeptiert
- **Kontext:** Der Schreibdurchsatz ist durch `fsync` pro Transaktion begrenzt (Durability vs. Performance). Ziel ist hoher Durchsatz *ohne* Durability aufzugeben. Storage-Engine bleibt bbolt (ADR-006 bestätigt).
- **Entscheidung:** Writes laufen standardmäßig über bbolts `Batch` (**Group Commit**): gleichzeitige Schreibvorgänge werden zu möglichst wenigen Transaktionen mit *einem* `fsync` pro Batch gebündelt. Umschaltbar via `CLIO_SYNC`: `group` (Default), `always` (fsync pro Write, geringste Einzel-Latenz), `off` (kein fsync, maximaler Durchsatz, Crash-Verlust möglich).
- **Konsequenzen:** Unter gleichzeitiger Last drastisch höherer Durchsatz bei voller Durability (Benchmark: ~31× gegenüber `always`, nahe an `off`). Nachteil: bei *einzelnen, sequentiellen* Schreibern erhöht die Batch-Verzögerung die Latenz — dann ist `always` (oder `off`) die bessere Wahl. Die `Batch`-Funktion kann die Schreibfunktion bei Retries mehrfach aufrufen; sie ist daher idempotent gehalten.

### ADR-010: Komfort-Leseroute `GET /api/v1/events/<subject>`
- **Status:** Akzeptiert
- **Kontext:** Die ursprüngliche Entscheidung (Abschnitt 5) ist „alles POST mit JSON-Body" (wie beim Vorbild). Für schnelles Lesen mit `curl`/Tools ist eine pfadbasierte GET-Route deutlich ergonomischer, da Subjects ohnehin hierarchische URL-Pfade sind.
- **Entscheidung:** Zusätzliche, schreibgeschützte Route `GET /api/v1/events/<subject>` (namespaced unter `/events/`, um Kollisionen mit reservierten Routen zu vermeiden). Subject = Pfad; Optionen als Query (`recursive`, Default `true` für natürliches „alles unterhalb"; `lowerBound`/`upperBound`; `type` wiederholbar; `watch=true` für Live-Streaming). `GET /api/v1/events` ohne Subject = Wurzel. Die POST-Routen bleiben unverändert; Read/Observe teilen denselben Kern (`doRead`/`doObserve`).
- **Konsequenzen:** Bequemes Lesen/Beobachten ohne Body. Auth weiterhin per Bearer-Header — direktes Öffnen im Browser (ohne Header) ist damit nicht vorgesehen. `recursive` defaultet hier auf `true` (abweichend von `read-events`), passend zum Pfad-Browsing.

### ADR-011: Eingebettete OpenAPI-Spec + Swagger UI
- **Status:** Akzeptiert
- **Kontext:** Kunden brauchen eine maschinenlesbare API-Beschreibung und eine Möglichkeit, die API ohne eigenes Setup auszuprobieren.
- **Entscheidung:** Eine handgepflegte OpenAPI-3-Spec (`internal/apidocs/openapi.yaml`) wird per `go:embed` ins Binary aufgenommen und unter `GET /openapi.yaml` ausgeliefert. Eine interaktive Swagger UI wird unter `GET /docs` bereitgestellt; die UI-Assets sind via Modul `swaggest/swgui` (statigz, `go:embed`) ebenfalls eingebettet — passend zum „abhängigkeitsfreien Single-Binary"-Ziel (ADR-001). Beide Routen sind ohne Auth erreichbar (nicht sensibel); „Try it out" nutzt das vom Nutzer eingegebene Bearer-Token gegen dieselbe Instanz (same-origin, kein CORS).
- **Konsequenzen:** Selbsterklärende, sofort testbare API ohne externe Dienste. Zwei zusätzliche (build-time/eingebettete) Abhängigkeiten und ein größeres Binary (~1,5 MB UI-Assets). Die Spec wird manuell gepflegt — bei API-Änderungen mitziehen.

### ADR-012: Hash-Kette für Tamper-Evidence
- **Status:** Akzeptiert
- **Kontext:** „Garantierte Unveränderlichkeit" war bisher nur organisatorisch (append-only API). Wer Zugriff auf die Datei hat, könnte die Historie offline und unbemerkt ändern. Gewünscht ist ein *mathematischer* Nachweis der Unverändertheit (wie beim Vorbild: `predecessorhash`/`hash`).
- **Entscheidung:** Jedes Event erhält einen SHA-256-`hash` über seinen Inhalt **und** den `predecessorhash` (Hash des Vorgängers; Genesis = 64 Nullen). Der Ketten-Kopf wird transaktional im `meta`-Bucket fortgeschrieben — die global serialisierte Schreibstelle (ADR-003) macht die Kette eindeutig. Felder werden längenpräfigiert kanonisch serialisiert; `data` wird kompakt gespeichert, damit die Prüfung reproduzierbar ist. `GET /api/v1/verify` rechnet die Kette nach. Signaturen (Authentizität) sind ein optionaler späterer Schritt; das `signature`-Feld ist vorhanden, aber im Integritäts-Modus `null`.
- **Konsequenzen:** Jede nachträgliche Änderung an einem historischen Event ist beweisbar erkennbar (Tamper-Evidence). Mehraufwand pro Write (ein SHA-256) ist vernachlässigbar. Die Kette bezieht sich auf die globale Schreibreihenfolge; sie setzt eine konsistente Storage-Engine voraus (gegeben). Byte-genaue Kompatibilität mit den Hashes des Vorbilds ist **nicht** garantiert (eigenes, dokumentiertes Kanonisierungsschema).

### ADR-013: Eigene, abhängigkeitsfreie Metriken statt Prometheus-Client
- **Status:** Akzeptiert
- **Kontext:** Betriebssichtbarkeit (Request-Logs, Metriken) wird benötigt. Der offizielle Prometheus-Client zieht zahlreiche transitive Abhängigkeiten nach — im Widerspruch zum „schlankes, abhängigkeitsfreies Binary"-Ziel (ADR-001).
- **Entscheidung:** Ein kleines internes `metrics`-Paket sammelt Counter/Gauge/Histogramm und rendert sie direkt im Prometheus-Textformat (`GET /metrics`). Eine Middleware loggt jede Anfrage strukturiert (slog) und verbucht sie; als Route-Label dient das gematchte Mux-Pattern (`r.Pattern`) — geringe Kardinalität trotz variabler Pfade.
- **Konsequenzen:** Volle Prometheus-Scrape-Kompatibilität ohne neue Abhängigkeiten. Funktionsumfang bewusst begrenzt (eine globale Latenz-Histogramm-Serie, fester Bucket-Satz). `/metrics` ist ohne Auth erreichbar (Konvention; im Betrieb per Netz/Proxy abzusichern).

### ADR-014: Event-Schemas via JSON Schema
- **Status:** Akzeptiert
- **Kontext:** Produzenten/Konsumenten brauchen einen Vertrag über die Struktur der `data` eines Event-Typs (wie `registerEventSchema` beim Vorbild). JSON Schema selbst nachzubauen wäre unverhältnismäßig.
- **Entscheidung:** Pro Event-Typ kann ein **JSON Schema** registriert werden (`POST /api/v1/register-event-schema`); beim `write-events` wird `data` dagegen validiert (Verstoß → 400). Schemas sind **unveränderlich** (erneute Registrierung → 409), und eine Registrierung gelingt nur, wenn **alle bereits gespeicherten Events** des Typs konform sind — so erfüllt jeder Typ mit Schema durchgängig seinen Vertrag. Validierung über `github.com/santhosh-tekuri/jsonschema/v6`; kompilierte Schemas werden inhaltsbasiert gecacht (window-frei).
- **Konsequenzen:** Starke Strukturgarantien ohne EventQL. Eine zusätzliche Abhängigkeit (bewusst, wie bbolt/swgui). Schemas können nicht gelockert werden — das schützt die Historie, erfordert aber Sorgfalt beim ersten Entwurf. Typen ohne Schema bleiben frei (abwärtskompatibel).

### ADR-015: Kompaktierung defragmentiert, löscht aber keine Events
- **Status:** Akzeptiert
- **Kontext:** Die Datenbank wächst monoton (Events sind unveränderlich und werden nie gelöscht); zugleich fragmentiert bbolt intern. „Kompaktierung/Rotation" im klassischen Sinn (alte Daten löschen, Retention, Log-Compaction nach Key) widerspricht dem Kernprinzip und würde die Hash-Kette (ADR-012) brechen.
- **Entscheidung:** Kompaktierung bedeutet ausschließlich **bbolt-Defragmentierung** über `cliostore compact`: die Datei wird offline neu geschrieben (temp-Datei + atomarer Rename) und damit verkleinert/entfragmentiert — **ohne** Events zu löschen oder zu verändern. Der Befehl scheitert bewusst, wenn eine Instanz die Datei hält (Datei-Lock). Die DB-Größe ist als `clio_db_size_bytes` beobachtbar.
- **Konsequenzen:** Wiedergewinnung von Speicher-Overhead bei voller Erhaltung der Historie (verify bleibt grün). Echte Archivierung/Segmentierung alter Events (Cold Storage, Kette über Segmente) bleibt ein separater, größerer Architektur-Schritt gegen das Single-File-Design — bewusst zurückgestellt.
- **Nachtrag (Füllgrad-Sichtbarkeit):** `clio_db_size_bytes` ist die **Dateigröße auf der Platte** (`os.Stat`), nicht das Datenvolumen — bbolt vergrößert die Datei, gibt sie aber nie von selbst frei (freie Seiten werden zuerst wiederverwendet; ein Reset, ADR-022, lässt die Größe stehen). Damit der Anteil echter Nutzung sichtbar ist, liefert `store.Stats()` aus der bbolt-Freelist zusätzlich **belegt/frei/Füllgrad**, ausgelagert über `/api/v1/info` (`databaseFileBytes`/`databaseUsedBytes`/`databaseFreeBytes`/`databaseFillPercent`) und die Gauges `clio_db_used_bytes`/`clio_db_free_bytes`. Das Dashboard (ADR-020) zeigt einen Füllbalken; die echte Wachstumsgrenze bleibt der Plattenplatz, freier Anteil ist per `compact` rückgewinnbar.

### ADR-016: Ed25519-Signaturen für Authentizität
- **Status:** Akzeptiert
- **Kontext:** Die Hash-Kette (ADR-012) beweist *Integrität* (nichts wurde nachträglich geändert), aber nicht *Authentizität* (von wem stammen die Events). Das `signature`-Feld war dafür bereits vorgesehen.
- **Entscheidung:** Ist ein Ed25519-Schlüssel über `CLIO_SIGNING_KEY` konfiguriert, signiert der Server jedes Event über seinen Hash (`signature` = base64). `cliostore gen-key` erzeugt ein Schlüsselpaar; `GET /api/v1/public-key` liefert den öffentlichen Schlüssel, sodass Clients unabhängig prüfen können. `GET /api/v1/verify` prüft die Signaturen mit, sofern ein Schlüssel aktiv ist. Ohne Schlüssel bleibt `signature` `null` (abwärtskompatibel).
- **Konsequenzen:** Nachweisbare Urheberschaft zusätzlich zur Integrität. Die Signatur geht bewusst **nicht** in den Hash ein (Trennung von Integrität und Authentizität; Verfälschen der Signatur bricht nur die Signaturprüfung). Schlüsselverwaltung/-rotation liegt beim Betreiber; nur ein aktiver Schlüssel wird unterstützt (Rotation alter Signaturen ist nicht abgedeckt).

### ADR-017: Abfrageschicht auf CEL statt eigener EventQL-Sprache
- **Status:** Akzeptiert, in Umsetzung (Etappen 1–4 umgesetzt: CEL-Prädikat-Layer, `run-query`-Endpoint, Query-Preconditions, Projektion via `select`-Feldliste; `google/cel-go` als Abhängigkeit)
- **Kontext:** Das Vorbild EventSourcingDB nutzt eine selbst entworfene, SQL-inspirierte Sprache (EventQL) mit eigenem Parser/Executor. Es gibt **keine offene EventQL-Grammatik/Bibliothek** zum Aufsetzen; ein syntaxgetreuer Nachbau bedeutete Lexer+Parser+Planner+Executor (Monatsaufwand). PartiQL (offene SQL-für-JSON-Spec) hat keine reife Go-Implementierung.
- **Entscheidung:** Die Abfrageschicht wird **CEL-basiert** (`google/cel-go`) statt als eigene Sprache gebaut. Eine Query = Subject-Scope (vorhandene Primitive) + **CEL-Prädikat** über das Event (`event.type`, `event.data.*` …) + optionale Projektion/Limit. Der `isEventQlQueryTrue`-Gedanke wird zu einer Precondition mit CEL-Bedingung. Endpoint: `POST /api/v1/run-query` (bewusst nicht `run-eventql-query`, da keine EventQL-Syntax).
- **Konsequenzen:** Drastisch geringerer Aufwand und Risiko; die wertvollen Teile (Bedingungen auf `data`, Precondition) entstehen mit einer reifen, getesteten Engine. Eine zusätzliche Abhängigkeit (`cel-go` + protobuf). **Keine** Byte-Kompatibilität mit EventSourcingDB-EventQL — strikte Kompatibilität bliebe ein separates, großes Vorhaben. Ersetzt die EventQL-Annahme in ADR-007 für die Umsetzung (ADR-007 bleibt als Kontext gültig).

### ADR-018: Bewusste Abweichungen von den Swiss API Guidelines
- **Status:** Akzeptiert (Analyse dokumentiert; volle Konformität bewusst nicht verfolgt)
- **Kontext:** Geprüft wurde, was die Einhaltung der [Swiss API Guidelines](https://github.com/swiss/api-guidelines) bedeuten würde (vollständige Gap-Analyse: `docs/swiss-api-guidelines-gap.md`). Deren zentrale MUST-Regeln fordern eine ressourcenorientierte REST-API (Maturity 2) mit Top-Level-JSON-Objekten und Pagination.
- **Entscheidung:** `clio` behält die **EventSourcingDB-kompatible, RPC-/NDJSON-Streaming-orientierte** API. Die drei harten Konflikte werden als **bewusste, dokumentierte Abweichungen** geführt: (1) Verb-Endpunkte statt Ressourcen (ESDB-Kompatibilität), (2) NDJSON-Streaming/`observe` statt JSON-Listen+Cursor-Pagination, (3) CloudEvents-„flatcase"-Feldnamen statt durchgängigem camelCase. Konfliktfreie Quick Wins (problem+json, OpenAPI-Meta `x-audience`/`license`/`contact`, `Cache-Control: no-store`, camelCase-Konsistenz eigener Felder) sind als optionaler Folgeschritt vorgemerkt, aber nicht Teil dieser Entscheidung.
- **Konsequenzen:** `clio` ist **nicht** Swiss-Guidelines-konform und zielt bewusst nicht darauf. Sollte Konformität später nötig werden, wäre der Weg eine **separate REST-Fassade** neben der bestehenden API. Die Abweichungen sind nachvollziehbar begründet (zwei Kernziele: ESDB-Kompatibilität, Streaming).
- **Nachtrag:** Die hier vorgemerkten Quick Wins `problem+json` und `Cache-Control: no-store` sind inzwischen umgesetzt (siehe ADR-019). Die drei harten Konflikte bleiben unverändert dokumentierte Abweichungen.

### ADR-019: Swiss-Guidelines Quick Wins — problem+json & Cache-Control
- **Status:** Akzeptiert (umgesetzt: problem+json, `Cache-Control: no-store` und OpenAPI-Meta `x-audience: external-public`/`license`/`contact` im `info`-Block)
- **Kontext:** ADR-018 führt die zentralen Swiss-Guidelines-Konflikte bewusst als Abweichungen, hält aber fest, dass ein Teil der Regeln **konfliktfrei** erfüllbar ist (Quick Wins). Davon bringen ein **einheitliches, maschinenlesbares Fehlerformat** und ein **Cache-Default** echten Client-Nutzen ohne Designkonflikt.
- **Entscheidung:** Fehlerantworten werden als **`application/problem+json`** (RFC 7807) ausgeliefert: `{type:"about:blank", title:<HTTP-Statustext>, status:<code>, detail:<Meldung>}`. Die zentrale `writeError`-Funktion erzeugt sie, sodass alle Routen ohne Aufrufänderung profitieren. Zusätzlich setzt die Observability-Middleware **`Cache-Control: no-store`** als Default auf alle Antworten (dynamische Daten, kein Caching); Handler können dies bei Bedarf überschreiben (z. B. statische Doc-Assets). Die OpenAPI-Spec referenziert ein `ProblemDetails`-Schema und trägt im `info`-Block die Meta-Felder `x-audience: external-public`, `license` (MIT) und `contact`.
- **Konsequenzen:** Konsistente, RFC-konforme Fehler erleichtern die Client-Verarbeitung; `no-store` verhindert versehentliches Caching sensibler/aktueller Daten. Bewusst **kein** problemspezifischer `type`-URI-Katalog (generisches `about:blank` genügt) und **keine** Änderung des Erfolgs-Antwortformats (NDJSON/JSON bleiben — die harten Konflikte aus ADR-018 sind weiterhin Abweichungen). Byte-Kompatibilität mit EventSourcingDB ist davon unberührt.

### ADR-020: Eingebettetes Betriebs-Dashboard unter `/ui`
- **Status:** Akzeptiert (Stufen 1–3, 5–7 umgesetzt: Health-/Monitoring-Übersicht, Live-Event-Viewer, read-only Subject-Browser/Explorer, Query-Konsole + Hilfe, Sci-Fi-Theme/Telemetrie/EKG sowie ein Event-/Schema-Generator zum Onboarding; schreibende **Maintenance**-Konsole bewusst zurückgestellt — siehe [`docs/web-ui-scope.md`](./docs/web-ui-scope.md))
- **Kontext:** Betriebssichtbarkeit gibt es bisher nur maschinenlesbar (`/metrics` im Prometheus-Format, ADR-013) und über `/api/v1/info`. Für „mal eben draufschauen" (Maintenance, Observing, Monitoring) fehlt eine menschenlesbare Oberfläche — die „Betrieb"-Rolle des Learning Paths hatte kein UI. Eine vollwertige Frontend-Toolchain (npm/Bundler/SPA-Framework) widerspricht aber dem „schlankes, abhängigkeitsfreies Single-Binary"-Ziel (ADR-001).
- **Entscheidung:** Ein **statisches Dashboard** als **eine einzige HTML-Datei** mit Inline-CSS/-JS (Vanilla, kein Framework, kein Build-Step, kein CDN) wird via `go:embed` (`internal/webui`) ins Binary aufgenommen und unter **`GET /ui`** ausgeliefert. Die Seite selbst ist — wie die Swagger UI (ADR-011) — **ohne Auth** erreichbar (nicht sensibel: nur HTML/JS). Die angezeigten Daten holt sie **clientseitig** von `/api/v1/info` (mit vom Nutzer eingegebenem **Bearer-Token**, same-origin) und `/metrics`; das Prometheus-Textformat wird im Browser geparst, Latenz-Perzentile (p50/p99) werden — wie `histogram_quantile` in PromQL — aus dem kumulativen Histogramm interpoliert. Das Token bleibt nur im Tab (`sessionStorage`). Das UI ist überwiegend lesend; als bewusste Ausnahme schreibt der **„Erzeugen"-Tab** (Onboarding) Events und registriert Schemas über die bestehenden token-geschützten Endpunkte (`write-events`/`register-event-schema`) — normale Daten-Writes ohne neuen Endpunkt und ohne neue Privilegien-Ebene. Schreibende **Maintenance**-Aktionen (Kompaktierung etc.) bleiben davon abgegrenzt und einem separaten, eigens abgesicherten Schritt vorbehalten.
- **Konsequenzen:** Sofort nutzbares Monitoring ohne externe Dienste (Prometheus/Grafana bleiben optional, nicht Voraussetzung) und **ohne neue Abhängigkeiten** — nur eine eingebettete Textdatei, vernachlässigbare Binary-Größe. Das UI ist eine reine View-Schicht auf bestehende Endpunkte und führt **keine neue Privilegien-Ebene** ein. Grenzen: keine historischen Zeitreihen (nur Live-Momentaufnahmen + clientseitig berechnete Rate), kein Alerting — dafür bleibt `/metrics` + Prometheus der Weg. Kein CORS nötig (same-origin).
- **Nachtrag (Keys-Tab, ADR-025):** Mit dem Schlüsselbund (ADR-025) existiert nun das in dieser ADR vermisste „eigene Auth-/Audit-Konzept für betriebskritische Schreibaktionen". Das Dashboard erhält daher einen **„Keys"-Tab** als erste Admin-Schreibfläche: Übersicht aller Keys (kid, Name, Scopes, Status, erstellt/widerrufen), **Anlegen** (das einmalige `kid.secret` wird mit Copy-Knopf und Einmal-Hinweis gezeigt) und **Widerruf** (mit Bestätigung und Self-Lockout-Warnung beim letzten aktiven Admin). Er nutzt ausschließlich die bestehenden `admin`-geschützten Endpunkte `/api/v1/keys*` — eine read/write-Token deckt ihn nicht ab (403). Die vollständige **Maintenance-Konsole** (Kompaktierung etc.) bleibt weiterhin zurückgestellt.

### ADR-021: Typ-Index für `run-query`
- **Status:** Akzeptiert
- **Kontext:** `run-query` (CEL, ADR-017) scannte für jede Abfrage den **gesamten** Scope und materialisierte alle Events, bevor das Prädikat ausgewertet wurde. Bei großen Stores (Hunderttausende Events) dauerte schon ein einfacher `event.type == 'X'`-Filter mehrere Sekunden — zwei vermeidbare Kosten: (1) `event.data` wurde je Event in eine Map geparst, auch wenn das Prädikat es nicht nutzt; (2) ohne Index muss jedes Event angefasst werden.
- **Entscheidung:** Zwei Optimierungen. **(a)** Das Prädikat parst `event.data` nur noch, wenn der Ausdruck `data` referenziert (Token-Check beim Compile; konservativ — ein Fehlalarm parst wie bisher, nie ein falsches Ergebnis). **(b)** Ein persistenter **Sekundärindex `type_idx`** (Bucket-Schlüssel `type + 0x00 + seq`, analog zum Subject-Index) bildet Event-Typ → Sequenzen ab. Aus dem kompilierten CEL-AST wird die Menge der **notwendig** geforderten `event.type`-Werte abgeleitet (`==`, `in`, `&&`→Schnitt, `||`→Vereinigung nur wenn beide Seiten einschränken; alles andere → kein Constraint). Liegt ein sicherer Typ-Constraint vor, lädt `run-query` nur die Events dieser Typen über den Index (mit Limit-Abbruch) statt den ganzen Scope; Subject-Scope und das restliche Prädikat werden danach geprüft. Für Bestands-DBs wird der Index beim Öffnen einmalig aus der Historie nachgebaut (idempotenter Backfill, wie bei den Typ-Zählern).
- **Konsequenzen:** `event.type == 'X'` über große Scopes fällt von Sekunden auf **Millisekunden** (Benchmark: 150k Events, Treffer am Ende, `/` rekursiv: ~1,5 s → ~1,6 ms; Allokationen 410 MB → 0,44 MB). Die Ableitung ist **sicher**: kann der Typ nicht zuverlässig eingeschränkt werden (z. B. `!=`, ein `||` mit unbeschränkter Seite), fällt die Abfrage auf den vollständigen Scan zurück — nie ein falsches Ergebnis. Kosten: ein zusätzlicher Index (mehr Schreib-/Speicheraufwand pro Event, wie beim Subject-Index) und der einmalige Backfill beim ersten Öffnen. Ein kombinierter Subject-und-Typ-Index sowie Index-Nutzung für Bereichsvergleiche bleiben mögliche spätere Schritte.

### ADR-022: Dev-Mode mit destruktivem DB-Reset und gated Bulk-Import-Fenster
- **Status:** Akzeptiert
- **Kontext:** In Entwicklungs- und Demoumgebungen will man häufig wieder bei null beginnen („Tabula rasa"): Spiel-Events löschen, Schemas verwerfen, frisch ausprobieren. Im Normalbetrieb ist genau das **unerwünscht** — Events sind unveränderlich und werden nie gelöscht (Kernprinzip, vgl. ADR-015, der nur defragmentiert und bewusst nichts löscht). Ein dauerhaft erreichbarer „Datenbank löschen"-Endpunkt wäre ein gefährlicher Fußabwurf; selbst hinter dem Bearer-Token (ADR-008) widerspräche er dem Append-only-Versprechen. Daneben braucht es für Szenario-Seeding/Migration die Möglichkeit, **direkt nach Start oder Reset große Eventmengen** zu laden — aber ohne dauerhaft offenen High-Volume-Import-Pfad, der später im Betrieb versehentlich befüllt wird.
- **Entscheidung:** Ein **Dev-Mode**, der ausschließlich über die Umgebungsvariable `CLIO_DEV_MODE` (truthy, sonst aus) aktiviert wird. Nur in diesem Modus registriert der Server die destruktive Route `POST /api/v1/dev/reset-database` — ist er aus, **existiert die Route nicht** (404 statt nur 401: Defense in Depth, kein erreichbarer Code-Pfad). Der Reset leert alle Buckets atomar in einer Transaktion (`store.Reset`), inklusive der Event-Sequenz (Start wieder bei `#0`), und verwirft den Schema-Cache; der optionale Signaturschlüssel bleibt. `/api/v1/info` meldet `devMode`, woran das eingebettete Dashboard (ADR-020) eine **„Dev-Zone"** ein- bzw. ausblendet — mit einem spielerischen „Supernova"-Reset (Hold-to-fire gegen Versehen, lokaler Reset-Zähler/Rang als Gamification, rein clientseitig).
- **Bulk-Import-Fenster:** Ebenfalls nur im Dev-Mode registriert sind `POST /api/v1/dev/bulk-import-events` (schreibt mit derselben Semantik wie `write-events`) und `POST /api/v1/dev/close-bulk-import`. Ein serverseitiger Schalter `bulkImportOpen` (mutex-geschützt) steuert das Fenster: Es ist **bei Server-Start im Dev-Mode offen**, wird durch `close-bulk-import` explizit geschlossen und durch jeden `reset-database` wieder geöffnet. Bei geschlossenem Fenster antwortet der Bulk-Import mit `409`. So ist High-Volume-Import genau im kontrollierten Startfenster (frische Instanz oder nach Supernova) erlaubt und im restlichen Betrieb gesperrt. Der reguläre `write-events`-Pfad bleibt davon unberührt.
- **Konsequenzen:** Bequemes Zurücksetzen und Befüllen im Entwicklungsumfeld, ohne das Append-only-Versprechen im Produktivbetrieb aufzuweichen — die gefährlichen Pfade sind dort schlicht nicht vorhanden. Der Reset bricht bewusst die Hash-Kette (ADR-012) ab und beginnt eine neue ab Genesis; das ist gewollt (es ist *kein* Mutieren der Historie, sondern ein vollständiger Neustart). Bereits offene Observer-Streams werden nicht aktiv getrennt und liefern erst nach Reconnect wieder (akzeptabel für ein Dev-Werkzeug). Das Bulk-Fenster ist bewusst **in-memory und nicht persistent**: nach einem Neustart ist es wieder offen — passend dazu, dass „frischer Start" der gewollte Lade-Zeitpunkt ist. Wer diese Routen in einer geteilten Umgebung bereitstellt, muss sich der Tragweite bewusst sein — daher die explizite Opt-in-Variable und der laute Warn-Log beim Start.

### ADR-023: Kostenbasierte Index-Wahl für `run-query` (Subject vs. Typ)
- **Status:** Akzeptiert
- **Kontext:** Mit dem Typ-Index (ADR-021) nutzt `run-query` bei einem sicheren `event.type`-Constraint **immer** den Typ-Index — auch wenn der Subject-Scope sehr eng ist. Beispiel `subject:/orders/42, where: event.type=='placed'`: es werden **alle** `placed`-Events der gesamten DB iteriert und der Subject-Scope erst nachträglich gefiltert. Bei engem Teilbaum + häufigem Typ ist der **Subject-Index** drastisch selektiver. Welcher Index günstiger ist, hängt von den jeweiligen Kardinalitäten ab und lässt sich nicht statisch entscheiden.
- **Entscheidung:** Liegt ein Typ-Constraint vor, wählt `run-query` pro Anfrage den **selektiveren** der beiden vorhandenen Indizes anhand exakter, billig verfügbarer Zähler: **(a)** `CountByTypes` summiert die vorhandenen Typ-Zähler (`types`-Bucket, ADR-014) — die Kosten des Typ-Index-Scans; **(b)** `CountSubject` liefert die Eventzahl des Scopes aus einem neuen persistenten **Subject-Zähler-Index `subj_count`** (Subject → Anzahl, analog zu den Typ-Zählern). Letzterer macht die Subtree-Kardinalität günstig: Wurzel = O(1), nicht-rekursiv = ein Lookup, rekursiv = O(distinkte Subjects im Teilbaum) statt O(Events). Ist `subjCost < typeCost`, scannt die Abfrage den Subject-Index (`store.Read`) und schiebt den Typ-Filter über `ReadOptions.Types` ein; sonst bleibt es beim Typ-Index. Für Bestands-DBs wird `subj_count` beim Öffnen einmalig aus der Historie nachgebaut (idempotenter Backfill, wie bei Typ-Zählern/-Index).
- **Konsequenzen:** Enge Subjects mit Typ-Filter werden nicht mehr durch DB-weite Typ-Scans ausgebremst; breite Scopes nutzen weiter den Typ-Index. Die Wahl beeinflusst **nur die Kosten**, nie das Ergebnis (beide Pfade liefern identische Treffer); bei einem Fehler der Kostenschätzung fällt die Abfrage sicher auf den Typ-Index zurück. Preis: ein weiterer kleiner Zähler-Index (ein zusätzlicher Bucket-Eintrag pro Subject, vergleichbar mit den Typ-Zählern) und der einmalige Backfill. Bewusst kein vollwertiger kombinierter Subject-und-Typ-Index — die Wahl zwischen den zwei bestehenden Indizes deckt den häufigen Fall ab; Bereichs-/`data`-Feld-Indizes bleiben wie in ADR-021 offen.

### ADR-024: Transparente Wert-Kompression der Event-Ablage (DEFLATE + Preset-Dictionary)
- **Status:** Akzeptiert
- **Kontext:** Events werden im `events`-Bucket als rohes JSON abgelegt (ADR-004/006). Bei einem durchschnittlichen Eventstrom wächst die DB spürbar, weil sich pro Event viel wiederholt: das CloudEvents-JSON-Gerüst (Feldnamen, Quotes), konstante Werte (`"specversion":"1.0"`, `"application/json"`) und lange, sich wiederholende `source`/`subject`/`type`-Strings — dazu zwei 64-stellige Hex-Hashes. Bei kleinen Events dominieren so Metadaten und Wiederholungen die Nutzdaten. `cliostore compact` (ADR-015) ist nur Defragmentierung und verdichtet die Events selbst nicht.
- **Entscheidung:** Ein **Storage-Codec** (`internal/store/codec.go`) komprimiert den gespeicherten Event-Wert transparent mit **DEFLATE** (`compress/flate`, reine **Standardbibliothek** — keine neue Abhängigkeit, passt zum Single-Binary-/Abhängigkeitsfrei-Prinzip), unterstützt durch ein **Preset-Dictionary** mit dem CloudEvents-Gerüst, sodass gerade kleine Events stark als Rückverweise kodiert werden. Jeder gespeicherte Wert trägt ein **Frame-Byte**: `0x01` = DEFLATE+Dictionary-v1, sonst = rohes JSON (Legacy beginnt mit `{`). Damit sind bestehende DBs voll **abwärtskompatibel** und dürfen beliebig **gemischte** Werte enthalten. Kompression ist **opt-in** über `CLIO_COMPRESS` (Default aus → byte-identisch zum bisherigen Verhalten) und wirkt nur auf **neu geschriebene** Events; alte bleiben lesbar. **Wichtig:** Kompression ist reine Ablage — Hash und Signatur (ADR-012/Ed25519) werden weiterhin über die kanonischen Event-Felder berechnet, nicht über die Bytes auf Platte; `/verify` bleibt unberührt. Bringt DEFLATE im Einzelfall keine Ersparnis, wird der rohe Wert behalten (die Ablage wächst nie).
- **Konsequenzen:** Auf einem realistischen Eventstrom rund **−45 bis −50 %** auf der Event-Nutzlast (Messung im Test: ~520 B → ~266 B je Event; 2000 Events ~1,06 MB → ~0,58 MB). Preis: CPU pro Event (~30 µs Encode, ~6 µs Decode bei `BestCompression`) — gegenüber dem fsync-dominierten Group-Commit-Default (ADR-009) vernachlässigbar, nur auf dem extremen `CLIO_SYNC=off`-Pfad spürbar; deshalb opt-in. Lesen dekomprimiert zentral über einen Helfer, durch den **alle** `events`-Bucket-Zugriffe laufen. Die Indizes (`subject_idx`/`type_idx`) bleiben unkomprimiert (rebuildbare bbolt-Keys). Bewusst **kein** zstd (bessere Ratio, aber externe Abhängigkeit) und **keine** echte Segment-/Cold-Storage-Rotation — Letztere bliebe der große Schritt gegen das Single-File-Design (vgl. ADR-015) und ist weiterhin zurückgestellt. Das Dictionary ist über das Frame-Byte versioniert (künftige Varianten = neues Byte).

### ADR-025: Mehrere benannte API-Keys mit Scopes, Widerruf und Audit
- **Status:** Akzeptiert
- **Kontext:** Bisher schützt ein einzelnes, geteiltes Bearer-Token alle Routen (ADR-008). Das hat drei Grenzen: (1) es ist **nicht pro Beteiligtem widerrufbar** — ein Leak zwingt zum globalen Token-Tausch samt Neuverteilung an alle; (2) es trennt **nicht Lesen von Schreiben** (Alles-oder-nichts); (3) eine Anfrage ist **nicht zuordenbar** (keine Identität fürs Audit). RBAC/OIDC wären überdimensioniert und widersprächen dem Single-Binary-/Stdlib-Prinzip (ADR-001).
- **Entscheidung:** Ein **persistenter Schlüsselbund** im bbolt-Store (eigener Bucket `auth_keys`, getrennt vom Event-Strom). Jeder Schlüssel ist ein benanntes Credential mit eigener Identität (`kid`), einem Satz **Scopes** (`read`/`write`/`admin`) und einem **Status** (`active`/`revoked`). Auf der Leitung gilt das Format `kid.secret` (`Authorization: Bearer kid_ci01.<secret>`); der Server macht über den `kid` einen O(1)-Lookup und vergleicht `sha256(secret)` **zeitkonstant** (`crypto/subtle`) gegen den gespeicherten Hash — auch bei unbekanntem `kid` gegen einen Dummy-Hash, um kein Timing-Orakel über die Existenz zu öffnen. **Persistiert wird nur der SHA-256-Hash** des Geheimnisses, nie der Klartext. **Widerruf** ist ein Status-Wechsel (kein Delete), damit die `kid`-Zuordnung im Audit dauerhaft bleibt. Routen verlangen je einen Scope (lesend `read`, schreibend `write`, Verwaltung/Dev `admin`); fehlender/ungültiger Schlüssel → **401**, gültiger Schlüssel ohne Scope → **403** (neu sauber getrennt). Drei Admin-Routen verwalten Schlüssel zur Laufzeit (`POST /api/v1/keys`, `GET /api/v1/keys`, `POST /api/v1/keys/{kid}/revoke`); das Geheimnis wird nur beim Anlegen einmalig ausgeliefert. Ein **Bootstrap** über `CLIO_BOOTSTRAP_ADMIN_KEY` legt — nur bei leerem Bund — einen initialen Admin-Key an (löst das Henne-Ei-Problem). `CLIO_API_TOKEN` bleibt als **deprecated** Bootstrap-Pfad: bei leerem Bund wird daraus ein `legacy-token`-Admin-Key gebootet. Bewusst gewählt wurde dabei die **Migration auf `kid.secret`** (Variante b): der Leitungswert ist ausschließlich `kid.secret`; ein altes `Bearer <token>` ohne `kid`-Präfix wird **nicht** mehr akzeptiert (der Betreiber stellt einmalig den Wert um). Jede Autorisierungsentscheidung (allow/deny) wird strukturiert ins **Audit-Log** (`slog`) geschrieben — ohne jedes Geheimnis. Keine neue externe Abhängigkeit (nur `crypto/sha256`, `crypto/subtle`, `crypto/rand`).
- **Konsequenzen:** Echte Mehrbenutzer-Sicherheit (mehrere widerrufbare Keys, Lese-/Schreibtrennung, Zuordenbarkeit) ohne externe Abhängigkeit. Der `auth_keys`-Bucket ist **mutabler Steuerungs-State** und damit eine bewusste, klar abgegrenzte Ausnahme vom Append-only-Versprechen des Event-Stroms (ADR-015) — er wird vom Dev-Reset (ADR-022) **ausgenommen**, sonst sperrte man sich beim Reset selbst aus. Die 401/403-Semantik ist neu. OpenAPI (ADR-011) wurde nachgezogen; Dashboard (ADR-020) und README/Beispielskripte sind anzupassen. Bewusste Nicht-Ziele: kein OIDC/JWT, keine Tenancy/Rollenhierarchie über Scopes hinaus, keine At-rest-Verschlüsselung der DB. **Event-Urheberschaft (opt-in):** die authentifizierte Identität wird optional (`CLIO_EVENT_AUTHORSHIP`, Default aus) als CloudEvents-Extension `clioauthkid` in jedes neu geschriebene Event übernommen — append-only-konform (neues Attribut auf neuen Events) und in die Hash-Kette/Signatur (ADR-012/016) gebunden. Der `kid` geht **nur dann** in den Hash ein, wenn gesetzt; Bestands-Events und der Feature-aus-Pfad bleiben damit byte-identisch (verify unberührt). Der Wert stammt serverseitig aus der authentifizierten Identität (nicht client-setzbar).
- **Erweiterung (Key-Lifecycle, additiv):** Das anfangs als Nicht-Ziel notierte „automatische Rotation/Ablauf" ist — wie vorgesehen über das `expiresAt`-Feld — nun umgesetzt, vollständig rückwärtskompatibel (omitempty; Bestands-Keys laden mit Zero-Werten unverändert). Neu: (1) **Ablauf** `ExpiresAt` — die Middleware prüft `Usable(now) = active && !expired`, ein abgelaufener Key wird wie ein widerrufener mit 401 abgewiesen; (2) **Rotation** `RotateKey` (`POST /api/v1/keys/{kid}/rotate`) ersetzt nur das Geheimnis (kid/Scopes/Status/Metadaten bleiben, alter Wert sofort ungültig); (3) optionale **Inventar-Metadaten** `Owner`/`Purpose`/`Description`; (4) eine **Offline-CLI** `cliostore keys <list|create|rotate|revoke>` auf der DB-Datei als Bootstrap- und **Recovery-/Lockout-Pfad** (Server gestoppt, Datei-Lock), parallel zur HTTP-Admin-API. Geheimnisse erscheinen weiterhin nur einmalig bei create/rotate und nie im Log/in der Liste. Praxisleitfaden: [`docs/security.md`](docs/security.md).

### ADR-026: Authentifizierte Event-Herkunft über Tokens
- **Status:** Vorgeschlagen
- **Kontext:** Das `source`-Feld eines Events ist nach CloudEvents-Spec ein vom Producer selbst gesetzter URI-Reference und damit **selbstdeklariert** — ein Client kann beliebige Herkunft behaupten. Für einen Event Store, dessen Wert maßgeblich auf Auditierbarkeit beruht, ist das eine Lücke: Die aufgezeichnete Herkunft eines Events ist nur so vertrauenswürdig wie die Ehrlichkeit des Schreibers. Diese Entscheidung führt eine Bindung zwischen **Schreib-Token** (Access Token) und erlaubter `source` ein. Wer schreiben will, braucht ein Token; das Token bestimmt, als welche Source(s) der Schreiber auftreten darf. Damit wird die aufgezeichnete Herkunft **attributiert** statt nur behauptet. (Baut auf dem Schlüsselbund aus ADR-025 auf, dessen `kid.secret`-Tokens und Scopes hier zur Source-Autorisierung erweitert werden.)
- **Entscheidung:**
  1. **Token autorisiert eine Menge von Sources.** Ein Token trägt eine Liste erlaubter Source-Werte. Der 1:1-Fall ist der Spezialfall einer einelementigen Menge. Multi-Source ist ein realer Anwendungsfall (Gateway-/Ingest-Producer, die für mehrere logische Sources schreiben). Das Matching erfolgt zunächst über **exakte Werte**; Präfix-/Pattern-Matching für dynamisch erzeugte Sources (z. B. pro Tenant) ist eine bewusst zurückgestellte, additiv nachrüstbare Erweiterung.
  2. **Server setzt oder validiert die Source, abhängig von der Token-Menge.** Erlaubt das Token genau eine Source: Der Client darf `source` weglassen; der Server setzt sie. Schickt der Client eine abweichende Source → **harte Ablehnung** (kein stilles Überschreiben). Erlaubt das Token mehrere Sources: Der Client **muss** `source` mitschicken; der Server validiert gegen die erlaubte Menge. Nicht enthaltener Wert → **harte Ablehnung**.
  3. **Token-Verwaltung als eigene Domäne, getrennt vom Store-Kern.** Eine `auth`/`principal`-Domäne kennt Tokens und ihre erlaubten Sources. Der Store-Kern bleibt **auth-unwissend** und erhält ausschließlich eine bereits gesetzte/validierte Source. Token werden ausschließlich als Hash (SHA-256) persistiert, nie im Klartext; Vergleich in konstanter Zeit (`crypto/subtle.ConstantTimeCompare`). Bleibt CGO-frei (passt zu ADR-001).
  4. **Token sind revozierbar; Revocation wirkt nur nach vorn.** Ein Token ist ein Entity mit Lebenszyklus (`id`, `hash`, erlaubte Sources, `created_at`, `revoked_at`). Der Lookup verlangt zusätzlich `revoked_at == null`. Bereits geschriebene Events bleiben gültig und attributiert — die Historie ist immutable, ein revoktes Token entwertet nicht, was es legitim geschrieben hat. (Ob der Token-Lifecycle selbst als Tabelle oder als interner Event-Stream im Store geführt wird, ist als **ADR-027** separat zu entscheiden — siehe offener Punkt unten.)
  5. **Tokenlose Writes landen in einem isolierten Inbox-Stream, standardmäßig deaktiviert.** Events ohne gültiges Token werden in einen eigenen, klar benannten Stream (z. B. `_inbox`) geschrieben, **physisch getrennt** vom authentifizierten Event-Raum — nicht nur per Konvention. Konsumenten des Hauptraums sehen sie nicht, sofern sie die Inbox nicht explizit abonnieren. Zusätzlich setzt der Server ein nicht-fälschbares, serverkontrolliertes Attribut (CloudEvents-Extension, z. B. `principal: anonymous`), das die Vertrauensstufe pro Event unmissverständlich macht — unabhängig vom Stream. Der tokenlose Pfad ist grundsätzlich **aus** und wird pro Ziel-Source oder global per Config explizit aktiviert; andernfalls wäre er ein ungesicherter Write-Endpoint.
  6. **Überführung aus der Inbox erfolgt als neues, anreicherndes Event.** Ein Inbox-Event wird **nie verschoben** (Inbox-Historie bleibt immutable). Stattdessen schreibt ein Promoter mit gültigem Token ein **neues** Event in den authentifizierten Raum, das den Inbox-Ursprung über eine Extension (`promotedfrom` mit der Inbox-Event-ID, alternativ `dataref`) referenziert. Der Promoter darf den Inhalt beim Überführen anreichern/normalisieren. Das promotete Event ist semantisch eine Aussage des Promoters und gehört dessen authentifizierter Source — nicht dem anonymen Absender. Der Rückverweis ist die einzige, aber lückenlose Brücke zum Original.
- **Konsequenzen (positiv):** Die aufgezeichnete Herkunft ist an einen authentifizierten Schreibkanal gebunden statt selbstdeklariert. Klare physische und semantische Trennung zwischen authentifizierten und anonymen Events; kein versehentliches Vermischen. Multi-Source-Producer werden ohne Token-Wildwuchs unterstützt. Auditierbarkeit bleibt durchgängig: Revocation, Inbox-Ursprung und Promotion sind alle nachvollziehbar, ohne die Immutability der Historie zu verletzen. Bleibt pure-Go ohne CGO (ADR-001).
- **Konsequenzen (negativ / Grenzen):** Das Feature liefert **attributierte**, keine kryptografisch bewiesene Herkunft. Ein geleaktes oder geteiltes Token bricht die Garantie — ein kompromittiertes Token kann beliebigen Payload unter seiner legitimen Source schreiben. Echte inhaltliche Provenance über **Event-Signaturen** (vgl. ADR-016, dort serverseitig) ist ausdrücklich out of scope und ein eigenes späteres ADR. Die auth-Domäne mit Lifecycle und Revocation vergrößert den Umfang gegenüber einem reinen String-Feld spürbar. Anreichernde Promotion bedeutet, dass das promotete Event vom Original inhaltlich abweichen kann; die Nachvollziehbarkeit „was wurde verändert" hängt allein am `promotedfrom`-Verweis und liegt in der Verantwortung des Promoters.
- **Offene Punkte (für Folge-ADRs):** (1) Token-Lifecycle als Tabelle vs. interner Event-Stream (Bootstrap-/Henne-Ei-Frage bei letzterem) → **ADR-027**. (2) Präfix-/Pattern-Matching für dynamische Sources, falls ein Producer Sources zur Laufzeit erzeugt.

### ADR-028: `run-query`-Resilienz unter Last — Heartbeat, Query-Deadline & Index-Warnung
- **Status:** Akzeptiert
- **Kontext:** Eine Payload-Suche über die `/ui`-Query-Konsole (`subject:/employees/`, rekursiv, `limit:100000`, Prädikat `event.data.lastName == 'User25199'`) lieferte unter Last (paralleler Schreib-Benchmark, ~5.000 Ev/s) **HTTP 502**. Ursache war eine Kombination aus dem Streaming-Design von `run-query`: (1) Das Prädikat referenziert `event.data` ohne `event.type`-Constraint → kein Typ-Index nutzbar (ADR-021), jedes Payload wird deserialisiert, vollständiger rekursiver Scan. (2) Der Handler flusht NDJSON erst alle 512 Treffer; bei einem einzigen, am Scan-Ende liegenden Treffer floss **bis zum Scan-Ende kein Byte** — die `net/http`-Header blieben serverseitig gepuffert und erreichten den Reverse-Proxy nie. Time-to-first-byte = gesamte Scan-Dauer → der Proxy setzt die Upstream-Verbindung nach seinem Read-Timeout zurück (**502 am Ingress**, nicht vom Handler erzeugt). (3) Es gab **keine Deadline**: der Scan hält eine bbolt-Lesetransaktion über seine gesamte Dauer offen, was unter Schreiblast die Wiederverwendung freier Seiten blockiert (DB-/Speicherwachstum → Risiko eines OOM-Kills/Probe-Timeouts).
- **Entscheidung:** Drei Maßnahmen, analog zum bereits gehärteten `observe`-Stream (ADR-020-nah), ohne Bruch der öffentlichen API:
  1. **Heartbeat.** `run-query` flusht sofort beim Verbindungsaufbau eine Leerzeile (Header erreichen den Proxy umgehend) und sendet danach periodisch (`queryHeartbeat`, 15 s) ein Lebenszeichen, solange noch kein Treffer floss. Zusätzlich `X-Accel-Buffering: no` und `Cache-Control: no-store, no-transform`, damit puffernde Proxies den Stream durchreichen. Leerzeilen sind im NDJSON-Protokoll Heartbeat und werden klientseitig ignoriert. Der Scan-Loop prüft das Heartbeat-/Deadline-`guard` vor jedem Event (auch für übersprungene Subjects im Typ-Index-Pfad).
  2. **Query-Deadline.** `CLIO_QUERY_TIMEOUT` (Go-Dauer, Default `0` = aus → **rückwärtskompatibel**) begrenzt die Scan-Dauer per `context`-Timeout. Der `guard` prüft `ctx.Done()` und bricht den Scan **sauber** ab. Da bei einem laufenden Stream die `200`-Header bereits gesendet sind, endet der Stream definiert (ggf. unvollständig, im Log als Warnung vermerkt) statt zu hängen — die Statusentscheidung fällt vor dem ersten Byte (Validierung → `400`, sonst `200`).
  3. **Index-Warnung.** Kann ein Prädikat keinen Typ-Index nutzen (kein `event.type ==`-Constraint), setzt der Server den Antwort-Header `X-Clio-Query-Warning` (mit Zusatz, wenn `event.data` deserialisiert wird). Das `/ui`-Dashboard zeigt daraufhin einen Hinweis (ADR-020-konform: Vanilla JS, kein Build-Step). Bewusst **nur Hinweis**, kein Clamping/Ablehnen des Limits — die Query bleibt kompatibel ausführbar.
- **Konsequenzen:** Der gemeldete 502/Hänger entfällt: Header und Heartbeats halten die Proxy-Verbindung über lange Scans offen, die Deadline begrenzt die Haltezeit der Lesetransaktion. Betrieb: Liveness/Readiness-Probes auf `GET /api/v1/ping` legen (auth-frei, ohne Store-Zugriff, eigene Goroutine je Request) — so bleibt die Probe von langen Scans entkoppelt; `proxy_read_timeout`/`proxy_buffering` am Ingress und RAM-Headroom sind dokumentiert (README). Der Heartbeat fügt dem Stream führende/zwischengeschaltete Leerzeilen hinzu — bestehende NDJSON-Clients ignorieren Leerzeilen bereits (wie beim `observe`-Stream). Bewusste Nicht-Ziele: kein `data`-Feld-Index (separat in ADR-029 entschieden), kein automatisches Limit-Clamping, keine Panic-Recovery-Middleware (separat).

### ADR-029: Sekundär-Query auf `event.data` — interner Feld-Index zuerst, externes Read-Model (OpenSearch) zurückgestellt
- **Status:** Akzeptiert · interner Index **v1 umgesetzt** (Gleichheit auf deklarierten Top-Level-String-Feldern via `CLIO_DATA_INDEX_FIELDS`); Folgeschritte (numerische/Range-Werte, verschachtelte Pfade, Schema-Registry-Kopplung, `reindex`-Befehl, Cost-Modell-Feinschliff) offen
- **Kontext:** Prädikate auf Payload-Feldern (z. B. `event.data.department == 'support'`) können den Typ-Index (ADR-021) nicht nutzen: selbst *mit* `event.type ==`-Constraint grenzt der Typ-Index nur nach Typ ein, danach wird **jedes** Event dieses Typs deserialisiert und das Datenfeld linear geprüft. Über große Scopes ist das langsam und hält eine lange Lesetransaktion offen — derselbe Mechanismus, der den 502-Fall in ADR-028 ausgelöst hat (dort als „kein `data`-Feld-Index" als Nicht-Ziel notiert). Es gibt zwei tragfähige Lösungsrichtungen: **(A) interner Sekundärindex** auf deklarierte `data`-Felder, gespeichert als zusätzlicher bbolt-Bucket — analog zum bestehenden Typ-Index (ADR-021) und in die kostenbasierte Index-Wahl (ADR-023) integriert; **(B) externes Read-Model** (CQRS): eine abgeleitete OpenSearch-Projektion, gespeist von clios Event-Strom, die zusätzlich Volltext, Facetten und **Aggregation/Grouping** (das in Abschnitt 5/Stufe 4 vertagte Ziel) sowie read-seitige Skalierung liefert.
- **Entscheidung:** **Wir starten mit dem einfachen internen Indexer (Variante A).** Das externe Read-Model (Variante B) wird **bewusst zurückgestellt** und nur als Option dokumentiert — es widerspricht dem Kern (abhängigkeitsfreies Single-Binary, ADR-001; Single-Instance, ADR-002) und lohnt erst, wenn Volltext, Aggregation oder Read-Scale konkret gebraucht werden. Eckpunkte des internen Index:
  1. **Deklarativ / opt-in.** Indiziert werden **nur explizit benannte** `data`-Pfade (keine generische Auto-Indizierung — die würde Index-Größe und Write-Last unbeschränkt aufblähen). Die Felddeklaration koppelt idealerweise an die bestehende Schema-Registry (ADR-014), z. B. ein Feld-Marker im registrierten JSON-Schema; alternativ eine Konfigliste. Default: kein Feld indiziert → vollständig rückwärtskompatibel.
  2. **Speicherung als bbolt-Bucket**, analog `type_idx` (`store.go`, ADR-021): Composite-Key `(typ, feld, wert, seq)` → Event-Sequenz. Damit ist `event.type == X && event.data.feld == v` *ein* enger Range-Scan, und die Typ-Heterogenität von `data` (Feld existiert nur bei manchen Typen) ist im Key gelöst. Weil der Index in derselben DB/Transaktion lebt, bleibt er **crash-konsistent ohne separaten Rebuild** (ADR-006).
  3. **Operatoren:** Gleichheit (exakter Präfix) und `startsWith` (Präfix-Scan) zuerst; Bereichsabfragen (`<`/`>`) nur bei ordnungserhaltender Wert-Kodierung; `!=`/beliebige Funktionen fallen auf den bestehenden Scan zurück.
  4. **Planner & Cost-Wahl:** analog `pred.RequiredTypes()` (ADR-021) eine Extraktion von `event.data.<feld> == <konst>`; Aufnahme des Daten-Index in die selektivste-Index-Wahl neben Typ- und Subject-Index (ADR-023), gestützt auf Zähler je `(feld,wert)`.
  5. **Backfill/Reindex:** vorhandene Events einmalig nachindizieren (analog `backfillTypeIdx`), als expliziter Pfad (z. B. `cliostore reindex`), **nicht** im Hot-Path; bei ~10M Events ein einmaliger Scan.
- **Konsequenzen:** Der teure Payload-Scan aus ADR-028 wird für deklarierte Felder zum direkten Range-Scan (Millisekunden statt Voll-Deserialisierung). clio bleibt ein abhängigkeitsfreies Single-Binary (ADR-001) und Single-Instance (ADR-002). Kosten: **Write-Amplification** (zusätzliche `Put`s pro indiziertem Feld in der Single-Writer-Tx, ADR-003) und — weil nie gelöscht wird (ADR-015) — **monoton wachsende Indizes**; pro deklariertem Feld vertretbar, generisch nicht (daher opt-in). **Bewusste Nicht-Ziele (bleiben in Variante B / Stufe 4 offen):** Volltext, Facetten, Aggregation/Grouping und horizontale Read-Skalierung. Der OpenSearch-Pfad bleibt **sauber nachrüstbar**, gerade *weil* das Log unveränderlich und damit jede Projektion rekonstruierbar ist (CQRS): ein separater Indexer-Prozess **außerhalb** des clio-Binaries macht Backfill über `run-query`/`read`, hängt sich live an `observe` (SSE), checkpointet auf die globale Sequenz und schreibt idempotente Upserts (Key = Event-`id`); Mappings lassen sich aus den registrierten Schemas (ADR-014) ableiten. So bliebe clios Kern unangetastet (ADR-001/002), und die Projektion ist bei Mapping-Drift per Replay ab Seq 0 neu aufbaubar.

### ADR-030: Backup/Restore/Verify über konsistente bbolt-Snapshots; PITR optional
- **Status:** Akzeptiert · **Stufe 1 umgesetzt** (`cliostore backup`/`restore`/`verify`, `backup --verify`, HTTP `GET /api/v1/backup`); Stufe 2 (Continuous Archiving / PITR) bewusst zurückgestellt. Detailkonzept: [`docs/backup-restore-dr-concept.md`](docs/backup-restore-dr-concept.md), Betriebsanleitung: [`docs/backup-restore.md`](docs/backup-restore.md).
- **Kontext:** Ein Event Store braucht eine glaubwürdige Antwort auf Verlustszenarien (Platte weg, Datei korrupt, Fehlbedienung). Es gab `verify` (ADR-012) und `compact` (ADR-015), aber keinen definierten Backup-/Restore-Weg. clio ist Single-Node auf einer bbolt-Datei (ADR-002/006); die Frage war, ob DR über *Replikation* (Kafka-artig) oder *Snapshots* (Postgres-artig) gelöst wird. *(Die ursprünglich im Konzeptdokument vorgeschlagene Nummer ADR-026 war zum Umsetzungszeitpunkt bereits durch die Event-Herkunft belegt — daher ADR-030.)*
- **Entscheidung:** DR wird **snapshot-basiert** gelöst, nicht über Replikation. `cliostore backup`/HTTP `GET /api/v1/backup` schreiben einen **konsistenten Online-Snapshot** der ganzen DB via bbolt `Tx.WriteTo` (Read-Tx, kein Schreiber-Lock) atomar (temp + fsync + Rename) in eine `.clio`-Datei (selbst eine gültige bbolt-DB). `cliostore restore` spielt einen Snapshot atomar an einen Zielpfad ein (defragmentierte Kopie via `bolt.Compact` + Rename; Überschreiben nur mit `--force`). `cliostore verify` hebt `Store.Verify()` (ADR-012) auf die Offline-CLI und prüft die Hash-Kette read-only (skriptbarer Exit-Code). **Einschränkung (ehrlich dokumentiert):** Weil bbolt im Read-Write-Modus einen exklusiven Datei-Lock hält, kann das CLI `backup` eine **laufende** Instanz nicht öffnen — es ist der Cold/Offline-Pfad (Server gestoppt). Für ein **Hot-Backup** gegen den laufenden Server dient der admin-scoped HTTP-Endpunkt (in-Process `Store.Backup`). Ein End-to-End-Testfall (Backup → DB löschen → Restore → Verify → Replay → nahtloser Folge-Append) ist verbindlicher Teil der Definition.
- **Konsequenzen:** Betriebsreife DR-Story für den Single-Node-Fall mit minimalem Code, weil bbolt-Snapshot und Hash-Ketten-Verify wiederverwendet werden; das Artefakt ist kryptografisch selbstvalidierend (Vorteil gegenüber Postgres-WAL). Replikation/Hochverfügbarkeit (Follower, Failover) bleibt **außerhalb** und würde ADR-002 ablösen. Verschlüsselung/Transport der `.clio` liegt beim Betreiber. PITR (Base-Backup + fortlaufendes Event-Archiv ab Sequenz N, `restore --until`) senkt das RPO auf nahe 0, kostet aber einen laufenden Archiv-Prozess und wird erst bei konkretem Bedarf ausspezifiziert — clios Event-Strom *ist* das WAL, weshalb diese Erweiterung ohne zweite Engine/Format auskäme.
- **Bezug:** baut auf ADR-002 (Single-Instance), ADR-003/006 (serialisierte Schreibstelle, append-only + Sequenz), ADR-012 (Hash-Kette/`verify`) und ADR-015 (atomarer temp+Rename-Mechanismus von `compact`).

### ADR-031: Persistentes Audit-Log administrativer Aktionen (separater bbolt-Bucket)
- **Status:** Akzeptiert · umgesetzt (`audit_log`-Bucket, `GET /api/v1/audit`, Scope `audit`). Betriebsdoku: [`docs/audit.md`](docs/audit.md).
- **Kontext:** clio loggt bereits **jede Autorisierungsentscheidung** strukturiert nach `slog` (ADR-025, `auditDecision`) — hochvolumig, flüchtig und nicht abfragbar. Was fehlte, ist eine **nachvollziehbare, dauerhafte Spur administrativer Aktionen** („wer hat wann welchen Key angelegt/rotiert/widerrufen, ein Schema registriert, ein Backup gezogen, die DB zurückgesetzt"). Zwei Storage-Designs standen zur Wahl: **(A) interner Event-Stream** — Audit-Einträge als normale Events; **(B) separater bbolt-Bucket**. (A) wurde verworfen: Audit-Events würden in `read-events`/`run-query`/`observe` auftauchen, die globale Event-Sequenz und die Hash-Kette (ADR-012) mitprägen und so **Fach-Events stören** — genau das, was vermieden werden soll. Außerdem wäre der Audit-Strom dann über die normale Write-API zugänglich/fälschbar.
- **Entscheidung:** Das Audit-Log ist ein **separater, append-only `audit_log`-Bucket** in derselben bbolt-DB — analog zum `auth_keys`-Bucket (ADR-025) ein mutabler/kontrollierender State **getrennt vom Event-Strom**. Jeder Eintrag (`AuditEntry`) trägt eine eigene monotone Sequenz, Zeit, Actor (`kid`/Name, leer bei system/CLI), Aktion, Ergebnis (`success`/`failure`), Zielobjekt und — bei Misserfolg — eine Fehlermeldung. **Nie ein Geheimnis** (kein secret/hash). Geschrieben wird ausschließlich aus server-internen Admin-Codepfaden (Key create/rotate/revoke, Schema-Registrierung, Backup, Dev-Reset, Online-Compaction) und der Offline-CLI (`cliostore keys …`, Actor `cli`); die **normale Write-API kann den Bucket nicht erreichen** (sie schreibt nur in `events`). Gelesen wird read-only über **`GET /api/v1/audit`**, das den **neuen Scope `audit`** *oder* `admin` verlangt (`requireAnyScope`) — ein reiner Auditor-Key braucht keine Admin-Rechte. Der Dev-Reset (ADR-022) **leert den Audit-Bucket nicht** (wie `auth_keys`), damit die Spur eines Resets erhalten bleibt; der Reset selbst wird auditiert.
- **Konsequenzen:** Nachvollziehbarkeit ohne externe Abhängigkeit und ohne den Event-Strom zu verunreinigen. **Ehrliche Grenze (v1):** das Audit-Log ist append-only **per Konvention/Codepfad, aber nicht kryptografisch fälschungssicher** — anders als die Event-Hash-Kette gibt es (noch) keine Verkettung der Audit-Einträge. Wer direkten Schreibzugriff auf die bbolt-Datei oder einen `admin`-Key hat, kann Einträge manipulieren; das Audit-Log schützt gegen unbeabsichtigtes Vergessen und unprivilegierte Manipulation, nicht gegen einen kompromittierten Admin/Host (siehe `docs/threat-model.md`). Eine optionale Hash-Verkettung der Audit-Einträge (analog ADR-012) ist additiv nachrüstbar und als Folgeschritt notiert. Offline-Aktionen, die **nicht** die Live-DB schreiben (CLI-`backup`/`restore`/`verify`), erscheinen nicht im in-DB-Audit, sondern in ihrer eigenen Ausgabe/den Prozess-Logs.
- **Bezug:** baut auf ADR-025 (Keys/Scopes/Actor-Identität, slog-Audit der Authz-Entscheidungen), grenzt sich von ADR-006/012 ab (eigener Bucket statt Event-Strom) und folgt dem Reset-Ausnahme-Muster von ADR-022.

---

## 8. Offene Fragen / zu entscheiden

- ~~Persistenz für Stufe 0: eigenes Datei-Log vs. `bbolt`?~~ **Entschieden:** `bbolt` (schneller, korrekter Start).
- Genaues Format der `fromLatestEvent`-Option und deren Semantik bei fehlendem Event.
- ~~fsync-Politik: pro Write vs. gebündelt (Durability-/Performance-Abwägung) — spätestens Stufe 3.~~ **Entschieden:** Group Commit als Default, umschaltbar via `CLIO_SYNC` (ADR-009).
- ~~Event-Schemas (geplant, PR B)~~ **Umgesetzt:** JSON Schema je Event-Typ (`register-event-schema`/`read-event-schema`), Validierung beim Write, `read-event-types` liefert `hasSchema` (ADR-014).
- Versionierung von Event-Typen: nur Konvention oder Tooling-Unterstützung?
- Weitere `run-query`-Indizierung (aufbauend auf Typ-Index ADR-021 + kostenbasierter Index-Wahl ADR-023): enge Subjects mit Typ-Filter sind durch die Subject-vs-Typ-Wahl (ADR-023) adressiert. Offen bleiben: ein **echter kombinierter Subject-und-Typ-Index** und **Index-Nutzung für `lowerBound`/`upperBound`-Bereiche** (offen, bis ein konkreter Bedarf das rechtfertigt; bewusst nicht vorab gebaut). **Entschieden:** Indizes auf `event.data`-Felder werden als **interner, deklarativer Sekundärindex** umgesetzt; ein externes Read-Model (OpenSearch) ist bewusst zurückgestellt (ADR-029).
- Authentifizierte Event-Herkunft (ADR-026, *vorgeschlagen*): offen bleiben (a) Token-Lifecycle als **Tabelle vs. interner Event-Stream** (Bootstrap-/Henne-Ei-Frage) — separat als **ADR-027** zu entscheiden; (b) **Präfix-/Pattern-Matching** für dynamisch erzeugte Sources (z. B. pro Tenant), additiv nachrüstbar; (c) kryptografisch bewiesene inhaltliche Provenance über **Event-Signaturen** als eigenes späteres ADR (ADR-026 liefert nur attributierte, kein bewiesene Herkunft).
- Namespace: `cliostore` ist als Name auf GitHub/in der Go-Welt frei (kein nennenswertes bestehendes Projekt). Bewusst gewählt statt des kürzeren `clio` (mehrfach belegt, u. a. OpenTelemetry-Collector `openconfig/clio`) und `cliodb` (existiert bereits als Datomic-ähnliche immutable DB, `loganmhb/cliodb`). Modulpfad voraussichtlich `github.com/<owner>/cliostore`.

---

## 9. Glossar der Abkürzungen

- **ADR** — Architecture Decision Record
- **MVP** — Minimum Viable Product
- **NDJSON** — Newline-Delimited JSON
- **CQRS** — Command Query Responsibility Segregation
- **RBAC** — Role-Based Access Control

---

## 10. Hinweise zur Pflege dieses Dokuments

- Dieses Dokument ist ein **lebendes Dokument**. Bei jeder relevanten Änderung: Versionsnummer und Datum oben aktualisieren.
- Statusmarkierungen in der Roadmap (Abschnitt 6) bei Fortschritt pflegen.
- Neue Entscheidungen als neuen ADR mit fortlaufender Nummer ergänzen; bestehende ADRs nicht löschen, sondern bei Bedarf auf `Abgelöst durch ADR-XYZ` setzen.
- Rechtlicher Hinweis: `cliostore` ist eine unabhängige Implementierung. Es wird kein Quellcode oder geschütztes Material des Vorbilds übernommen; nur öffentlich dokumentierte Konzepte und API-Formate werden nachgebildet.
