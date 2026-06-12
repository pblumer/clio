# Baut Clio (falls noetig) und startet es mit dem gesetzten $env:TOKEN.
# (Windows / PowerShell)
#
# Verwendung:
#   $env:TOKEN = 'dein-geheimes-token'
#   .\examples\bibliothek\00-start-server.ps1
. "$PSScriptRoot\..\_env.ps1"

# Repo-Wurzel relativ zu diesem Skript.
$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
Set-Location $root

$exe = Join-Path $root 'cliostore.exe'
if (-not (Test-Path $exe)) {
    Section "Baue cliostore.exe"
    go build -o cliostore.exe ./cmd/cliostore
}

Section "Starte cliostore"
Write-Host "Basis-URL : $($env:CLIO_BASE)"
Write-Host "DB        : $(if ($env:CLIO_DB_PATH) { $env:CLIO_DB_PATH } else { 'clio.db' })"
Write-Host "(Beenden mit Strg-C)"
$env:CLIO_API_TOKEN = $env:TOKEN
& $exe
