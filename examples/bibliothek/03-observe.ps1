# M03 - Live beobachten: oeffnet einen Observe-Stream, schreibt parallel Events
# (per Hintergrund-Job) und zeigt, wie sie live eintreffen.
# (Windows / PowerShell; kompatibel mit 5.1 und 7+)
. "$PSScriptRoot\..\_env.ps1"

# System.Net.Http fuer Windows PowerShell 5.1 nachladen (in 7+ bereits vorhanden).
try { Add-Type -AssemblyName System.Net.Http -ErrorAction SilentlyContinue } catch { }

Section "Hintergrund-Job: schreibt nach 2s zwei neue Events nach /books/99"
$writer = Start-Job -ArgumentList $script:ClioBase, $env:TOKEN -ScriptBlock {
    param($base, $token)
    Start-Sleep -Seconds 2
    $headers = @{ Authorization = "Bearer $token" }
    foreach ($ev in @(
            @{ type = 'acquired'; data = @{ title = 'Live-Demo' } },
            @{ type = 'borrowed'; data = @{ member = 'm-1' } }
        )) {
        $body = @{ events = @(@{ source = 'library'; subject = '/books/99'; type = $ev.type; data = $ev.data }) } |
            ConvertTo-Json -Depth 8 -Compress
        Invoke-RestMethod -Method Post -Uri "$base/api/v1/write-events" -Headers $headers `
            -Body $body -ContentType 'application/json' | Out-Null
        Start-Sleep -Seconds 1
    }
}

Section "Observe /books (recursive): erst History, dann LIVE (~8s)"
$client = [System.Net.Http.HttpClient]::new()
$client.Timeout = [TimeSpan]::FromMinutes(5)
$reader = $null
try {
    $req = [System.Net.Http.HttpRequestMessage]::new('Post', "$($script:ClioBase)/api/v1/observe-events")
    $req.Headers.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $env:TOKEN)
    $payload = '{"subject":"/books","recursive":true}'
    $req.Content = [System.Net.Http.StringContent]::new($payload, [System.Text.Encoding]::UTF8, 'application/json')

    $resp = $client.SendAsync($req, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).Result
    $stream = $resp.Content.ReadAsStreamAsync().Result
    $reader = [System.IO.StreamReader]::new($stream)

    # Bis zum Deadline lesen. Wichtig: bei Timeout denselben ausstehenden Task
    # weiterlaufen lassen (nicht erneut ReadLineAsync aufrufen, sonst Stream-Konflikt).
    $deadline = (Get-Date).AddSeconds(8)
    $task = $null
    while ((Get-Date) -lt $deadline) {
        if ($null -eq $task) { $task = $reader.ReadLineAsync() }
        if ($task.Wait(500)) {
            $line = $task.Result
            $task = $null
            if ($null -eq $line) { break }   # Stream-Ende
            if ($line) { Write-Host $line }
        }
    }
}
finally {
    if ($null -ne $reader) { $reader.Dispose() }
    $client.Dispose()
    Stop-Job $writer -ErrorAction SilentlyContinue | Out-Null
    Remove-Job $writer -Force -ErrorAction SilentlyContinue | Out-Null
}

Write-Host "`nFertig. Reconnect-Idee: erneut mit lowerBound=<letzte gesehene ID> verbinden."
