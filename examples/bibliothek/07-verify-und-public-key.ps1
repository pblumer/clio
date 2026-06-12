# M07 - Integritaet & Signaturen: Hash-Kette pruefen, public-key abrufen.
# (Windows / PowerShell)
. "$PSScriptRoot\..\_env.ps1"

Section "Integritaet der gesamten Hash-Kette pruefen"
(Invoke-Clio -Method Get -Path '/api/v1/verify').Body
Write-Host "-> {`"ok`":true,...} bedeutet: Historie unveraendert."
Write-Host "-> {`"ok`":false,`"brokenAt`":...} weist auf Manipulation/Korruption hin."

Section "Oeffentlichen Ed25519-Schluessel abrufen (falls Signing aktiv)"
# Liefert nur einen Schluessel, wenn der Server mit CLIO_SIGNING_KEY laeuft.
# Schluesselpaar erzeugen:  .\cliostore.exe gen-key
(Invoke-Clio -Method Get -Path '/api/v1/public-key').Body

Write-Host "`nSigning aktivieren: .\cliostore.exe gen-key  ->  `$env:CLIO_SIGNING_KEY=<seed> beim Start setzen."
Write-Host "Mit aktivem Schluessel prueft /verify auch die Signaturen mit."
