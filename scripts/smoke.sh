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
# SMOKE_TOKEN ist das Bootstrap-Geheimnis (der secret-Teil). Der vollstaendige
# Schluessel auf der Leitung ist kid.secret (ADR-025); der kid wird beim Start
# generiert und unten aus dem Server-Log gelesen.
TOKEN="${SMOKE_TOKEN:-smoke-token}"
BASE_URL="http://127.0.0.1:${PORT}"
BIN="./cliostore"
DB="$(mktemp -t clio-smoke-XXXXXX.db)"
LOG="$(mktemp -t clio-smoke-log-XXXXXX.txt)"
SRV_PID=""

cleanup() {
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true
  [ -n "$SRV_PID" ] && wait "$SRV_PID" 2>/dev/null || true
  rm -f "$DB" "$DB"* "$LOG" 2>/dev/null || true
}
trap cleanup EXIT

echo "==> Binary bauen"
go build -o "$BIN" ./cmd/cliostore

echo "==> Server starten auf :${PORT} (temp DB: ${DB})"
# Bei leerem Schluesselbund bootet CLIO_BOOTSTRAP_ADMIN_KEY einen Admin-Key; den
# generierten kid lesen wir gleich aus dem Log. Stdout/Stderr nach $LOG umleiten.
CLIO_BOOTSTRAP_ADMIN_KEY="$TOKEN" CLIO_ADDR=":${PORT}" CLIO_DB_PATH="$DB" "$BIN" >"$LOG" 2>&1 &
SRV_PID=$!

echo "==> Auf Erreichbarkeit warten"
for i in $(seq 1 50); do
  if curl -fsS "${BASE_URL}/api/v1/ping" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$SRV_PID" 2>/dev/null; then
    echo "Server ist vorzeitig beendet." >&2
    cat "$LOG" >&2 || true
    exit 1
  fi
  sleep 0.1
  if [ "$i" -eq 50 ]; then
    echo "Server wurde nicht rechtzeitig erreichbar." >&2
    cat "$LOG" >&2 || true
    exit 1
  fi
done

echo "==> Bootstrap-kid aus dem Server-Log lesen"
KID="$(grep -o '"kid":"kid_[A-Za-z0-9]*"' "$LOG" | head -1 | cut -d'"' -f4)"
if [ -z "$KID" ]; then
  echo "Konnte den Bootstrap-kid nicht aus dem Log lesen." >&2
  cat "$LOG" >&2 || true
  exit 1
fi
FULL_TOKEN="${KID}.${TOKEN}"
echo "==> Verwende API-Key ${KID}.<secret>"

echo "==> Newman ausfuehren"
npx --yes newman run postman/clio.postman_collection.json \
  --env-var "baseUrl=${BASE_URL}" \
  --env-var "token=${FULL_TOKEN}"
