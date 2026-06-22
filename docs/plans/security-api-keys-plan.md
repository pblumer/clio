# Implementierungsplan: Benannte API-Keys mit Scopes, Widerruf und Audit

**Projekt:** `github.com/pblumer/clio`
**Status:** PLANUNG — bereit zur Umsetzung durch einen AI-Coding-Agenten
**Vorbild-Doku:** Temis-WP-Format (in sich geschlossene Work-Packages mit Akzeptanzkriterien)

---

## 0. Zusammenfassung

Heute schützt clio alle Routen mit *einem* geteilten Bearer-Token (`CLIO_API_TOKEN`,
ADR-008). Dieser Plan ersetzt das geteilte Geheimnis durch einen **persistenten
Schlüsselbund (Keyring)** im bbolt-Store. Jeder Schlüssel ist ein benanntes
*Credential* mit eigener Identität, einem Satz **Scopes** (`read`, `write`,
`admin`) und einem **Status** (`active` / `revoked`). Damit werden drei Probleme
gemeinsam gelöst:

1. **Mehrere Tokens + Widerruf** — pro Beteiligtem ein eigener Schlüssel,
   widerrufbar ohne globalen Neustart und ohne Neuverteilung an alle.
2. **Lese-/Schreibtrennung** — Scopes statt Alles-oder-nichts; `read`-Clients
   können nicht schreiben.
3. **Zuordenbarkeit** — jede authentifizierte Anfrage trägt eine Identität
   (`kid`), die ins Audit-Log und optional in die Event-Urheberschaft fließt.

Das Single-Binary-/Stdlib-Prinzip (ADR-001) bleibt erhalten: **keine neue
externe Abhängigkeit** (Hashing via `crypto/sha256`, zeitkonstanter Vergleich
via `crypto/subtle`, beides bereits im Einsatz).

### Designentscheidungen, die alles Weitere prägen

| Entscheidung | Gewählt | Begründung |
|---|---|---|
| Speicherort | Persistent im bbolt-Store, eigener Bucket `auth_keys` | Laufzeit-Verwaltung ohne Restart; klar getrennt vom Event-Strom |
| Schlüsselformat auf der Leitung | `kid.secret` | O(1)-Lookup über `kid`; sauberer, eindeutiger Widerruf |
| Speicherung des Geheimnisses | Nur **Hash** des `secret` (SHA-256), nie Klartext | Kompromittierung der DB gibt keine gültigen Schlüssel preis |
| Widerruf | Status-Flag `revoked`, **kein** Delete | Audit-Zuordnung eines `kid` bleibt dauerhaft möglich |
| Bootstrap | `CLIO_BOOTSTRAP_ADMIN_KEY`, greift nur bei leerem Bucket | Löst Henne-Ei-Problem; hält ADR-008-Abwärtskompatibilität |

### Abgrenzung zum Append-only-Prinzip (wichtig fürs ADR)

clio löscht **Events** nie (ADR-015). Schlüssel sind **Steuerungsdaten**, kein
Event-Strom — sie leben in einem eigenen Bucket und dürfen mutiert werden
(Status `active` → `revoked`). Das ist **keine** Aufweichung des
Append-only-Versprechens. Der Reset (ADR-022, Dev-Mode) lässt den
`auth_keys`-Bucket **unangetastet** — sonst sperrt man sich beim Dev-Reset selbst
aus.

---

## 1. Zielarchitektur

### 1.1 Datenmodell (Bucket `auth_keys`)

Key: `kid` (z. B. `kid_ci01`, 8-stellige zufällige Basis32-ID)
Value: JSON-kodierter Datensatz

```json
{
  "kid": "kid_ci01",
  "name": "ci-writer",
  "secretHash": "<hex sha256(secret)>",
  "scopes": ["read", "write"],
  "status": "active",
  "createdAt": "2026-06-17T10:00:00Z",
  "revokedAt": null
}
```

### 1.2 Schlüssel auf der Leitung

```
Authorization: Bearer kid_ci01.W8xqT2vK9pL4mN6rS1dF3hJ5
                      └──┬───┘ └──────────┬──────────┘
                       kid              secret (Klartext, nur Client kennt ihn)
```

Server-Prüfung: `kid` aus dem Header extrahieren → O(1)-Lookup im Bucket →
`status == active` → `subtle.ConstantTimeCompare(sha256(secret), record.secretHash)`
→ Scope der Route gegen `record.scopes`.

