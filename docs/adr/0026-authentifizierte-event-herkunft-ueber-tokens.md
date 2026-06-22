# ADR-026: Authentifizierte Event-Herkunft über Tokens

> Diese Datei ist die als Einzeldokument ausgelagerte, fortan maßgebliche Fassung
> dieser Entscheidung. Der Body wurde **wörtlich** aus [`ARCHITECTURE.md` §7](../../ARCHITECTURE.md#7-architecture-decision-records-adrs)
> übernommen (nichts hinzuerfunden). Diese Entscheidung wurde als erste in eine
> Einzeldatei überführt, weil sie als einzige noch im Status „Vorgeschlagen" und
> damit in Bewegung war. Index: [`README.md`](./README.md).

**Status:** Vorgeschlagen

**Datum:** — (im Bestand undatiert; als Einzeldatei ausgelagert am 2026-06-22)

**Kontext**

Das `source`-Feld eines Events ist nach CloudEvents-Spec ein vom Producer selbst gesetzter URI-Reference und damit **selbstdeklariert** — ein Client kann beliebige Herkunft behaupten. Für einen Event Store, dessen Wert maßgeblich auf Auditierbarkeit beruht, ist das eine Lücke: Die aufgezeichnete Herkunft eines Events ist nur so vertrauenswürdig wie die Ehrlichkeit des Schreibers. Diese Entscheidung führt eine Bindung zwischen **Schreib-Token** (Access Token) und erlaubter `source` ein. Wer schreiben will, braucht ein Token; das Token bestimmt, als welche Source(s) der Schreiber auftreten darf. Damit wird die aufgezeichnete Herkunft **attributiert** statt nur behauptet. (Baut auf dem Schlüsselbund aus ADR-025 auf, dessen `kid.secret`-Tokens und Scopes hier zur Source-Autorisierung erweitert werden.)

**Entscheidung**

1. **Token autorisiert eine Menge von Sources.** Ein Token trägt eine Liste erlaubter Source-Werte. Der 1:1-Fall ist der Spezialfall einer einelementigen Menge. Multi-Source ist ein realer Anwendungsfall (Gateway-/Ingest-Producer, die für mehrere logische Sources schreiben). Das Matching erfolgt zunächst über **exakte Werte**; Präfix-/Pattern-Matching für dynamisch erzeugte Sources (z. B. pro Tenant) ist eine bewusst zurückgestellte, additiv nachrüstbare Erweiterung.
2. **Server setzt oder validiert die Source, abhängig von der Token-Menge.** Erlaubt das Token genau eine Source: Der Client darf `source` weglassen; der Server setzt sie. Schickt der Client eine abweichende Source → **harte Ablehnung** (kein stilles Überschreiben). Erlaubt das Token mehrere Sources: Der Client **muss** `source` mitschicken; der Server validiert gegen die erlaubte Menge. Nicht enthaltener Wert → **harte Ablehnung**.
3. **Token-Verwaltung als eigene Domäne, getrennt vom Store-Kern.** Eine `auth`/`principal`-Domäne kennt Tokens und ihre erlaubten Sources. Der Store-Kern bleibt **auth-unwissend** und erhält ausschließlich eine bereits gesetzte/validierte Source. Token werden ausschließlich als Hash (SHA-256) persistiert, nie im Klartext; Vergleich in konstanter Zeit (`crypto/subtle.ConstantTimeCompare`). Bleibt CGO-frei (passt zu ADR-001).
4. **Token sind revozierbar; Revocation wirkt nur nach vorn.** Ein Token ist ein Entity mit Lebenszyklus (`id`, `hash`, erlaubte Sources, `created_at`, `revoked_at`). Der Lookup verlangt zusätzlich `revoked_at == null`. Bereits geschriebene Events bleiben gültig und attributiert — die Historie ist immutable, ein revoktes Token entwertet nicht, was es legitim geschrieben hat. (Ob der Token-Lifecycle selbst als Tabelle oder als interner Event-Stream im Store geführt wird, ist als **ADR-027** separat zu entscheiden — siehe offener Punkt unten.)
5. **Tokenlose Writes landen in einem isolierten Inbox-Stream, standardmäßig deaktiviert.** Events ohne gültiges Token werden in einen eigenen, klar benannten Stream (z. B. `_inbox`) geschrieben, **physisch getrennt** vom authentifizierten Event-Raum — nicht nur per Konvention. Konsumenten des Hauptraums sehen sie nicht, sofern sie die Inbox nicht explizit abonnieren. Zusätzlich setzt der Server ein nicht-fälschbares, serverkontrolliertes Attribut (CloudEvents-Extension, z. B. `principal: anonymous`), das die Vertrauensstufe pro Event unmissverständlich macht — unabhängig vom Stream. Der tokenlose Pfad ist grundsätzlich **aus** und wird pro Ziel-Source oder global per Config explizit aktiviert; andernfalls wäre er ein ungesicherter Write-Endpoint.
6. **Überführung aus der Inbox erfolgt als neues, anreicherndes Event.** Ein Inbox-Event wird **nie verschoben** (Inbox-Historie bleibt immutable). Stattdessen schreibt ein Promoter mit gültigem Token ein **neues** Event in den authentifizierten Raum, das den Inbox-Ursprung über eine Extension (`promotedfrom` mit der Inbox-Event-ID, alternativ `dataref`) referenziert. Der Promoter darf den Inhalt beim Überführen anreichern/normalisieren. Das promotete Event ist semantisch eine Aussage des Promoters und gehört dessen authentifizierter Source — nicht dem anonymen Absender. Der Rückverweis ist die einzige, aber lückenlose Brücke zum Original.

**Konsequenzen**

*Positiv:* Die aufgezeichnete Herkunft ist an einen authentifizierten Schreibkanal gebunden statt selbstdeklariert. Klare physische und semantische Trennung zwischen authentifizierten und anonymen Events; kein versehentliches Vermischen. Multi-Source-Producer werden ohne Token-Wildwuchs unterstützt. Auditierbarkeit bleibt durchgängig: Revocation, Inbox-Ursprung und Promotion sind alle nachvollziehbar, ohne die Immutability der Historie zu verletzen. Bleibt pure-Go ohne CGO (ADR-001).

*Negativ / Grenzen:* Das Feature liefert **attributierte**, keine kryptografisch bewiesene Herkunft. Ein geleaktes oder geteiltes Token bricht die Garantie — ein kompromittiertes Token kann beliebigen Payload unter seiner legitimen Source schreiben. Echte inhaltliche Provenance über **Event-Signaturen** (vgl. ADR-016, dort serverseitig) ist ausdrücklich out of scope und ein eigenes späteres ADR. Die auth-Domäne mit Lifecycle und Revocation vergrößert den Umfang gegenüber einem reinen String-Feld spürbar. Anreichernde Promotion bedeutet, dass das promotete Event vom Original inhaltlich abweichen kann; die Nachvollziehbarkeit „was wurde verändert" hängt allein am `promotedfrom`-Verweis und liegt in der Verantwortung des Promoters.

**Offene Punkte / Folge-ADRs**

(1) Token-Lifecycle als Tabelle vs. interner Event-Stream (Bootstrap-/Henne-Ei-Frage bei letzterem) → **ADR-027**. (2) Präfix-/Pattern-Matching für dynamische Sources, falls ein Producer Sources zur Laufzeit erzeugt. (3) Verhältnis zu den bestehenden Ed25519-Signaturen (ADR-016, kryptografisch bewiesene Authentizität serverseitig) — diese ADR liefert nur *attributierte*, keine *bewiesene* Herkunft; beide Konzepte ergänzen sich und werden nicht vermischt.
