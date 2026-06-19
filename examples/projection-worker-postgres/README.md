# Projection Worker → PostgreSQL Read Model (Referenzbeispiel)

> **clio bekommt keine Projection Engine im Kern.** Dieses Beispiel ist die
> offizielle Vorlage, wie man ein **Read Model** sauber *außerhalb* von clio baut —
> nach den Prinzipien von Event Sourcing / CQRS.

Der Worker liest den clio-Event-Strom (Domäne **Orders**) und pflegt daraus eine
abgefragte PostgreSQL-Tabelle `orders`. Er zeigt die fünf Kernprinzipien:

| Prinzip | Wo im Code |
|---|---|
| **Event Store = Source of Truth**, Projektion = *abgeleitetes* Read Model | gesamte Architektur — Postgres hält nur abgeleiteten Zustand |
| **Live-Konsum über `observe`** (History + Live in einem Stream) | [`clio.go`](clio.go) `observe()` |
| **Checkpointing** (zuletzt verarbeitete globale Sequenz, persistent) | [`projection.go`](projection.go) `checkpoint()` / `projection_checkpoint`-Tabelle |
| **Idempotenz / exactly-once** (Checkpoint + Daten in *einer* Tx, Guard) | [`projection.go`](projection.go) `apply()` |
| **Replay / vollständiger Neuaufbau** | `--rebuild` → `rebuild()` ab Sequenz 0 |
| **Lag / Monitoring** | [`main.go`](main.go) `lagMonitor()` |

---

## Warum das funktioniert (das Wichtige in 3 Sätzen)

1. clios Events tragen eine **global monotone `id`** (Sequenz). Der Worker
   speichert die zuletzt verarbeitete `id` als **Checkpoint** und verbindet
   `observe` mit `lowerBound = checkpoint+1` — so bekommt er Catch-up **und** Live
   in einem Stream.
2. Jedes Event wird in **einer** Postgres-Transaktion angewendet, die **zugleich**
   den Checkpoint fortschreibt. Stürzt der Worker ab, liefert clio bei Reconnect
   ggf. Events erneut — der Guard (`id <= checkpoint → skip`) verwirft sie. Ergebnis:
   **exactly-once auf dem Read Model**.
3. Weil der Event-Strom **unveränderlich und vollständig** ist, lässt sich das
   Read Model jederzeit **per Replay neu aufbauen** (`--rebuild`) — z. B. nach
   einer Schema-/Mapping-Änderung.

---

## Schnellstart (Docker Compose, turnkey)

Voraussetzung: `docker compose`, `curl`, `jq`.

```bash
cd examples/projection-worker-postgres
./setup.sh
```

`setup.sh` startet clio + Postgres, leitet den Admin-Key aus dem clio-Startlog ab,
legt einen **dedizierten `read`-Key** an (least privilege → `.env`), schreibt
Demo-Orders und startet den Worker.

Danach:

```bash
# Worker-Logs (Lag/Read-Model-Statistik im 10-s-Takt)
docker compose logs -f worker

# Read Model abfragen
docker compose exec postgres psql -U clio -d clio_readmodel \
  -c 'select order_id, customer, status, carrier, tracking_id from orders order by order_id;'
```

Erwartetes Read Model:

| order_id | customer | status | carrier | tracking_id |
|---|---|---|---|---|
| o-1 | alice | shipped | DHL | TRK-001 |
| o-2 | bob | cancelled | | |
| o-3 | carol | placed | | |

Neue Events live sehen: weitere Orders schreiben und der Tabelle beim Wachsen zusehen:

```bash
CLIO_TOKEN="$(docker compose logs clio | grep -oE 'kid_[a-z2-7]+' | head -1).demo-bootstrap-secret-change-me" \
  ./seed-orders.sh   # (oder eigene write-events absetzen)
```

### Replay / Neuaufbau demonstrieren

```bash
docker compose run --rm worker -rebuild
```

Das verwirft die `orders`-Tabelle, setzt den Checkpoint auf 0 und baut das Read
Model **aus dem Event-Strom neu auf** — identisch zum vorherigen Stand. Genau das
macht ein abgeleitetes Read Model unkritisch: es ist jederzeit reproduzierbar.

Aufräumen: `docker compose --profile worker down -v`.

---

## Lokal ohne Docker

```bash
# 1. Postgres bereitstellen und DATABASE_URL setzen
export DATABASE_URL='postgres://clio:clio@127.0.0.1:5432/clio_readmodel?sslmode=disable'

# 2. clio starten (siehe Haupt-README) und einen read-Key anlegen
export CLIO_BASE='http://127.0.0.1:3000'
export CLIO_TOKEN='kid_xxxx.dein-read-secret'

# 3. Worker laufen lassen
go run .            # normaler Betrieb (Catch-up + Live)
go run . -rebuild   # Read Model ab Sequenz 0 neu aufbauen
```

`go test ./...` deckt die reinen Hilfsfunktionen ab (kein Postgres nötig).

---

## Konfiguration (Umgebungsvariablen)

| Variable | Default | Zweck |
|---|---|---|
| `CLIO_BASE` | `http://127.0.0.1:3000` | Basis-URL der clio-Instanz |
| `CLIO_TOKEN` | — (Pflicht) | clio-API-Key `kid.secret` mit **`read`**-Scope |
| `DATABASE_URL` | `postgres://clio:clio@127.0.0.1:5432/clio_readmodel?sslmode=disable` | Postgres-DSN (Read Model) |
| `PROJECTION_SUBJECT` | `/orders` | rekursiver Subject-Scope, den die Projektion konsumiert |

---

## Domäne & Mapping

Subjects `/orders/<id>`, Event-Typen → Read-Model-Wirkung:

| Event-Typ | `data` | Wirkung auf `orders` |
|---|---|---|
| `order.placed` | `{customer, totalCents}` | Zeile anlegen/aktualisieren, `status=placed` |
| `order.paid` | — | `status=paid` |
| `order.shipped` | `{carrier, trackingId}` | `status=shipped`, Versandfelder |
| `order.cancelled` | `{reason}` | `status=cancelled`, Grund |

Unbekannte Event-Typen werden **bewusst ignoriert** (vorwärtskompatibel — neue
Typen brechen die Projektion nicht).

---

## Bewusste Grenzen dieses Beispiels

- **Ein Worker, eine Projektion.** Kein verteiltes Consumer-Group-Modell (das wäre
  Kafka — und ausdrücklich kein clio-Ziel).
- **At-least-once-Transport + idempotente Anwendung = exactly-once-Effekt** auf dem
  Read Model. Seiteneffekte nach außen (E-Mails o. Ä.) wären zusätzlich zu
  entduplizieren.
- **Fehlerstrategie:** Bei Stream-Fehlern reconnectet der Worker mit Backoff ab dem
  Checkpoint. Ein *dauerhaft* fehlschlagendes Event (z. B. kaputte Payload) stoppt
  den Worker mit Fehler — bewusst „fail fast" statt es still zu überspringen.
