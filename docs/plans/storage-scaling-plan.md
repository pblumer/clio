# Implementierungsplan: Skalierbare Speicherverwaltung für bbolt

**Projekt:** `github.com/pblumer/clio`  
**Status:** UMGESETZT (Etappen 1–3) — siehe **§0.5** für die Korrekturen zum ursprünglichen Plan und **§6** für die Benchmark-Ergebnisse  
**Vorbild-Doku:** Temis-WP-Format (in sich geschlossene Work-Packages mit Akzeptanzkriterien)  
**Auslöser:** Benchmark vom 2026-06-18 zeigt Performance-Einbruch bei DB-Füllgrad > 85%

---

## 0. Zusammenfassung

`cliostore` nutzt heute bbolt (ADR-006) mit einer **1:1-Abbildung** zwischen Event-Strom und einer einzelnen bbolt-Datei. Ein Load-Test mit 1 Mio. Events auf einem VPS hat gezeigt: ab einem DB-Füllgrad von **~85 % bricht der Durchsatz von ~990 ev/s auf ~460 ev/s ein**, bei **~95 % ist praktischer Stillstand**
Das Problem ist nicht Plattenplatz (frei: 326 GB), sondern bbolts interne **4-KiB-Seitenverwaltung**: bei hohem Füllgrad entstehen massive Seiten-Splits und Rebalancing-Operationen im B+Tree. Jeder Write blockiert, weil bbolt das komplette rebalance im write lock durchführt.

Dieser Plan ersetzt die naive Datei-Größen-Annahme durch drei Maßnahmen:

1. **Konfigurierbare Initialgröße** — clio legt die bbolt-Datei mit einem konfigurierbaren `initialSize` an (z.B. 1 GiB, 2 GiB, 4 GiB).
2. **Auto-Up-Allocation** — clio überwacht den Füllgrad und vergrößert die Datei automatisch, *bevor* bbolt in den kritischen Bereich kommt.
3. **Hintergrund-Kompaktierung** — ein asynchroner `compact`-Goroutine wirft tote Seiten weg und defragmentiert die Datei, ohne den Write-Path zu blockieren.

Das Single-Binary-/Stdlib-Prinzip (ADR-001) bleibt erhalten: **keine neue externe Abhängigkeit**.

### Designentscheidungen, die alles Weitere prägen

| Entscheidung | Gewählt | Begründung |
|---|---|---|
| Speicherort | Weiterhin bbolt (kein Wechsel zu BadgerDB/Pebble) | ADR-006 bleibt gültig; Migration der DB aufwändig; bbolt erfüllt die Anforderungen, wenn richtig konfiguriert |
| Vergrößerung | **Pre-allocation** via `fallocate` / `truncate` | Kontrollierter als bbolts auto-grow (das fragmentiert); Datei ist von Anfang an groß |
| Scheduling | **Hintergrund-Goroutine** mit konfigurierbarem Intervall | Kein blocking im Request-Path; HTTP-API bleibt latenz-arm |
| Kompaktierung | `ReAttach + compact` Muster (neue Datei anlegen, alte ersetzen) | Bbolt erlaubt kein online-compact; wir nutzen den bestehenden `Compact()` in `store_compact.go` |
| Konfiguration | `CLIO_DB_INITIAL_MB` + `CLIO_DB_THRESHOLD_PCT` | Operator kann die Werte an VM-Plattengröße anpassen |

---

## 0.5 Umsetzungsstand & Korrekturen zum Plan

Die Umsetzung (Etappen 1–3) hat per Code-Verifikation (bbolt v1.4.3) und
Benchmark drei Plan-Annahmen widerlegt. Die folgenden Abschnitte 1–6 sind der
ursprüngliche Plan; hier steht, was davon **tatsächlich** gilt:

