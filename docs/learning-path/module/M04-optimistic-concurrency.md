# M04 — Optimistic Concurrency (Preconditions)

> **Tracks:** Anwendungsentwickler, Architekt · **Dauer:** ~25 Min
> **Beispiel-Domäne:** Bankkonto (`/accounts/...`) — hier werden Invarianten
> greifbar.

## Lernziele

- Verstehen, warum man beim Schreiben **Bedingungen** braucht.
- Die drei Precondition-Typen anwenden: `isSubjectPristine`,
  `isSubjectOnEventId`, `isQueryResultEmpty`/`isQueryResultNonEmpty`.
- Den **HTTP 409** als Konfliktsignal korrekt behandeln.
- Zwei Invarianten absichern: „nur einmal eröffnen" und „kein verlorenes Update".

## Voraussetzungen

- [M01](M01-erstes-event.md)–[M02](M02-lesen-und-filtern.md). Idee von CEL hilft
  ([M06](M06-cel-queries.md)), ist aber nicht zwingend.

## Inhalt

### Das Problem

Zwei Clients lesen den Kontostand `100 €`, beide heben `80 €` ab, beide
schreiben. Ohne Schutz entstünde ein Saldo von `20 €` statt einer abgelehnten
zweiten Abhebung — ein **verlorenes Update**.

Clio löst das **optimistisch**: Der Write trägt eine **Precondition**, die
**atomar** in der Schreibtransaktion geprüft wird. Schlägt sie fehl, wird
**nichts** geschrieben und der Server antwortet mit **HTTP 409 Conflict**.

### Precondition-Typen

| Typ | Bedeutung | Anwendungsfall |
|---|---|---|
| `isSubjectPristine` | schreibe nur, wenn der Stream noch **leer** ist | „Konto nur einmal eröffnen" |
| `isSubjectOnEventId` | schreibe nur, wenn das **letzte** Event des Streams diese ID hat | optimistisches Sperren (verlorene Updates verhindern) |
| `isQueryResultEmpty` / `isQueryResultNonEmpty` | schreibe nur, wenn eine **CEL-Abfrage** über den Scope (kein) Treffer liefert | Bedingung über Inhalte (`event.data`) |

> Die Query-Preconditions sind das `isEventQlQueryTrue`-Äquivalent
> ([ADR-017](../../../ARCHITECTURE.md#adr-017-abfrageschicht-auf-cel-statt-eigener-eventql-sprache)).

### Invariante 1 — Konto nur einmal eröffnen

```bash
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
        "events":[{"source":"bank","subject":"/accounts/42","type":"opened","data":{"owner":"Ada"}}],
        "preconditions":[{"type":"isSubjectPristine","payload":{"subject":"/accounts/42"}}]
      }'
```

Ein **zweiter** Aufruf desselben Befehls liefert **409** — der Stream ist nicht
mehr pristine.

Inhaltsbasierte Variante (gleiches Ziel, über CEL): „schreibe nur, wenn es noch
kein `opened` gibt":

```bash
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
        "events":[{"source":"bank","subject":"/accounts/42","type":"opened"}],
        "preconditions":[{"type":"isQueryResultEmpty",
          "payload":{"subject":"/accounts/42","where":"event.type == '\''opened'\''"}}]
      }'
```

### Invariante 2 — kein verlorenes Update (optimistisches Sperren)

Der Ablauf:

1. **Lesen:** den Stream `/accounts/42` lesen, die **ID des letzten Events**
   merken (z. B. `7`).
2. **Entscheiden:** App prüft die Geschäftsregel (Saldo reicht für die Abhebung).
3. **Schreiben mit Bedingung:** nur, wenn das letzte Event *immer noch* `7` ist:

```bash
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
        "events":[{"source":"bank","subject":"/accounts/42","type":"withdrawn","data":{"amount":80}}],
        "preconditions":[{"type":"isSubjectOnEventId","payload":{"subject":"/accounts/42","eventId":"7"}}]
      }'
```

Hat in der Zwischenzeit jemand anderes geschrieben, ist das letzte Event nicht
mehr `7` → **409**. Deine App liest dann neu, entscheidet neu und versucht es
erneut (Retry-Schleife). So bleibt die Saldo-Invariante korrekt, ohne Locks.

## Hands-on

Skript: [`examples/bankkonto/04-preconditions.sh`](../../../examples/bankkonto/04-preconditions.sh)
(auch unter `examples/bibliothek/04-preconditions.sh` als kurze Variante).

Es eröffnet ein Konto, zeigt den 409 beim zweiten `opened` und demonstriert das
optimistische Sperren bei einer Abhebung.

## Checkpoint

1. Warum heißt das Verfahren *optimistic* concurrency und nicht *pessimistic*?
2. Du bekommst 409 bei einer Abhebung. Was tut deine App als Nächstes?
3. Wann nimmst du `isSubjectOnEventId`, wann `isQueryResultEmpty`?

→ [Lösungen](../uebungen/loesungen.md#m04)

---

**Weiter:** [M05 — Event-Schemas](M05-schemas.md)
