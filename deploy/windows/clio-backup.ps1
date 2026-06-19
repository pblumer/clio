# clio-backup.ps1 — Hot-Backup des laufenden cliostore über den HTTP-Endpunkt
# (ADR-030), danach Offline-verify und Rotation. Per Aufgabenplanung (nächtlich)
# ausführen. Vollständige Windows-Anleitung: docs/windows-server-2022.md.
#
# Beispiel:
#   $env:CLIO_TOKEN = 'kid_xxx.secret'    # Scope admin
#   .\clio-backup.ps1 -BackupDir 'C:\clio\backup' -RetentionDays 14

param(
    [string]$Base = 'http://127.0.0.1:3000',
    [string]$BackupDir = 'C:\clio\backup',
    [string]$Exe = 'C:\clio\cliostore.exe',
    [int]$RetentionDays = 14
)
$ErrorActionPreference = 'Stop'

if (-not $env:CLIO_TOKEN) { throw 'CLIO_TOKEN (Scope admin) muss gesetzt sein' }
New-Item -ItemType Directory -Force -Path $BackupDir | Out-Null

$stamp = Get-Date -Format 'yyyyMMddTHHmmssZ'
$out = Join-Path $BackupDir "clio-$stamp.clio"

# 1) Konsistenten Snapshot streamen (blockiert keine Schreiber).
Invoke-WebRequest -Uri "$Base/api/v1/backup" `
    -Headers @{ Authorization = "Bearer $($env:CLIO_TOKEN)" } `
    -OutFile $out -TimeoutSec 3600

# 2) Verifizieren (ExitCode != 0 -> Abbruch).
& $Exe verify --db $out
if ($LASTEXITCODE -ne 0) { throw "verify fehlgeschlagen fuer $out" }

# 3) Rotation.
Get-ChildItem -Path $BackupDir -Filter 'clio-*.clio' |
    Where-Object { $_.LastWriteTime -lt (Get-Date).AddDays(-$RetentionDays) } |
    Remove-Item -Force

Write-Host "backup ok: $out"
