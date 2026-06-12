# Grundlagen 4 — Postman & automatisierte Tests

> **Für alle Rollen, besonders Anwendungsentwickler:innen.** Ziel: Clio nicht
> per `curl`, sondern **klickbar in Postman** bedienen — und dieselben Aufrufe
> als automatisierten Smoke-Test (Newman) laufen lassen.

## Lernziele

Nach diesem Modul kannst du:

- die mitgelieferte **Postman-Collection** und das **Environment** importieren,
- alle Endpunkte per Klick aufrufen und **NDJSON-Antworten** lesen,
- die in der Collection hinterlegten **Tests** verstehen und im **Collection
  Runner** ausführen,
- dieselben Tests **headless** per `make smoke` / Newman fahren — die Brücke zu
  [CI](../module/M09-observability.md) und zum
  [Quickstart mit `curl`](02-clio-quickstart.md).

## Voraussetzungen

- [Grundlagen 1–3](.) und idealerweise [M01](../module/M01-erstes-event.md) /
  [M02](../module/M02-lesen-und-filtern.md) — du solltest Events,
  Subjects und NDJSON schon kennen.
- Eine **laufende Clio-Instanz** (siehe [Quickstart, Schritt 1–2](02-clio-quickstart.md))
  und dein API-Token.
- **[Postman](https://www.postman.com/downloads/)** (Desktop oder Web) für den
  Klick-Weg; für den headless-Weg nur **`npx`** (Node).

> **Quelle der Wahrheit bleibt die OpenAPI-Spec**
> ([`internal/apidocs/openapi.yaml`](../../../internal/apidocs/openapi.yaml)).
> Die Collection ist daraus abgeleitet — siehe
> [README, „Postman & Smoke-Test"](../../../README.md#postman--smoke-test-newman).

## Inhalt

### Schritt 1 — Die mitgelieferten Dateien

Im Repo liegen unter [`postman/`](../../../postman/) zwei Dateien:

| Datei | Inhalt |
|---|---|
| `clio.postman_collection.json` | Alle Endpunkte, geordnet als Durchlauf (schreiben → lesen → abfragen → verifizieren), mit Test-Skripten |
| `local.postman_environment.json` | Die Variablen `baseUrl` (z. B. `http://localhost:3000`) und `token` |

Die Collection ist in drei Ordner gegliedert: **System** (`ping`, `/metrics`),
**Events** (Schreiben, Lesen, `run-query`, `verify`, …) und **Negativ-Fälle**
(401 ohne Token, 400 bei ungültiger Anfrage).

### Schritt 2 — In Postman importieren

1. In Postman **Import** → beide Dateien aus `postman/` auswählen.
2. Oben rechts das Environment **„clio local"** aktivieren.
3. Den Wert der Variable `token` auf dein API-Token setzen (Environment öffnen,
   `token` eintragen, speichern). `baseUrl` passt für eine lokale Instanz auf
   `:3000` bereits.

> **Alternative ohne die Repo-Dateien:** Postman → **Import** → die laufende
> Instanz `http://127.0.0.1:3000/openapi.yaml` angeben. Postman generiert dann
> die Endpunkte direkt aus der Spec (allerdings **ohne** die Test-Skripte).

Die Authentifizierung ist auf **Collection-Ebene** als *Bearer Token* mit
`{{token}}` hinterlegt — jeder Request erbt sie automatisch. Nur `ping`,
`/metrics` und die Negativ-Fälle setzen sie bewusst außer Kraft.

### Schritt 3 — Den ersten Request senden

Öffne **System → Ping (Liveness)** und klick **Send**. Du bekommst
`{"status":"ok"}` — die einzige Route ohne Auth.

Dann **Events → Write events** → **Send**. Der Request schreibt ein Event in den
Stream `{{smokeSubject}}` (Default `/smoke/books/42`). Die Antwort kommt als
**NDJSON**: ein JSON-Objekt pro Zeile, Content-Type `application/x-ndjson` —
kein JSON-Array. Postman zeigt sie im **Body**; das Test-Skript zieht die `id`
des geschriebenen Events heraus und legt sie in der Collection-Variable
`lastEventId` ab.

Mit **Events → Read events** liest du denselben Stream wieder — der Test prüft,
dass genau die eben geschriebene `id` zurückkommt. So hängen die Requests
bewusst zu einem kleinen End-to-End-Faden zusammen
(schreiben → lesen → `run-query` → `verify`).

### Schritt 4 — Tests lesen und im Runner ausführen

Jeder Request hat im Tab **Scripts → Post-response** (in älteren Postman-
Versionen: **Tests**) ein kleines `pm.test(...)`-Skript. Nach einem **Send**
siehst du das Ergebnis im Tab **Test Results** (z. B. *„200 OK"*, *„NDJSON
Content-Type"*, *„Kette intakt (ok=true)"*).

Statt einzeln zu klicken, kannst du die **ganze Collection auf einmal** prüfen:

- In Postman: Collection auswählen → **Run** (Collection Runner) → **Run clio**.
  Alle Requests laufen in Reihenfolge, du siehst pass/fail pro Test.

Das ist exakt derselbe Durchlauf, den die CI headless fährt.

### Schritt 5 — Headless: `make smoke` / Newman

Du brauchst Postman nicht, um die Collection laufen zu lassen — **Newman** (der
CLI-Runner von Postman) macht das im Terminal und in der CI:

```bash
make smoke
```

Das Target baut das Binary, startet eine **Wegwerf-Instanz** auf `:3999` (eigene
temporäre DB), wartet auf `ping`, lässt Newman die Collection laufen und fährt
den Server sauber wieder herunter. Es braucht nur `npx` — Newman wird bei Bedarf
geholt. Port und Token sind über `SMOKE_PORT` / `SMOKE_TOKEN` überschreibbar.

Genau dieser Schritt läuft auch in der **CI** bei jedem Pull Request (Job
`api smoke` in [`.github/workflows/ci.yml`](../../../.github/workflows/ci.yml)) —
so bleiben API und Doku verifiziert, ohne dass jemand manuell klicken muss.

> Nach Änderungen an der OpenAPI-Spec lässt sich das Gerüst der Collection mit
> `make postman-gen` neu erzeugen; die gepflegten `pm.test`-Skripte ergänzt man
> danach von Hand.

## Hands-on

1. Importiere Collection + Environment, setze dein `token` und sende
   **Write events**, dann **Read events** — beobachte, wie `lastEventId` von
   einem Request zum nächsten weitergereicht wird.
2. Lass die Collection einmal komplett im **Collection Runner** laufen.
3. Führe denselben Durchlauf headless mit `make smoke` aus und vergleiche die
   Test-Ergebnisse.

## Checkpoint

1. Warum funktionieren **Ping** und die **Negativ-Fälle** trotz Collection-
   weiter Bearer-Auth ohne bzw. ganz ohne gültiges Token?
2. Die Antwort von **Write/Read events** ist **kein** JSON-Array. Was ist sie,
   und woran erkennst du das am Response-Header?
3. Worin unterscheidet sich `make smoke` von einem Lauf im Postman Collection
   Runner — und warum kollidiert er **nicht** mit einer laufenden Dev-Instanz?

→ [Lösungen](../uebungen/loesungen.md#grundlagen-4)

---

**Weiter:** [Grundlagen 3 — Das Beispiel: Bibliothek](03-beispiel-bibliothek.md)
oder direkt in deinen [Rollen-Track](../README.md#wähle-deine-rolle) — als
Anwendungsentwickler:in geht es mit
[M01 — Erstes Event](../module/M01-erstes-event.md) weiter.
