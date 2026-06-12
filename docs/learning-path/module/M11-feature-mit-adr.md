# M11 — Ein Feature mit Test & ADR beitragen

> **Tracks:** Contributor · **Dauer:** ~45 Min (Übung)

## Lernziele

- Ein kleines Feature im Stil des Projekts hinzufügen: Handler → Store → Test.
- Die **OpenAPI-Spec** mitziehen.
- Einen **ADR** schreiben und Roadmap/Versionsstand pflegen.

## Voraussetzungen

- [M10](M10-codebasis-tour.md). Du kennst den Datenfluss und die Pakete.

## Inhalt

### Die Definition of Done für einen Beitrag

Ein PR-würdiger Beitrag in Clio umfasst typischerweise:

1. **Code** im passenden Paket (Handler in `internal/httpapi`, Logik in
   `internal/store` o. ä.).
2. **Tests** daneben (`*_test.go`), inkl. Fehlerfälle; `go test -race ./...`
   grün.
3. **OpenAPI** aktualisiert (`internal/apidocs/openapi.yaml`), falls die API
   sich ändert ([ADR-011](../../../ARCHITECTURE.md#adr-011-eingebettete-openapi-spec--swagger-ui)).
4. **ADR** für jede relevante Entscheidung + Roadmap-/Versionspflege in
   `ARCHITECTURE.md`.
5. **`gofmt`** gelaufen.

### Einen ADR schreiben

ADRs sind nummeriert fortlaufend (aktuell bis ADR-017) und folgen dem Schema:

```markdown
### ADR-0XX: <Kurztitel der Entscheidung>
- **Status:** Vorgeschlagen | Akzeptiert | Abgelöst durch ADR-YYY
- **Kontext:** Welches Problem/Spannungsfeld? Welche Zwänge?
- **Entscheidung:** Was wird konkret getan?
- **Konsequenzen:** Gewinn UND Preis. Auch unangenehme Folgen ehrlich nennen.
```

Regeln (aus [`ARCHITECTURE.md` §10](../../../ARCHITECTURE.md#10-hinweise-zur-pflege-dieses-dokuments)):

- Bestehende ADRs **nie löschen** — bei Ablösung auf „Abgelöst durch ADR-XYZ"
  setzen.
- Bei jeder relevanten Änderung **Versionsnummer und Datum** oben im Dokument
  aktualisieren, Roadmap-Status (§6) pflegen.
- **Abhängigkeitsarmut** beachten ([ADR-001](../../../ARCHITECTURE.md#adr-001-implementierungssprache-go)):
  neue Dependencies nur bewusst und begründet.

### Beispiel-Übung: Endpoint `GET /api/v1/event-count`

Eine bewusst kleine, in sich geschlossene Aufgabe zum Üben des Gesamtflusses
(`/api/v1/info` liefert die Zahl bereits — hier geht es um den **Prozess**, nicht
um Neuheit):

1. **Store:** Es gibt bereits `Count()` (von `handleInfo` genutzt) — du
   brauchst keine neue Storage-Logik, nur den Zugriff.
2. **Handler:** `handleEventCount` in `internal/httpapi/server.go`, gibt
   `{"count": <n>}` als JSON zurück. Mit `requireAuth` registrieren.
3. **Test:** in `server_test.go` — leerer Store → 0; nach N Writes → N; ohne
   Token → 401.
4. **OpenAPI:** Route in `openapi.yaml` ergänzen (Response-Schema).
5. **Doku/ADR:** Wenn die Entscheidung trivial ist, reicht ein README-Hinweis;
   bei echtem Designspielraum ein ADR-Entwurf.

> Bevor du loslegst: Lies, wie `handleInfo` `s.store.Count()` nutzt — dein
> Handler ist eine schlankere Variante davon.

### Branch, Commit, PR

- Entwickle auf einem Feature-Branch.
- Aussagekräftige Commit-Messages.
- `go test ./...`, `go test -race ./...` und `gofmt` müssen grün/sauber sein,
  bevor du den PR aufmachst.

## Hands-on

Setze die Beispiel-Übung um (oder wähle ein offenes Roadmap-Thema aus Stufe 4,
z. B. die **Projektion/Feldliste** für `run-query`). Liefere Code + Test +
OpenAPI-Eintrag + (falls nötig) ADR-Entwurf.

## Checkpoint

1. Welche **vier bis fünf** Artefakte gehören zu einem vollständigen Beitrag?
2. Du löst eine alte Entscheidung ab — was passiert mit dem alten ADR?
3. Warum ist das Hinzufügen einer neuen Abhängigkeit in Clio eine bewusste
   Entscheidung und kein Automatismus?

→ [Lösungen](../uebungen/loesungen.md#m11)

---

**Geschafft!** Zurück zum [Contributor-Track](../rollen/contributor.md).
