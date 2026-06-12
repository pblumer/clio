#!/usr/bin/env bash
# Gemeinsame Einstellungen für alle Beispielskripte.
#
# Verwendung in einem Skript:
#   source "$(dirname "$0")/../_env.sh"
#
# Erwartet:
#   TOKEN       (Pflicht)  - Bearer-Token, identisch zu CLIO_API_TOKEN des Servers
#   CLIO_BASE   (optional) - Basis-URL, Default http://127.0.0.1:3000
set -euo pipefail

: "${CLIO_BASE:=http://127.0.0.1:3000}"

if [[ -z "${TOKEN:-}" ]]; then
  echo "Fehler: bitte TOKEN setzen, z. B.:  export TOKEN=dein-geheimes-token" >&2
  echo "       (muss dem CLIO_API_TOKEN des laufenden Servers entsprechen)" >&2
  exit 1
fi

# Wiederverwendbare curl-Bausteine.
AUTH=(-H "Authorization: Bearer ${TOKEN}")

# Kleiner Helfer: Überschrift ausgeben.
section() { printf '\n=== %s ===\n' "$*"; }
