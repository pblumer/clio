# Kubernetes — Single Instance (mit deutlicher Warnung)

> ⚠️ **clio skaliert NICHT über Replicas.** clio ist Single-Instance auf einer
> bbolt-Datei (ADR-002). Zwei Pods auf demselben Volume = exklusiver Datei-Lock-
> Konflikt bzw. Datenkorruption. **`replicas` MUSS `1` bleiben.** Diese Manifeste
> verwenden bewusst ein `StatefulSet` mit `replicas: 1` und
> `strategy/updateStrategy`, die **niemals zwei Pods gleichzeitig** auf das Volume
> lassen (`OnDelete` bzw. Recreate). Kubernetes liefert hier **kein** HA/Failover —
> es ist nur ein bequemer Scheduler/Neustarter für **eine** Instanz.

Wer echte Hochverfügbarkeit braucht, ist mit clio falsch (siehe
[`docs/production-readiness.md`](../../docs/production-readiness.md)).

## Inhalt

| Datei | Zweck |
|---|---|
| `pvc.yaml` | PersistentVolumeClaims für Daten und Backups |
| `statefulset.yaml` | die **eine** clio-Instanz (`replicas: 1`, `OnDelete`) |
| `service.yaml` | ClusterIP-Service (Cluster-intern; TLS/öffentlich über Ingress) |
| `backup-cronjob.yaml` | täglicher Hot-Backup gegen den **HTTP-Endpunkt** des Service |

## Anwenden

```bash
kubectl create namespace clio
kubectl -n clio apply -f pvc.yaml -f statefulset.yaml -f service.yaml

# Erststart: Bootstrap-Admin-Key setzen (einmalig), kid aus dem Log lesen,
# benannte Keys anlegen, dann die Bootstrap-Variable entfernen (ADR-025).
kubectl -n clio logs statefulset/cliostore | grep -o 'kid_[a-z2-7]*'

# Backup-Token (Scope admin) als Secret hinterlegen, dann den CronJob anwenden:
kubectl -n clio create secret generic clio-backup --from-literal=token='kid_xxx.secret'
kubectl -n clio apply -f backup-cronjob.yaml
```

> **Backup-Hinweis:** Der CronJob ruft `GET /api/v1/backup` über den Service auf
> (in-Process Hot-Backup, ADR-030) — er öffnet **nicht** die DB-Datei direkt
> (das ginge wegen des Locks der laufenden Instanz schief). Ein roher
> CSI-`VolumeSnapshot` ist wegen bbolts Copy-on-Write meist konsistent, aber
> `GET /api/v1/backup` ist die garantiert konsistente, plattformunabhängige Variante.
