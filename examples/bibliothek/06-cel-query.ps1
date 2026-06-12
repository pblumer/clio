# M06 - Abfragen mit CEL (run-query): Filter ueber event.type und event.data.
# (Windows / PowerShell)
. "$PSScriptRoot\..\_env.ps1"

Section "Alle Ausleihen (event.type == 'borrowed') ueber alle Buecher"
$body = ConvertTo-ClioBody @{
    subject = '/books'; recursive = $true
    where   = "event.type == 'borrowed'"
}
(Invoke-Clio -Method Post -Path '/api/v1/run-query' -Body $body).Body

Section "Ausleihen eines bestimmten Mitglieds (mit has(...)-Guard)"
$body = ConvertTo-ClioBody @{
    subject = '/books'; recursive = $true
    where   = "event.type == 'borrowed' && has(event.data.member) && event.data.member == 'm-7'"
}
(Invoke-Clio -Method Post -Path '/api/v1/run-query' -Body $body).Body

Section "Mit Limit (hoechstens 1 Treffer)"
$body = ConvertTo-ClioBody @{
    subject = '/books'; recursive = $true; limit = 1
    where   = "event.type == 'acquired'"
}
(Invoke-Clio -Method Post -Path '/api/v1/run-query' -Body $body).Body

Write-Host "`nhas(event.data.x) schuetzt vor fehlenden Feldern; ein Auswertungsfehler"
Write-Host "eines einzelnen Events gilt als 'kein Treffer' (Query bricht nicht ab)."
