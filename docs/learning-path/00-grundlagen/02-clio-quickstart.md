# Grundlagen 2 — Clio Quickstart

> **Für alle Rollen.** Ziel: in ~5 Minuten von Null zu einem geschriebenen und
> gelesenen Event.

## Lernziele

Nach diesem Modul kannst du:

- Clio bauen und mit einem API-Token starten,
- die Erreichbarkeit prüfen (`ping`),
- dein erstes Event schreiben und wieder lesen,
- die interaktive API-Doku (Swagger UI) öffnen.

## Voraussetzungen

- **Go ≥ 1.24** und ein Terminal.
- `curl` (für die Hands-on-Teile).

## Schritt 1 — Bauen

```bash
# Im Repo-Wurzelverzeichnis
make build            # erzeugt ./cliostore
./cliostore -version  # -> cliostore <version>
```

## Schritt 2 — Starten

Clio braucht **zwingend** ein API-Token. Wähle ein beliebiges Geheimnis:

```bash
export TOKEN=dein-geheimes-token
CLIO_API_TOKEN=$TOKEN ./cliostore
```

Du siehst eine Log-Zeile wie `cliostore lauscht addr=:3000`. Lass diesen
Prozess laufen und öffne ein **zweites Terminal** für die nächsten Schritte.

> Standardmäßig lauscht Clio auf `:3000` und legt die Datenbank als `clio.db`
> im Arbeitsverzeichnis an. Beides ist über Env-Variablen konfigurierbar —
> siehe [Konfiguration in der README](../../../README.md#konfiguration).

## Schritt 3 — Erreichbarkeit prüfen

`ping` ist die einzige Route ohne Auth:

```bash
curl http://127.0.0.1:3000/api/v1/ping
# -> {"status":"ok"}
```

## Schritt 4 — Erstes Event schreiben

Alle Datenrouten brauchen den Bearer-Header. Wir schreiben in den Stream
`/books/42` (das wird unser durchgehendes Beispiel):

```bash
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[{"source":"library","subject":"/books/42","type":"acquired","data":{"title":"Dune"}}]}'
```

Der Server ergänzt `id`, `time` und `specversion` selbst und antwortet mit dem
gespeicherten Event (inkl. `hash` der Tamper-Evidence-Kette).

## Schritt 5 — Event lesen

```bash
curl -X POST http://127.0.0.1:3000/api/v1/read-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"/books/42"}'
```

Du bekommst dein Event als **NDJSON** zurück (ein JSON-Objekt pro Zeile).
Glückwunsch — du hast Event Sourcing in Aktion gesehen. 🎉

## Schritt 6 — Interaktive Doku

Clio liefert seine eigene Doku aus (ins Binary eingebettet, kein Internet
nötig):

- **Swagger UI:** <http://127.0.0.1:3000/docs> — „Authorize" mit deinem Token,
  dann „Try it out".
- **OpenAPI-Spec:** <http://127.0.0.1:3000/openapi.yaml>

## Schritt 7 — Status der Instanz

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/info
```

Liefert u. a. `version`, `uptimeSeconds`, `eventsTotal` und `syncMode` —
praktisch zum Verifizieren eines Deployments.

## Checkpoint

1. Warum funktioniert `read-events` ohne den `Authorization`-Header **nicht**,
   `ping` aber schon?
2. Welche drei Felder ergänzt der Server beim Schreiben automatisch?
3. Schreibe ein zweites Event in `/books/42` mit `type` `borrowed` und lies den
   Stream erneut. In welcher Reihenfolge kommen die Events zurück?

→ Lösungen in [`uebungen/loesungen.md`](../uebungen/loesungen.md#grundlagen-2).

---

**Weiter:** [Grundlagen 3 — Das Beispiel: Bibliothek](03-beispiel-bibliothek.md)
oder direkt zu deinem [Rollen-Track](../README.md#wähle-deine-rolle).
