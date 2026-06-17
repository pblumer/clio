#!/usr/bin/env bash
# Gemeinsame Einstellungen für alle Beispielskripte.
#
# Verwendung in einem Skript:
#   source "$(dirname "$0")/../_env.sh"
#
# Erwartet:
#   TOKEN       (Pflicht)  - API-Key im Format kid.secret (ADR-025), z. B.
#                            kid_ci01.W8xq... — der vollständige Wert, den
#                            `POST /api/v1/keys` einmalig ausliefert. Der Scope des
#                            Keys (read/write/admin) muss zur jeweiligen Route passen.
#   CLIO_BASE   (optional) - Basis-URL, Default http://127.0.0.1:3000
set -euo pipefail

: "${CLIO_BASE:=http://127.0.0.1:3000}"

if [[ -z "${TOKEN:-}" ]]; then
  echo "Fehler: bitte TOKEN setzen, z. B.:  export TOKEN=kid_ci01.dein-geheimnis" >&2
  echo "       (API-Key im Format kid.secret; Scope muss zur Route passen)" >&2
  exit 1
fi

# Wiederverwendbare curl-Bausteine.
AUTH=(-H "Authorization: Bearer ${TOKEN}")

# Kleiner Helfer: Überschrift ausgeben.
section() { printf '\n=== %s ===\n' "$*"; }
