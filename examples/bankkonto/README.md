# Beispiele: Bankkonto 🏦

Lauffähige `curl`-Skripte für **fortgeschrittene Concurrency & Invarianten** —
das klassische Event-Sourcing-Lehrbeispiel. Domäne: Konten unter
`/accounts/...` mit `opened → deposited → withdrawn` (siehe
[Grundlagen 3](../../docs/learning-path/00-grundlagen/03-beispiel-bibliothek.md#domäne-2-bankkonto)).

Diese Beispiele gehören zu
[M04 — Optimistic Concurrency](../../docs/learning-path/module/M04-optimistic-concurrency.md).

Jedes Skript gibt es als **`.sh`** (Linux/macOS, Bash+curl) und **`.ps1`**
(Windows, PowerShell 5.1 / 7+).

## Voraussetzungen

**Linux/macOS (Bash):**
```bash
export TOKEN=kid_xxxx.demo-secret       # API-Key kid.secret (ADR-025), vom Start-Helfer ausgegeben
export CLIO_BASE=http://127.0.0.1:3000  # optional
```
**Windows (PowerShell):**
```powershell
$env:TOKEN = 'kid_xxxx.demo-secret'        # API-Key kid.secret (ADR-025), vom Start-Helfer ausgegeben
$env:CLIO_BASE = 'http://127.0.0.1:3000'   # optional
```

## Skripte

| Skript (`.sh` / `.ps1`) | Zeigt |
|---|---|
| `04-preconditions` | Beide Invarianten: „nur einmal eröffnen" + optimistisches Sperren (kein verlorenes Update) |

## Ausführen

**Linux/macOS:**
```bash
examples/bankkonto/04-preconditions.sh
```
**Windows:**
```powershell
.\examples\bankkonto\04-preconditions.ps1
```

> Das Skript nutzt ein eindeutiges Konto-Subject pro Lauf, ist also wiederholbar.
