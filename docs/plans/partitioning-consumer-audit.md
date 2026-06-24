# Konsumenten-Audit: Globale Total-Order vor Partitionierung (WP-0)

**Projekt:** `github.com/pblumer/clio`
**Bezug:** [ADR-034](../adr/0034-partitionierungsmodell-fuer-horizontale-skalierung.md) · Work-Package **WP-0** aus [`partitioning-plan.md`](./partitioning-plan.md)
**Status:** ABGESCHLOSSEN (Bestandsaufnahme 2026-06-24)
**Zweck:** ADR-034 gibt die **globale Total-Order** auf (Invariante INV-P1: nur noch per-Partition-Ordnung). Dieser Audit listet **jede** Stelle, die heute eine globale, monoton steigende Event-Sequenz/Ordnung annimmt, und klassifiziert sie. Er ist die Voraussetzung für WP-3 (Read-Path/Cursor) und liefert die Eingangsdaten für die Folge-ADR **ADR-035** (Tamper-Evidence unter Partitionierung).

---

## 0. Methode & Klassen

Durchsucht wurden Store-Kern, HTTP-API, Web-UI, Beispiele, Postman und Doku nach
`seq`/`sequence`/`NextSequence`/`eventNumber`/`lastEventId`/`lowerBound`/`upperBound`/
„global(e) Ordnung/Monotonie"/Hash-Kette. Drei Klassen:

| Klasse | Bedeutung | Folge |
|---|---|---|
| **BRICHT** | Nimmt echte globale Total-Order / **eine** Kette / **eine** Sequenzquelle an, die es nach Partitionierung nicht mehr gibt. | Architektur-Umbau (WP-2) |
| **BRAUCHT-CURSOR** | Nutzt einen **skalaren** globalen Cursor/Offset (`id`/`lowerBound`), der zu einem **per-Partition-Cursor-Vektor** werden muss. | Schnittstellen-/Client-Migration (WP-3) |
| **DOKU** | Rein beschreibend/lehrend; übersteht die Umstellung technisch, braucht aber eine **Formulierungs-Korrektur** („global" → „per Partition"). | Doku-Nachzug |

---

## 1. Kernbefund (verifiziert im Code)

Der Store führt **genau eine** globale Achse — bestätigt durch direkte Code-Lektüre:

- `internal/store/store.go:1246` — `seq, err := evts.NextSequence()` auf **einem**
  `bucketEvents`: eine zentrale, global monotone Sequenzquelle für **alle** Events.
- `internal/store/store.go:1259` — `ID: strconv.FormatUint(seq, 10)`: die Event-**ID
  ist** die globale Sequenz (numerisch, lückenlos ab 1).
- `internal/store/store.go:1267` — `PredecessorHash: head`: **ein** Ketten-Head über
  den gesamten Store (ADR-012).
- `internal/store/store.go:1373–1400` — `verifyChain` läuft `bucketEvents` per Cursor
  `First()→Next()` von `event.GenesisHash` linear durch: **eine** Kette, **eine**
  Reihenfolge.

> Konsequenz: ID-Vergabe, Hash-Kette und Read-Ordnung hängen alle an **derselben**
> globalen Sequenz. Partitionierung muss alle drei zugleich per-Partition machen —
> das ist der Kern von WP-2 und der Grund, warum die Bestands-Migration (eine Kette
> → n Ketten) eine eigene ADR braucht (ADR-035).

---

## 2. BRICHT — Architektur-Kern (Umbau in WP-2)

