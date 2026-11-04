package store_test

import (
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestScopedWhereGrantsOnlyTheEvaluatedScope(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	storetest.Source(t, st, "beta", "private")
	if view, ok, err := st.ScopedWhere(func(*domain.Database) []string { return nil }); view != nil || ok || err != nil {
		t.Fatal("nil scope granted a snapshot")
	}
	view, ok, err := st.ScopedWhere(func(*domain.Database) []string { return []string{"alpha"} })
	if err != nil || !ok {
		t.Fatal("scope rejected", err)
	}
	db := storetest.Read(t, view)
	if _, ok := db.Sources["alpha"]; !ok || len(db.Sources) != 1 {
		t.Fatal("snapshot has wrong sources")
	}
	if view.Update(func(*store.Tx) error { return nil }) == nil {
		t.Fatal("snapshot is writable")
	}
}

// The scope decision and the snapshot come from one state: a writer that
// removes the granted source while the scope is evaluated waits for the
// snapshot. sync.RWMutex blocks new readers once a writer waits, so a
// snapshot taken under a second read lock would miss the source.
func TestScopedWhereIsAtomicWithWriters(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	var wg sync.WaitGroup
	view, ok, err := st.ScopedWhere(func(db *domain.Database) []string {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := st.Update(func(tx *store.Tx) error { delete(tx.DB().Sources, "alpha"); return nil }); err != nil {
				t.Error(err)
			}
		}()
		time.Sleep(50 * time.Millisecond)
		if _, ok := db.Sources["alpha"]; ok {
			return []string{"alpha"}
		}
		return nil
	})
	wg.Wait()
	if err != nil || !ok {
		t.Fatal("scope rejected", err)
	}
	if _, present := storetest.Read(t, view).Sources["alpha"]; !present {
		t.Fatal("scope and snapshot came from different states")
	}
	if _, present := storetest.Read(t, st).Sources["alpha"]; present {
		t.Fatal("writer did not run")
	}
}