### 1.3 Scope-Mapping der Routen

| Route | Benötigter Scope |
|---|---|
| `GET /api/v1/ping` | — (offen, wie heute) |
| `GET /api/v1/info`, `event-stats`, `read-events`, `read-subjects`, `read-event-types`, `read-event-schema`, `observe-events`, `run-query`, `verify`, `public-key`, `events/{subject...}` | `read` |
| `POST /api/v1/write-events`, `register-event-schema` | `write` |
| `POST /api/v1/keys/*` (neue Admin-Routen), `dev/*` | `admin` |
| `GET /metrics`, `/docs`, `/openapi.yaml`, `/ui` | — (wie heute, nicht sensibel) |

Fehlender/ungültiger Schlüssel → `401 Unauthorized`. Gültiger Schlüssel ohne
passenden Scope → `403 Forbidden`. (Die Unterscheidung 401/403 ist neu und
wichtig.)

---

## 2. Work-Packages

Reihenfolge ist die empfohlene Implementierungssequenz. Jedes WP ist isoliert
testbar und endet in grünem `go test ./... && go vet ./...`.

### WP-01 — Auth-Domänenmodell (`internal/auth/keyring.go`)

**Ziel:** Reines Domänenpaket ohne Storage-/HTTP-Abhängigkeit.

**Inhalt:**
- Typen `Scope` (`read|write|admin`), `Status` (`active|revoked`), `Key`-Record.
- `func ParseBearer(header string) (kid, secret string, ok bool)` — zerlegt
  `Bearer kid.secret`, robust gegen Fehlformate.
- `func HashSecret(secret string) string` — `sha256` → hex.
- `func (k Key) HasScope(s Scope) bool`.
- `func GenerateKey(name string, scopes []Scope) (Key, plaintextSecret string, err error)`
  — erzeugt `kid` + zufälligen `secret` (`crypto/rand`, mind. 160 Bit),
  speichert nur den Hash im Record. Gibt den Klartext-`secret` **einmalig**
  zurück (danach nie wieder rekonstruierbar).

**Akzeptanzkriterien:**
- Tabellengetriebene Tests für `ParseBearer` (gültig, kein Punkt, leer, nur
  `kid`, doppelter Punkt im secret → secret darf Punkte enthalten, nur der
  *erste* Punkt trennt).
- `GenerateKey` erzeugt bei 1000 Aufrufen kollisionsfreie `kid`s und secrets
  mit ausreichender Entropie.
- Kein Import von `store` oder `net/http`.

---

### WP-02 — Persistenz im Store (`internal/store/store_authkeys.go`)

**Ziel:** Keyring im bbolt-Store, neuer Bucket `auth_keys`, sauber von den
Event-Buckets getrennt.

**Inhalt:**
- Neue Bucket-Konstante `bucketAuthKeys = []byte("auth_keys")`.
- In `OpenWithOptions` den Bucket zur `CreateBucketIfNotExists`-Schleife
  hinzufügen (Zeile ~176). **Bestehende Stores:** Bucket wird beim nächsten Open
  leer angelegt — Backfill nicht nötig (es gibt keine Alt-Keys).
- Store-Methoden:
  - `PutKey(k auth.Key) error`
  - `GetKey(kid string) (auth.Key, bool, error)`
  - `ListKeys() ([]auth.Key, error)`
  - `RevokeKey(kid string) (bool, error)` — setzt `status=revoked`,
    `revokedAt=now`; **kein** Delete. Liefert `false`, wenn `kid` unbekannt.
  - `CountKeys() (int, error)` — für den Bootstrap-Check.

**Wichtig — `Reset()` anpassen (ADR-022):**
Der Dev-Reset (Zeile ~252) darf den `auth_keys`-Bucket **nicht** löschen. Die
Bucket-Liste in `Reset()` bleibt also bewusst **ohne** `bucketAuthKeys`. Test,
der genau das absichert (nach Reset ist ein vorher angelegter Key noch da).

**Akzeptanzkriterien:**
- Roundtrip-Test Put/Get/List/Revoke gegen einen temporären bbolt-File.
- `Reset()` lässt Keys unangetastet (expliziter Regressionstest).
- `auth_keys` taucht **nicht** in der Reset-Bucket-Liste auf (Code-Review-Punkt).

