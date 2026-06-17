# Baut Clio (falls noetig), bootet beim ersten Start einen Admin-Key und gibt den
# vollstaendigen API-Key kid.secret aus, den die uebrigen Beispielskripte als
# $env:TOKEN verwenden (ADR-025: der kid wird vom Server erzeugt, daher hier aus
# dem Start-Log gelesen). (Windows / PowerShell)
#
# Verwendung:
#   $env:BOOTSTRAP_SECRET = 'dein-geheimnis'   # optional, sonst Demo-Default
#   .\examples\bibliothek\00-start-server.ps1
#   # Danach in einer ZWEITEN Shell die ausgegebene Zeile ausfuehren:
#   #   $env:TOKEN = 'kid_xxxx.<secret>'
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
Set-Location $root

$secret = if ($env:BOOTSTRAP_SECRET) { $env:BOOTSTRAP_SECRET } else { 'demo-secret' }
$base   = if ($env:CLIO_BASE) { $env:CLIO_BASE } else { 'http://127.0.0.1:3000' }
$exe    = Join-Path $root 'cliostore.exe'
$log    = [System.IO.Path]::GetTempFileName()

if (-not (Test-Path $exe)) {
    Write-Host "`n=== Baue cliostore.exe ==="
    go build -o cliostore.exe ./cmd/cliostore
}

Write-Host "`n=== Starte cliostore ==="
Write-Host "Basis-URL : $base"
$env:CLIO_BOOTSTRAP_ADMIN_KEY = $secret
$proc = Start-Process -FilePath $exe -RedirectStandardOutput $log -RedirectStandardError "$log.err" -PassThru -NoNewWindow

try {
    # Auf Erreichbarkeit warten und den generierten kid aus dem Log lesen.
    $kid = $null
    for ($i = 0; $i -lt 50; $i++) {
        if ($proc.HasExited) { Get-Content $log, "$log.err" -ErrorAction SilentlyContinue; throw "Server vorzeitig beendet." }
        $m = Select-String -Path $log -Pattern '"kid":"(kid_[A-Za-z0-9]+)"' -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($m) { $kid = $m.Matches[0].Groups[1].Value }
        try { Invoke-WebRequest -UseBasicParsing "$base/api/v1/ping" -TimeoutSec 1 | Out-Null; if ($kid) { break } } catch {}
        Start-Sleep -Milliseconds 100
    }

    Get-Content $log -ErrorAction SilentlyContinue
    if ($kid) {
        Write-Host "`n=== API-Key bereit ==="
        Write-Host "Setze in einer ZWEITEN Shell fuer die uebrigen Skripte:"
        Write-Host "    `$env:TOKEN = '$kid.$secret'"
    }
    Write-Host "`n(Server laeuft. Beenden mit Strg-C)`n"
    Wait-Process -Id $proc.Id
}
finally {
    if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -ErrorAction SilentlyContinue }
    Remove-Item $log, "$log.err" -ErrorAction SilentlyContinue
}
