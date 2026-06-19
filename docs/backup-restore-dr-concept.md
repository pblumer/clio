# Backup, Restore & Disaster-Recovery — Konzept & Roadmap

> Begleitdokument zu [`ARCHITECTURE.md`](../ARCHITECTURE.md). Spezifiziert die
> Backup-/Restore-/DR-Story von `cliostore`. **Stufe 1** (Snapshot/Restore) ist
> das committete Zielbild und vollständig ausspezifiziert; **Stufe 2**
> (Continuous Archiving / PITR) ist eine bewusst abgegrenzte, optionale
> Ausbaustufe und nur skizziert. Die zugehörige Entscheidung ist **ADR-031**
> (siehe §6) und in `ARCHITECTURE.md` §7 übernommen. *(Stufe 1 ist umgesetzt;
> die Betriebsanleitung steht in [`backup-restore.md`](backup-restore.md).)*
>
> **Hinweis Nummerierung:** Dieses Dokument wurde verfasst, als die nächste freie
> ADR-Nummer 026 war; bei der Umsetzung war 026 bereits durch die Event-Herkunft
> belegt, daher trägt die Entscheidung final die Nummer **ADR-031**. Ältere
> „ADR-026"-Erwähnungen unten meinen dieselbe Entscheidung.

---

## 1. Worum geht es?

Ein Event Store ohne glasklare Backup-/Restore-Story ist nicht attraktiv —
Betrieb und Architektur müssen sehen, dass das System über Verlustszenarien
nachdenkt. Dieses Dokument legt fest, **welche DR-Lösungen für `cliostore`
überhaupt möglich sind**, welche wir bauen, und auf welchem Weg wir dorthin
kommen.

Die Leitfrage ist nicht „Snapshot oder Replikation?", sondern: *Wie viel
Datenverlust (RPO) und wie viel Ausfallzeit (RTO) ist tolerierbar, und welcher
Betriebsaufwand rechtfertigt das?* Daran hängt die Stufenwahl.

| Kennzahl | Bedeutung | Stufe 1 | Stufe 2 (PITR) |
|---|---|---|---|
| **RPO** (max. Datenverlust) | „Wie viele Events dürfen wir verlieren?" | = Backup-Intervall (z. B. 24 h) | ≈ 0 (= Archiv-Lag) |
| **RTO** (max. Ausfallzeit) | „Wie schnell sind wir wieder online?" | Minuten (eine Datei einspielen) | Minuten + Replay-Dauer |
| **Betriebsaufwand** | laufender Aufwand | ein getimter Job | Job **+** fortlaufender Archiv-Prozess |

---

## 2. Ausgangslage (was Clio uns geschenkt liefert)

`cliostore` ist (Stand heute, ADR-002) ein **Single-Node-Store auf einer
einzelnen bbolt-Datei**. Das ist für DR ein Vorteil, kein Nachteil — drei
vorhandene Eigenschaften machen die Backup-Story unüblich einfach:

1. **Konsistenter Online-Snapshot ist eingebaut.** bbolt erlaubt über
   `Tx.WriteTo` innerhalb einer Read-Transaktion (MVCC, copy-on-write) das
   Herausschreiben der gesamten Datei in einem konsistenten Punkt-in-der-Zeit-
   Zustand — ohne Schreiber zu blockieren, während der Server läuft. Wir bauen
   keinen Snapshot-Mechanismus, wir nutzen einen.

2. **Der Event-Strom *ist* das Änderungsprotokoll.** Wo Postgres für Continuous
   Archiving einen separaten, binären WAL bewirtschaftet, hat Clio die fachliche
   Wahrheit selbst: append-only Events mit global monoton steigender
   Sequenznummer (ADR-003/006). Ein „WAL-Archiv" ist hier schlicht „alle Events
   ab Sequenz N exportieren" — und die Leseschicht kann das bereits
   (`Read` mit `ReadOptions{LowerBound, UpperBound}`).

3. **Die Hash-Kette macht Backups selbstvalidierend.** Jedes Event ist über
   `predecessorHash` an seinen Vorgänger gebunden (ADR-012); `Store.Verify()`
   rechnet die ganze Kette nach. Ein wiederhergestellter Store oder ein
   archiviertes Event-Segment lässt sich damit **kryptografisch** auf
   Vollständigkeit und Unverändertheit prüfen — eine Eigenschaft, die ein
   Postgres-WAL nicht hat.

