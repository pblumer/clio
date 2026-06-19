#!/usr/bin/env bash
# /usr/local/bin/clio-backup.sh
#
# Hot-Backup des laufenden cliostore über den HTTP-Endpunkt (ADR-030), danach
# Offline-`verify` des frischen Artefakts und Rotation alter Backups. Aufgerufen
# von clio-backup.service; Konfiguration via Environment (siehe clio-backup.service).
set -euo pipefail

: "${CLIO_TOKEN:?CLIO_TOKEN (Scope admin) erforderlich}"
: "${CLIO_BASE:=http://127.0.0.1:3000}"
: "${BACKUP_DIR:=/var/backups/clio}"
: "${RETENTION_DAYS:=14}"

mkdir -p "$BACKUP_DIR"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
out="$BACKUP_DIR/clio-$stamp.clio"
tmp="$out.partial"

# 1) Konsistenten Snapshot streamen (in-Process, blockiert keine Schreiber).
curl -fsS --max-time 3600 -H "Authorization: Bearer $CLIO_TOKEN" \
  "$CLIO_BASE/api/v1/backup" -o "$tmp"
mv -f "$tmp" "$out"

# 2) Frisches Backup verifizieren (Hash-Kette). Exit 1 bricht den Service ab.
/usr/local/bin/cliostore verify --db "$out"

# 3) Alte Backups rotieren.
find "$BACKUP_DIR" -name 'clio-*.clio' -type f -mtime "+$RETENTION_DAYS" -delete

echo "backup ok: $out"
