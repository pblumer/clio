package store

import (
	"errors"
	"fmt"
	"os"
	"sync"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/partition"
)

// eventBuckets liegen je Partition in der eigenen Partitionsdatei (ADR-037):
// Events, Indizes, Zähler und der Ketten-Kopf (im meta-Bucket) sind per-Partition.
var eventBuckets = [][]byte{
	bucketEvents, bucketSubjectIdx, bucketTypeIdx, bucketMeta, bucketTypes, bucketSubjCount, bucketDataIdx,
}

// allBuckets ist die Bucket-Menge der Partition 0 (zugleich Träger der zentralen
// Buckets): die Event-Buckets PLUS die zentralen, partitionsübergreifenden Buckets
// (Schemas, Schlüsselbund, Audit-Log). Reihenfolge bewusst identisch zur
// historischen, nicht-partitionierten Ablage, damit eine frische Partition-0-Datei
// dasselbe Bucket-Layout erhält wie bisher (n=1 verhaltensgleich).
var allBuckets = [][]byte{
	bucketEvents, bucketSubjectIdx, bucketTypeIdx, bucketMeta, bucketTypes, bucketSubjCount,
	bucketSchemas, bucketDataIdx, bucketAuthKeys, bucketAuditLog,
}

// centralBuckets sind die partitionsübergreifenden Buckets — sie leben einmalig in
// der Datei der Partition 0, nicht je Partition: mutable Steuerdaten (Schlüsselbund
// ADR-025), append-only Audit-Log (ADR-032) und die Event-Schemas (ADR-014, global
// je Typ registriert).
var centralBuckets = [][]byte{bucketSchemas, bucketAuthKeys, bucketAuditLog}

// shard kapselt den Speicher EINER Partition (ADR-034/037, file-per-partition):
// eine eigene bbolt-Datei mit den Event-Buckets, eigener bbolt-Sequenz und eigener
// Hash-Kette (eigener Ketten-Kopf im meta-Bucket). Die Partition 0 trägt zusätzlich
// die zentralen Buckets (siehe openShard).
type shard struct {
	id partition.ID

	// dbMu schützt db gegen den Austausch beim Online-Reopen (Compaction/Grow,
	// ADR-015). Jeder Zugriff hält RLock für die Dauer seiner Transaktion; der
	// Reopen nimmt Lock exklusiv. Nie reentrant — wie beim bisherigen Single-DB.
	dbMu sync.RWMutex
	db   *bolt.DB

	initialMmapSize int
	syncMode        SyncMode
	pageSize        int

	lastCompactMu sync.Mutex
	lastCompact   *CompactionInfo
}

// partitionPath bildet den Dateipfad einer Partition: Partition 0 liegt unter dem
// Basis-Pfad (zugleich zentrale Datei, n=1 byte-identisch zum bisherigen Layout);
// jede weitere Partition in einer Geschwisterdatei `<base>.p<id>` (ADR-037).
func partitionPath(base string, id partition.ID) string {
	if id == 0 {
		return base
	}
	return fmt.Sprintf("%s.p%d", base, id)
}

// openShard öffnet (oder erstellt) die Datei der Partition id und legt die nötigen
// Buckets an. Die Partition 0 (central == true) bekommt zusätzlich die zentralen
// Buckets. Backfills laufen je Partition idempotent.
func openShard(base string, id partition.ID, opts Options, central bool) (*shard, error) {
	p := partitionPath(base, id)
	db, err := openBolt(p, opts.InitialMmapSize, opts.SyncMode)
	if err != nil {
		return nil, err
	}
	buckets := eventBuckets
	if central {
		buckets = allBuckets
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		if err := backfillTypeCounts(tx); err != nil {
			return err
		}
		if err := backfillTypeIdx(tx); err != nil {
			return err
		}
		if err := backfillSubjCount(tx); err != nil {
			return err
		}
		return backfillDataIdx(tx, normalizeDataIdxFields(opts.DataIndexFields))
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("buckets anlegen (partition %d): %w", id, err)
	}
	return &shard{
		id:              id,
		db:              db,
		initialMmapSize: opts.InitialMmapSize,
		syncMode:        opts.SyncMode,
		pageSize:        db.Info().PageSize, // sicher: noch keine Nebenläufigkeit
	}, nil
}

