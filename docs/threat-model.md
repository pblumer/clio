# Threat Model

> Ehrliche Bedrohungsbetrachtung für `cliostore`. Sie sagt, **wogegen clio
> schützt und wogegen nicht** — damit Betreiber bewusst entscheiden. Verwandt:
> [`security.md`](security.md) (Keys/Scopes), [`audit.md`](audit.md),
> [`backup-restore.md`](backup-restore.md), [`production-readiness.md`](production-readiness.md).

## Annahmen & Vertrauensgrenzen

- clio ist ein **Single-Binary, Single-Instance**-Dienst auf **einer** bbolt-Datei
  (ADR-001/002). Es gibt kein Clustering, kein Mandanten-Isolationsmodell.
- Vertraut wird dem **Host** (OS, Dateisystem, Prozessumgebung) und einem
  **kompromittierungsfreien Admin-Key**. Wer eines davon kontrolliert, kontrolliert
  die Daten — das ist die äußere Grenze.
- clio terminiert **kein TLS** selbst. Transportsicherheit liefert ein
  vorgelagerter Reverse Proxy.
- Die HTTP-API ist die einzige Online-Angriffsfläche; CLI/Backup laufen offline.

---

## Bedrohungen im Einzelnen

### 1. Token-/Key-Leak (`kid.secret` abgeflossen)
- **Risiko:** Wer einen gültigen `kid.secret` erlangt, handelt mit dessen Scope.
- **Schutz:** Keys sind **einzeln widerrufbar/rotierbar** (ADR-025), tragen
  minimale Scopes (least privilege), können **ablaufen** (`expiresAt`). Nur der
  SHA-256-Hash wird gespeichert; Vergleich zeitkonstant (kein Timing-Orakel).
  Jede Nutzung ist über den `kid` zuordenbar (slog-Authz-Log + Audit-Log).
- **Restrisiko / Pflicht des Betreibers:** **TLS erzwingen** (sonst reist
  `kid.secret` im Klartext). Bei Verdacht **rotieren/widerrufen**. Kurze
  `expiresAt` für CI/temporäre Zugänge.

### 2. Admin-Key-Kompromittierung
- **Risiko:** Ein `admin`-Key kann Keys verwalten, Backups ziehen (gesamte
  Historie!), im Dev-Mode zurücksetzen. Ein kompromittierter Admin ist innerhalb
  von clio die **höchste Eskalation**.
- **Schutz:** wenige, getrennte Admin-Keys; Auditor liest mit eigenem `audit`-Scope
  (least privilege); Admin-Aktionen landen im **persistenten Audit-Log** (ADR-031).
- **Restrisiko (ehrlich):** Das Audit-Log ist v1 **nicht kryptografisch
  fälschungssicher** — ein kompromittierter Admin (oder Host) kann es manipulieren
  (siehe [`audit.md`](audit.md) §4). clio schützt **nicht** gegen einen
  kompromittierten Admin/Host. Gegenmaßnahme: Audit-Log **off-host** exportieren
  (zentrales, append-only Log-Ziel), Admin-Keys offline/HSM-nah verwahren.

### 3. Replay-Angriffe (Wiedereinspielen alter Requests)
- **Risiko:** Ein abgefangener Write-Request wird erneut gesendet.
- **Schutz:** Writes sind über **Optimistic-Concurrency-Preconditions**
  absicherbar (`isSubjectOnEventId`, `isQueryResultEmpty/NonEmpty`, ADR-017):
  ein erneutes Anwenden schlägt mit **409** fehl, wenn der Stream weitergelaufen
  ist. Die **Hash-Kette** (ADR-012) macht jede nachträgliche Strom-Manipulation
  erkennbar.
- **Restrisiko:** clio kennt **keine** Request-Nonces/Idempotency-Keys auf
  HTTP-Ebene. Wer ohne Preconditions schreibt, kann durch Replay **Duplikate**
  erzeugen. Gegenmaßnahme: **Preconditions nutzen** und/oder fachliche Idempotenz
  (Consumer entduplizieren über `id`). TLS verhindert das Abfangen.