1. **`Options.InitialSize` gibt es in bbolt nicht** (§3.1, Option C). Der
   wirksame Hebel ist **`Options.InitialMmapSize`** — er dimensioniert die Mmap
   vorab. Wurzel des Durchsatz-Einbruchs ist nicht primär B+Tree-Rebalancing,
   sondern der **Mmap-Remap** in `db.allocate()`: überschreitet der Schreib-
   Highwater-Mark die Mmap-Grenze, ruft bbolt `db.mmap()` (munmap+mmap), nimmt
   dabei `mmaplock` exklusiv und **wartet auf das Freigeben aller
   Lese-Transaktionen** → Schreib-Latenzspitzen unter Leselast. Zusätzlich wird
   die Datei per `os.Truncate` real vorbelegt (sparse).

2. **Auto-Up-Allocation per `ftruncate` wirkt nicht wie geplant** (§3.3).
   `db.allocate()` recycelt freie Seiten **ausschließlich aus der bbolt-Freelist**;
   externes Vergrößern der Datei liefert bbolt keine wiederverwendbaren Seiten
   und senkt den (Freelist-basierten) Füllgrad nicht. Statt „Auto-Grow der Datei"
   ist der wirksame Ansatz die **vorab große Mmap** (Etappe 1) plus ein
   **Headroom-Monitor** (Etappe 2), der warnt, bevor der genutzte Umfang die
   vorbelegte Grenze erreicht. `CLIO_DB_AUTO_GROW`/`CLIO_DB_GROW_FACTOR` aus §4
   wurden **nicht** umgesetzt (wirkungslos).

3. **Online-Compaction ist keine reine `Compact()`-Erweiterung** (§3.4).
   `Compact()` braucht die Datei exklusiv. Online realisiert über einen **Reopen
   hinter einem `sync.RWMutex`** (Etappe 3): jeder DB-Zugriff hält RLock, der
   Reopen nimmt Lock exklusiv, wartet bis alle Transaktionen fertig sind, schließt
   → defragmentiert → öffnet neu. Kurze Downtime pro Lauf statt „online ohne
   Blockieren".

Zusätzlich behoben: `Stats()` las die Seitengröße live über `db.Info()`, das ohne
`mmaplock` auf den Mmap-Pointer zugreift und gegen remap-auslösende Writes racet
(über `/info` und `/metrics` unter Last erreichbar — vorbestehender latenter Bug).
Die Seitengröße wird jetzt beim Open/Reopen gecached.

### Tatsächlich implementierte Konfiguration

| Variable | Default | Etappe | Wirkung |
|---|---|---|---|
| `CLIO_DB_INITIAL_MB` | `0` (aus) | 1 | Vorab-Mmap (`InitialMmapSize`) + reale Dateivorbelegung; strikt grow-only |
| `CLIO_DB_MONITOR_INTERVAL` | `60s` | 2 | Intervall des Headroom-Monitors (`0` = aus) |
| `CLIO_DB_GROW_THRESHOLD_PCT` | `80` | 2 | Warn-Schwelle (% der vorbelegten Größe) |
| `CLIO_DB_COMPACT_ENABLED` | `false` | 3 | Online-Hintergrund-Kompaktierung |
| `CLIO_DB_COMPACT_INTERVAL_H` | `6` | 3 | Intervall der Hintergrund-Kompaktierung (Stunden) |

Neue Beobachtbarkeit: `/info` und die Prometheus-Metriken weisen jetzt
`DataBytes` (genutzter Umfang/Highwater-Mark) getrennt von `FileBytes` aus; der
Füllgrad bezieht sich auf den genutzten Umfang (sonst läse eine frisch
vorbelegte, leere DB ~100 %).

---

## 1. Analyse: Warum bricht bbolt bei 85 % ein?

### Beobachtete Daten (Benchmark 2026-06-18, Raspberry Pi → VPS)

| Phase | Events | DB-Füllgrad | Durchsatz | Latenz p95 |
|---|---|---|---|---|
| 1 | 59.000 | 31,1 % | 972 ev/s | 1.120 ms |
| 5 | 299.000 | 50,9 % | 990 ev/s | 608 ms |
| 10 | 579.000 | 75,2 % | 959 ev/s | 1.232 ms |
| 14 | 673.000 | 83,5 % | 792 ev/s | 534 ms |
| 16 | 692.000 | 85,1 % | 712 ev/s | 755 ms |
| 22 | 702.000 | 86,0 % | 464 ev/s | 316 ms |
| 28 | ca. 1.008.000 | 95,9 % | < 500 ev/s | — |

