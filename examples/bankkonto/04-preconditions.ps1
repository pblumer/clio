# M04 - Optimistic Concurrency am Bankkonto:
#   Invariante 1: ein Konto wird nur einmal eroeffnet.
#   Invariante 2: kein verlorenes Update (optimistisches Sperren).
# (Windows / PowerShell)
. "$PSScriptRoot\..\_env.ps1"

# Eindeutiges Konto pro Lauf -> Demo ist wiederholbar.
$acc = "/accounts/demo-$([int][double]::Parse((Get-Date -UFormat %s)))"
Write-Host "Konto: $acc"

# ---------------------------------------------------------------------------
Section "Invariante 1 - Konto eroeffnen (isSubjectPristine) -> erwartet HTTP 200"
$body = ConvertTo-ClioBody @{
    events        = @(@{ source = 'bank'; subject = $acc; type = 'opened'; data = @{ owner = 'Ada' } })
    preconditions = @(@{ type = 'isSubjectPristine'; payload = @{ subject = $acc } })
}
Write-Host "HTTP $((Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Code)"

Section "Konto ein zweites Mal eroeffnen -> erwartet HTTP 409"
$body = ConvertTo-ClioBody @{
    events        = @(@{ source = 'bank'; subject = $acc; type = 'opened'; data = @{ owner = 'Mallory' } })
    preconditions = @(@{ type = 'isSubjectPristine'; payload = @{ subject = $acc } })
}
Write-Host "HTTP $((Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Code)"

Section "Einzahlung 100 (ohne Precondition) -> erwartet HTTP 200"
$body = ConvertTo-ClioBody @{
    events = @(@{ source = 'bank'; subject = $acc; type = 'deposited'; data = @{ amount = 100 } })
}
Write-Host "HTTP $((Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Code)"

# ---------------------------------------------------------------------------
# Invariante 2 - optimistisches Sperren.
Section "Stream lesen, letzte Event-ID ermitteln"
$ndjson = (Invoke-Clio -Method Post -Path '/api/v1/read-events' -Body (ConvertTo-ClioBody @{ subject = $acc })).Body
$lastId = $null
foreach ($line in ($ndjson -split "`n")) {
    if ($line.Trim()) { $lastId = ($line | ConvertFrom-Json).id }
}
Write-Host "letzte Event-ID: $lastId"

Section "Abhebung NUR, wenn letztes Event noch ID $lastId ist (isSubjectOnEventId) -> erwartet HTTP 200"
$body = ConvertTo-ClioBody @{
    events        = @(@{ source = 'bank'; subject = $acc; type = 'withdrawn'; data = @{ amount = 30 } })
    preconditions = @(@{ type = 'isSubjectOnEventId'; payload = @{ subject = $acc; eventId = "$lastId" } })
}
Write-Host "HTTP $((Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Code)"

Section "Zweite Abhebung mit der VERALTETEN ID $lastId -> erwartet HTTP 409"
Write-Host "(In der Zwischenzeit hat sich der Stream durch die erste Abhebung veraendert.)"
$body = ConvertTo-ClioBody @{
    events        = @(@{ source = 'bank'; subject = $acc; type = 'withdrawn'; data = @{ amount = 40 } })
    preconditions = @(@{ type = 'isSubjectOnEventId'; payload = @{ subject = $acc; eventId = "$lastId" } })
}
Write-Host "HTTP $((Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Code)"

Write-Host "`nLehre: bei 409 liest die App neu, prueft die Regel erneut und versucht es"
Write-Host "mit aktualisierter Precondition wieder (Retry-Schleife) - kein verlorenes Update."
