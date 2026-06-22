# Implementierungspläne / Work-Packages

Dieses Verzeichnis enthält **Umsetzungspläne** — das *Wie* (Work-Packages mit
Akzeptanzkriterien, Etappen, Migrationsschritten). Sie sind von den
**Entscheidungen** ([`../adr/`](../adr/) — das *Warum*) und vom aktuellen
Soll-/Ist-Zustand ([`../../ARCHITECTURE.md`](../../ARCHITECTURE.md) — das *Was
gilt heute*) getrennt. Die Einordnung der drei Quellen steht in der
[`AGENTS.md`](../../AGENTS.md).

Ein Plan setzt typischerweise **eine** ADR um und verlinkt auf sie. Pläne dürfen
fortgeschrieben oder als „umgesetzt" markiert werden; die zugehörige Entscheidung
bleibt in der ADR.

| Plan | Bezug (ADR) | Status (laut Dokument) |
|---|---|---|
| [`storage-scaling-plan.md`](./storage-scaling-plan.md) | Storage-Scaling | Umgesetzt (Etappen 1–3) |
| [`security-api-keys-plan.md`](./security-api-keys-plan.md) | ADR-025 | Planung (umgesetzt) |
| [`activity-presence-plan.md`](./activity-presence-plan.md) | ADR-030 | Planung (umgesetzt) |
| [`backup-restore-dr-concept.md`](./backup-restore-dr-concept.md) | ADR-031 | Stufe 1 umgesetzt, Stufe 2 offen |
| [`web-ui-modularization-plan.md`](./web-ui-modularization-plan.md) | ADR-020 | Vorschlag, nicht umgesetzt |
