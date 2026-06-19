// Backup, Restore und Offline-Verify (ADR-026).
//
// Clios DR-Story ist snapshot-basiert, nicht replikationsbasiert: bbolt erlaubt
// über `Tx.WriteTo` in einer Read-Transaktion (MVCC, copy-on-write) einen
// konsistenten Punkt-in-der-Zeit-Snapshot der gesamten Datei. Das Backup-Artefakt
// ist selbst eine gültige, eigenständig öffenbare bbolt-Datei, deren Hash-Kette
// (ADR-012) sich mit `verify` kryptografisch prüfen lässt — ein Backup ist also
// selbstvalidierend.
package store

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

// ErrTargetExists meldet, dass ein Restore (oder ein Offline-Backup) eine bereits
// vorhandene Zieldatei nicht ohne `--force`/overwrite überschreibt (INV-R1:
// kein stiller Verlust).
var ErrTargetExists = errors.New("ziel existiert bereits (mit --force überschreiben)")

// ErrInvalidBackup meldet, dass die als Backup angegebene Datei keine gültige
// clio-Datenbank ist (der events-Bucket fehlt).
var ErrInvalidBackup = errors.New("keine gültige clio-datenbank (events-bucket fehlt)")

// BackupResult fasst das Ergebnis eines Backups zusammen. Bytes ist die Größe des
// geschriebenen Snapshots, Events die Anzahl enthaltener Events und Head der Kopf
// der Hash-Kette — alle drei stammen aus derselben Read-Transaktion wie der
// Snapshot (INV-B1: Konsistenz).
type BackupResult struct {
	Bytes  int64  `json:"bytes"`
	Events uint64 `json:"events"`
	Head   string `json:"head"`
}

// RestoreResult fasst das Ergebnis eines Restores zusammen (Events/Head des
// wiederhergestellten Stands, aus dem Backup gelesen).
type RestoreResult struct {
	Events uint64 `json:"events"`
	Head   string `json:"head"`
}

// backupTx schreibt den Snapshot der Transaktion tx nach w und liest Count und
// Head aus derselben Transaktion (INV-B1). Gemeinsame Grundlage für das
// In-Process-Backup (Store.Backup) und das Offline-Backup (BackupFile).
func backupTx(tx *bolt.Tx, w io.Writer) (BackupResult, error) {
	res := BackupResult{Head: event.GenesisHash}
	if eb := tx.Bucket(bucketEvents); eb != nil {
		res.Events = eb.Sequence()
	}
	if mb := tx.Bucket(bucketMeta); mb != nil {
		if h := mb.Get(metaChainHead); len(h) > 0 {
			res.Head = string(h)
		}
	}
	n, err := tx.WriteTo(w)
	res.Bytes = n
	if err != nil {
		return BackupResult{}, fmt.Errorf("snapshot schreiben: %w", err)
	}
	return res, nil
}

// Backup schreibt einen konsistenten Online-Snapshot der gesamten Datenbank nach
// w. Läuft in einer Read-Transaktion des laufenden Stores und blockiert keine
// Schreiber (MVCC). Das ist der echte „Hot Backup"-Pfad — genutzt vom
// HTTP-Endpunkt `GET /api/v1/backup`.
func (s *Store) Backup(w io.Writer) (BackupResult, error) {
	var res BackupResult
	err := s.view(func(tx *bolt.Tx) error {
		r, e := backupTx(tx, w)
		res = r
		return e
	})
	if err != nil {
		return BackupResult{}, err
	}
	return res, nil
}

// BackupToFile schreibt einen Online-Snapshot atomar in eine Datei: erst in eine
// temporäre Datei im selben Verzeichnis, dann fsync, dann atomarer Rename
// (INV-B2: unter dem Zielnamen existiert nie ein halb geschriebenes Backup).
func (s *Store) BackupToFile(path string) (BackupResult, error) {
	var res BackupResult
	err := atomicWriteFile(path, func(f *os.File) error {
		r, e := s.Backup(f)
		res = r
		return e
	})
	if err != nil {
		return BackupResult{}, err
	}
	return res, nil
}

// BackupFile erstellt aus einer Datenbankdatei (dbPath) atomar einen Snapshot in
// outPath, ohne den Store-Prozess. Es öffnet die DB read-only — und benötigt
// daher, dass keine schreibende Instanz die Datei hält (bbolt hält im
// Read-Write-Modus einen exklusiven Datei-Lock). Das ist der Pfad für ein
// Cold/Offline-Backup (Server gestoppt). Für ein Hot-Backup gegen einen laufenden
// Server dient der HTTP-Endpunkt `GET /api/v1/backup` (in-Process, Store.Backup).
func BackupFile(dbPath, outPath string, overwrite bool) (BackupResult, error) {
	if !overwrite {
		if exists, err := fileExists(outPath); err != nil {
			return BackupResult{}, err
		} else if exists {
			return BackupResult{}, ErrTargetExists
		}
	}
	src, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: time.Second, ReadOnly: true})
	if err != nil {
		return BackupResult{}, fmt.Errorf("db öffnen (hält eine schreibende instanz die datei? "+
			"dann server stoppen oder GET /api/v1/backup nutzen): %w", err)
	}
	defer func() { _ = src.Close() }()

	var res BackupResult
	err = atomicWriteFile(outPath, func(f *os.File) error {
		return src.View(func(tx *bolt.Tx) error {
			r, e := backupTx(tx, f)
			res = r
			return e
		})
	})
	if err != nil {
		return BackupResult{}, err
	}
	return res, nil
}

