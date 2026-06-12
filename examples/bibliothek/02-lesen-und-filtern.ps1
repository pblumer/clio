# M02 - Lesen & Filtern: Einzel-Stream, recursive, Bounds, Typ-Filter, GET-Route.
# (Windows / PowerShell)
. "$PSScriptRoot\..\_env.ps1"

Section "Ein Stream lesen (/books/42)"
$body = ConvertTo-ClioBody @{ subject = '/books/42' }
(Invoke-Clio -Method Post -Path '/api/v1/read-events' -Body $body).Body

Section "Rekursiv: alle Buecher (/books, recursive)"
$body = ConvertTo-ClioBody @{ subject = '/books'; recursive = $true }
(Invoke-Clio -Method Post -Path '/api/v1/read-events' -Body $body).Body

Section "ID-Bereich (lowerBound/upperBound, beide inklusive)"
$body = ConvertTo-ClioBody @{ subject = '/books'; recursive = $true; lowerBound = '1'; upperBound = '3' }
(Invoke-Clio -Method Post -Path '/api/v1/read-events' -Body $body).Body

Section "Typ-Filter: nur borrowed/returned ueber alle Buecher"
$body = ConvertTo-ClioBody @{ subject = '/books'; recursive = $true; types = @('borrowed', 'returned') }
(Invoke-Clio -Method Post -Path '/api/v1/read-events' -Body $body).Body

Section "GET-Komfortroute (recursive default true): alles unter /books"
(Invoke-Clio -Method Get -Path '/api/v1/events/books').Body

Section "GET-Route mit Optionen: nur borrowed ab ID 1"
(Invoke-Clio -Method Get -Path '/api/v1/events/books?type=borrowed&lowerBound=1').Body

Write-Host "`nFertig. Weiter mit 03-observe.ps1"
