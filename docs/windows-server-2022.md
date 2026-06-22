# Clio auf Windows Server 2022 Standard betreiben

> Schritt-für-Schritt-Anleitung, um **cliostore** (Clio) auf einem **Windows
> Server 2022 Standard** zu installieren, zu starten, dauerhaft als Dienst laufen
> zu lassen und produktionsnah abzusichern. Voraussetzung: Du installierst mit
> einem Konto, das **lokale Administratorrechte** hat.

Clio ist ein **einzelnes, abhängigkeitsfreies Binary** (`cliostore.exe`). Es
braucht keine Runtime, keine Datenbank und keine externen Dienste — die Events
liegen in einer einzigen bbolt-Datei. Das macht den Windows-Betrieb angenehm
schlank: Datei hinlegen, als Dienst registrieren, Port freigeben, fertig.

Alle Pfade/Befehle sind für eine **PowerShell mit Administratorrechten**
formuliert (Start: Rechtsklick auf das Start-Menü → *Terminal (Administrator)*
bzw. *Windows PowerShell (Administrator)*).

---

## Inhalt

1. [Voraussetzungen](#1-voraussetzungen)
2. [Installation — Variante A: Fertiges Release-Binary (empfohlen)](#2-installation--variante-a-fertiges-release-binary-empfohlen)
3. [Installation — Variante B: Selbst aus dem Quellcode bauen](#3-installation--variante-b-selbst-aus-dem-quellcode-bauen)
4. [Erster Start (interaktiv testen)](#4-erster-start-interaktiv-testen)
5. [Konfiguration über Umgebungsvariablen](#5-konfiguration-über-umgebungsvariablen)
6. [Als Windows-Dienst betreiben](#6-als-windows-dienst-betreiben)
7. [Windows-Firewall freigeben](#7-windows-firewall-freigeben)
8. [HTTPS / Reverse Proxy (optional, empfohlen)](#8-https--reverse-proxy-optional-empfohlen)
9. [Datensicherung (Backup)](#9-datensicherung-backup)
10. [Wartung: compact, verify, gen-key](#10-wartung-compact-verify-gen-key)
11. [Updates einspielen](#11-updates-einspielen)
12. [Fehlersuche (Troubleshooting)](#12-fehlersuche-troubleshooting)

---

## 1. Voraussetzungen

- **Windows Server 2022 Standard** (Desktop Experience oder Core — beides geht,
  die Beispiele nutzen PowerShell).
- **Lokale Administratorrechte** (für Dienst-Registrierung, Firewall-Regel und
  systemweite Umgebungsvariablen).
- **Architektur amd64/x64** — die Release-Archive heißen `..._windows_amd64.zip`.
- **Ausgehender Internetzugriff** zum Herunterladen des Release-Archivs (oder du
  überträgst die ZIP per anderem Weg auf den Server).
- Optional für Variante B (Selbstbau): **Go ≥ 1.24**.

Lege vorab ein festes Verzeichnis für Programm und Daten an:

```powershell
New-Item -ItemType Directory -Force -Path 'C:\clio'      | Out-Null   # Binary
New-Item -ItemType Directory -Force -Path 'C:\clio\data' | Out-Null   # Datenbank
```

> **Tipp:** Halte **Programm** (`cliostore.exe`) und **Daten** (`clio.db`)
> getrennt. So kannst du das Binary bei Updates austauschen, ohne die Datenbank
> zu berühren, und das Datenverzeichnis gezielt sichern.

---

## 2. Installation — Variante A: Fertiges Release-Binary (empfohlen)

Für Windows gibt es bei jedem Release ein fertiges ZIP-Archiv inklusive
`cliostore.exe`, `LICENSE`, `README.md` sowie eine `checksums.txt` (SHA-256).

> Aktuelle Version siehe **<https://github.com/pblumer/clio/releases/latest>**.
> Setze `$Version` unten auf den passenden Tag (Beispiel: `v0.2.0`).

```powershell
$Version = 'v0.2.0'                          # gewünschte Version, siehe Releases-Seite
$Archive = "cliostore_${Version}_windows_amd64.zip"
$Base    = "https://github.com/pblumer/clio/releases/download/$Version"

Set-Location 'C:\clio'

# Archiv + Checksums laden (curl.exe statt des PowerShell-Alias, damit -O/-L wie erwartet wirken)
curl.exe -sSL -o $Archive       "$Base/$Archive"
curl.exe -sSL -o checksums.txt  "$Base/checksums.txt"
```

**Integrität prüfen** (Windows hat kein `sha256sum`, dafür `Get-FileHash`):

```powershell
$expected = (Select-String -Path checksums.txt -Pattern $Archive |
             Select-Object -First 1).Line.Split(' ')[0].Trim()
$actual   = (Get-FileHash $Archive -Algorithm SHA256).Hash.ToLower()

if ($actual -eq $expected) { Write-Host 'OK — Prüfsumme stimmt.' -ForegroundColor Green }
else { Write-Error "Prüfsumme falsch! erwartet=$expected ist=$actual" }
```

**Entpacken** und das Binary an seinen finalen Platz legen:

```powershell
Expand-Archive -Path $Archive -DestinationPath 'C:\clio\unpack' -Force
Copy-Item "C:\clio\unpack\cliostore_${Version}_windows_amd64\cliostore.exe" 'C:\clio\cliostore.exe' -Force
Remove-Item 'C:\clio\unpack' -Recurse -Force

# Funktioniert das Binary?
C:\clio\cliostore.exe -version          # -> cliostore v0.2.0
```

Weiter mit [Abschnitt 4 — Erster Start](#4-erster-start-interaktiv-testen).

---

## 3. Installation — Variante B: Selbst aus dem Quellcode bauen

Nur nötig, wenn du keine Releases nutzen willst/kannst (z. B. abgeschotteter Server
mit eigener Build-Pipeline) oder eine ungetaggte Version brauchst.

1. **Go installieren** (≥ 1.24) — z. B. per winget:

   ```powershell
   winget install --id GoLang.Go -e
   # Danach PowerShell neu öffnen, damit go im PATH ist:
   go version
   ```

2. **Quellcode holen und bauen:**

   ```powershell
   Set-Location 'C:\clio'
   git clone https://github.com/pblumer/clio.git src
   Set-Location 'C:\clio\src'

   # Statisches Single-Binary bauen (CGO aus, damit keine C-Toolchain nötig ist)
   $env:CGO_ENABLED = '0'
   go build -trimpath -ldflags '-s -w' -o C:\clio\cliostore.exe .\cmd\cliostore

   C:\clio\cliostore.exe -version
   ```

> Ohne Git: Quell-ZIP von GitHub (*Code → Download ZIP*) herunterladen,
> entpacken, in den Ordner wechseln und denselben `go build`-Befehl ausführen.

---

## 4. Erster Start (interaktiv testen)

Bevor wir einen Dienst einrichten, prüfen wir, dass alles läuft. Bei einer
**frischen Datenbank** muss beim ersten Start ein **initialer Admin-Key**
gebootet werden — dazu setzt du das Geheimnis `CLIO_BOOTSTRAP_ADMIN_KEY`. Der
Server erzeugt daraus einen Schlüssel und **loggt den `kid`** beim Start; der
vollständige API-Key auf der Leitung ist dann `kid.secret`.

```powershell
Set-Location 'C:\clio'

# Geheimnis + Datenpfad nur für DIESE Sitzung setzen
$env:CLIO_BOOTSTRAP_ADMIN_KEY = 'bitte-ein-starkes-geheimnis'
$env:CLIO_DB_PATH             = 'C:\clio\data\clio.db'

.\cliostore.exe
```

Im JSON-Log erscheint eine Zeile mit dem generierten `kid`, z. B.:

```json
{"level":"INFO","msg":"...","kid":"kid_ab12cd34"}
{"level":"INFO","msg":"cliostore lauscht","addr":":3000","version":"v0.1.0"}
```

Dein vollständiger Admin-Key ist damit **`kid_ab12cd34.bitte-ein-starkes-geheimnis`**.

In einer **zweiten** Administrator-PowerShell die Erreichbarkeit prüfen:

```powershell
curl.exe http://127.0.0.1:3000/api/v1/ping
# -> {"status":"ok"}
```

Browser auf dem Server: **<http://127.0.0.1:3000/ui>** (Dashboard, Bearer-Token
eingeben) bzw. **<http://127.0.0.1:3000/docs>** (Swagger UI).

Den Testlauf mit **Strg-C** beenden (Clio fährt sauber herunter). Den `kid`
notieren — du brauchst zusammen mit dem Geheimnis den Key `kid.secret` für jeden
API-Zugriff.

> **Wichtig zum Bootstrap:** `CLIO_BOOTSTRAP_ADMIN_KEY` wirkt **nur bei leerem
> Schlüsselbund** (frische DB). Ist bereits ein Schlüssel vorhanden, wird nichts
> gebootet — die Variable darf dann gesetzt bleiben, ohne Schaden anzurichten.
> Lege weitere Keys mit Scopes (`read`/`write`/`admin`) **zur Laufzeit** über die
> `admin`-geschützten `/api/v1/keys`-Routen oder den **Keys**-Tab im Dashboard an.

---

## 5. Konfiguration über Umgebungsvariablen

Clio wird ausschließlich über `CLIO_*`-Umgebungsvariablen konfiguriert. Die
wichtigsten:

| Variable | Default | Bedeutung |
|---|---|---|
| `CLIO_BOOTSTRAP_ADMIN_KEY` | — | Geheimnis für den initialen Admin-Key (nur bei leerer DB). **Als Secret behandeln.** |
| `CLIO_DB_PATH` | `clio.db` | Pfad zur Datenbankdatei. Auf Windows **absolut** setzen, z. B. `C:\clio\data\clio.db`. |
| `CLIO_ADDR` | `:3000` | Listen-Adresse. `:3000` = alle Interfaces; `127.0.0.1:3000` = nur lokal (empfohlen hinter Reverse Proxy). |
| `CLIO_SYNC` | `group` | Schreibstrategie: `group` (guter Durchsatz bei voller Durability), `always` (fsync pro Write), `off` (max. Durchsatz, kein fsync). |
| `CLIO_SIGNING_KEY` | — | base64-Ed25519-Schlüssel; aktiviert Event-Signaturen (siehe `gen-key`). |
| `CLIO_COMPRESS` | `false` | Transparente DEFLATE-Kompression neuer Events (`1`/`true`). |
| `CLIO_QUERY_TIMEOUT` | `0` (aus) | Max. Laufzeit einer `run-query`-Auswertung (Go-Dauer, z. B. `30s`, `2m`). |
| `CLIO_DEV_MODE` | `false` | **Nicht in Produktion** — schaltet destruktive Dev-Routen (DB-Reset) frei. |

> Die vollständige Tabelle (inkl. `CLIO_EVENT_AUTHORSHIP`,
> `CLIO_OBSERVE_PREAMBLE_BYTES`) steht im
> [README → Konfiguration](../README.md#konfiguration).

**Systemweit/persistente** Variablen lassen sich mit `setx /M` setzen (Maschinen-
Ebene, Adminrechte nötig). Für den Dienstbetrieb empfehlen wir aber, die
Variablen **direkt am Dienst** zu hinterlegen (siehe nächster Abschnitt) — dann
sind Secrets nicht für jeden Prozess auf dem Server sichtbar.

```powershell
# Beispiel: nicht-geheime Werte maschinenweit setzen (wirken nach Neustart der Shell/Dienste)
setx /M CLIO_DB_PATH 'C:\clio\data\clio.db'
setx /M CLIO_ADDR    '127.0.0.1:3000'
```

---

## 6. Als Windows-Dienst betreiben

Damit Clio **beim Systemstart automatisch** läuft, neu startet, falls es abstürzt,
und sauber gestoppt werden kann, registrierst du es als Windows-Dienst.

`cliostore.exe` ist eine normale Konsolen-Anwendung (kein nativer Windows-
Dienst). Sie reagiert auf das Stop-Signal (Strg-C / `WM_CLOSE`) mit **Graceful
Shutdown**. Der saubere Weg, so ein Programm als Dienst zu führen, ist ein
**Service-Wrapper**. Zwei bewährte Optionen:

### Variante 6a: NSSM (empfohlen — komfortabel, sauberer Stop)

[NSSM](https://nssm.cc/) („Non-Sucking Service Manager") startet das Binary,
hängt dessen Stdout/Stderr-Log in Dateien um, startet bei Absturz neu und sendet
beim Stoppen das korrekte Shutdown-Signal.

```powershell
# 1) NSSM beschaffen (z. B. via winget oder Download von https://nssm.cc/download)
winget install --id NSSM.NSSM -e          # alternativ: nssm.exe manuell nach C:\clio\nssm.exe legen

# 2) Dienst anlegen
nssm install Clio C:\clio\cliostore.exe
nssm set Clio AppDirectory C:\clio
nssm set Clio Description "cliostore — Event Store"
nssm set Clio Start SERVICE_AUTO_START

# 3) Konfiguration als Umgebungsvariablen am Dienst hinterlegen (ein Eintrag pro Zeile)
nssm set Clio AppEnvironmentExtra `
  CLIO_DB_PATH=C:\clio\data\clio.db `
  CLIO_ADDR=127.0.0.1:3000 `
  CLIO_SYNC=group `
  CLIO_BOOTSTRAP_ADMIN_KEY=bitte-ein-starkes-geheimnis

# 4) Logs in Dateien umlenken (NSSM rotiert auf Wunsch)
New-Item -ItemType Directory -Force -Path 'C:\clio\logs' | Out-Null
nssm set Clio AppStdout C:\clio\logs\clio.out.log
nssm set Clio AppStderr C:\clio\logs\clio.err.log
nssm set Clio AppRotateFiles 1
nssm set Clio AppRotateBytes 10485760      # 10 MB pro Datei

# 5) Starten
nssm start Clio
Get-Service Clio
```

Steuerung im Alltag:

```powershell
nssm restart Clio        # neu starten (z. B. nach Konfig-Änderung)
nssm stop Clio           # stoppen (Graceful Shutdown)
nssm edit Clio           # GUI-Editor für alle Einstellungen
nssm remove Clio confirm # Dienst wieder entfernen
```

> Nach Änderungen an `AppEnvironmentExtra` ist ein `nssm restart Clio` nötig,
> damit der Prozess die neuen Werte sieht.

### Variante 6b: Bordmittel ohne Zusatzsoftware

Kein NSSM erlaubt? Dann gibt es zwei Bordmittel-Wege:

**Aufgabenplanung „Beim Start"** (einfach, läuft im Hintergrund, aber kein
echtes Dienst-Lifecycle):

```powershell
$action  = New-ScheduledTaskAction -Execute 'C:\clio\cliostore.exe' -WorkingDirectory 'C:\clio'
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings  = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)

Register-ScheduledTask -TaskName 'Clio' -Action $action -Trigger $trigger `
  -Principal $principal -Settings $settings

# Umgebungsvariablen für SYSTEM bereitstellen (maschinenweit) und Aufgabe starten
setx /M CLIO_DB_PATH 'C:\clio\data\clio.db'
setx /M CLIO_ADDR    '127.0.0.1:3000'
# CLIO_BOOTSTRAP_ADMIN_KEY ebenso, falls die DB noch leer ist
Start-ScheduledTask -TaskName 'Clio'
```

**`sc.exe create`** registriert zwar einen Dienst, aber eine reine Konsolen-EXE
reagiert nicht auf die Service-Steuerbefehle des Windows-SCM (Stop/Start). Ohne
einen Wrapper wie NSSM wird der Dienst als „startet/stoppt nicht sauber" gemeldet.
**Deshalb ist NSSM (6a) der empfohlene Weg.**

---

## 7. Windows-Firewall freigeben

Standardmäßig blockt die Windows-Firewall eingehende Verbindungen. Nur freigeben,
wenn Clio **von anderen Rechnern** erreichbar sein soll (bei reinem Reverse-
Proxy-Betrieb auf demselben Host ist das nicht nötig — dann lauscht Clio nur auf
`127.0.0.1`).

```powershell
New-NetFirewallRule -DisplayName 'Clio (cliostore) TCP 3000' `
  -Direction Inbound -Action Allow -Protocol TCP -LocalPort 3000 `
  -Profile Domain,Private          # Public bewusst NICHT freigeben
```

> **Sicherheitshinweis:** `/metrics` und `/docs` sind **ohne Authentifizierung**
> erreichbar. Exponiere Port 3000 daher **nicht** ungeschützt ins offene Netz —
> entweder nur im vertrauenswürdigen LAN freigeben oder (besser) hinter einen
> Reverse Proxy mit TLS und Zugriffsschutz legen.

---

## 8. HTTPS / Reverse Proxy (optional, empfohlen)

Clio terminiert **kein TLS** selbst und ist als **Single-Instance** gedacht. Für
verschlüsselten Zugriff und sauberes Hostnamen-Routing stellst du einen Reverse
Proxy davor und lässt Clio nur lokal lauschen (`CLIO_ADDR=127.0.0.1:3000`).

Unter Windows Server bietet sich **IIS mit Application Request Routing (ARR) +
URL Rewrite** an:

1. IIS-Rolle installieren, dann **ARR** und **URL Rewrite** (Web Platform
   Installer oder direkter Download).
2. Im IIS-Manager *Application Request Routing* → *Server Proxy Settings* →
   **Enable proxy** aktivieren.
3. Eine Website mit Bindung auf **443** (TLS-Zertifikat hinterlegen) anlegen.
4. Eine **URL-Rewrite-Regel** anlegen, die alle Anfragen nach
   `http://127.0.0.1:3000/` weiterleitet.

> **Wichtig für Live-Streams:** Clios `observe`/`watch`-Endpunkte streamen über
> eine offen gehaltene HTTP-Verbindung. Stelle sicher, dass der Proxy **Response
> Buffering deaktiviert** (ARR: *Disable response buffering*) — sonst kommen
> Live-Events verzögert oder gar nicht an. Hilfsweise lässt sich serverseitig das
> Anti-Buffering-Polster über `CLIO_OBSERVE_PREAMBLE_BYTES` (z. B. `65536`)
> hochdrehen.

Alternativen, die unter Windows ebenfalls gut funktionieren: **Caddy** (Auto-
HTTPS, sehr einfach) oder **nginx für Windows**.

---

## 9. Datensicherung (Backup)

Der gesamte Zustand liegt in **einer Datei** (`CLIO_DB_PATH`, hier
`C:\clio\data\clio.db`). Clio bringt dafür echte Backup-Kommandos mit, statt nur
die Datei zu kopieren — das Ergebnis (`.clio`) ist ein **konsistenter,
hash-ketten-prüfbarer** Snapshot.

**Hot-Backup im laufenden Betrieb** (admin-scoped HTTP-Endpunkt, kein Stopp
nötig, blockiert keine Schreiber):

```powershell
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
New-Item -ItemType Directory -Force -Path 'C:\clio\backup' | Out-Null
$out = "C:\clio\backup\clio-$stamp.clio"

curl.exe -fsS -H "Authorization: Bearer $env:CLIO_TOKEN" `
  http://127.0.0.1:3000/api/v1/backup -o $out
C:\clio\cliostore.exe verify --db $out   # ExitCode 0 = Kette intakt
```

**Cold-Backup über die CLI** (Dienst gestoppt — das CLI-`backup` kann eine
laufende Instanz wegen des bbolt-Datei-Locks nicht öffnen):

```powershell
$env:CLIO_DB_PATH = 'C:\clio\data\clio.db'
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
nssm stop Clio
C:\clio\cliostore.exe backup --db 'C:\clio\data\clio.db' `
  --output "C:\clio\backup\clio-$stamp.clio" --verify
nssm start Clio
```

**Restore** (immer offline — Dienst stoppen, einspielen, prüfen, starten):

```powershell
nssm stop Clio
C:\clio\cliostore.exe restore --input 'C:\clio\backup\clio-20260618-030000.clio' `
  --db 'C:\clio\data\clio.db' --force
C:\clio\cliostore.exe verify --db 'C:\clio\data\clio.db'
nssm start Clio
```

Für **regelmäßige** Backups die Hot-Backup-Zeilen in ein `.ps1`-Skript packen und
per Aufgabenplanung (z. B. nächtlich) ausführen. Die `.clio`-Dateien anschließend
an einen anderen Ort (Netzwerkfreigabe, Objektspeicher) wegschaffen und dort
verschlüsseln (das Artefakt ist nicht verschlüsselt).

> Vollständige Anleitung mit Garantien, Fehlerfällen und RPO/RTO:
> [`docs/backup-restore.md`](./backup-restore.md). Architekturhintergrund:
> [`docs/backup-restore-dr-concept.md`](./plans/backup-restore-dr-concept.md).

---

## 10. Wartung: compact, verify, gen-key

`cliostore.exe` bringt ein paar Subkommandos mit. Sie arbeiten auf der Datei aus
`CLIO_DB_PATH` — vor `compact` daher dieselbe Variable setzen wie im Betrieb.

**Kompaktieren** (Defragmentierung der DB-Datei; löscht **keine** Events,
arbeitet offline — Dienst vorher stoppen):

```powershell
$env:CLIO_DB_PATH = 'C:\clio\data\clio.db'
nssm stop Clio
C:\clio\cliostore.exe compact
# -> kompaktiert: C:\clio\data\clio.db — <alt> -> <neu> bytes (xx.x% kleiner)
nssm start Clio
```

**Signaturschlüssel erzeugen** (optional, für Event-Signaturen). Gibt
`CLIO_SIGNING_KEY=...` und den zugehörigen Public Key aus:

```powershell
C:\clio\cliostore.exe gen-key
# CLIO_SIGNING_KEY=<base64-seed>
# # public key (zum Verifizieren): <base64-pub>
```

Den `CLIO_SIGNING_KEY`-Wert dann am Dienst hinterlegen (NSSM:
`AppEnvironmentExtra`) und den Public Key zum Verifizieren verteilen.

**Integrität prüfen** (Tamper-Evidence): online über die laufende Instanz
(`GET /api/v1/verify` bzw. der **Explorer**-Tab im Dashboard) **oder** offline auf
einer Datei/`.clio` — skriptbar über den Exit-Code:

```powershell
C:\clio\cliostore.exe verify --db 'C:\clio\data\clio.db'
# verify: OK — <n> events, head <hash>…      (ExitCode 0)
# verify: KETTE GEBROCHEN — <grund> ...        (ExitCode 1)
```

---

## 11. Updates einspielen

Da Clio ein einzelnes Binary ist, ist ein Update ein Datei-Austausch — die
Datenbank bleibt unberührt:

```powershell
# 1) Neues Release laden + Prüfsumme verifizieren (siehe Abschnitt 2)
# 2) Dienst stoppen, Binary tauschen, starten
nssm stop Clio
Copy-Item 'C:\clio\data\clio.db' "C:\clio\backup\clio-vor-update.db"   # Sicherheitskopie
Copy-Item '<pfad-zur-neuen>\cliostore.exe' 'C:\clio\cliostore.exe' -Force
C:\clio\cliostore.exe -version          # neue Version verifizieren
nssm start Clio
```

> Vor jedem Update ein Backup ziehen (Abschnitt 9). Das Datenformat ist
> abwärtskompatibel; bestehende Events bleiben lesbar.

---

## 12. Fehlersuche (Troubleshooting)

| Symptom | Ursache / Lösung |
|---|---|
| **Server startet nicht, „Auth-Material" fehlt** | Bei leerer DB muss `CLIO_BOOTSTRAP_ADMIN_KEY` (oder das deprecated `CLIO_API_TOKEN`) gesetzt sein. Am Dienst hinterlegen, neu starten. |
| **`ping` antwortet nicht** | Dienststatus prüfen: `Get-Service Clio`. Logs ansehen: `Get-Content C:\clio\logs\clio.err.log -Tail 50`. |
| **Port 3000 belegt** | `Get-NetTCPConnection -LocalPort 3000` zeigt den Prozess. Anderen Port wählen: `CLIO_ADDR=127.0.0.1:3001`. |
| **Von außen nicht erreichbar** | Lauscht Clio auf `127.0.0.1`? Für externen Zugriff `CLIO_ADDR=:3000` **und** Firewall-Regel (Abschnitt 7) setzen. |
| **401 bei API-Aufrufen** | Falscher/fehlender Bearer-Token. Format ist `kid.secret` — der `kid` steht im Start-Log, das `secret` ist dein `CLIO_BOOTSTRAP_ADMIN_KEY`. |
| **403 bei API-Aufrufen** | Token gültig, aber falscher Scope. Für Schreiben einen Key mit `write`-Scope anlegen. |
| **Live-Events kommen verzögert/nicht an** | Reverse Proxy puffert. Buffering abschalten und ggf. `CLIO_OBSERVE_PREAMBLE_BYTES` hochdrehen (Abschnitt 8). |
| **Welchen `kid` habe ich?** | Im Dienst-Log nach `"kid":"kid_..."` suchen: `Select-String -Path C:\clio\logs\clio.out.log -Pattern 'kid_'`. |

### Logs ansehen

```powershell
# Bei NSSM-Betrieb (umgeleitete Dateien)
Get-Content C:\clio\logs\clio.out.log -Tail 100 -Wait

# Dienststatus
Get-Service Clio | Format-List *
```

---

## Weiterführend

- [README → Konfiguration & API-Keys](../README.md#konfiguration)
- [Learning Path → Track Betrieb/DevOps](./learning-path/rollen/betrieb.md)
- [M08 — Betrieb & Durability](./learning-path/module/M08-betrieb-und-durability.md)
- [Backup-/Restore-/DR-Konzept](./plans/backup-restore-dr-concept.md)
- [ARCHITECTURE.md](../ARCHITECTURE.md) — alle Architekturentscheidungen (ADRs)