| Datei:Zeile | Befund | Anmerkung |
|---|---|---|
| `internal/store/store.go:1246` | `evts.NextSequence()` — globale Sequenzquelle | → `NextSequence()` pro Partitions-Bucket |
| `internal/store/store.go:1259` | Event-`ID` = globale Sequenz | IDs sind nicht mehr global vergleichbar/dicht; ID-Schema muss Partition tragen (z. B. `p<part>/<seq>`) → **berührt API & Clients**, s. §3 |
| `internal/store/store.go:1267` | `PredecessorHash: head` — **ein** globaler Ketten-Head | → ein Head **pro Partition** |
| `internal/store/store.go:1373–1400` | `verifyChain` prüft **eine** lineare Kette über `bucketEvents` | → Verify pro Partition; globaler Verify = Konjunktion. Übergeordnetes Anchoring = **ADR-035** |
| `internal/store/store.go` (`bucketEvents`, `bucketSubjectIdx`) | Events liegen global nach Sequenz; Subject-Index bildet Subject → globale Seqs ab | Read-Pfad sortiert/merged über die globale Sequenz |
| `internal/store/store.go:~1691` | `sort.Slice(seqs, … seqs[i] < seqs[j])` — rekursive Treffer nach **globaler** Sequenz geordnet | rekursiver Read über mehrere Subjects bewahrt heute globale Ordnung; nach Partitionierung nur noch per-Partition + dokumentierte Approx-Merge (INV-P1) |
| `internal/store/store.go:~1713–1740` | `scanEventsRecursive` nutzt globale Seq-Ordnung (`Seek(seqKey(LowerBound))`, `seq > UpperBound`) | Read-Grenzen sind global-numerisch → Vektor-Cursor |

---

## 3. BRAUCHT-CURSOR — Schnittstellen & Clients (Migration in WP-3)

| Datei:Zeile | Befund | Migration |
|---|---|---|
| `internal/httpapi/handlers.go:~934` | `parseBound()` liest `lowerBound`/`upperBound` als `uint64` | Cursor-Vektor `{partition: seq}` akzeptieren (skalar bleibt als Spezialfall `CLIO_PARTITIONS=1`) |
| `internal/httpapi/handlers.go:~1018` | `observe`-History: `ReadOptions{LowerBound: lower}` | dito; Resume per Vektor |
| `internal/httpapi/handlers.go:~1022,1045` | Dedup über `strconv.ParseUint(ev.ID)` + `id > lastID` | numerischer ID-Vergleich bricht bei partitionierten IDs → Dedup pro Partition |
| `internal/httpapi/handlers.go:82` | `/info` liefert `eventsTotal` (global `Count()`) | bleibt als Summe gültig, ist aber **kein** Cursor mehr → Clients dürfen `eventsTotal` nicht als „höchste ID" lesen |
| `internal/webui/static/js/dashboard.js:~540–544` | Kommentar + `estream.lastId = tot`: „höchste ID = Gesamtzahl" | Annahme bricht; Live-Cursor pro Partition |
| `internal/webui/static/js/dashboard.js:~516,569` | `id > estream.lastId`, `lower = lastId+1` | numerischer Cursor → Vektor |
| `internal/webui/static/js/dashboard.js:~1101,1139,1155` | Live-Tab analog (`live.lastId = tot`, `lowerBound=lastId+1`) | dito |
| `examples/projection-worker-postgres/projection.go:33,77,115` | `last_event_id BIGINT` Singleton-Checkpoint; Guard `id <= last` | **größter Einzel-Brecher**: Singleton-Checkpoint → Checkpoint **pro Partition**; Idempotenz pro Partition |
| `examples/projection-worker-postgres/main.go:83,129` | „verbinde ab sequenz cp+1"; `lag = total - cp` | Resume + Lag pro Partition; Lag-Skalar ungültig |
| `examples/projection-worker-postgres/clio.go:38` | „`lowerBound` … ab nächster unverarbeiteter Sequenz" | Vektor-Cursor |
| `examples/projection-worker-postgres/README.md:14,23` | beschreibt „globale Sequenz" als Cursor-Vertrag | Beispiel + Text auf per-Partition umstellen (oder explizit auf 1-Partition-Fall einschränken) |
| `postman/clio.postman_collection.json:15,91` | `lastEventId`-Variable, `set('lastEventId', first.id)` | Smoke-Test-Cursor pro Partition oder auf `CLIO_PARTITIONS=1` festnageln |
| `docs/learning-path/module/M03-live-observe.md:45,50` | „global monotone IDs sind dein **Cursor**", Reconnect `lowerBound:"42"` | Cursor-Lehrtext aktualisieren |
| `docs/learning-path/rollen/anwendungsentwickler.md:37` | „Event-IDs sind global monoton — nutze sie als Cursor" | dito |
| `docs/web-ui-scope.md:164` | „`lowerBound = höchste ID + 1` (`= eventsTotal + 1`)" | UI-Vertrag aktualisieren |