// view/update/batch führen eine bbolt-Transaktion auf der Partitionsdatei aus und
// halten dabei den Reopen-Guard (RLock). Sie dürfen NICHT verschachtelt werden.
func (sh *shard) view(fn func(*bolt.Tx) error) error {
	sh.dbMu.RLock()
	defer sh.dbMu.RUnlock()
	return sh.db.View(fn)
}

func (sh *shard) update(fn func(*bolt.Tx) error) error {
	sh.dbMu.RLock()
	defer sh.dbMu.RUnlock()
	return sh.db.Update(fn)
}

func (sh *shard) batch(fn func(*bolt.Tx) error) error {
	sh.dbMu.RLock()
	defer sh.dbMu.RUnlock()
	return sh.db.Batch(fn)
}

// write führt eine Schreibtransaktion gemäß SyncMode aus (Group Commit im
// SyncGroup-Modus, ADR-009). Die Funktion kann bei Group-Commit-Coalescing mehrfach
// laufen und muss daher idempotent sein.
func (sh *shard) write(fn func(*bolt.Tx) error) error {
	if sh.syncMode == SyncGroup {
		return sh.batch(fn)
	}
	return sh.update(fn)
}

func (sh *shard) path() string {
	sh.dbMu.RLock()
	defer sh.dbMu.RUnlock()
	return sh.db.Path()
}

func (sh *shard) close() error {
	sh.dbMu.Lock()
	defer sh.dbMu.Unlock()
	return sh.db.Close()
}

// size liefert die Dateigröße dieser Partition in Bytes.
func (sh *shard) size() (int64, error) {
	info, err := os.Stat(sh.path())
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// stats liefert die Speicherbelegung dieser Partitionsdatei (siehe DBStats). Eine
// RLock über die ganze Berechnung hält den db-Pointer stabil und vermeidet
// verschachteltes RLock (daher os.Stat hier inline). db.Info() würde ohne mmaplock
// gegen einen remap-auslösenden Write racen — deshalb die gecachte pageSize.
func (sh *shard) stats() (DBStats, error) {
	sh.dbMu.RLock()
	defer sh.dbMu.RUnlock()

	info, err := os.Stat(sh.db.Path())
	if err != nil {
		return DBStats{}, err
	}
	fileBytes := info.Size()

	var dataBytes int64
	if err := sh.db.View(func(tx *bolt.Tx) error {
		dataBytes = tx.Size()
		return nil
	}); err != nil {
		return DBStats{}, err
	}
	st := sh.db.Stats()
	freeBytes := int64(st.FreeAlloc)
	if freeBytes > dataBytes {
		freeBytes = dataBytes
	}
	used := dataBytes - freeBytes
	var fill float64
	if dataBytes > 0 {
		fill = float64(used) / float64(dataBytes) * 100
	}
	return DBStats{
		FileBytes:   fileBytes,
		DataBytes:   dataBytes,
		UsedBytes:   used,
		FreeBytes:   freeBytes,
		FillPercent: fill,
		FreePages:   st.FreePageN + st.PendingPageN,
		PageSize:    sh.pageSize,
	}, nil
}

// reopen schließt die Partitionsdatei, lässt optional mutate sie verändern
// (defragmentieren/ersetzen) und öffnet sie unter denselben Optionen neu — alles
// unter dem exklusiven Reopen-Guard (ADR-015). Identisch zur bisherigen
// Single-DB-Logik, nur auf eine Partition bezogen.
func (sh *shard) reopen(mutate func(path string) error) error {
	sh.dbMu.Lock()
	defer sh.dbMu.Unlock()

	path := sh.db.Path()
	if err := sh.db.Close(); err != nil {
		return fmt.Errorf("db schließen: %w", err)
	}

	if mutate != nil {
		if mErr := mutate(path); mErr != nil {
			db, oErr := openBolt(path, sh.initialMmapSize, sh.syncMode)
			if oErr != nil {
				return errors.Join(mErr, fmt.Errorf("wiederöffnen: %w", oErr))
			}
			sh.db = db
			sh.pageSize = db.Info().PageSize
			return mErr
		}
	}

	db, err := openBolt(path, sh.initialMmapSize, sh.syncMode)
	if err != nil {
		return fmt.Errorf("wiederöffnen: %w", err)
	}
	sh.db = db
	sh.pageSize = db.Info().PageSize
	return nil
}