---

### WP-03 — Config-Erweiterung (`internal/config/config.go`)

**Ziel:** Bootstrap-Pfad und Abwärtskompatibilität zu `CLIO_API_TOKEN`.

**Inhalt:**
- Neues Feld `BootstrapAdminKey string` aus `CLIO_BOOTSTRAP_ADMIN_KEY`.
- `CLIO_API_TOKEN` bleibt erhalten, wird aber **deprecated**: ist es gesetzt und
  der `auth_keys`-Bucket leer, wird daraus beim Start ein einzelner Admin-Key
  gebootet (Identität `legacy-token`, Scopes `read,write,admin`). Damit läuft
  jedes bestehende Single-Token-Deployment unverändert weiter (ADR-008 → ADR-025
  als degenerierter Sonderfall).
- **Validierung lockern:** `CLIO_API_TOKEN` ist nicht mehr Pflicht. Stattdessen:
  Start ist erlaubt, wenn **entweder** Bucket nicht leer **oder**
  `CLIO_BOOTSTRAP_ADMIN_KEY` **oder** `CLIO_API_TOKEN` gesetzt ist. Andernfalls
  Fehler „kein Auth-Material: setze CLIO_BOOTSTRAP_ADMIN_KEY oder lege Keys an".
  (Dieser Check braucht den Store → erfolgt in WP-05, nicht in `FromEnv`.)

**Akzeptanzkriterien:**
- Tests: `CLIO_API_TOKEN` allein → ok; `CLIO_BOOTSTRAP_ADMIN_KEY` allein → ok;
  beide leer → `FromEnv` ok (Check wandert in den Bootstrap, WP-05).
- Bestehende Config-Tests bleiben grün (ggf. minimal angepasst, da Token nicht
  mehr hart Pflicht ist).

---

### WP-04 — Auth-Middleware im Server (`internal/httpapi/server.go`)

**Ziel:** `requireAuth` → scope-bewusste Middleware mit Identität im Context.

**Inhalt:**
- Server-Feld `store` wird ohnehin gehalten; Lookup läuft über die
  WP-02-Methoden.
- Neue Methode `requireScope(scope auth.Scope, next http.HandlerFunc) http.HandlerFunc`:
  1. `ParseBearer` aus `Authorization`.
  2. `GetKey(kid)`; fehlt/Fehler → `401`.
  3. `status != active` → `401`.
  4. `subtle.ConstantTimeCompare(HashSecret(secret), record.secretHash)` → bei
     Ungleichheit `401`. **Zeitkonstanz beibehalten** wie im heutigen
     `requireAuth`.
  5. `!record.HasScope(scope)` → `403`.
  6. Identität (`kid`, `name`, `scopes`) via `context.WithValue` in den Request
     legen; `next` aufrufen.
- Helfer `identityFromContext(r) (auth.Identity, bool)` für Handler/Audit.
- `routes()` (Zeile ~192 ff.) umstellen: jede geschützte Route mit dem
  passenden Scope aus der Tabelle in §1.3 annotieren. `requireAuth` entfernen.

**Akzeptanzkriterien:**
- `server_test.go` erweitern: read-Key darf lesen, nicht schreiben (`403`);
  write-Key darf beides nicht-admin; revoked-Key → `401`; falsches secret →
  `401`; unbekannte `kid` → `401`.
- Bestehende Auth-Tests (gültig/falsch/kein Token) auf das neue Schema
  übertragen (Test-Helfer `do(...)` erzeugt jetzt `kid.secret`).
- 401 vs. 403 wird in den Tests klar unterschieden.

---

### WP-05 — Bootstrap-Verdrahtung (`cmd/cliostore/main.go`)

**Ziel:** Initialen Admin-Key beim ersten Start anlegen; Start ohne
Auth-Material verweigern.

