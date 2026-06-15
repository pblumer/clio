# Projekt-Architektur & Kontext: `cliostore` — ein Event Store in Go

> **Zweck dieses Dokuments**
> Dieses Dokument ist die *Single Source of Truth* für das Projekt. Es ist so geschrieben, dass eine KI oder eine Person ohne Vorwissen nach dem Lesen vollständig versteht: **Was** gebaut wird, **warum**, **welche Ziele** verfolgt werden, **welche Entscheidungen** getroffen wurden und **wo das Projekt aktuell steht**. Es kombiniert ein Kontextdokument mit eingebetteten Architecture Decision Records (ADRs).
>
> **Status des Gesamtprojekts:** `IN ENTWICKLUNG` — **Stufe 0–3 abgeschlossen** plus **Ed25519-Signaturen** (Authentizität). Write/Read/Observe, Optimistic Concurrency, Hash-Kette + Signaturen (`/verify`, `/public-key`), Event-Typen + JSON-Schemas, Group Commit (`CLIO_SYNC`), Observability (`/metrics`), Distribution (Cross-Builds/Docker/Release), Kompaktierung (`cliostore compact`), OpenAPI/Swagger UI, **CEL-Abfragen (`run-query`, ADR-017) mit Typ-Index (ADR-021)** und ein **eingebettetes Betriebs-Dashboard (`/ui`, ADR-020)**. Offen (Stufe 4): Aggregation/Grouping und Snapshots.
> **Letzte Aktualisierung:** 2026-06-15
> **Dokumentversion:** 1.29

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
- **Kein RBAC:** Authentifizierung erfolgt über ein einzelnes API-Token. Keine feingranularen Rollen.
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
│ Append-only Log │     │ Channels/Goroutinen  │     │ subject → []offset   │
│ (Datei / bbolt) │     │ pro Verbindung       │     │ In-Memory + Rebuild  │
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

**Auth:** Header `Authorization: Bearer <API_TOKEN>` gegen ein konfiguriertes Einzeltoken.

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
- [x] Pub/Sub via Channels (`internal/pubsub`), `http.Flusher` pro Zeile; langsame Subscriber werden abgehängt (→ Reconnect) statt den Schreibpfad zu blockieren
- [x] Reconnect via `lowerBound`
- [x] `recursive`-Flag + Subject-Prefix-Matching (`store.MatchSubject`) — auch für `read-events`; rekursive Reads laufen über den globalen `events`-Bucket und bewahren so die globale Ordnung
- **Ergebnis:** Live-Beobachtung von Streams inkl. rekursiver Subjects. ✅

### Stufe 3 — Robustheit & Betrieb `✅`
*Schätzung: 2–4 Wochen*
- [x] Crash-Recovery: durch bbolts ACID-Transaktionen gegeben — Index ist Teil derselben DB und damit immer konsistent; ein separater Rebuild entfällt (siehe ADR-006).
- [x] fsync-Strategie (Durability vs. Performance) → **Group Commit** als Default (ADR-009), umschaltbar via `CLIO_SYNC` (`group`/`always`/`off`). Benchmarks belegen den Effekt.
- [x] Kompaktierung — `cliostore compact` (offline, atomarer Swap) defragmentiert die bbolt-Datei ohne Events zu löschen; `clio_db_size_bytes`-Metrik (ADR-015). *Rotation/Archivierung bewusst nicht: widerspricht der Unveränderlichkeit (siehe ADR-015).*
- [x] Observability: strukturiertes Request-Logging (slog) + Prometheus-`/metrics` (Requests, Latenz-Histogramm, geschriebene Events, 409-Failures, aktive Observer, Event-Count, DB-Größe, Laufzeit: Speicher/Goroutinen/CPU) — ADR-013, ohne Prometheus-Client-Dependency
- [x] Single-Binary-Builds für alle Plattformen (`GOOS`/`GOARCH`) — `make dist` (linux/darwin/windows × amd64/arm64), Version via `-ldflags` eingebettet; Release-Workflow bei Tags `v*`
- [x] Docker-Image — mehrstufig, `distroless/static`, nonroot, `/data`-Volume
- **Ergebnis:** Betriebsreif — Durability-Tuning, Observability, Distribution, Wartung. ✅

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
- **Status:** Akzeptiert
- **Kontext:** Zugriffsschutz wird benötigt, RBAC ist ein Non-Goal.
- **Entscheidung:** Ein konfiguriertes Bearer-Token schützt alle Routen.
- **Konsequenzen:** Minimaler Aufwand. Keine Mandantentrennung/Rollen — bewusst akzeptiert.

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

