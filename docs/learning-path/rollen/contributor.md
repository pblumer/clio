# Track: Contributor / Go-Entwickler:in 🧩

> Du willst Clio **erweitern** — die Codebasis verstehen, einem PR-würdigen
> Standard folgen und Features mitsamt Tests und einem ADR beitragen. Dieser
> Track macht dich mit Struktur und Arbeitsweise des Projekts vertraut.

## Dein Ziel

Du findest dich im Code zurecht, kennst den Weg vom HTTP-Handler bis zum
Storage, und kannst ein kleines Feature im Stil des Projekts hinzufügen —
inklusive Test, OpenAPI-Update und ADR.

## Voraussetzungen

- Solide **Go**-Kenntnisse; `go test` und Standardbibliothek vertraut.
- Den [Anwendungsentwickler-Track](anwendungsentwickler.md) zumindest überflogen
  (du solltest die API als Nutzer kennen, bevor du sie änderst).
- [`ARCHITECTURE.md`](../../../ARCHITECTURE.md) einmal ganz gelesen — besonders
  Roadmap (§6) und ADRs (§7).

## Reihenfolge

| # | Modul | Du lernst… |
|---|---|---|
| 1 | [M10 — Tour durch die Codebasis](../module/M10-codebasis-tour.md) | Paketstruktur (`cmd`, `internal/*`), Datenfluss Request→Store, Testkonventionen |
| 2 | [M11 — Feature mit Test & ADR](../module/M11-feature-mit-adr.md) | end-to-end: Handler + Store + Test + OpenAPI + ADR |

## Arbeitsweise des Projekts (Konventionen)

- **`ARCHITECTURE.md` ist die Single Source of Truth.** Jede relevante
  Entscheidung wird als **ADR** festgehalten (fortlaufende Nummer, nie löschen,
  bei Ablösung auf „Abgelöst durch ADR-XYZ" setzen). Roadmap-Status pflegen.
- **Abhängigkeitsarmut ist ein Designziel** ([ADR-001](../../../ARCHITECTURE.md#adr-001-implementierungssprache-go)).
  Neue Dependencies nur bewusst und begründet (wie bbolt, cel-go, jsonschema).
- **Tests gehören dazu.** `go test ./...` und `go test -race ./...` müssen grün
  sein; Nebenläufigkeit (Observe, Group Commit) ist race-sensibel.
- **OpenAPI-Spec ist handgepflegt** ([ADR-011](../../../ARCHITECTURE.md#adr-011-eingebettete-openapi-spec--swagger-ui)).
  Bei API-Änderungen `internal/apidocs/openapi.yaml` mitziehen.
- **`gofmt`** vor dem Commit.

## Erste sinnvolle Beiträge

Schau in die offene Roadmap ([`ARCHITECTURE.md` §6, Stufe 4](../../../ARCHITECTURE.md#stufe-4--abfragen-cel-basiert--snapshots-))
und die offenen Fragen (§8). Gute Einstiegsthemen:

- **Projektion/Feldliste** für `run-query` (Stufe 4, Etappe 4 — offen).
- Kleine API-Ergänzungen mit klarem Vertrag und Test.

## Geschafft, wenn…

Du den Pfad eines Writes von `internal/httpapi/server.go` bis
`internal/store/store.go` nachzeichnen kannst — und einen kleinen Endpoint mit
Test, OpenAPI-Eintrag und ADR-Entwurf vorgelegt hast.