**Fazit:** Ab 85 % fill ist der Durchsatz halbiert; ab 95 % steht die DB.

### Root-Cause

bbolt nutzt ein **copy-on-write B+Tree** mit fixen 4-KiB-Seiten. Beim Einfügen eines neuen Schlüssels passiert folgendes:

1. Die Zielseite ist voll → **Seiten-Split** nötig.
2. Der Split propagiert nach oben → **Elternseite** muss neu geschrieben.
3. Diese Kaskade kann bis zur Root gehen → **mehrere hundert Seiten** müssen kopiert werden.
4. bbolt hält währenddessen den **write lock** auf der ganzen DB.

Bei **85 % Füllgrad** hat praktisch jede Seite nur noch Platz für 1-2 neue Einträge. Jeder Write triggert fast sicher einen Split.

---

## 2. Lösungsoptionen (evaluiert)

### Option A: BadgerDB (LSM-Tree)
- **Pro:** Kein Füllgrad-Problem; Append-only-Log; komprimiert on-disk
- **Con:** Neue Dependency; komplette Rewrite von `internal/store`; keine ACID-Transaktionen wie bbolt
- **Fazit:** Zuviel Aufwand für das aktuelle Problem. Nicht für WP.

### Option B: Pebble (RocksDB-Go)
- **Pro:** Google-maintained; LSM; schnell
- **Con:** Auch neue Dependency; API inkompatibel zu bbolts `Update()` / `View()`
- **Fazit:** Overkill. bbolt kann das Problem intern lösen.

### Option C: Pre-Allocation + Monitoring (empfohlen)
- **Pro:** Ohne neue Dependency; bbolts `Options.InitialSize` nutzen; eigene Logik für Schwellwert
- **Con:** Datei fällt nicht automatisch kleiner (nur durch `compact`)
- **Fazit:** Minimale Code-Änderung, maximale Wirkung.

---

## 3. Gewählter Lösungsweg

### 3.1 Konfigurierbare Initialgröße

Neue Env-Variable:
- `CLIO_DB_INITIAL_MB` (Default: `1024` = 1 GiB)

Vor dem ersten `bolt.Open` alloziiert clio die Datei mit `os.Truncate(path, size)` auf die gewünschte Größe.

### 3.2 Füllgrad-Monitoring im Server-Startup

Erweitere `databaseFileBytes` / `databaseFillPercent` / `databaseFreeBytes` aus `internal/store/store_compact.go`:
- Aktuell wird `FreeAlloc` berechnet → `FillPercent`.
- Der Wert wird alle 60 Sekunden aktualisiert (periodischer `view`-Scan auf Metadaten, nicht auf Event-Daten).

### 3.3 Auto-Up-Allocation (Hintergrund-Goroutine)

In `cmd/cliostore/main.go` oder `internal/store/store.go` startet der Server einen Goroutine mit folgender Logik:

```go
for {
    time.Sleep(60 * time.Second)
    stats := st.DBStats()
    if stats.FillPercent > threshold {  // z.B. 75 %
        newSize := currentSize * 1.5    // +50 %
        st.Grow(newSize)                // ftruncate + bbolt-Remap
        log.Info("DB auto-grown", "old_mb", oldSize/1024/1024, "new_mb", newSize/1024/1024)
    }
}
```

### 3.4 Hintergrund-Kompaktierung

Der existierende Code in `internal/store/store_compact.go` (`Compact()`) wird erweitert:
- **Trigger:** Cron-Goroutine (z.B. alle 6 Stunden) **ODER** bei Überschreitung von `CLIO_DB_COMPACT_THRESHOLD_PCT`.
- **Ablauf:**
  1. Online-Phase: `db.Stats()` prüft `FreeAlloc`.
  2. Wenn > threshold: Alte DB auf Read-Only setzen.
  3. Neue DB mit `Compact()` anlegen.
  4. Atomare Ersetzung: `db.Close()`, `os.Rename(tmp, main)`, `bolt.Open(...)`.
  5. Downtime: ca. 1-2 Sekunden für 1 GB (getestet).

