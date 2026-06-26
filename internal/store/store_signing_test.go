package store

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, _, err := GenerateKey()
	if err != nil {
		t.Fatalf("gen-key: %v", err)
	}
	key, err := ParsePrivateKey(seed)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return key
}

func openSigned(t *testing.T, key ed25519.PrivateKey) *Store {
	t.Helper()
	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "s.db"), Options{SyncMode: SyncGroup, SigningKey: key})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestParsePrivateKey(t *testing.T) {
	seed, _, _ := GenerateKey()
	key, err := ParsePrivateKey(seed) // 32-Byte-Seed
	if err != nil {
		t.Fatalf("seed parsen: %v", err)
	}
	// Auch der volle 64-Byte-Key (base64) muss akzeptiert werden.
	full64 := base64.StdEncoding.EncodeToString(key)
	if _, err := ParsePrivateKey(full64); err != nil {
		t.Fatalf("64-byte-key parsen: %v", err)
	}
	if _, err := ParsePrivateKey("nicht base64!!"); err == nil {
		t.Fatal("ungültiges base64 sollte fehlschlagen")
	}
	if _, err := ParsePrivateKey("YWJj"); err == nil { // "abc" -> 3 bytes
		t.Fatal("falsche länge sollte fehlschlagen")
	}
}

func TestSignedAppendAndVerify(t *testing.T) {
	st := openSigned(t, newKey(t))

	got := appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})
	if got[0].Signature == nil || *got[0].Signature == "" {
		t.Fatal("signatur fehlt bei aktivem Signieren")
	}
	if _, ok := st.PublicKey(); !ok {
		t.Fatal("PublicKey sollte verfügbar sein")
	}
	if res, _ := st.Verify(); !res.OK || res.Count != 1 {
		t.Fatalf("verify: %+v", res)
	}
}

func TestNoSigningByDefault(t *testing.T) {
	st := openTemp(t)
	got := appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})
	if got[0].Signature != nil {
		t.Fatal("ohne Schlüssel darf keine Signatur gesetzt sein")
	}
	if _, ok := st.PublicKey(); ok {
		t.Fatal("ohne Schlüssel sollte PublicKey nicht verfügbar sein")
	}
}

func TestVerifyDetectsBadSignature(t *testing.T) {
	st := openSigned(t, newKey(t))
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})

	// Signatur des gespeicherten Events verfälschen.
	err := st.central.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketEvents)
		raw := b.Get(seqKey(1))
		var ev event.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return err
		}
		bad := "AAAA" + (*ev.Signature)[4:]
		ev.Signature = &bad
		patched, _ := json.Marshal(ev)
		return b.Put(seqKey(1), patched)
	})
	if err != nil {
		t.Fatalf("präparieren: %v", err)
	}

	res, _ := st.Verify()
	if res.OK || res.BrokenAt != "1" {
		t.Fatalf("verfälschte signatur nicht erkannt: %+v", res)
	}
}

func TestVerifyWithWrongKeyFails(t *testing.T) {
	// Mit Schlüssel A signieren ...
	keyA := newKey(t)
	path := filepath.Join(t.TempDir(), "s.db")
	st, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, SigningKey: keyA})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})
	_ = st.Close()

	// ... mit Schlüssel B verifizieren -> Signatur passt nicht.
	st2, err := OpenWithOptions(path, Options{SyncMode: SyncGroup, SigningKey: newKey(t)})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	if res, _ := st2.Verify(); res.OK {
		t.Fatal("verify mit falschem Schlüssel sollte fehlschlagen")
	}
}
