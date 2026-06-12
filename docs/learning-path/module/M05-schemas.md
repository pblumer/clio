# M05 — Event-Schemas (JSON Schema)

> **Tracks:** Anwendungsentwickler · **Dauer:** ~20 Min

## Lernziele

- Pro Event-Typ einen **JSON-Schema-Vertrag** registrieren.
- Verstehen, dass `data` beim Schreiben gegen das Schema validiert wird (→ 400).
- Die **Unveränderlichkeit** von Schemas und die Bedingung beim Registrieren
  einordnen.

## Voraussetzungen

- [M01](M01-erstes-event.md). Grundkenntnis von [JSON Schema](https://json-schema.org/).

## Inhalt

### Warum Schemas?

Produzenten und Konsumenten brauchen einen **Vertrag** über die Struktur der
`data` eines Event-Typs. Ohne Vertrag schleichen sich Tippfehler und fehlende
Felder ein. Clio lässt dich pro Typ ein **JSON Schema** registrieren; danach
wird jedes geschriebene `data` dagegen geprüft
([ADR-014](../../../ARCHITECTURE.md#adr-014-event-schemas-via-json-schema)).

### Schema registrieren

Beispiel: `book-acquired` muss einen `title` (String) haben.

```bash
curl -X POST http://127.0.0.1:3000/api/v1/register-event-schema \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"type":"acquired","schema":{"type":"object","required":["title"],
       "properties":{"title":{"type":"string"},"author":{"type":"string"}}}}'
```

### Validierung beim Schreiben

Ab jetzt wird jedes `acquired`-Event geprüft. Ein Verstoß liefert **400**:

```bash
# OK
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[{"source":"library","subject":"/books/77","type":"acquired","data":{"title":"Foundation"}}]}'

# Fehlt "title" -> 400
curl -X POST http://127.0.0.1:3000/api/v1/write-events \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"events":[{"source":"library","subject":"/books/78","type":"acquired","data":{"author":"Asimov"}}]}'
```

### Schema lesen

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:3000/api/v1/read-event-schema?type=acquired"
```

`read-event-types` zeigt pro Typ zusätzlich `hasSchema`:

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/read-event-types
# -> {"type":"acquired","count":3,"hasSchema":true}
```

### Zwei wichtige Regeln

1. **Schemas sind unveränderlich.** Erneutes Registrieren desselben Typs → 409.
   Das schützt die Historie, verlangt aber Sorgfalt beim ersten Entwurf.
2. **Registrierung gelingt nur, wenn die bestehende Historie konform ist.**
   Hast du schon nicht-konforme `acquired`-Events, wird das Schema abgelehnt —
   so erfüllt ein Typ *mit* Schema durchgängig seinen Vertrag.

Typen ohne Schema bleiben frei (abwärtskompatibel).

## Hands-on

Skript: [`examples/bibliothek/05-schema-registrieren.sh`](../../../examples/bibliothek/05-schema-registrieren.sh)

Registriert ein Schema, schreibt ein gültiges und ein ungültiges Event und zeigt
den 400.

## Checkpoint

1. Du willst ein registriertes Schema „lockern" (ein Pflichtfeld entfernen).
   Geht das? Warum (nicht)?
2. Warum kann eine Schema-Registrierung mit **409** *oder* mit **400-Historie**
   scheitern — was ist der Unterschied?
3. Wie prüfst du schnell, welche Typen schon ein Schema haben?

→ [Lösungen](../uebungen/loesungen.md#m05)

---

**Weiter:** [M06 — CEL-Abfragen](M06-cel-queries.md)