---

## 4. Schnittstellenänderungen

### Neue / geänderte Env-Variablen

| Variable | Default | Beschreibung |
|---|---|---|
| `CLIO_DB_INITIAL_MB` | `1024` | Initiale Dateigröße in MiB |
| `CLIO_DB_AUTO_GROW` | `true` | Automatisches Vergrößern bei Schwellwert |
| `CLIO_DB_GROW_THRESHOLD_PCT` | `75` | Schwellwert für Auto-Grow |
| `CLIO_DB_GROW_FACTOR` | `1.5` | Multiplikator bei Vergrößerung |
| `CLIO_DB_COMPACT_ENABLED` | `false` | Hintergrund-Kompaktierung an/aus |
| `CLIO_DB_COMPACT_INTERVAL_H` | `6` | Intervall für Hintergrund-Compact |

### Neue interne Methoden

```go
// internal/store/store.go
func (s *Store) PreAllocate(mb int) error
func (s *Store) Grow(targetBytes int64) error
func (s *Store) DBStats() DBStats  // Erweiterung um FillPercent

// cmd/cliostore/main.go
func startBackgroundMaintenance(st *store.Store, cfg config.Config, logger *slog.Logger)
```

### Keine Änderungen an HTTP-API
- `GET /api/v1/info` liefert bereits `databaseFileBytes`, `databaseFillPercent`, `databaseFreeBytes`.
- Keine neuen Routen nötig.

---

## 5. Akzeptanzkriterien für den PR

1. **Konfiguration** — `CLIO_DB_INITIAL_MB` funktioniert: Supernova mit `CLIO_DB_INITIAL_MB=4096` legt eine 4-GB-Datei an.
2. **Auto-Grow** — Bei 75 % Füllgrad wächst die Datei automatisch um 50 % (getestet mit Load-Generator bis 1,5 Mio. Events).
3. **Performance** — Nach Auto-Grow bleibt der Durchsatz bei > 900 ev/s (gemessen gegen den alten Einbruch bei 85 %).
4. **Hintergrund-Compact** — `CLIO_DB_COMPACT_ENABLED=true` führt alle 6h einen Compact durch; die Datei schrumpft um mindestens 20 %.
5. **Backward-Compatibility** — Ohne `CLIO_DB_INITIAL_MB` verhält sich clio genau wie heute (1 GB Default).
6. **Tests** — Unit-Test für `PreAllocate` und `Grow`; Integrationstest mit gefüllter DB.

### 5.1 Reformuliert nach Umsetzung (verbindlich)

Die obige Liste stammt aus dem Plan; nach den Korrekturen (§0.5) gilt:

1. **Konfiguration ✅** — `CLIO_DB_INITIAL_MB=2048` dimensioniert die Mmap vorab
   und belegt die Datei real auf 2 GiB vor. (`os.Truncate` erzeugt eine
   **sparse** Datei — reale Blöcke erst beim Schreiben; echte Block-Reservierung
   gegen ENOSPC bliebe `fallocate`, bewusst zurückgestellt.)
2. **Headroom statt Auto-Grow ✅** — Der Monitor warnt ab
   `CLIO_DB_GROW_THRESHOLD_PCT` (Default 80 %) der vorbelegten Grenze. Automatisches
   Vergrößern per `ftruncate` entfällt (wirkungslos, §0.5).
3. **Performance ✅ (präzisiert)** — Pre-Sizing **eliminiert die Remap-bedingten
   Latenzspitzen** (Store-Benchmark §6: max 210 ms → 112 ms, p99 ~150 ms → ~70 ms)
   und hebt den Durchsatz ~25–35 %. Der **graduelle** Durchsatz-Abfall mit
   wachsender DB (B+Tree-Tiefe, Working-Set > RAM) ist inhärent und wird durch
   Mmap-Dimensionierung **nicht** beseitigt — die ursprüngliche pauschale
   „> 900 ev/s"-Forderung war zu grob.
