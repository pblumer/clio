# ADR-041: Deklarative Feld-Reduktionsstrategien für die Zustandssicht

**Status:** Akzeptiert (2026-06-26)

**Datum:** 2026-06-26

**Kontext**

ADR-039 faltet die Zustandssicht eines Subjects mit **einer festen Semantik**:
Last-Write-Wins-Deep-Merge der `data`-Payloads (null = Tombstone). Das passt für
„aktueller Stand eines Dokuments", aber nicht für verbreitete Aggregat-Bedürfnisse:
**Zähler/Summen** (`amount += …`), **Extremwerte** (`max(score)`), **Listen**
(Historie anhängen) oder **Mengen** (Tags vereinigen). ADR-039 hat „wählbare/
registrierbare Reduktionen" als Folge-ADR offen gelassen.

clio ist ein **generischer** Event Store und kennt die domänenspezifische
Reduktion nicht. Die Lösung muss daher **deklarativ und vorhersehbar** sein, zum
Hausstil passen (deklarative JSON-Schemas ADR-014, deklarativer Sekundärindex
ADR-029, Subject-Prefix-Scopes ADR-033) und darf die bestehende, abwärtskompatible
Default-Semantik (ADR-039) nicht verändern.

Verworfene Alternativen: ein **CEL-Reducer** (acc, event) → acc (maximal flexibel,
aber teuer pro Event, schwer zu cachen/abzusichern); **In-Payload-Operatoren**
(`$inc`/`$set`) — vermischen Reduktions-Semantik in die fachlichen Event-Daten.

**Entscheidung**

1. **Deklarative Reduce-Spec, registriert pro Subject-Prefix.** Eine Spec bildet
   punkt-separierte **Feldpfade** auf eine **Strategie** ab und nennt eine
   `default`-Strategie für nicht aufgeführte Felder:
   ```json
   { "fields": { "amount": "sum", "tags": "union", "createdAt": "first" },
     "default": "lww" }
   ```
   Für ein konkretes Subject gilt die Spec des **längsten passenden Prefix**
   (Routing-Tabelle); ohne passende Spec bleibt es beim Default-LWW aus ADR-039
   (voll abwärtskompatibel).

2. **Strategie-Vokabular (v1):** `lww` (Default, Deep-Merge + null-Tombstone),
   `sum`, `min`, `max`, `append` (Array; Array-Werte elementweise), `union` (wie
   append, mengenartig/dedupliziert), `first` (ersten nicht-null-Wert behalten).
   Numerische Strategien ignorieren nicht-numerische Werte; `null` ist nur unter
   `lww` ein Tombstone, sonst ein No-op. Alle Strategien sind **assoziativ als
   Links-Fold** und damit korrekt unter der inkrementellen Cache-Fortschreibung
   (ADR-040).

3. **Mutable Lese-Konfiguration, kein historisches Faktum.** Anders als Event-
   Schemas (ADR-014, unveränderlich) darf eine Reduce-Spec **überschrieben und
   gelöscht** werden — sie ändert nur **abgeleitete Sichten**, nie gespeicherte
   Events. Persistiert in einem eigenen bbolt-Bucket (`reduce_specs`, Prefix →
   kanonische JSON-Spec); vom Reset (ADR-022) erfasst.

4. **API.** `POST /register-reduce-spec` und `DELETE /reduce-spec?prefix=…` (Scope
   `write`, da prefix-weit wirksam — wie Schema-Registrierung); `GET
   /read-reduce-spec` (`?prefix=` exakt, `?subject=` wirksam, ohne Parameter Liste;
   Scope `read`). Registrierung/Löschung sind audit-pflichtig (ADR-032). Die
   Antwort von `GET /state/<subject>` nennt im Feld `reducer` den wirksamen Prefix.

5. **Cache-Kopplung über Fingerprint (ADR-040).** Der kanonische Spec-Inhalt ist
   Teil des Cache-Fingerprints; eine Spec-Änderung invalidiert die betroffenen
   Stände implizit.

**Konsequenzen**

*Positiv (gewonnen):*

- Verbreitete Aggregate (Zähler, Summen, Extremwerte, Listen, Mengen) sind ohne
  externes Read-Model direkt über `GET /state` abbildbar.
- Deklarativ, vorhersehbar, hausstil-konform (wie Schemas/Index/Scopes);
  prefix-basiert „einmal registrieren, gilt für alle Subjects der Art".
- Default-Verhalten unverändert (ADR-039) — kein Bestands-Client merkt etwas.
- Strategien sind links-fold-assoziativ → harmonieren mit dem inkrementellen Cache.

*Negativ / Grenzen (geopfert):*

- **Fixes Strategie-Vokabular:** Reduktionen jenseits der genannten (gewichtete
  Mittel, bedingte Logik, Joins) bleiben einem externen Read-Model (CQRS)
  vorbehalten — die Grenze aus ADR-029/036 bleibt gewahrt.
- **Retroaktiv:** eine geänderte Spec ändert die Sicht auf die gesamte Historie
  (gewollt — es ist eine View-Konfiguration, kein Faktum), erzwingt aber eine
  Neufaltung (ADR-040).
- **Single-subject** wie ADR-039: keine Cross-Subject-Aggregation.
- Eine Spec gilt **prefix-weit**; Sonderfälle löst man über einen längeren Prefix.

**Offene Punkte / Folge-ADRs**

- **CEL-Reducer** als Power-Option — nur, falls ein realer Bedarf das feste
  Vokabular übersteigt.
- **Weitere Strategien** (z. B. `count`, `last-non-null`, `concat`) — additiv bei
  Bedarf.
- **Validierung gegen Event-Schemas** (Strategie passt zum deklarierten Feldtyp) —
  optional später.

**Referenzen**

- ADR-039 (Zustandssicht über REST) — diese ADR verallgemeinert deren feste
  Fold-Semantik; der Default bleibt unverändert.
- ADR-040 (In-Memory-Snapshot-Cache) — nutzt den Spec-Fingerprint zur
  Invalidierung; die Strategien sind bewusst links-fold-assoziativ.
- ADR-014 (JSON-Schemas), ADR-029 (deklarativer Sekundärindex), ADR-033 (Subject-
  Prefix-Scopes) — Vorbilder für deklarative, prefix-/typweite Registrierung.
- ADR-029/036 (externes Read-Model zurückgestellt) — komplexere Reduktionen bleiben
  außerhalb des Kerns.
