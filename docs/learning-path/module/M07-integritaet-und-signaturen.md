# M07 — Integrität & Signaturen

> **Tracks:** Anwendungsentwickler, Betrieb, Architekt · **Dauer:** ~25 Min

## Lernziele

- Die **Hash-Kette** als Tamper-Evidence verstehen und mit `verify` prüfen.
- Den Unterschied zwischen **Integrität** (nichts wurde geändert) und
  **Authentizität** (von wem stammt es) erklären.
- Optionale **Ed25519-Signaturen** aktivieren und den öffentlichen Schlüssel
  abrufen.

## Voraussetzungen

- [M01](M01-erstes-event.md). Für die Signing-Teile: Zugriff auf den
  Serverstart (Env-Variablen).

## Inhalt

### Integrität: die Hash-Kette

Jedes Event wird über eine **SHA-256-Kette** mit seinem Vorgänger verknüpft
(`predecessorhash` → `hash`, Genesis = 64 Nullen). Damit ist jede nachträgliche
Änderung an der Historie **kryptografisch nachweisbar** — nicht nur durch die
append-only-API verhindert
([ADR-012](../../../ARCHITECTURE.md#adr-012-hash-kette-für-tamper-evidence)).

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/verify
# -> {"ok":true,"count":123,"head":"<hash>"}
# Bei Manipulation: {"ok":false,"brokenAt":"<id>","reason":"..."}
```

`verify` rechnet die gesamte Kette nach. Für den **Betrieb** ist das ein
periodischer Check: bleibt `ok:true`, ist die Historie unverändert.

### Authentizität: Ed25519-Signaturen (optional)

Die Hash-Kette beweist *Integrität*, aber nicht *Urheberschaft*. Mit einem
Ed25519-Schlüssel signiert der Server zusätzlich jedes Event über seinen Hash
([ADR-016](../../../ARCHITECTURE.md#adr-016-ed25519-signaturen-für-authentizität)).

```bash
# 1) Schlüsselpaar erzeugen
./cliostore gen-key
# -> CLIO_SIGNING_KEY=<seed-base64>
#    # public key (zum Verifizieren): <public-base64>

# 2) Server mit Signieren starten
CLIO_API_TOKEN=$TOKEN CLIO_SIGNING_KEY=<seed-base64> ./cliostore

# 3) Öffentlichen Schlüssel abrufen (Clients prüfen damit selbst)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:3000/api/v1/public-key
```

Mit aktivem Schlüssel prüft `verify` auch die Signaturen mit. Ohne
`CLIO_SIGNING_KEY` bleibt `signature` `null` (abwärtskompatibel).

### Wichtige Designdetails

- Die **Signatur geht nicht in den Hash ein** — Integrität und Authentizität
  sind getrennt. Verfälschen der Signatur bricht nur die Signaturprüfung, nicht
  die Hash-Kette.
- **Keine Byte-Kompatibilität** zu EventSourcingDB-Hashes (eigenes,
  dokumentiertes Kanonisierungsschema).
- **Schlüsselverwaltung/-rotation** liegt beim Betreiber; nur *ein* aktiver
  Schlüssel wird unterstützt.

## Hands-on

Skript: [`examples/bibliothek/07-verify-und-public-key.sh`](../../../examples/bibliothek/07-verify-und-public-key.sh)

Prüft die Kette mit `verify` und ruft (falls Signing aktiv) den öffentlichen
Schlüssel ab.

## Checkpoint

1. Was beweist die Hash-Kette, was die Signatur — und warum reicht eines allein
   nicht?
2. Warum geht die Signatur bewusst **nicht** in den Hash ein?
3. Als Betrieb: Wie setzt du `verify` sinnvoll ein, und was bedeutet
   `ok:false,brokenAt:"57"`?

→ [Lösungen](../uebungen/loesungen.md#m07)

---

**Weiter (AppDev):** zurück zum [Track](../rollen/anwendungsentwickler.md) ·
**Weiter (Betrieb):** [M08 — Betrieb & Durability](M08-betrieb-und-durability.md)