4. **Online-Compaction ✅ (präzisiert)** — `CLIO_DB_COMPACT_ENABLED=true`
   defragmentiert periodisch im Betrieb (Reopen unter RWMutex). Die Rückgewinnung
   zeigt sich im gesunkenen **`DataBytes`**; bei vorbelegter Datei bleibt die
   **Dateigröße** per Design auf der reservierten Größe (Vorbelegung wird nach dem
   Compact wiederhergestellt). Die „Datei schrumpft ≥ 20 %"-Forderung gilt nur
   **ohne** Vorbelegung.
5. **Backward-Compatibility ✅** — Ohne `CLIO_DB_INITIAL_MB` (Default 0) verhält
   sich clio byte-identisch wie zuvor.
6. **Tests ✅** — Unit-Tests für Vorbelegung/Stats/Reopen/Compaction; der
   Nebenläufigkeitstest fährt Schreiben+Lesen parallel zu mehrfacher
   Online-Compaction unter `-race`. Die End-to-End-Validierung steht als
   On-Demand-Test bereit (§6).

---

## 6. Benchmark-Ergebnisse der Umsetzung

Zwei Messungen bestätigen den Mechanismus. fsync ist jeweils abgeschaltet, damit
der **einzige** Unterschied der Mmap-Remap ist; vier Leser erzeugen Kontention.

**Etappe 0 — Roh-bbolt-Spike** (isoliert den Remap; 6 Mio. Keys, → ~3,7 GB):

| Phase | Baseline ev/s | Presize ev/s | Baseline max-Latenz | Presize max-Latenz |
|---|---|---|---|---|
| 1 Mio | 113k | **194k** | 154 ms | 61 ms |
| 3,5 Mio | 73k | **108k** | **438 ms** ⚡ | 94 ms |
| 5 Mio | 60k | **83k** | **589 ms** ⚡ | 91 ms |

**Etappe 4 — durch die echte Store-API** (`Append` inkl. Hash-Kette + Indizes;
1,5 Mio. Events, presize = 2048 MiB):

| Phase | Baseline ev/s | Presize ev/s | Baseline max-Latenz | Presize max-Latenz | Datei |
|---|---|---|---|---|---|
| 250k | 18.064 | **22.888** | 133 ms | 82 ms | 292 MB → fix 2048 MB |
| 750k | 15.968 | **20.433** | **210 ms** ⚡ | 87 ms | 860 MB → fix 2048 MB |
| 1,5 Mio | 17.809 | **23.873** | 173 ms | 79 ms | 1710 MB → fix 2048 MB |

**Fazit:** Pre-Sizing entfernt die Remap-Latenzspitzen und stabilisiert den
Durchsatz (~25–35 % höher). Reproduktion:

```bash
CLIO_SCALING_BENCH=1 go test ./internal/store/ -run TestScalingValidation -v -timeout 30m
```

---

## 7. Appendix: Referenz-Werte für Benchmark-Replikation

Um den Fix zu validieren, soll der AI-Agent den folgenden Benchmark erneut durchführen:

```bash
# 1. Server mit 4 GB initial starten
CLIO_DB_INITIAL_MB=4096 ./cliostore

# 2. Load-Generator (vom Pi oder lokal)
python3 clio_benchmark.py --total 5_000_000 --rate 1000 --batch 1000

# 3. Erwartung: Kein Einbruch bei 1 Mio. Events; Durchsatz bleibt > 900 ev/s
```

**Erfolgsmetrik:** Die CSV darf keine Phase mit `FillPercent > 90 %` und gleichzeitigem Durchsatz < 800 ev/s enthalten.

---

**Vorbereitet für:** Claude Code (Anthropic) oder vergleichbaren AI-Coding-Agenten  
**Schätzung:** 1-2 Tage Implementierung + Tests  
**Abhängigkeiten:** Keine externen Dependencies