> **Abgrenzung zu ADR-015:** Kompaktierung defragmentiert die Datei offline,
> Backup erstellt eine Kopie online — verschiedene Operationen, gleiche
> bbolt-Bausteine (`temp-Datei + atomarer Rename`). Backup löscht und ändert
> nichts; die Kette bleibt grün.

---

## 3. Lösungsraum — welche DR-Ansätze sind möglich?

Vollständigkeitshalber das Spektrum, von „klein & sofort" bis „großer
Architektur-Sprung". Die ersten beiden sind Gegenstand dieses Dokuments; die
übrigen sind als Kontext und für die Abgrenzung dokumentiert.

### A) Konsistenter Snapshot + geprüfter Restore — *Stufe 1, committet*
Periodisches Online-Backup der ganzen DB in eine `.clio`-Datei, atomarer
Restore an einen Zielpfad, Hash-Ketten-Verify nach dem Restore. Deckt den
Totalverlust ab (Platte weg, Datei korrupt, Fehlbedienung). Vorbild: ein sauber
betriebener Single-Node-Dienst mit `pg_basebackup`-artigem Vollbackup, betrieben
über systemd-Timer / Cron / K8s-CronJob. **Das deckt ~90 % der Betriebsrealität
eines Single-Node-Event-Stores ab.**

### B) Continuous Archiving / Point-in-Time-Recovery — *Stufe 2, optional*
Base-Backup (= A) **plus** fortlaufendes Wegsichern aller neuen Events ab der
zuletzt archivierten Sequenz. Restore wird zweistufig: Base einspielen, dann
Events bis zu einem gewählten Zeit-/Sequenzpunkt nachspielen (`--until`).
Senkt das RPO von „Backup-Intervall" auf „nahe 0". Vorbild: Postgres WAL-
Archiving — bei Clio aber dramatisch einfacher, weil der Event-Strom der WAL ist
(§2.2) und Segmente über die Hash-Kette selbstvalidierend sind (§2.3).

### C) Replikation / Follower-Knoten — *außerhalb Scope, Kontext*
Ein zweiter Cliostore-Knoten liest den Strom live mit (Push über Observe-Stream
oder Pull über `run-query`-Cursor) und hält einen warmen Stand-by. Das ist
*Hochverfügbarkeit*, nicht Backup, und bricht mit ADR-002 (Single-Instance):
Es bräuchte Follower-Modus, Failover-Logik und ein Konzept gegen Split-Brain.
**Bewusst zurückgestellt** — der Kafka-artige Cluster ist ein eigenes,
großes Vorhaben und kein DR-Thema im engeren Sinn.

### D) Tiered / Cold Storage auf Objektspeicher — *außerhalb Scope, Kontext*
Auslagern alter Event-Segmente auf S3-kompatiblen Speicher (analog Kafka
KIP-405). Das ist primär Kosten-/Kapazitätsoptimierung und überschneidet sich
mit dem in ADR-015 zurückgestellten „Segmentierung/Cold Storage". Die Stufe-2-
Archiv-Sink (B) ist die natürliche Andockstelle dafür, falls es je gebraucht
wird.

---

## 4. Stufe 1 — Snapshot/Restore (committetes Zielbild, voll spezifiziert)

### 4.1 Kommando-Oberfläche

Drei Offline-/Online-Kommandos, konsistent zum bestehenden CLI-Stil
(`compact`, `gen-key`):

```
cliostore backup  --output backup-2026-06-17.clio [--db clio.db] [--json]
cliostore restore --input  backup-2026-06-17.clio --db /data/clio.db [--force] [--json]
cliostore verify  --db /data/clio.db [--json]
```

- **`backup`** — konsistenter Online-Snapshot. Läuft gegen eine *laufende*
  Instanz (bbolt erlaubt gleichzeitige Reader); blockiert keine Schreiber. Das
  `.clio`-Artefakt ist selbst eine gültige, eigenständig öffenbare bbolt-Datei.
