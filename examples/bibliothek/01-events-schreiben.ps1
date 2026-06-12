# M01 - Erstes Event schreiben: einzeln und atomar mehrere. (Windows / PowerShell)
. "$PSScriptRoot\..\_env.ps1"

Section "Ein einzelnes Event schreiben (/books/42 acquired)"
$body = ConvertTo-ClioBody @{
    events = @(
        @{ source = 'library'; subject = '/books/42'; type = 'acquired';
           data = @{ title = 'Dune'; author = 'Herbert' } }
    )
}
(Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Body

Section "Mehrere Events atomar (borrowed + returned in EINER Transaktion)"
$body = ConvertTo-ClioBody @{
    events = @(
        @{ source = 'library'; subject = '/books/42'; type = 'borrowed'; data = @{ member = 'm-7' } },
        @{ source = 'library'; subject = '/books/42'; type = 'returned'; data = @{ member = 'm-7' } }
    )
}
(Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Body

Section "Ein zweites Buch anlegen (/books/43)"
$body = ConvertTo-ClioBody @{
    events = @(
        @{ source = 'library'; subject = '/books/43'; type = 'acquired';
           data = @{ title = 'Foundation'; author = 'Asimov' } }
    )
}
(Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body).Body

Write-Host "`nFertig. Weiter mit 02-lesen-und-filtern.ps1"
