# M05 - Event-Schemas: Schema registrieren, gueltiges und ungueltiges Event.
# (Windows / PowerShell)
. "$PSScriptRoot\..\_env.ps1"

Section "Schema fuer Typ 'acquired' registrieren (title Pflicht) -> erwartet HTTP 200"
# Hinweis: gelingt nur, wenn bereits gespeicherte 'acquired'-Events konform sind.
# Schemas sind unveraenderlich (erneute Registrierung -> 409).
$body = ConvertTo-ClioBody @{
    type   = 'acquired'
    schema = @{
        type       = 'object'
        required   = @('title')
        properties = @{
            title  = @{ type = 'string' }
            author = @{ type = 'string' }
        }
    }
}
$r = Invoke-Clio -Method Post -Path '/api/v1/register-event-schema' -Body $body
Write-Host "HTTP $($r.Code)"

Section "Gueltiges Event (hat title) -> erwartet HTTP 200"
$body = ConvertTo-ClioBody @{
    events = @(@{ source = 'library'; subject = '/books/77'; type = 'acquired'; data = @{ title = 'Snow Crash' } })
}
$r = Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body
Write-Host "HTTP $($r.Code)"

Section "Ungueltiges Event (title fehlt) -> erwartet HTTP 400"
$body = ConvertTo-ClioBody @{
    events = @(@{ source = 'library'; subject = '/books/78'; type = 'acquired'; data = @{ author = 'Stephenson' } })
}
$r = Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body
Write-Host "HTTP $($r.Code)"

Section "read-event-types zeigt hasSchema"
(Invoke-Clio -Method Get -Path '/api/v1/read-event-types').Body

Write-Host "`nSchema lesen: Invoke-Clio -Path '/api/v1/read-event-schema?type=acquired'"