- **`restore`** — spielt einen Snapshot an einen Zielpfad ein. Verweigert das
  Überschreiben einer existierenden DB ohne `--force`. **Offline auszuführen**
  (keine Instanz auf `--db`).
- **`verify`** — rechnet die Hash-Kette einer DB nach (Tamper-Evidence,
  ADR-012). Exit-Code `0` bei intakter Kette, `1` bei Bruch — skriptbar für
  Backup-Audits und CI.

Spiegelung in die HTTP-API (read-only, admin-scoped): `GET /api/v1/backup`
streamt den Snapshot als `application/octet-stream`; `GET /api/v1/verify`
existiert bereits. Restore bleibt bewusst CLI-only (zustandsverändernd, offline).

### 4.2 Datenfluss

```
backup:   laufende DB ──(View-Tx, WriteTo)──▶ temp ──(fsync, Rename)──▶ snapshot.clio
restore:  snapshot.clio ──(ReadOnly open, validate)──▶ Compact-Kopie ──(fsync, Rename)──▶ /data/clio.db
verify:   /data/clio.db ──(ReadOnly open)──▶ Hash-Kette nachrechnen ──▶ ok | brokenAt
```

### 4.3 Invarianten (verbindlich)

- **INV-B1 — Konsistenz:** Ein Backup ist ein Punkt-in-der-Zeit-Snapshot. Alle
  zu Transaktionsbeginn committeten Events sind enthalten, spätere nicht. Der im
  Ergebnis gemeldete `head` und `events`-Count stammen aus **derselben**
  Read-Transaktion wie der Snapshot.
- **INV-B2 — Atomarität des Artefakts:** Unter dem Zielnamen existiert nie ein
  halb geschriebenes Backup. Geschrieben wird in eine temp-Datei im selben
  Verzeichnis, dann `fsync`, dann atomarer `Rename`.
- **INV-B3 — Selbstvalidierung:** Jedes `.clio` ist als eigenständige DB
  öffenbar und besteht `verify` (Hash-Kette intakt, gespeicherter Ketten-Kopf
  passt zum letzten Event).
- **INV-R1 — Kein stiller Verlust:** `restore` überschreibt eine existierende
  Ziel-DB nur mit `--force`. Ohne `--force` → Fehler `ErrTargetExists`.
- **INV-R2 — Identität nach Restore:** Nach `restore` + `verify` gilt
  `head == head_des_backups` und `count == count_des_backups`. Ein
  Event-für-Event-Replay liefert bit-identische Hashes wie das Original.
- **INV-R3 — Nahtlose Fortsetzung:** Nach einem Restore schließt der nächste
  `Append` lückenlos an die wiederhergestellte Kette an (`verify` bleibt grün,
  Count = vorher + 1).
- **INV-V1 — Read-only:** `verify` (und der ReadOnly-Open für `restore`-
  Validierung) verändert die geprüfte Datei nicht (keine Bucket-Anlage, keine
  Backfills).

### 4.4 Betriebsmodell (das „wie Kafka via systemd"-Bild)

Ein sauber betriebener Single-Node-Dienst: der Server läuft als systemd-Unit,
ein **getimter** Job zieht periodisch ein Backup, rotiert alte Artefakte und
prüft das frische. Beispiele für die drei gängigen Umgebungen:

**a) systemd (Service + Timer)** — Server als Dienst, Backup als getriggerte
Unit:

```ini
# /etc/systemd/system/cliostore.service
[Unit]
Description=cliostore event store
After=network-online.target
[Service]
ExecStart=/usr/local/bin/cliostore
Environment=CLIO_DB_PATH=/var/lib/clio/clio.db
Environment=CLIO_BOOTSTRAP_ADMIN_KEY=…   # nach erstem Start entfernen (ADR-025)
Restart=on-failure
StateDirectory=clio
[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/clio-backup.service  (Type=oneshot)
[Service]
Type=oneshot
ExecStart=/usr/local/bin/cliostore backup \
  --db /var/lib/clio/clio.db \
  --output /var/backups/clio/clio-%i.clio
ExecStartPost=/usr/local/bin/cliostore verify --db /var/backups/clio/clio-%i.clio
```

