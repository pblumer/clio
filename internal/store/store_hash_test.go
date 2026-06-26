package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/pblumer/clio/internal/event"
)

func TestHashChainLinks(t *testing.T) {
	st := openTemp(t)
	got := appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t1", Data: []byte(`{"k":1}`)},
		event.Candidate{Source: "s", Subject: "/a", Type: "t2"},
		event.Candidate{Source: "s", Subject: "/b", Type: "t3"},
	)

	if got[0].PredecessorHash != event.GenesisHash {
		t.Fatalf("erstes predecessorhash = %q, want Genesis", got[0].PredecessorHash)
	}
	for i := 1; i < len(got); i++ {
		if got[i].PredecessorHash != got[i-1].Hash {
			t.Fatalf("event[%d].predecessorhash verweist nicht auf den Vorgänger", i)
		}
	}
	for i, ev := range got {
		if ev.Hash == "" || ev.Hash != event.ComputeHash(ev) {
			t.Fatalf("event[%d].hash inkonsistent", i)
		}
		if ev.Signature != nil {
			t.Fatalf("event[%d].signature sollte null sein (Integritäts-Modus)", i)
		}
	}
}

func TestVerifyOK(t *testing.T) {
	st := openTemp(t)
	got := appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t1"},
		event.Candidate{Source: "s", Subject: "/a", Type: "t2"},
	)
	res, err := st.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Count != 2 {
		t.Fatalf("verify = %+v, want ok=true count=2", res)
	}
	if res.Head != got[len(got)-1].Hash {
		t.Fatalf("head = %q, want %q", res.Head, got[len(got)-1].Hash)
	}
}

func TestVerifyEmpty(t *testing.T) {
	st := openTemp(t)
	res, err := st.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.OK || res.Count != 0 || res.Head != event.GenesisHash {
		t.Fatalf("leerer store: %+v", res)
	}
}

func TestVerifyDetectsTamper(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st,
		event.Candidate{Source: "s", Subject: "/a", Type: "t1"},
		event.Candidate{Source: "s", Subject: "/a", Type: "t2"},
	)

	// Event #2 direkt verändern, ohne den Hash neu zu berechnen.
	err := st.central.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketEvents)
		raw := b.Get(seqKey(2))
		var ev event.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return err
		}
		ev.Type = "manipuliert"
		patched, _ := json.Marshal(ev)
		return b.Put(seqKey(2), patched)
	})
	if err != nil {
		t.Fatalf("präparieren: %v", err)
	}

	res, err := st.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.OK || res.BrokenAt != "2" {
		t.Fatalf("manipulation nicht erkannt: %+v", res)
	}
}

func TestVerifyDetectsHeadMismatch(t *testing.T) {
	st := openTemp(t)
	appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t"})

	err := st.central.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMeta).Put(metaChainHead, []byte("deadbeef"))
	})
	if err != nil {
		t.Fatalf("präparieren: %v", err)
	}

	res, _ := st.Verify()
	if res.OK {
		t.Fatalf("head-mismatch nicht erkannt: %+v", res)
	}
}

func TestHashChainAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	first := appendAll(t, st, event.Candidate{Source: "s", Subject: "/a", Type: "t1"})
	_ = st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = st2.Close() })

	// Nach Reopen muss die Kette beim persistierten Kopf weitergehen.
	more, err := st2.Append([]event.Candidate{{Source: "s", Subject: "/a", Type: "t2"}}, nil)
	if err != nil {
		t.Fatalf("append nach reopen: %v", err)
	}
	if more[0].PredecessorHash != first[0].Hash {
		t.Fatalf("kette nach reopen unterbrochen: pred=%q want=%q", more[0].PredecessorHash, first[0].Hash)
	}
	if res, _ := st2.Verify(); !res.OK || res.Count != 2 {
		t.Fatalf("verify nach reopen: %+v", res)
	}
}