**Inhalt:**
- Nach `store.OpenWithOptions` (Zeile ~122) und vor `httpapi.New`:
  - `CountKeys()`; ist der Bucket **leer**:
    - `CLIO_BOOTSTRAP_ADMIN_KEY` gesetzt → daraus einen Admin-Key anlegen
      (Format: der ENV-Wert ist der Klartext-`secret`; `kid` wird generiert; der
      vollständige `kid.secret` wird **einmalig in den Log** geschrieben, damit
      der Betreiber ihn kopieren kann — mit deutlichem Warnhinweis).
    - sonst falls `CLIO_API_TOKEN` gesetzt → Legacy-Admin-Key `legacy-token`
      (Klartext = Token-Wert), damit `Bearer <token>` ohne `kid.`-Präfix … —
      **Achtung:** Legacy-Token hat *kein* `kid.`-Format. Zwei saubere Optionen:
      (a) Legacy-Modus erlaubt headerweise das alte Format und mappt es intern
      auf den `legacy-token`-Key; (b) Migration: Betreiber stellt einmalig auf
      `kid.secret` um. **Empfehlung:** (a) als befristeter Kompatibilitätspfad,
      im ADR als deprecated markiert.
    - sonst → **Start abbrechen** mit der Meldung aus WP-03.
  - Ist der Bucket nicht leer: nichts tun (normaler Start).
- Lauter Warn-Log analog zum Dev-Mode-Hinweis (ADR-022), wenn ein
  Bootstrap-Key erzeugt wurde.

**Akzeptanzkriterien:**
- Integrationstest (kann `main`-nah über ein Helfer-Setup laufen): frischer
  Store + Bootstrap-ENV → genau ein Admin-Key vorhanden, danach kein erneutes
  Bootstrapping bei Neustart.
- Frischer Store ohne jedes Auth-Material → Start scheitert mit klarer Meldung.

---

### WP-06 — Admin-Routen zur Key-Verwaltung (`internal/httpapi/server_keys.go`)

**Ziel:** Schlüssel zur Laufzeit verwalten (Scope `admin`).

**Routen:**
- `POST /api/v1/keys` — Body `{ "name": "...", "scopes": ["read","write"] }`;
  erzeugt Key, antwortet **einmalig** mit dem Klartext-`kid.secret`
  (Hinweis im Response, dass er nicht erneut abrufbar ist). `201 Created`.
- `GET /api/v1/keys` — Liste **ohne** Geheimnisse/Hashes (nur `kid`, `name`,
  `scopes`, `status`, `createdAt`, `revokedAt`).
- `POST /api/v1/keys/{kid}/revoke` — widerruft; `200` bzw. `404` bei
  unbekanntem `kid`.

Alle drei unter `requireScope(admin, …)`. Self-Lockout-Schutz: ein Admin darf
sich selbst widerrufen können (kein Sonderfall), aber `GET /keys` sollte vorm
Widerruf des letzten aktiven Admin-Keys **warnen** (Response-Hinweis, kein
harter Block — bewusst, um nicht handlungsunfähig zu werden).

**OpenAPI/Dashboard:**
- `internal/apidocs/openapi.yaml` um die drei Routen ergänzen (ADR-011: Spec
  wird handgepflegt — bei API-Änderungen mitziehen).
- Optional (eigenes WP, s. u.): Dashboard-Sektion.

**Akzeptanzkriterien:**
- Tests: admin-Key kann anlegen/listen/widerrufen; read/write-Key bekommt `403`.
- `GET /keys` enthält in keiner Antwort `secretHash` oder Klartext.
- Angelegter Key ist sofort (ohne Restart) zur Authentifizierung nutzbar.

---

### WP-07 — Audit-Log (`internal/httpapi/audit.go`)

**Ziel:** Zuordenbarkeit „wer / wann / welche Route / Ergebnis".

**Inhalt:**
- In der Observability-Middleware (`instrument`, Zeile ~164) bzw. in
  `requireScope` strukturiertes Audit-Logging via `slog`:
  `audit kid=… name=… method=… path=… scope=… decision=allow|deny status=…`.
- Bei `deny` (401/403) ebenfalls loggen — fehlgeschlagene Zugriffe sind
  sicherheitsrelevant. Bei fehlendem `kid` (401 ohne gültigen Header) wird `kid`
  weggelassen.

**Optionale Erweiterung (Flag, Default aus):** authentifizierte Identität als
`source`/Attribut in geschriebene CloudEvents übernehmen. Das ist
Append-only-konform (neues Event, keine Mutation) und verbindet Urheberschaft
mit der bestehenden Hash-Kette/Ed25519-Signatur (ADR-012/016). **Bewusst opt-in**
und in einem Folge-WP, um den Kern schlank zu halten.

