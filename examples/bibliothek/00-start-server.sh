#!/usr/bin/env bash
# Baut Clio (falls nötig), bootet beim ersten Start einen Admin-Key und gibt den
# vollständigen API-Key `kid.secret` aus, den die übrigen Beispielskripte als
# TOKEN verwenden (ADR-025: der `kid` wird vom Server erzeugt, daher hier aus dem
# Start-Log gelesen).
#
# Verwendung:
#   export BOOTSTRAP_SECRET=dein-geheimnis   # optional, sonst Demo-Default
#   examples/bibliothek/00-start-server.sh
#   # Danach in einer ZWEITEN Shell die ausgegebene Zeile ausführen:
#   #   export TOKEN=kid_xxxx.<secret>
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

SECRET="${BOOTSTRAP_SECRET:-demo-secret}"
: "${CLIO_BASE:=http://127.0.0.1:3000}"
DB="${CLIO_DB_PATH:-clio.db}"
LOG="$(mktemp -t clio-start-XXXXXX.txt)"
SRV_PID=""
TAIL_PID=""

cleanup() {
  [ -n "$TAIL_PID" ] && kill "$TAIL_PID" 2>/dev/null || true
  [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null || true
  [ -n "$SRV_PID" ] && wait "$SRV_PID" 2>/dev/null || true
  rm -f "$LOG" 2>/dev/null || true
}
trap cleanup EXIT

if [[ ! -x ./cliostore ]]; then
  printf '\n=== Baue cliostore ===\n'
  make build
fi

printf '\n=== Starte cliostore ===\n'
echo "Basis-URL : ${CLIO_BASE}"
echo "DB        : ${DB}"
CLIO_BOOTSTRAP_ADMIN_KEY="$SECRET" ./cliostore >"$LOG" 2>&1 &
SRV_PID=$!

# Auf Erreichbarkeit warten und den generierten kid aus dem Log lesen.
KID=""
for i in $(seq 1 50); do
  if ! kill -0 "$SRV_PID" 2>/dev/null; then
    echo "Server vorzeitig beendet:" >&2; cat "$LOG" >&2; exit 1
  fi
  KID="$(grep -o '"kid":"kid_[A-Za-z0-9]*"' "$LOG" | head -1 | cut -d'"' -f4 || true)"
  if [[ -n "$KID" ]] && curl -fsS "${CLIO_BASE}/api/v1/ping" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

cat "$LOG"
if [[ -n "$KID" ]]; then
  printf '\n=== API-Key bereit ===\n'
  echo "Setze in einer ZWEITEN Shell für die übrigen Skripte:"
  echo "    export TOKEN=${KID}.${SECRET}"
fi
printf '\n(Server läuft. Beenden mit Strg-C)\n\n'

# Server-Logs live durchreichen; Strg-C beendet tail, der EXIT-Trap den Server.
tail -f "$LOG" &
TAIL_PID=$!
wait "$SRV_PID"