### 4. Payload-Leakage in Logs
- **Risiko:** Sensible Event-Daten landen in Logs/Fehlermeldungen.
- **Schutz:** clio loggt **keine Event-Payloads** und **keine Geheimnisse** —
  nur Metadaten (Methode, Pfad, Status, `kid`, Counts). Fehlertexte sind generisch.
- **Restrisiko / Pflicht des Betreibers:** Was du als **Event-Data** schreibst,
  liegt im Klartext in der DB und in Backups. **Keine Roh-Geheimnisse/PII
  unnötig** in `data` ablegen; bei Bedarf vor dem Schreiben verschlüsseln/maskieren.
  Reverse-Proxy-Access-Logs nicht versehentlich Bodies loggen lassen.

### 5. Breite Queries als DoS-Risiko
- **Risiko:** Ein `run-query`/Read über einen sehr breiten Scope hält eine lange
  Lesetransaktion offen und bindet Ressourcen (potenzieller Self-DoS / 502 am
  Ingress).
- **Schutz:** **`CLIO_QUERY_TIMEOUT`** begrenzt die Scan-Dauer; Reads streamen
  (konstanter Server-Speicher) und haben ein **Default-Limit** (10 000) ohne
  explizites `limit`. **Heartbeats** halten Proxy-Verbindungen offen statt sie
  ins Timeout laufen zu lassen (ADR-028). Typ-/Subject-/Daten-Indizes (ADR-021/
  023/029) vermeiden Voll-Scans für indizierte Prädikate.
- **Restrisiko:** Es gibt **kein Rate-Limiting/Quota** im Kern. In nicht
  vertrauenswürdigen Umgebungen Rate-Limiting im Reverse Proxy ergänzen und
  `CLIO_QUERY_TIMEOUT` **setzen** (Default aus!).

### 6. Dev-Mode-Risiko
- **Risiko:** `CLIO_DEV_MODE=true` schaltet **destruktive** Routen frei
  (`dev/reset-database` = Tabula rasa, Bulk-Import).
- **Schutz:** Default **aus**; die Routen werden ohne Dev-Mode **gar nicht
  registriert** (404 statt 401, Defense in Depth) und sind zusätzlich `admin`-
  scoped. Ein Reset wird **auditiert** und lässt Audit-Log/Keys intakt.
- **Restrisiko / Pflicht des Betreibers:** **Niemals** in Produktion aktivieren.

### 7. Backup-Diebstahl
- **Risiko:** Ein `.clio`-Backup enthält die **gesamte Historie** samt
  Schlüsselbund (Secret-**Hashes**, keine Klartexte).
- **Schutz:** Backups enthalten keine Klartext-Geheimnisse; die Hash-Kette macht
  Manipulation am Backup erkennbar (`verify`).
- **Restrisiko / Pflicht des Betreibers:** Ein Backup ist so vertraulich wie die
  Event-Daten. **At-rest verschlüsseln** (Volume-Encryption, GPG, Objektspeicher-
  SSE) und Zugriff/Transport absichern — clio verschlüsselt das Artefakt nicht.

### 8. Beschädigte DB
- **Risiko:** Bit-Rot, abgebrochener Schreibvorgang, defektes Dateisystem.
- **Schutz:** bbolt ist **ACID** (atomare Commits); clio schreibt serialisiert
  (ADR-003). Die **Hash-Kette** macht inhaltliche Beschädigung über `verify`
  nachweisbar; **Backups** stellen wieder her (`restore` + `verify`).
- **Restrisiko / Pflicht des Betreibers:** Regelmäßige Backups **und getestete
  Restores** (Drill). `CLIO_SYNC=off` opfert Crash-Durability für Durchsatz —
  nur bewusst einsetzen.

