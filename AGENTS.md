# AGENTS.md — Einstieg für KI-Agenten

Diese Datei sagt dir in < 1 Minute, **wohin ein Dokument gehört** und **wie du
eine Entscheidung festhältst**. Sie ist operativ; die inhaltlichen Prinzipien und
Arbeitsregeln stehen ausführlich in [`SESSION_PROMPT.md`](./SESSION_PROMPT.md)
(lies sie zuerst). Sprache aller Dokumente: **Deutsch**.

## Reihenfolge der Wahrheit

| Quelle | Antwortet auf | Eigenschaft |
|---|---|---|
| [`ARCHITECTURE.md`](./ARCHITECTURE.md) | **Was gilt heute?** (Soll-/Ist-Zustand, Roadmap §6, ADR-Bodies §7) | *mutable*, wird fortgeschrieben |
| [`docs/adr/`](./docs/adr/) | **Warum** wurde etwas entschieden? | *append-only*, mit Status-Lebenszyklus |
| [`docs/plans/`](./docs/plans/) | **Wie** wird etwas umgesetzt? (Work-Packages, Akzeptanzkriterien) | *mutable*, darf „umgesetzt" werden |

**Bei Konflikt:** Für „**warum**" gewinnt die ADR. Für „**was gilt heute**"
gewinnt `ARCHITECTURE.md`.

> Historischer Hinweis: ADR-001…033 stehen als ausformulierte Bodies in
> `ARCHITECTURE.md` §7, **nicht** als Einzeldateien. Sie werden nicht rückwirkend
> extrahiert. Der Index [`docs/adr/README.md`](./docs/adr/README.md) führt sie und
> ist die **Single Source of Truth für vergebene Nummern**. ADR-026 wurde als erste
> Entscheidung in eine Einzeldatei ausgelagert.

## Wohin gehört mein Dokument? (Entscheidungsbaum)

1. **Hält es eine Entscheidung fest** (Kontext → Entscheidung → Konsequenzen)?
   → ADR in `docs/adr/` (klein und fokussiert). Den Soll-/Ist-Text dazu in
   `ARCHITECTURE.md` pflegen.
2. **Beschreibt es einen Umsetzungsweg** (Schritte, Work-Packages, Migration)?
   → Plan in `docs/plans/`. In der ADR nur **verlinken**, nicht ausbreiten.
3. **Ist es Betriebs-/Referenzdoku** (Anleitung, Threat-Model, Tests …)?
   → bleibt unter `docs/` (Wurzel).
4. **Beides (Entscheidung + Plan)?** → ADR-Kern nach `docs/adr/`, ausführlichen
   Umsetzungsteil nach `docs/plans/`, beide gegenseitig verlinken.

## Eine neue ADR festhalten (Checkliste)

- [ ] Nächste freie Nummer aus [`docs/adr/README.md`](./docs/adr/README.md) ziehen
      und dort **sofort** als Zeile eintragen (das ist die Nummern-SSoT).
- [ ] Datei aus [`docs/adr/_template.md`](./docs/adr/_template.md) anlegen:
      `docs/adr/NNNN-kebab-case-titel.md` (Dateiname vierstellig; ID im Text
      dreistellig „ADR-NNN").
- [ ] Status-Vokabular (aus `ARCHITECTURE.md` §7): `Vorgeschlagen` · `Akzeptiert`
      · `Abgelöst durch ADR-MMM` · `Verworfen`.
- [ ] Auf einschlägige ADRs verweisen (z. B. „erweitert ADR-025"), nicht duplizieren.
- [ ] Falls die Entscheidung systemrelevant ist: `ARCHITECTURE.md` an der
      passenden Stelle mit `(ADR-NNN)` verlinken.

## Eine Entscheidung ändern (Checkliste)

- [ ] Eine angenommene ADR wird **nie editiert, um sie zu ändern**.
- [ ] Stattdessen **neue ADR** mit nächster Nummer anlegen.
- [ ] Die alte ADR auf `Abgelöst durch ADR-NNN` setzen (Body bleibt, nicht löschen);
      Index-Status mitziehen.

## Konventionen & Prinzipien

- Verbindliche Projektprinzipien (Abhängigkeitsarmut ADR-001, Append-only
  ADR-006/012/015, Single-Instance ADR-002 …) und das Qualitätstor
  (`make lint` / `make test` / `make race`) stehen in
  [`SESSION_PROMPT.md`](./SESSION_PROMPT.md) §2–§5. Eine separate
  `invariants`-Datei gibt es nicht — diese Quelle ist maßgeblich.
- Commit-Stil: Conventional Commits auf Deutsch, betroffene ADR referenzieren
  (z. B. `feat(auth): … — ADR-033`).

## Verhältnis zu `SESSION_PROMPT.md`

`SESSION_PROMPT.md` ist der **kopierbare Start-Prompt** für eine Agenten-Session
(Prinzipien, Designregeln, Qualitätstor, Git-Workflow). **AGENTS.md** ist die
**Navigations-/Ablageregel** (wohin gehört was, wie halte ich eine Entscheidung
fest). Sie ergänzen sich und werden nicht dupliziert: Prinzipien stehen dort,
Ablage-/ADR-Mechanik hier. Beide verweisen für Inhalte auf `ARCHITECTURE.md`.
