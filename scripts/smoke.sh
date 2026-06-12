#!/usr/bin/env bash
# smoke.sh — startet cliostore, fuehrt die Postman-Collection per Newman aus
# und faehrt den Server wieder herunter. Exit-Code = Newman-Ergebnis.
#
# Voraussetzungen: go, npx (Node). Newman wird bei Bedarf via npx geholt.
# Konfigurierbar ueber Umgebungsvariablen:
#   SMOKE_PORT   (Default 3999)  — Port, auf dem getestet wird
#   SMOKE_TOKEN  (Default smoke-token)
set -euo pipefail

cd "$(dirname "$0")/.."

PORT="${SMOKE_PORT:-3999}"
TOKEN="${SMOKE_TOKEN:-smoke-token}"
BASE_URL="http://127.0.0.1:${PORT}"
BIN="./cliostore"
DB="$(mktemp -t clio-smoke-XXXXXX.db)"
SRV_PID=""

cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true
  [ -n "$SRV_PID" ] && wait "$SRV_PID" 2>/dev/null || true
  rm -f "$DB" "$DB"* 2>/dev/null || true
}
trap cleanup EXIT

echo "==> Binary bauen"
go build -o "$BIN" ./cmd/cliostore

echo "==> Server starten auf :${PORT} (temp DB: ${DB})"
CLIO_API_TOKEN="$TOKEN" CLIO_ADDR=":${PORT}" CLIO_DB_PATH="$DB" "$BIN" &
SRV_PID=$!

echo "==> Auf Erreichbarkeit warten"
for i in $(seq 1 50); do
  if curl -fsS "${BASE_URL}/api/v1/ping" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$SRV_PID" 2>/dev/null; then
    echo "Server ist vorzeitig beendet." >&2
    exit 1
  fi
  sleep 0.1
  if [ "$i" -eq 50 ]; then
    echo "Server wurde nicht rechtzeitig erreichbar." >&2
    exit 1
  fi
done

echo "==> Newman ausfuehren"
npx --yes newman run postman/clio.postman_collection.json \
  --env-var "baseUrl=${BASE_URL}" \
  --env-var "token=${TOKEN}"
