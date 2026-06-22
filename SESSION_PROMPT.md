# Clio — Session-Prompt für KI-Agenten

> **Zweck dieser Datei:** Ein wiederverwendbarer Prompt, den du zu Beginn
> **jeder neuen Agenten-Session** (Claude Code, Codex o. ä.) einfügst. Er
> konfiguriert den Agenten so, dass er den Prinzipien und Konventionen von Clio
> treu bleibt. Kopiere den Block unter [„Prompt zum Kopieren"](#prompt-zum-kopieren)
> und füge ihn als erste Nachricht ein.
>
> **Pflege:** Diese Datei ist Teil des Repos und damit selbst der Konvention
> unterworfen. Ändert sich eine Konvention (neuer ADR, neuer Workflow), wird
> dieser Prompt mitgezogen. Die *inhaltliche* Single Source of Truth bleibt
> [`ARCHITECTURE.md`](./ARCHITECTURE.md) — dieser Prompt verweist nur darauf.

---

## Prompt zum Kopieren

```text
Du arbeitest am Projekt „clio" (cliostore) — einem in Go geschriebenen,
abhängigkeitsarmen Single-Binary-Event-Store. Bevor du Code änderst, machst du
dich mit den Projektprinzipien vertraut und bleibst ihnen während der gesamten
Session treu.

# 1. Quellen der Wahrheit (zuerst lesen)
- ARCHITECTURE.md ist die Single Source of Truth: Ziele, Nicht-Ziele, Roadmap
  (§6) und alle Architecture Decision Records (§7, ADRs). Lies sie, bevor du
  eine Entscheidung triffst, und ordne deine Aufgabe in Roadmap/ADRs ein.
- README.md für die Nutzersicht (API, Betrieb, Dashboard).
- docs/ für Detailkonzepte (security, threat-model, testing, production-readiness,
  backup-restore, operations-profiles, swiss-api-guidelines-gap).
- docs/learning-path/rollen/contributor.md beschreibt die Arbeitsweise des
  Projekts — halte dich daran.
- Sprache: Doku, Kommentare und Commit-Messages auf Deutsch (Projektsprache).

# 2. Leitende Designprinzipien (nicht verletzen ohne ADR)
- Abhängigkeitsarmut (ADR-001): Ein schlankes, statisch gelinktes Single-Binary
  ohne externe Laufzeitdienste. Neue Dependencies NUR bewusst, begründet und in
  einem ADR festgehalten (Vorbilder: bbolt, cel-go, jsonschema). Im Zweifel:
  Standardbibliothek statt neuer Abhängigkeit.
- Unveränderlichkeit / Append-only (ADR-006, ADR-012, ADR-015): Events werden
  nie geändert oder gelöscht. Kompaktierung defragmentiert nur, sie löscht
  nichts. Brich niemals die Hash-Kette bzw. die Signatur-Garantien.
- Single-Instance (ADR-002): Kein Clustering/horizontale Skalierung. Serielle,
  atomare Schreibvorgänge (ADR-003) sind Absicht.
- Web-UI ohne Build-Step (ADR-020): Vanilla JS, go:embed, kein npm/CDN/Bundler,
  keine neuen Frontend-Abhängigkeiten.
- Eigene, abhängigkeitsfreie Metriken (ADR-013) statt schwergewichtiger Clients.
- Swiss API Guidelines: bewusste Abweichungen sind dokumentiert (ADR-018/019) —
  prüfe sie, bevor du API-Verträge änderst.

# 3. Arbeitsweise & Qualitätstor (vor jedem Commit)
- Tests gehören dazu: Jede Verhaltensänderung kommt mit Tests. Es gilt
  „Ehrlichkeit vor Vollständigkeit" — dokumentiere bewusste Testlücken, statt
  sie zu verschweigen.
- Diese Befehle müssen grün sein, bevor du committest:
    make lint    # gofmt-Check + go vet (wie in CI)
    make test    # go test ./...
    make race    # go test -race ./...  (Nebenläufigkeit ist race-sensibel)
  Vor größeren/serverseitigen Änderungen zusätzlich: make smoke (echter Prozess
  + Postman/Newman-Roundtrips).
- gofmt vor jedem Commit (make fmt).
- Halte dich beim Code an den Stil der umliegenden Pakete (cmd/, internal/*).

# 4. Dokumentations-Pflichten (Teil der Definition of Done)
- ADRs: Jede relevante Entscheidung wird in ARCHITECTURE.md §7 als ADR
  festgehalten — fortlaufende Nummer, NIE löschen. Wird ein ADR abgelöst, setze
  seinen Status auf „Abgelöst durch ADR-XYZ" und ergänze den Kontext, statt ihn
  zu entfernen.
- Roadmap-Status (ARCHITECTURE.md §6) bei abgeschlossenen Punkten pflegen.
- OpenAPI ist handgepflegt (ADR-011): Bei jeder API-Änderung
  internal/apidocs/openapi.yaml mitziehen; ggf. Postman-Collection
  (make postman-gen) und README/Doku aktualisieren.

# 5. Git-Workflow
- Entwickle auf einem Feature-Branch, nicht direkt auf main.
- Conventional Commits auf Deutsch im Projektstil, z. B.:
    feat(auth): Subject-/Prefix-basierte Scopes (read:/orders/*) — ADR-033
    fix(webui): Layout-Bruch im Explorer bei langen Event-Typen verhindern
  Referenziere den/die betroffenen ADR(s) in der Commit-Message.
- Keinen Pull Request anlegen, außer ich bitte ausdrücklich darum.

# 6. Vorgehen bei Aufgaben
- Erst verstehen, dann handeln: relevante ADRs/Doku lesen, Datenfluss
  Request → internal/httpapi → internal/store nachvollziehen.
- Bei architektonisch bedeutsamen oder mehrdeutigen Entscheidungen nachfragen,
  statt zu raten.
- Berichte Ergebnisse ehrlich: schlagen Tests fehl, sag es mit Ausgabe; wurde
  ein Schritt ausgelassen, nenne ihn.

Bestätige kurz, dass du ARCHITECTURE.md (Roadmap + relevante ADRs) gelesen hast,
und nenne die ADRs, die für die anstehende Aufgabe einschlägig sind, bevor du
mit der Umsetzung beginnst.
```

---

## Warum diese Punkte?

Eine knappe Zuordnung der Prompt-Regeln zu ihren Quellen im Repo — falls du den
Prompt anpassen oder einem Kollegen erklären willst:

| Regel im Prompt | Quelle |
|---|---|
| Single Source of Truth, ADRs, Roadmap | `ARCHITECTURE.md` §6/§7 |
| Abhängigkeitsarmut, Single-Binary | ADR-001 |
| Unveränderlichkeit, Hash-Kette, Kompaktierung | ADR-006, ADR-012, ADR-015 |
| Single-Instance, serielle Writes | ADR-002, ADR-003 |
| Web-UI ohne Build-Step | ADR-020 |
| Eigene Metriken | ADR-013 |
| Swiss API Guidelines / Abweichungen | ADR-018, ADR-019, `docs/swiss-api-guidelines-gap.md` |
| Tests, `make`-Tore, „Ehrlichkeit vor Vollständigkeit" | `docs/testing.md`, `Makefile`, `docs/production-readiness.md` |
| Handgepflegte OpenAPI | ADR-011 |
| Arbeitsweise/Konventionen, Commit-Stil | `docs/learning-path/rollen/contributor.md`, Git-Historie |

> **Tipp:** Viele Agenten (z. B. Claude Code) laden eine Datei namens
> `CLAUDE.md` bzw. `AGENTS.md` automatisch als Projektkontext. Wenn du die
> Konfiguration ohne manuelles Einfügen willst, kannst du den Prompt-Block oben
> zusätzlich in eine solche Datei spiegeln.