### 9. Voller Datenträger
- **Risiko:** Kein Platz → Writes scheitern, im schlimmsten Fall Korruption.
- **Schutz:** Der **Headroom-Monitor** warnt vor Annäherung an die vorbelegte
  Grenze (`CLIO_DB_INITIAL_MB` + `CLIO_DB_GROW_THRESHOLD_PCT`); `/api/v1/info`
  und Prometheus liefern Füllgrad/Disk-Free. Backup/Restore nutzen temp+Rename,
  hinterlassen also kein halbes Ziel.
- **Restrisiko / Pflicht des Betreibers:** **Disk-Headroom überwachen/alarmieren**
  (Compaction gibt freien Platz nur zurück, schafft keinen neuen). Genug Platz für
  Compaction (temporär ~Dateigröße) und Backups vorhalten.

### 10. Reverse-Proxy-/Timeout-Probleme
- **Risiko:** Ein puffernder/zu knapp getimter Proxy bricht lange Reads/Observe-
  Streams ab (502/504) oder hält die nie endende Observe-Antwort zurück.
- **Schutz:** clio sendet **Heartbeats** (Observe + langer Query) und ein
  **Anti-Buffering-Preamble** (`CLIO_OBSERVE_PREAMBLE_BYTES`), hebt die
  Schreib-Deadline für Streams bewusst auf. Server-Timeouts schützen den Header-
  Pfad (`ReadHeaderTimeout` 5 s gegen Slowloris) ohne Streams zu kappen.
- **Restrisiko / Pflicht des Betreibers:** **Proxy korrekt konfigurieren**:
  Buffering **aus**, großzügige `read/send`-Timeouts für `observe`/`run-query`
  (siehe [`production-readiness.md`](production-readiness.md)).

### 11. Signatur-/Hash-Vertrauensgrenzen
- **Was Hash/Signatur beweisen:** Die **Hash-Kette** (ADR-012) beweist
  **Integrität/Reihenfolge** — kein historisches Event wurde nachträglich
  verändert, ohne die Kette zu brechen (`verify` zeigt `brokenAt`). Mit
  `CLIO_SIGNING_KEY` (Ed25519, ADR-016) signiert clio jedes Event über seinen
  Hash → **Authentizität gegenüber dem Signierschlüssel**; der Public Key prüft
  unabhängig (`/api/v1/public-key`).
- **Was sie NICHT beweisen:** Der Signierschlüssel liegt **am Server** — er
  beweist „dieser clio-Server hat es geschrieben", **nicht** eine
  client-/end-to-end-Provenance. Wer den Signierschlüssel oder die DB
  kontrolliert, kann eine **konsistente alternative Kette** neu bilden
  (`verify` bliebe grün). Tamper-Evidence ist **Erkennung gegen nachträgliche
  Teil-Manipulation**, **kein** Schutz gegen einen kompromittierten Server.
- **Pflicht des Betreibers:** Signierschlüssel getrennt/sicher verwahren; den
  Public Key out-of-band verteilen; `verify` regelmäßig (auch auf Backups) laufen
  lassen und das Ergebnis off-host festhalten.

---

## Zusammenfassung: clios Schutzversprechen

| clio schützt gegen … | clio schützt NICHT gegen … |
|---|---|
| unprivilegierten API-Zugriff (Scopes, 401/403) | kompromittierten Host/Admin-Key |
| nachträgliche, unbemerkte Strom-Manipulation (Hash-Kette/`verify`) | E2E-Provenance jenseits des Server-Signierschlüssels |
| Geheimnis-Leaks in Logs/API (nie ausgegeben) | Klartext-PII, die du selbst in `data` schreibst |
| stillen Datenverlust beim Backup/Restore (atomar, `--force`) | Diebstahl/Vertraulichkeit unverschlüsselter Backups |
| versehentliche Last-Ausreißer (Query-Timeout, Limits, Heartbeats) | gezielten DoS ohne vorgelagertes Rate-Limiting |
| Strom-Verunreinigung durch Audit/Keys (eigene Buckets) | Audit-Manipulation durch Admin/Host (v1 nicht verkettet) |
