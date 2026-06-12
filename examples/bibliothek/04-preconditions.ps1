# M04 - Optimistic Concurrency (Kurzvariante in der Bibliothek-Domaene).
# Ausfuehrliches Bankkonto-Beispiel: ..\bankkonto\04-preconditions.ps1
# (Windows / PowerShell)
. "$PSScriptRoot\..\_env.ps1"

# Eindeutiges Subject pro Lauf, damit die Demo wiederholbar ist.
$subj = "/books/precond-$([int][double]::Parse((Get-Date -UFormat %s)))"
Write-Host "Subject: $subj"

Section "isSubjectPristine: erstes acquired gelingt -> erwartet HTTP 200"
$body = ConvertTo-ClioBody @{
    events        = @(@{ source = 'library'; subject = $subj; type = 'acquired'; data = @{ title = 'Unikat' } })
    preconditions = @(@{ type = 'isSubjectPristine'; payload = @{ subject = $subj } })
}
$r = Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body
Write-Host "HTTP $($r.Code)"

Section "Zweites acquired mit derselben Precondition -> erwartet HTTP 409"
$body = ConvertTo-ClioBody @{
    events        = @(@{ source = 'library'; subject = $subj; type = 'acquired'; data = @{ title = 'Duplikat' } })
    preconditions = @(@{ type = 'isSubjectPristine'; payload = @{ subject = $subj } })
}
$r = Invoke-Clio -Method Post -Path '/api/v1/write-events' -Body $body
Write-Host "HTTP $($r.Code)"

Write-Host "`n409 = Konflikt: der Stream ist nicht mehr pristine, es wurde NICHTS geschrieben."
Write-Host "Mehr (optimistisches Sperren) in ..\bankkonto\04-preconditions.ps1"