### ADR-021: Typ-Index für `run-query`
- **Status:** Akzeptiert
- **Kontext:** `run-query` (CEL, ADR-017) scannte für jede Abfrage den **gesamten** Scope und materialisierte alle Events, bevor das Prädikat ausgewertet wurde. Bei großen Stores (Hunderttausende Events) dauerte schon ein einfacher `event.type == 'X'`-Filter mehrere Sekunden — zwei vermeidbare Kosten: (1) `event.data` wurde je Event in eine Map geparst, auch wenn das Prädikat es nicht nutzt; (2) ohne Index muss jedes Event angefasst werden.
- **Entscheidung:** Zwei Optimierungen. **(a)** Das Prädikat parst `event.data` nur noch, wenn der Ausdruck `data` referenziert (Token-Check beim Compile; konservativ — ein Fehlalarm parst wie bisher, nie ein falsches Ergebnis). **(b)** Ein persistenter **Sekundärindex `type_idx`** (Bucket-Schlüssel `type + 0x00 + seq`, analog zum Subject-Index) bildet Event-Typ → Sequenzen ab. Aus dem kompilierten CEL-AST wird die Menge der **notwendig** geforderten `event.type`-Werte abgeleitet (`==`, `in`, `&&`→Schnitt, `||`→Vereinigung nur wenn beide Seiten einschränken; alles andere → kein Constraint). Liegt ein sicherer Typ-Constraint vor, lädt `run-query` nur die Events dieser Typen über den Index (mit Limit-Abbruch) statt den ganzen Scope; Subject-Scope und das restliche Prädikat werden danach geprüft. Für Bestands-DBs wird der Index beim Öffnen einmalig aus der Historie nachgebaut (idempotenter Backfill, wie bei den Typ-Zählern).
- **Konsequenzen:** `event.type == 'X'` über große Scopes fällt von Sekunden auf **Millisekunden** (Benchmark: 150k Events, Treffer am Ende, `/` rekursiv: ~1,5 s → ~1,6 ms; Allokationen 410 MB → 0,44 MB). Die Ableitung ist **sicher**: kann der Typ nicht zuverlässig eingeschränkt werden (z. B. `!=`, ein `||` mit unbeschränkter Seite), fällt die Abfrage auf den vollständigen Scan zurück — nie ein falsches Ergebnis. Kosten: ein zusätzlicher Index (mehr Schreib-/Speicheraufwand pro Event, wie beim Subject-Index) und der einmalige Backfill beim ersten Öffnen. Ein kombinierter Subject-und-Typ-Index sowie Index-Nutzung für Bereichsvergleiche bleiben mögliche spätere Schritte.

---

## 8. Offene Fragen / zu entscheiden

- ~~Persistenz für Stufe 0: eigenes Datei-Log vs. `bbolt`?~~ **Entschieden:** `bbolt` (schneller, korrekter Start).
- Genaues Format der `fromLatestEvent`-Option und deren Semantik bei fehlendem Event.
- ~~fsync-Politik: pro Write vs. gebündelt (Durability-/Performance-Abwägung) — spätestens Stufe 3.~~ **Entschieden:** Group Commit als Default, umschaltbar via `CLIO_SYNC` (ADR-009).
- ~~Event-Schemas (geplant, PR B)~~ **Umgesetzt:** JSON Schema je Event-Typ (`register-event-schema`/`read-event-schema`), Validierung beim Write, `read-event-types` liefert `hasSchema` (ADR-014).
- Versionierung von Event-Typen: nur Konvention oder Tooling-Unterstützung?
- Weitere `run-query`-Indizierung (Aufbauend auf dem Typ-Index, ADR-021): kombinierter **Subject-und-Typ-Index** für enge Subjects mit Typ-Filter, **Index-Nutzung für `lowerBound`/`upperBound`-Bereiche** sowie ggf. Indizes auf häufige `event.data`-Felder. Offen, bis ein konkreter Bedarf das rechtfertigt; bewusst nicht vorab gebaut.
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