```ini
# /etc/systemd/system/clio-backup.timer
[Timer]
OnCalendar=*-*-* 03:00:00
Persistent=true
[Install]
WantedBy=timers.target
```

> Der Online-`backup`-Lauf braucht die laufende Instanz nicht zu stoppen
> (gleichzeitige Reader sind erlaubt). Soll das Backup-Binary nicht selbst die
> DB öffnen, sondern der Server, ist `GET /api/v1/backup` die Alternative
> (`curl -H "Authorization: Bearer …" …/api/v1/backup -o snap.clio`).

**b) Docker (benanntes Volume)** — Daten und Backups als getrennte Volumes:

```
docker run -d --name clio \
  -v clio-data:/data -v clio-backups:/backups \
  -e CLIO_DB_PATH=/data/clio.db ghcr.io/pblumer/clio

# getimtes Backup (Host-Cron oder Sidecar):
docker exec clio cliostore backup --db /data/clio.db --output /backups/clio-$(date +%F).clio
docker exec clio cliostore verify --db /backups/clio-$(date +%F).clio
```

**c) Kubernetes (PVC + CronJob)** — DB auf einem PersistentVolumeClaim, Backup
als `CronJob`, der dasselbe PVC read-only mountet und auf ein Backup-PVC (oder
einen Objektspeicher-Sidecar) schreibt:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata: { name: clio-backup }
spec:
  schedule: "0 3 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: backup
              image: ghcr.io/pblumer/clio
              command: ["/bin/sh","-c"]
              args:
                - cliostore backup --db /data/clio.db --output /backups/clio-$(date +%F).clio &&
                  cliostore verify --db /backups/clio-$(date +%F).clio
              volumeMounts:
                - { name: data, mountPath: /data, readOnly: true }
                - { name: backups, mountPath: /backups }
          volumes:
            - { name: data,    persistentVolumeClaim: { claimName: clio-data } }
            - { name: backups, persistentVolumeClaim: { claimName: clio-backups } }
