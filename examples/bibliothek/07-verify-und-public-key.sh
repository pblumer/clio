#!/usr/bin/env bash
# M07 - Integritaet & Signaturen: Hash-Kette pruefen, public-key abrufen.
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

section "Integritaet der gesamten Hash-Kette pruefen"
curl -sS "${AUTH[@]}" "${CLIO_BASE}/api/v1/verify"
echo
echo "-> {\"ok\":true,...} bedeutet: Historie unveraendert."
echo "-> {\"ok\":false,\"brokenAt\":...} weist auf Manipulation/Korruption hin."

section "Oeffentlichen Ed25519-Schluessel abrufen (falls Signing aktiv)"
# Liefert nur einen Schluessel, wenn der Server mit CLIO_SIGNING_KEY laeuft.
# Schluesselpaar erzeugen:  ./cliostore gen-key
curl -sS "${AUTH[@]}" "${CLIO_BASE}/api/v1/public-key" || true
echo

echo
echo "Signing aktivieren: ./cliostore gen-key  ->  CLIO_SIGNING_KEY=<seed> beim Start setzen."
echo "Mit aktivem Schluessel prueft /verify auch die Signaturen mit."
