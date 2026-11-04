package importer

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestObservationsScopedCopyIsFrozenAndFiltered(t *testing.T) {
	var nilObservations *Observations
	if _, ok := nilObservations.Observed("alpha"); ok {
		t.Fatal("nil observations reported a time")
	}
	if _, ok := nilObservations.Scoped([]string{"alpha"}).Withheld("alpha"); ok {
		t.Fatal("scoped nil observations reported a count")
	}
	o := &Observations{}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	o.MarkObserved("alpha", at)
	o.MarkWithheld("alpha", 2)
	o.MarkObserved("beta", at)
	o.MarkWithheld("beta", 3)
	scoped := o.Scoped([]string{"alpha", "missing"})
	o.MarkObserved("alpha", at.Add(time.Hour))
	o.MarkWithheld("alpha", 5)
	if got, ok := scoped.Observed("alpha"); !ok || !got.Equal(at) {
		t.Fatal("scoped copy followed a later observation", got)
	}
	if n, ok := scoped.Withheld("alpha"); !ok || n != 2 {
		t.Fatal("scoped copy followed a later count", n)
	}
	if _, ok := scoped.Observed("beta"); ok {
		t.Fatal("scoped copy included another source")
	}
	if _, ok := scoped.Withheld("missing"); ok {
		t.Fatal("scoped copy invented a count")
	}
	o.Forget("alpha")
	if _, ok := o.Observed("alpha"); ok {
		t.Fatal("forgotten source still observed")
	}
	if _, ok := o.Withheld("alpha"); ok {
		t.Fatal("forgotten source still counted")
	}
}

func TestObservationsAreSafeForConcurrentUse(t *testing.T) {
	o := &Observations{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("source-%d", i%2)
			for j := 0; j < 100; j++ {
				o.MarkObserved(id, time.Now())
				o.MarkWithheld(id, j)
				o.Observed(id)
				o.Withheld(id)
				o.Scoped([]string{id})
			}
		}(i)
	}
	wg.Wait()
}

func TestImportObservationFollowsTheCommit(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	b.Complete, b.Withheld = false, 2
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	observed := s.observedAt(b.SourceID)
	if n, ok := s.Observations.Withheld(b.SourceID); observed.IsZero() || !ok || n != 2 {
		t.Fatal("successful import not observed", observed, n)
	}
	if err := store.MigrateSQLite(s.Store.Dir()); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.reopen(s.Store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reopened.Observations = s.Observations
	db := storetest.SQLite(t, s.Store.Dir())
	if _, err = db.Exec("DROP TABLE records"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	b = applyAgain(reopened, b)
	b.Files = map[string]string{"README.md": "# Changed"}
	b.Withheld = 1
	if _, err := reopened.importBatch(b); err == nil {
		t.Fatal("failed commit reported success")
	}
	if n, _ := s.Observations.Withheld(b.SourceID); !s.observedAt(b.SourceID).Equal(observed) || n != 2 {
		t.Fatal("failed commit advanced the observation", n)
	}
}
