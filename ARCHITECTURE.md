# Projekt-Architektur & Kontext: `goesdb` — ein Event Store in Go

> **Zweck dieses Dokuments**
> Dieses Dokument ist die *Single Source of Truth* für das Projekt. Es ist so geschrieben, dass eine KI oder eine Person ohne Vorwissen nach dem Lesen vollständig versteht: **Was** gebaut wird, **warum**, **welche Ziele** verfolgt werden, **welche Entscheidungen** getroffen wurden und **wo das Projekt aktuell steht**. Es kombiniert ein Kontextdokument mit eingebetteten Architecture Decision Records (ADRs).
>
> **Status des Gesamtprojekts:** `KONZEPT` — noch kein Code geschrieben. Dies ist das Gründungsdokument.
> **Letzte Aktualisierung:** 2026-06-10
> **Dokumentversion:** 1.0

---

## 1. Worum geht es? (Elevator Pitch)

`goesdb` ist eine eigenständige, von Grund auf in Go geschriebene Neuimplementierung eines dedizierten **Event Stores**, funktional orientiert am Vorbild **EventSourcingDB** (von the native web GmbH). Es ist *kein* Fork und nutzt keinen Code des Originals — es ist eine unabhängige Implementierung, die denselben Funktionsumfang und dieselben API-Konzepte nachbaut, um Event-Sourcing-Systeme zu betreiben.

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
| `POST /api/v1/read-events` | Events eines Subjects lesen; Optionen: `recursive`, `lowerBound`, `upperBound`, `fromLatestEvent` | 0 → 1 |
| `POST /api/v1/observe-events` | Wie read, aber Verbindung bleibt offen für Live-Updates; Reconnect via `lowerBound` | 2 |
| `POST /api/v1/run-eventql-query` | EventQL-Abfrage (spätes Ziel) | 4 |

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

### Stufe 0 — MVP `⬜`
*Schätzung: 1–2 Wochen (1 Person)*
- [ ] `write-events`: Candidate validieren, CloudEvents-Felder ergänzen, append-only schreiben
- [ ] `read-events`: Events eines Subjects als NDJSON
- [ ] `ping`
- [ ] Bearer-Token-Auth (ein Token via Env-Var)
- [ ] Storage: append-only Log (Datei) + In-Memory-Index `subject → []offset`, oder `bbolt`
- **Ergebnis:** Events können geschrieben und gelesen werden.

### Stufe 1 — Ordnung & Concurrency `⬜`
*Schätzung: 1–2 Wochen*
- [ ] Globale, monoton steigende Event-IDs (serialisiert)
- [ ] Atomares Schreiben mehrerer Events (alles-oder-nichts)
- [ ] Preconditions `isSubjectPristine`, `isSubjectOnEventId`
- [ ] `lowerBound` / `upperBound` beim Lesen
- [ ] Serialisierte Write-Queue / einzelner Write-Mutex (siehe ADR-003)

### Stufe 2 — Observe / Live-Streaming `⬜`
*Schätzung: 1–2 Wochen*
- [ ] `observe-events`: erst History, dann offene Verbindung
- [ ] Pub/Sub via Channels, eine Goroutine pro Verbindung, `http.Flusher`
- [ ] Reconnect via `lowerBound`
- [ ] `recursive`-Flag + Subject-Prefix-Matching

### Stufe 3 — Robustheit & Betrieb `⬜`
*Schätzung: 2–4 Wochen*
- [ ] Crash-Recovery / Index-Rebuild beim Start
- [ ] fsync-Strategie (Durability vs. Performance)
- [ ] Kompaktierung / Dateirotation
- [ ] Observability: Metrics, strukturiertes Logging
- [ ] Single-Binary-Builds für alle Plattformen (`GOOS`/`GOARCH`)
- [ ] Docker-Image

### Stufe 4 — Snapshots & EventQL `⬜`
*Schätzung: 1–3 Monate (EventQL dominiert)*
- [ ] Snapshots (Speichern/Laden aggregierten Zustands)
- [ ] EventQL: Lexer, Parser, Planner, Executor (größter Einzelposten — siehe ADR-007)
- [ ] `isEventQlQueryTrue`-Precondition

**Gesamteinschätzung:** Funktional brauchbarer Klon (Stufen 0–3, ohne EventQL) realistisch in **6–10 Wochen** für eine erfahrene Go-Person. Das produktreife „letzte Stück" (vollwertiges EventQL, Performance-Tuning, Format-Migrationen) nochmals **3–6+ Monate**.

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

### ADR-007: EventQL als spätes, optionales Ziel
- **Status:** Akzeptiert
- **Kontext:** Eine eigene Query-Sprache (Lexer/Parser/Planner/Executor) kann den Aufwand des gesamten Restprojekts übersteigen.
- **Entscheidung:** Der Kern (Write/Read/Observe/Preconditions) funktioniert ohne EventQL. EventQL kommt erst in Stufe 4; bis dahin genügt eine einfachere Filter-API.
- **Konsequenzen:** Früher Nutzwert ohne Sprachimplementierung. `isEventQlQueryTrue` erst ab Stufe 4 verfügbar.

### ADR-008: Authentifizierung über einzelnes API-Token
- **Status:** Akzeptiert
- **Kontext:** Zugriffsschutz wird benötigt, RBAC ist ein Non-Goal.
- **Entscheidung:** Ein konfiguriertes Bearer-Token schützt alle Routen.
- **Konsequenzen:** Minimaler Aufwand. Keine Mandantentrennung/Rollen — bewusst akzeptiert.

---

## 8. Offene Fragen / zu entscheiden

- Persistenz für Stufe 0: eigenes Datei-Log vs. `bbolt`? (Tendenz: `bbolt` für schnellen, korrekten Start.)
- Genaues Format der `fromLatestEvent`-Option und deren Semantik bei fehlendem Event.
- fsync-Politik: pro Write vs. gebündelt (Durability-/Performance-Abwägung) — spätestens Stufe 3.
- Versionierung von Event-Typen: nur Konvention oder Tooling-Unterstützung?

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
- Rechtlicher Hinweis: `goesdb` ist eine unabhängige Implementierung. Es wird kein Quellcode oder geschütztes Material des Vorbilds übernommen; nur öffentlich dokumentierte Konzepte und API-Formate werden nachgebildet.
