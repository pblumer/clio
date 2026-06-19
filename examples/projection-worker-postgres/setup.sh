#!/usr/bin/env bash
# Turnkey-Setup der Projection-Worker-Demo:
#   1. clio + Postgres starten
#   2. Admin-Key aus dem clio-Log ableiten (kid + bekanntes Bootstrap-Secret)
#   3. einen dedizierten read-Key anlegen (least privilege) -> .env (CLIO_TOKEN)
#   4. Demo-Orders schreiben (seed)
#   5. Worker bauen und starten
#
# Voraussetzung: docker compose, curl, jq.
set -euo pipefail
cd "$(dirname "$0")"

BOOTSTRAP_SECRET="demo-bootstrap-secret-change-me"   # muss zu docker-compose.yml passen
BASE="http://127.0.0.1:3000"

command -v jq >/dev/null || { echo "jq wird benötigt"; exit 1; }

echo "== 1/5 clio + postgres starten =="
docker compose up -d clio postgres

echo "== 2/5 auf clio warten und Admin-kid aus dem Log lesen =="
for _ in $(seq 1 30); do
  curl -fsS "$BASE/api/v1/ping" >/dev/null 2>&1 && break
  sleep 1
done
KID=""
for _ in $(seq 1 15); do
  KID=$(docker compose logs clio 2>/dev/null | grep -oE 'kid_[a-z2-7]+' | head -1 || true)
  [ -n "$KID" ] && break
  sleep 1
done
if [ -z "$KID" ]; then
  echo "konnte den Admin-kid nicht aus dem clio-Log lesen" >&2
  exit 1
fi
ADMIN="$KID.$BOOTSTRAP_SECRET"
echo "   Admin-kid: $KID"

echo "== 3/5 dedizierten read-Key anlegen =="
READ_TOKEN=$(curl -fsS -X POST "$BASE/api/v1/keys" \
  -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"orders-projection","scopes":["read"],"owner":"demo","purpose":"read-model projection"}' \
  | jq -r '.secret')
if [ -z "$READ_TOKEN" ] || [ "$READ_TOKEN" = "null" ]; then
  echo "read-Key anlegen fehlgeschlagen" >&2
  exit 1
fi
echo "CLIO_TOKEN=$READ_TOKEN" > .env
echo "   read-Token nach .env geschrieben"

echo "== 4/5 Demo-Orders schreiben =="
CLIO_TOKEN="$ADMIN" ./seed-orders.sh   # Schreiben braucht write-Scope -> Admin

echo "== 5/5 Worker bauen und starten =="
docker compose --profile worker up -d --build worker

echo
echo "fertig. Logs des Workers:   docker compose logs -f worker"
echo "Read Model abfragen:        docker compose exec postgres psql -U clio -d clio_readmodel -c 'select * from orders order by order_id;'"
echo "Neu aufbauen (Replay):      docker compose run --rm worker -rebuild   (danach normal weiterlaufen lassen)"
