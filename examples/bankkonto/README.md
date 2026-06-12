# Beispiele: Bankkonto 🏦

Lauffähige `curl`-Skripte für **fortgeschrittene Concurrency & Invarianten** —
das klassische Event-Sourcing-Lehrbeispiel. Domäne: Konten unter
`/accounts/...` mit `opened → deposited → withdrawn` (siehe
[Grundlagen 3](../../docs/learning-path/00-grundlagen/03-beispiel-bibliothek.md#domäne-2-bankkonto)).

Diese Beispiele gehören zu
[M04 — Optimistic Concurrency](../../docs/learning-path/module/M04-optimistic-concurrency.md).

## Voraussetzungen

```bash
export TOKEN=dein-geheimes-token        # = CLIO_API_TOKEN des Servers
export CLIO_BASE=http://127.0.0.1:3000  # optional
```

## Skripte

| Skript | Zeigt |
|---|---|
| `04-preconditions.sh` | Beide Invarianten: „nur einmal eröffnen" + optimistisches Sperren (kein verlorenes Update) |

## Ausführen

```bash
export TOKEN=dein-geheimes-token
examples/bankkonto/04-preconditions.sh
```

> Das Skript nutzt ein eindeutiges Konto-Subject pro Lauf, ist also wiederholbar.