// Restore spielt ein Backup (input) an den Zielpfad dbPath ein. Es öffnet das
// Backup read-only, validiert es (events-Bucket vorhanden), schreibt eine
// defragmentierte Kopie in eine temporäre Datei im Zielverzeichnis und ersetzt
// das Ziel atomar (temp + Rename). Eine existierende Ziel-DB wird nur mit
// overwrite überschrieben (INV-R1, sonst ErrTargetExists). Offline auszuführen:
// es darf keine laufende Instanz auf dbPath zugreifen.
func Restore(input, dbPath string, overwrite bool) (RestoreResult, error) {
	if exists, err := fileExists(dbPath); err != nil {
		return RestoreResult{}, err
	} else if exists && !overwrite {
		return RestoreResult{}, ErrTargetExists
	}

	src, err := bolt.Open(input, 0o600, &bolt.Options{Timeout: time.Second, ReadOnly: true})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backup öffnen: %w", err)
	}
	defer func() { _ = src.Close() }()

	// Validieren und Kennzahlen aus dem Backup lesen (read-only, INV-V1).
	var res RestoreResult
	if verr := src.View(func(tx *bolt.Tx) error {
		eb := tx.Bucket(bucketEvents)
		if eb == nil {
			return ErrInvalidBackup
		}
		res.Events = eb.Sequence()
		res.Head = event.GenesisHash
		if mb := tx.Bucket(bucketMeta); mb != nil {
			if h := mb.Get(metaChainHead); len(h) > 0 {
				res.Head = string(h)
			}
		}
		return nil
	}); verr != nil {
		return RestoreResult{}, verr
	}

	tmpName, err := makeTempFile(dbPath, ".clio-restore-*")
	if err != nil {
		return RestoreResult{}, err
	}
	cleanup := func() { _ = os.Remove(tmpName) }

	dst, err := bolt.Open(tmpName, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		cleanup()
		return RestoreResult{}, err
	}
	// bolt.Compact kopiert alle Buckets defragmentiert in die frische Datei.
	if err := bolt.Compact(dst, src, 0); err != nil {
		_ = dst.Close()
		cleanup()
		return RestoreResult{}, fmt.Errorf("backup kopieren: %w", err)
	}
	if err := dst.Close(); err != nil {
		cleanup()
		return RestoreResult{}, err
	}
	if err := os.Rename(tmpName, dbPath); err != nil {
		cleanup()
		return RestoreResult{}, fmt.Errorf("ziel ersetzen: %w", err)
	}
	return res, nil
}

// VerifyFile rechnet die Hash-Kette einer Datenbank-/Backup-Datei offline nach,
// ohne sie zu verändern (INV-V1: read-only Open, keine Bucket-Anlage/Backfills).
// Ist verifyKey gesetzt, werden vorhandene Event-Signaturen mitgeprüft.
func VerifyFile(path string, verifyKey ed25519.PublicKey) (VerifyResult, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, ReadOnly: true})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("db öffnen: %w", err)
	}
	defer func() { _ = db.Close() }()

	var res VerifyResult
	err = db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketEvents) == nil {
			return ErrInvalidBackup
		}
		r, e := verifyChain(tx, verifyKey)
		res = r
		return e
	})
	if err != nil {
		return VerifyResult{}, err
	}
	return res, nil
}

// atomicWriteFile schreibt über write in eine temporäre Datei im Verzeichnis von
// path, fsync't sie und benennt sie atomar auf path um. Bei einem Fehler bleibt
// path unangetastet und die temp-Datei wird entfernt.
func atomicWriteFile(path string, write func(*os.File) error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".clio-tmp-*")
	if err != nil {
		return fmt.Errorf("temp-datei anlegen: %w", err)
	}
	tmpName := tmp.Name()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("ziel ersetzen: %w", err)
	}
	return nil
}

// makeTempFile legt eine leere temp-Datei im Verzeichnis von neighbor an und gibt
// ihren Namen zurück (sie wird sofort wieder geschlossen, damit bbolt sie selbst
// öffnen kann).
func makeTempFile(neighbor, pattern string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(neighbor), pattern)
	if err != nil {
		return "", fmt.Errorf("temp-datei anlegen: %w", err)
	}
	name := tmp.Name()
	_ = tmp.Close()
	return name, nil
}

// fileExists meldet, ob path existiert. Ein anderer stat-Fehler (z. B. fehlende
// Rechte) wird durchgereicht statt als „existiert nicht" interpretiert.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