```

> **Hinweis PVC-Snapshots:** Wo der CSI-Treiber `VolumeSnapshot` kann, ist auch
> ein dateisystemnaher Snapshot möglich. Wegen bbolts copy-on-write ist ein
> roher Volume-Snapshot meist konsistent — aber `cliostore backup` ist die
> *garantiert* konsistente, plattformunabhängige Variante und sollte der Default
> bleiben.

### 4.5 Backup-Strategie (Empfehlung)

- **Frequenz/Retention:** täglich, Aufbewahrung nach 3-2-1 (z. B. 7 täglich,
  4 wöchentlich, off-site Kopie). Bei kleinen Stores ruhig häufiger.
- **Immer verifizieren:** jedes frische Backup direkt `verify`-en (siehe Units).
  Ein Backup, das man nie zurückgespielt/geprüft hat, ist kein Backup.
- **Restore-Drill:** der unten spezifizierte Testfall (M2) gehört als
  wiederkehrender Drill in den Betrieb, nicht nur in die CI.
- **Verschlüsselung/Transport:** `.clio` ist eine reine bbolt-Datei — Schutz im
  Ruhezustand/Transport (Volume-Encryption, GPG, Objektspeicher-SSE) liegt beim
  Betreiber.

---

## 5. Roadmap

Jeder Meilenstein ist für sich lauffähig und abnehmbar. ✅/🟡/⬜ = Status.

### M0 — `verify` als eigenständiges CLI-Kommando ✅
*Schätzung: < 0,5 Tag*
- `Store.Verify()` existiert bereits und wird über `GET /api/v1/verify`
  exponiert; M0 hebt es zusätzlich auf die **Offline-CLI** (`verify --db`).
- **Akzeptanz:** `cliostore verify --db x.db` rechnet die Kette nach; Exit `0`
  bei intakt, `1` bei Bruch (mit `brokenAt`); `--json` liefert `VerifyResult`.
  Read-only (INV-V1). Optionaler `CLIO_SIGNING_KEY` prüft Signaturen mit.

### M1 — Online-Backup ✅
*Schätzung: 0,5–1 Tag*
- `Store.Backup(io.Writer)` über `Tx.WriteTo` in einer View-Tx; `BackupToFile`
  mit temp + fsync + atomarem Rename. CLI `backup --output [--db] [--json]`.
- **Akzeptanz:** erfüllt INV-B1/B2/B3. Ergebnis meldet `output, bytes, events,
  head, durationMs`. Snapshot ist eigenständig öffenbar und `verify`-grün.
  Test `TestBackupConsistentSnapshot` (Count/Head aus einer Tx, Snapshot
  intakt).

### M2 — Restore + End-to-End-Testfall ✅
*Schätzung: 1 Tag*
- `store.Restore(input, dbPath, overwrite)`: ReadOnly-Open + Bucket-Validierung
  + defragmentierte Kopie (`bolt.Compact`) + fsync + atomarer Rename;
  `ErrTargetExists` ohne `--force`. CLI `restore --input --db [--force]`.
- **Headline-Testfall** `TestBackupRestoreVerifyReplay`:
  Append → **DB löschen** → Restore → Verify → Replay (Event-für-Event-
  Hash-Vergleich gegen Original) → erneuter Append schließt nahtlos an.
- **Akzeptanz:** erfüllt INV-R1/R2/R3. Restore ohne `--force` auf existierende
  DB schlägt fehl. Nach Restore: `head`/`count` == Backup; Replay bit-identisch.

### M3 — HTTP-`backup`-Endpoint + Doku ✅
*Schätzung: 0,5–1 Tag*
- `GET /api/v1/backup` (admin-scoped) streamt den Snapshot; OpenAPI-Spec +
  Swagger (ADR-011) ergänzen.
- Dieses Dokument in `ARCHITECTURE.md` verlinken; **ADR-026** in den ADR-
  Abschnitt übernehmen; systemd/Docker/K8s-Beispiele (§4.4) in `examples/`
  bzw. `docs/` ablegen; README-Abschnitt „Backup & Restore".
- **Akzeptanz:** Endpoint liefert ein `verify`-grünes `.clio`; Beispiele laufen
  unverändert; `make smoke` deckt einen Backup→verify-Durchlauf ab.

> **Ende Stufe 1.** Ab hier ist die committete DR-Story vollständig: konsistentes
> Online-Backup, geprüfter atomarer Restore, dokumentierte Strategie und
> Betriebsbeispiele, abgesichert durch den End-to-End-Testfall.

### M4+ — Stufe 2: Continuous Archiving / PITR ⬜ *(optional, skizziert)*
*Schätzung: mehrere überschaubare PRs; nur bei striktem RPO-Bedarf*

Nur die Skizze — ausspezifiziert wird erst bei Bedarf:

1. **Archiv-Sink + Sequenz-Cursor:** fortlaufender Export aller Events ab der
   zuletzt archivierten Sequenz (wiederverwendet `Read` mit `ReadOptions`).
   Segmente als NDJSON/CloudEvents-Batches, benannt nach `[fromSeq, toSeq]`.
   Persistenter Cursor (zuletzt archivierte Sequenz).
2. **Segment-Katalog:** Manifest, das Base-Backup + Segmente + deren
   Sequenz-/Zeitbereiche und Ketten-Köpfe verzeichnet (Grundlage für `--until`).
3. **`restore --until <zeit|seq>`:** Base einspielen, dann Segmente bis zum
   Zielpunkt nachspielen; bei jedem Schritt `predecessorHash`-Anschluss prüfen
   (selbstvalidierend, §2.3) → PITR.
4. **Andockpunkt Cold Storage (D):** Archiv-Sink kann statt lokalem Verzeichnis
   einen Objektspeicher beschreiben.

**Invarianten-Skizze:** lückenloser Sequenzraum über Base+Segmente (keine
Lücke, keine Überlappung); jedes Segment per Hash-Kette an seinen Vorgänger
anschlussfähig; PITR-Restore ist deterministisch reproduzierbar zum selben
`head` für denselben Zielpunkt.

**Bewusste Abgrenzung:** Stufe 2 bleibt *optional*. Sie baut additiv auf
Stufe 1 auf (Base-Backup = M1), führt keine zweite Storage-Engine und kein
neues Event-Format ein, und ist daher keine Sackgasse — aber sie kostet einen
laufenden Archiv-Prozess und gehört nur dort eingeschaltet, wo „kein
Backup-Intervall an Verlust" gefordert ist.

---

## 6. ADR-026 (zur Übernahme in `ARCHITECTURE.md` §7)

### ADR-026: Backup/Restore über konsistente bbolt-Snapshots; PITR optional
- **Status:** Vorgeschlagen *(Stufe 1 committet; Stufe 2 optional, zurückgestellt)*
- **Kontext:** Ein Event Store braucht eine glaubwürdige Antwort auf
  Verlustszenarien (Platte weg, Datei korrupt, Fehlbedienung). Bislang gibt es
  `verify` (ADR-012) und `compact` (ADR-015), aber keinen definierten Backup-/
  Restore-Weg. Clio ist Single-Node auf einer bbolt-Datei (ADR-002/006); die
  naheliegende Frage ist, ob DR über *Replikation* (Kafka-artig) oder über
  *Snapshots* (Postgres-artig) gelöst wird.
- **Entscheidung:** DR wird **snapshot-basiert** gelöst, nicht über Replikation.
  `cliostore backup` schreibt einen **konsistenten Online-Snapshot** der ganzen
  DB via bbolt `Tx.WriteTo` (Read-Tx, kein Schreiber-Lock) atomar in eine
  `.clio`-Datei (selbst eine gültige bbolt-DB). `cliostore restore` spielt einen
  Snapshot atomar an einen Zielpfad ein (defragmentierte Kopie + Rename;
  Überschreiben nur mit `--force`). `cliostore verify` hebt das bestehende
  `Store.Verify()` (ADR-012) auf die Offline-CLI und prüft die Hash-Kette
  (skriptbarer Exit-Code). Ein End-to-End-Testfall (Backup → DB löschen →
  Restore → Verify → Replay) ist verbindlicher Teil der Definition. Continuous
  Archiving / PITR (Base-Backup + fortlaufendes Event-Archiv ab Sequenz N,
  `restore --until`) ist als **optionale, additive Ausbaustufe** vorgesehen,
  aber **zurückgestellt** — Clios Event-Strom *ist* das WAL, weshalb diese
  Erweiterung ohne zweite Engine/Format auskäme.
- **Konsequenzen:** Sofort attraktive, betriebsreife DR-Story für den Single-
  Node-Fall (~90 % der Realität) mit minimalem Code, weil bbolt-Snapshot und
  Hash-Ketten-Verify wiederverwendet werden; das Backup-Artefakt ist
  kryptografisch selbstvalidierend (Vorteil gegenüber Postgres-WAL). Replikation/
  Hochverfügbarkeit (Follower-Knoten, Failover) bleibt **außerhalb** und würde
  ADR-002 ablösen — bewusst nicht hier. Verschlüsselung/Transport der `.clio`
  liegt beim Betreiber. PITR senkt das RPO auf nahe 0, kostet aber einen
  laufenden Archiv-Prozess und wird erst bei konkretem Bedarf ausspezifiziert.
- **Bezug:** baut auf ADR-002 (Single-Instance), ADR-003/006 (serialisierte
  Schreibstelle, append-only + Sequenz), ADR-012 (Hash-Kette/`verify`) und
  ADR-015 (atomarer temp+Rename-Mechanismus von `compact`).

---

## 7. Glossar

- **RPO** (Recovery Point Objective) — maximal tolerierbarer Datenverlust,
  gemessen in Zeit/Events „vor dem Ausfall".
- **RTO** (Recovery Time Objective) — maximal tolerierbare Ausfallzeit bis zur
  Wiederherstellung.
- **Base-Backup** — vollständiger Snapshot zu einem Zeitpunkt T₀ (= Stufe-1-
  Backup).
- **Continuous Archiving** — fortlaufendes Wegsichern aller Änderungen ab T₀;
  bei Clio = Events ab der zuletzt archivierten Sequenz.
- **PITR** (Point-in-Time Recovery) — Wiederherstellung auf einen *beliebigen*
  Zeit-/Sequenzpunkt = Base-Backup + Replay der Archiv-Segmente bis dorthin.
- **`.clio`** — Backup-Artefakt; eine eigenständig öffenbare, `verify`-bare
  bbolt-Datei.