---

## 4. DOKU — nur Formulierungs-Nachzug (kein Code-Bruch)

| Datei:Zeile | Befund |
|---|---|
| `ARCHITECTURE.md:68,164` | „Event-ID — Global monoton steigende … Kennung", „Globale, monoton steigende Event-IDs (serialisiert)" — beschreibt Ist-Zustand (Stufe 1) |
| `ARCHITECTURE.md:176` | „rekursive Reads … bewahren so die **globale Ordnung**" — beschreibt heutiges Verhalten; nach Annahme von ADR-034 zu relativieren (INV-P1) |
| `docs/learning-path/00-grundlagen/01-was-ist-event-sourcing.md:50,78` | Lehrt „global monoton" als Eigenschaft/Quizfrage |
| `docs/learning-path/module/M01-erstes-event.md:32` | „id — global monoton steigend" |
| `docs/learning-path/module/M08-betrieb-und-durability.md:105` | „DB wächst monoton" — bleibt wahr, unkritisch |

> Hinweis: `ARCHITECTURE.md` ist „was gilt heute". Diese Stellen werden **erst beim
> Umsetzungs-Merge** (WP-2/WP-3) nachgezogen, nicht jetzt — ADR-034 ist noch
> *vorgeschlagen*.

---

## 5. Quantitative Zusammenfassung

- **BRICHT:** 7 Fundstellen, alle im **Store-Kern** (`internal/store/store.go`) —
  konzentriert, nicht verstreut. Das ist die gute Nachricht: der Bruch sitzt an
  **einer** Stelle (ID-Vergabe + Kette + Read-Ordnung an derselben globalen Sequenz).
- **BRAUCHT-CURSOR:** ~16 Fundstellen über API, Web-UI, Beispiel-Worker, Postman,
  Lehrtexte — durchweg dieselbe Ursache: **skalare globale ID als Cursor**.
- **DOKU:** ~6 Stellen, reine Formulierung.

**Kein** Treffer ist „unklar" — WP-0-Akzeptanzkriterium erfüllt.

---

## 6. Konsequenzen

**Für die Annahme von ADR-034.** Der Bruch ist im Kern **konzentriert**, nicht
diffus — das senkt das Umbaurisiko von WP-2. Aber **jeder externe Konsument-Vertrag
hängt an der globalen, dichten, numerisch vergleichbaren ID** (`lowerBound`,
`eventsTotal+1`, Singleton-Checkpoint). Globale Total-Order aufzugeben ist daher
**nicht** rein intern: es ist eine **Breaking Change am Cursor-Vertrag** der
observe/read-Schnittstelle und am ID-Format. Das stützt ADR-034 (ehrlich als
„folgenschwerste Einzelentscheidung" benannt) und macht WP-3 zur eigentlichen
Risiko-Etappe.

**Für WP-2/WP-3.**
- WP-2: ID-Schema muss die Partition tragen (z. B. `p<part>/<seq>`); rein
  numerische IDs sind nicht erhaltbar. `CLIO_PARTITIONS=1` muss das alte Format
  bit-genau reproduzieren (Regressionstest).
- WP-3: Cursor wird ein **Vektor** `{partition: seq}`; `eventsTotal` verliert die
  Doppelrolle „Zähler **und** Cursor". Der Projektions-Worker braucht
  per-Partition-Checkpoints + per-Partition-Idempotenz.

**Für ADR-035 (Tamper-Evidence unter Partitionierung).** Direkte Eingabe aus §1/§2:
- Heute **ein** `verifyChain` über **einen** `bucketEvents` von `GenesisHash` aus.
- Nach Partitionierung **n** Ketten mit je eigenem Genesis/Head → ADR-035 muss
  klären: (a) **übergeordnetes Anchoring** der n Heads (Merkle-Wurzel/periodischer
  Anker?), (b) wie ein **globaler Verify** als Konjunktion + Anker-Prüfung aussieht,
  (c) wie der **Bestand** (eine existierende Kette) verifizierbar in n Ketten
  überführt oder von einem Anker überspannt wird, ohne ADR-012/ADR-015 (Append-only)
  zu verletzen.
