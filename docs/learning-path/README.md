# Clio Learning Path

Willkommen! Dieser Lernpfad bringt dir **cliostore** („Clio") bei — einen
eigenständigen Event Store in Go. Er ist **rollenbasiert**: Du startest bei der
Rolle, die zu dir passt, und folgst einer kuratierten Reihenfolge von Modulen.
Ein **durchgehendes Beispiel** (eine Bibliothek, später ein Bankkonto) zieht
sich als roter Faden durch alle Tracks.

> Dieser Pfad ist die **didaktische** Sicht auf Clio. Die **Referenz** bleibt
> [`README.md`](../../README.md) (Bedienung) und
> [`ARCHITECTURE.md`](../../ARCHITECTURE.md) (Architektur + ADRs). Die Module
> hier verlinken dorthin, statt Inhalte zu duplizieren.

---

## Wähle deine Rolle

| Rolle | Du willst… | Dein Track |
|---|---|---|
| 🧭 **Einsteiger:in** | verstehen, *was* Event Sourcing & Clio sind | [rollen/einsteiger.md](rollen/einsteiger.md) |
| 🛠️ **Anwendungsentwickler:in** | Apps gegen die Clio-API bauen | [rollen/anwendungsentwickler.md](rollen/anwendungsentwickler.md) |
| ⚙️ **Betrieb / DevOps / SRE** | Clio deployen, konfigurieren, überwachen | [rollen/betrieb.md](rollen/betrieb.md) |
| 🧩 **Contributor / Go-Dev** | die Codebasis erweitern | [rollen/contributor.md](rollen/contributor.md) |
| 📐 **Architekt:in / Entscheider:in** | Clio bewerten & einordnen | [rollen/architekt.md](rollen/architekt.md) |

Du bist unsicher? Beginne mit **Einsteiger:in** — die ersten beiden Module
(`Was ist Event Sourcing?` und `Quickstart`) sind für alle die Basis.

---

## Wie dieser Pfad aufgebaut ist

```
docs/learning-path/
  00-grundlagen/   ← Basiswissen für alle Rollen
  rollen/          ← je ein Track-Wegweiser pro Rolle (geordnete Modulliste)
  module/          ← die eigentlichen Lernbausteine M01…M11
  uebungen/        ← Musterlösungen zu den Checkpoints

examples/
  bibliothek/      ← lauffähige curl-Skripte (Hauptbeispiel)
  bankkonto/       ← fortgeschrittene Concurrency-/Invarianten-Beispiele
```

### Aufbau jedes Moduls

Jedes Modul in `module/` folgt demselben Muster, damit du dich nie neu
orientieren musst:

1. **Lernziele** — was du danach kannst
2. **Voraussetzungen** — welche Module/Begriffe du brauchst
3. **Inhalt** — die Erklärung, mit Links in die Referenz
4. **Hands-on** — etwas selbst tun (passende `examples/`-Skripte)
5. **Checkpoint** — Selbsttest; Lösungen in [`uebungen/loesungen.md`](uebungen/loesungen.md)

---

## Bevor du loslegst

Du brauchst **Go ≥ 1.24** und ein Terminal. Alle Hands-on-Teile arbeiten gegen
eine echte, lokal laufende Clio-Instanz. Der Schnellstart steht in
[`00-grundlagen/02-clio-quickstart.md`](00-grundlagen/02-clio-quickstart.md);
die lauffähigen Skripte liegen unter [`examples/`](../../examples/).

> **Windows wie Linux/macOS:** Jedes Beispiel gibt es als **`.sh`** (Bash/curl)
> und als **`.ps1`** (native PowerShell, 5.1 / 7+ — kein curl nötig). Quickstart
> und READMEs zeigen beide Wege nebeneinander.

> **Lieber grafisch?** [`00-grundlagen/04-postman-und-tests.md`](00-grundlagen/04-postman-und-tests.md)
> zeigt dieselbe API als klickbare **Postman**-Collection samt automatisiertem
> Smoke-Test (`make smoke` / Newman).

Viel Erfolg — und denk dran: In einem Event Store geht nichts verloren. 🕰️

---

## Modulübersicht (alle Rollen)

| Modul | Thema | Primär für |
|---|---|---|
| [M01](module/M01-erstes-event.md) | Erstes Event schreiben | Einsteiger, AppDev |
| [M02](module/M02-lesen-und-filtern.md) | Lesen & Filtern (recursive, bounds, types) | AppDev |
| [M03](module/M03-live-observe.md) | Live beobachten (observe-events) | AppDev |
| [M04](module/M04-optimistic-concurrency.md) | Optimistic Concurrency (Preconditions) | AppDev, Architekt |
| [M05](module/M05-schemas.md) | Event-Schemas (JSON Schema) | AppDev |
| [M06](module/M06-cel-queries.md) | Abfragen mit CEL (run-query) | AppDev |
| [M07](module/M07-integritaet-und-signaturen.md) | Integrität & Signaturen (verify) | AppDev, Betrieb, Architekt |
| [M08](module/M08-betrieb-und-durability.md) | Betrieb & Durability (Deploy, CLIO_SYNC, compact) | Betrieb |
| [M09](module/M09-observability.md) | Observability (Logs, /metrics) | Betrieb |
| [M10](module/M10-codebasis-tour.md) | Tour durch die Codebasis | Contributor |
| [M11](module/M11-feature-mit-adr.md) | Ein Feature mit Test & ADR beitragen | Contributor |
