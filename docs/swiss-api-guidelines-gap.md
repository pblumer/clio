# Gap-Analyse: clio vs. Swiss API Guidelines

> **Zweck:** Bewertung, was die Einhaltung der [Swiss Federal Administration API
> Guidelines](https://github.com/swiss/api-guidelines) für `clio` bedeuten würde.
> **Reine Analyse — keine Umsetzung.** Entscheidung: siehe ADR-018.
> **Stand:** 2026-06-12 (Quick Wins problem+json & Cache-Control umgesetzt, ADR-019)

## Kernaussage

`clio` ist bewusst **EventSourcingDB-kompatibel** (RPC-artige Verb-Endpunkte) und
**NDJSON-Streaming-orientiert** (inkl. `observe`/Live-Tail). Die Swiss Guidelines
fordern eine **ressourcenorientierte REST-API (Maturity Level 2)** mit JSON-Listen
und Pagination. Zwei unserer expliziten Designziele stehen damit in **direktem
Konflikt** zu den zentralen MUST-Regeln. Vollständige Konformität hieße faktisch
**eine andere (oder eine zweite) API**.

Es gibt aber eine klare Trennlinie: Ein Teil der Regeln ist **billig und
konfliktfrei** erfüllbar (Quick Wins), der konfliktäre Rest ist **groß und
zielwidrig**.

## Konformitätstabelle

| Guideline (Auszug) | Stufe | clio heute | Bewertung | Aufwand |
|---|---|---|---|---|
| URLs verb-frei, ressourcenorientiert | MUST | `write-events`, `read-events`, `run-query`, `register-event-schema`, `verify` … | ❌ RPC-Stil | **groß, zielkonfliktär** |
| HTTP-Methoden korrekt (Reads = GET) | MUST | Reads via POST (`read-events`), Schema-Register via POST | ❌ teilweise | **groß** |
| Top-Level-JSON-Objekt, keine Arrays | MUST | NDJSON-Streams (Objekt pro Zeile) | ❌ | **groß, zielkonfliktär** |
| Pagination für große Mengen (cursor) | MUST | `lowerBound`/`upperBound` + `limit`, kein Cursor | ⚠️ teils (Streaming statt Pagination) | **mittel–groß** |
| Offizielle Status-Codes | MUST | 200/400/401/404/409/500 | ✅ | — |
| `201 Created` + `Location` bei Erstellung | SHOULD | Writes → 200 | ⚠️ | klein |
| `412 Precondition Failed` (bei If-Match) | — | wir nutzen 409 für Preconditions (eigenes Modell) | ⚠️ (anderes Modell) | mittel |
| Fehler-Body strukturiert, keine Stacktraces | MUST/SHOULD | `problem+json` (RFC 7807), keine Stacktraces | ✅ (ADR-019) | — |
| `application/problem+json` (RFC 7807) | SHOULD | ja (`{type,title,status,detail}`) | ✅ (ADR-019) | — |
| Feldnamen konsistent snake_case **oder** camelCase | MUST | eigene Felder camelCase; CloudEvents-Felder „flatcase" (`specversion`, `datacontenttype`) | ⚠️ Mix (CloudEvents-bedingt) | mittel, teils unvermeidbar |
| Standard-Datumsformat RFC 3339 | MUST | `time` als RFC3339Nano | ✅ | — |
| OpenAPI 3.0+, eine YAML, versioniert | MUST | OpenAPI 3.0.3, `internal/apidocs/openapi.yaml` | ✅ | — |
| OpenAPI-Meta: `x-audience`, `license`, `contact` | MUST | fehlt | ⚠️ | **klein** |
| Versionierung vermeiden; falls, dann `/v2` (kein `/v1`) | SHOULD | `/api/v1/` | ⚠️ kosmetisch | klein |
| `Cache-Control: no-store` als Default | MUST | Default-Header auf allen Antworten | ✅ (ADR-019) | — |
| Standard-Header kebab-case lowercase | SHOULD | ✅ | ✅ | — |
| ETag + If-Match (optimistic locking) | MAY | eigenes Precondition-Modell (inkl. CEL) | ⚠️ (reicher, aber nicht ETag) | optional/groß |
| Idempotency-Key für POST/PATCH | MAY | nein | ⚠️ | optional |
| Token im Header (nicht Query) | — | ✅ Bearer im Header | ✅ | — |
| Sensible Daten nicht in Query | — | Filter in Query (nicht sensibel), Token im Header | ✅ | — |
| Kein `null` für Booleans/leere Arrays | MUST | Booleans nie null; Listen leer statt null | ✅ (Audit empfohlen) | klein |
| kebab-case in Pfadsegmenten | MUST | ✅ (`read-event-types`) | ✅ | — |

## Die drei harten Konflikte (zielwidrig)

1. **Verb-frei / Ressourcen statt Aktionen** vs. **EventSourcingDB-Kompatibilität.**
   Deren API *ist* genau `write-events`/`read-events`/… Beides gleichzeitig geht
   nicht. Konformität = anderer URL-Raum (z. B. `POST /events`,
   `GET /events?subject=…`, `PUT /event-types/{type}/schema`).
2. **Top-Level-JSON-Objekt + Cursor-Pagination** vs. **NDJSON-Streaming/`observe`.**
   Live-Tail und zeilenweises NDJSON passen nicht in „Liste mit
   `{items, pagination}`". Konformität würde das Streaming-Design verbiegen.
3. **Feldnamen-Konsistenz** vs. **CloudEvents-Konformität.**
   `specversion`, `datacontenttype`, `predecessorhash` sind vom CloudEvents-
   Standard vorgegeben (flatcase) — nicht frei wählbar, solange wir CloudEvents-
   konform bleiben (ebenfalls ein Ziel).

## Billige, konfliktfreie Quick Wins

**Umgesetzt (ADR-019):**
- ✅ Fehler als `application/problem+json` (RFC 7807): `{type,title,status,detail}`.
- ✅ `Cache-Control: no-store` als Default-Header (alle Antworten).

**Noch offen (optional):**
- OpenAPI-Meta ergänzen: `x-audience` (private/partner/public), `license`, `contact`.
- `201 Created` + `Location` bei `write-events` (sofern wir das Streaming-Antwortformat anpassen wollen — sonst dokumentierte Abweichung).
- camelCase-Konsistenz unserer **eigenen** Felder bestätigen/erzwingen; CloudEvents-Felder als dokumentierte Ausnahme.
- `null`/Leer-Array-Audit (formell bestätigen).

## Empfehlung

Die Guidelines sind für klassische, ressourcenorientierte Verwaltungs-APIs
gedacht; `clio` ist ein EventSourcingDB-kompatibler, Streaming-orientierter Event
Store. **Volle Konformität widerspricht den Kernzielen.** Sinnvoll wäre — *wenn*
Konformität ein Thema wird — die **Quick Wins** umzusetzen und die drei harten
Konflikte als **bewusste, dokumentierte Abweichungen** (ADR-018) zu führen.
Alternativ ließe sich eine **separate REST-Fassade** neben der RPC-API anbieten
(größtes Vorhaben).

**Quellen:** [swiss/api-guidelines](https://github.com/swiss/api-guidelines)
