# Windows-Betrieb

Die vollständige Schritt-für-Schritt-Anleitung (Installation, Dienst via NSSM
oder Bordmittel, Firewall, Reverse Proxy / HTTPS, Backup, Wartung, Updates) steht
unter [**docs/windows-server-2022.md**](../../docs/windows-server-2022.md).

Diese Mappe enthält ergänzend:

- [`clio-backup.ps1`](clio-backup.ps1) — Hot-Backup über den HTTP-Endpunkt
  (ADR-031) + `verify` + Rotation, für die Windows-Aufgabenplanung.

Empfohlene Umgebungsvariablen je Betriebsprofil:
[`docs/operations-profiles.md`](../../docs/operations-profiles.md).

> **Hinweis:** Das CLI `cliostore backup --db` kann eine **laufende** Instanz
> nicht öffnen (exklusiver bbolt-Datei-Lock). Für ein Backup im laufenden Betrieb
> immer den HTTP-Endpunkt nutzen (so wie `clio-backup.ps1`), oder den Dienst kurz
> stoppen für ein Cold-Backup.
