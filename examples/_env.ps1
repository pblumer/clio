# Gemeinsame Einstellungen für die PowerShell-Beispielskripte (Windows).
#
# Wird per Dot-Sourcing eingebunden:
#   . "$PSScriptRoot\..\_env.ps1"
#
# Erwartet:
#   $env:TOKEN      (Pflicht)  - API-Key im Format kid.secret (ADR-025), z. B.
#                                kid_ci01.W8xq... — der vollständige Wert, den
#                                `POST /api/v1/keys` einmalig ausliefert. Der Scope
#                                (read/write/admin) muss zur jeweiligen Route passen.
#   $env:CLIO_BASE  (optional) - Basis-URL, Default http://127.0.0.1:3000
#
# Kompatibel mit Windows PowerShell 5.1 und PowerShell 7+.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not $env:CLIO_BASE) { $env:CLIO_BASE = 'http://127.0.0.1:3000' }
$script:ClioBase = $env:CLIO_BASE

if (-not $env:TOKEN) {
    Write-Error "Bitte TOKEN setzen, z. B.:  `$env:TOKEN = 'kid_ci01.dein-geheimnis'  (API-Key im Format kid.secret; Scope muss zur Route passen)"
}
$script:ClioHeaders = @{ Authorization = "Bearer $($env:TOKEN)" }

# Überschrift ausgeben.
function Section([string]$Text) { Write-Host "`n=== $Text ===" }

# Einheitlicher HTTP-Aufruf gegen Clio.
# Gibt immer ein Objekt { Code = <int>; Body = <string> } zurück - auch bei
# 4xx/5xx (z. B. 409/400), die hier bewusst erwartet werden.
function Invoke-Clio {
    [CmdletBinding()]
    param(
        [string]$Method = 'Get',
        [Parameter(Mandatory)][string]$Path,
        [string]$Body
    )
    $uri = "$script:ClioBase$Path"
    $params = @{
        Method          = $Method
        Uri             = $uri
        Headers         = $script:ClioHeaders
        UseBasicParsing = $true
    }
    if ($Body) {
        $params.Body        = $Body
        $params.ContentType = 'application/json'
    }
    try {
        $resp = Invoke-WebRequest @params
        $content = $resp.Content
        # NDJSON wird je nach PowerShell-Version als Byte-Array geliefert -> nach UTF-8 dekodieren.
        if ($content -is [byte[]]) { $content = [System.Text.Encoding]::UTF8.GetString($content) }
        return [pscustomobject]@{ Code = [int]$resp.StatusCode; Body = [string]$content }
    }
    catch {
        # Bei HTTP-Fehlern Status und Body aus der Antwort ziehen
        # (PowerShell 7: ErrorDetails; Windows PowerShell 5.1: Response-Stream).
        $resp = $_.Exception.Response
        $code = 0
        $respBody = ''
        if ($resp) {
            try { $code = [int]$resp.StatusCode } catch { $code = 0 }
            if ($_.ErrorDetails -and $_.ErrorDetails.Message) {
                $respBody = $_.ErrorDetails.Message
            }
            elseif ($resp.PSObject.Methods.Name -contains 'GetResponseStream') {
                try {
                    $sr = New-Object System.IO.StreamReader($resp.GetResponseStream())
                    $respBody = $sr.ReadToEnd()
                    $sr.Close()
                } catch { }
            }
        }
        if ($code -eq 0) { throw }  # kein HTTP-Status -> echter Fehler (z. B. Server nicht erreichbar)
        return [pscustomobject]@{ Code = $code; Body = [string]$respBody }
    }
}

# Body bequem aus PowerShell-Objekten bauen (vermeidet JSON-Quoting).
function ConvertTo-ClioBody([Parameter(Mandatory)] $Object) {
    return ($Object | ConvertTo-Json -Depth 8 -Compress)
}