**Akzeptanzkriterien:**
- Test prüft, dass `allow`- und `deny`-Entscheidungen geloggt werden und `kid`
  bei erfolgreichem Zugriff enthalten ist (Log-Capture über `slog`-Handler im
  Test).
- Kein Geheimnis (secret/hash) landet je im Log.

---

### WP-08 — Dokumentation: ADR-025 + ARCHITECTURE.md

**Ziel:** Entscheidung im Doku-First-Stil festhalten.

**Inhalt:**
- Neuer **ADR-025: Mehrere benannte API-Keys mit Scopes, Widerruf und Audit**
  in `ARCHITECTURE.md` (nach ADR-024). Vorlage:
  - *Status:* Akzeptiert
  - *Kontext:* Geteiltes Single-Token (ADR-008) ist nicht widerrufbar pro
    Beteiligtem, trennt nicht Lesen/Schreiben, ist nicht zuordenbar.
  - *Entscheidung:* Persistenter Keyring (`auth_keys`-Bucket), `kid.secret` auf
    der Leitung, nur Hash gespeichert, Scopes je Key, Widerruf als Status,
    Admin-Routen, ENV-Bootstrap, Legacy-Token als deprecated
    Kompatibilitätspfad.
  - *Konsequenzen:* Echte Mehrbenutzer-Sicherheit ohne externe Abhängigkeit;
    `auth_keys` ist mutabler Steuerungs-State (klar getrennt vom Append-only
    Event-Strom, vom Reset ausgenommen); 401/403-Semantik neu; OpenAPI/Dashboard
    nachzuziehen.
- ADR-008 erhält einen Querverweis „abgelöst/erweitert durch ADR-025" (Status
  z. B. „Überholt durch ADR-025").
- `README.md` und `examples/_env.sh` / `_env.ps1` um die neuen ENV-Variablen
  und den `kid.secret`-Header ergänzen.

**Akzeptanzkriterien:**
- ADR-025 folgt exakt dem bestehenden ADR-Format (Status/Kontext/Entscheidung/
  Konsequenzen).
- Alle Beispielskripte funktionieren mit dem neuen Header-Format.

---

## 3. Sicherheits-Checkliste (Review-Gate vor Merge)

- [ ] Geheimnisse nur als SHA-256-Hash persistiert; Klartext nie in DB/Log.
- [ ] Zeitkonstanter Vergleich (`crypto/subtle`) auf dem Secret-Hash.
- [ ] `kid`-Lookup vor dem Vergleich → kein Timing-Leak über die Existenz (der
      Hash-Vergleich läuft auch bei unbekanntem `kid` gegen einen Dummy-Hash
      gleicher Länge, um die Antwortzeit anzugleichen).
- [ ] 401 (kein/ungültiger Schlüssel) sauber von 403 (kein Scope) getrennt.
- [ ] Reset (Dev-Mode) lässt `auth_keys` unangetastet.
- [ ] Bootstrap erzeugt höchstens einen Key und nur bei leerem Bucket.
- [ ] Admin-Routen ausschließlich unter `admin`-Scope.
- [ ] Keine neue externe Go-Abhängigkeit (go.mod unverändert außer evtl.
      Versionsbumps).

## 4. Sequenz & Abhängigkeiten

```
WP-01 (Domäne) ─┬─> WP-02 (Store) ─┬─> WP-04 (Middleware) ─> WP-06 (Admin-Routen)
                │                  │                           │
                └─> WP-03 (Config) ┴─> WP-05 (Bootstrap) ──────┘
                                                              │
                                              WP-07 (Audit) ──┤
                                                              │
                                              WP-08 (Doku) ───┘  (begleitend)
```

WP-01 und WP-03 sind parallelisierbar. WP-08 läuft begleitend mit, wird aber
vor dem finalen Merge abgeschlossen (Doku-First-Prinzip des Projekts).

## 5. Nicht-Ziele (bewusst ausgeschlossen)

- Kein OIDC/externer IdP, keine JWT-Bibliothek (Single-Binary-/Stdlib-Prinzip).
- Keine Mandantentrennung/Tenancy (über Scopes hinaus keine Rollen-Hierarchie).
- Keine automatische Key-Rotation/Ablaufdaten (kann später als `expiresAt`-Feld
  additiv ergänzt werden — das Record-Schema lässt Raum dafür).
- Keine Verschlüsselung des bbolt-Files at-rest (orthogonal; Deployment-Thema).
