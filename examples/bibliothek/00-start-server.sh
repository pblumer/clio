#!/usr/bin/env bash
# Baut Clio (falls nötig) und startet es mit dem gesetzten TOKEN.
# Praktisch für die Hands-on-Teile des Learning Path.
#
# Verwendung:
#   export TOKEN=dein-geheimes-token
#   examples/bibliothek/00-start-server.sh
set -euo pipefail
source "$(dirname "$0")/../_env.sh"

# Repo-Wurzel relativ zu diesem Skript.
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

if [[ ! -x ./cliostore ]]; then
  section "Baue cliostore"
  make build
fi

section "Starte cliostore"
echo "Basis-URL : ${CLIO_BASE}"
echo "DB        : ${CLIO_DB_PATH:-clio.db}"
echo "(Beenden mit Strg-C)"
exec env CLIO_API_TOKEN="${TOKEN}" ./cliostore
