package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestImportIsAtomicIdempotentAndPersists(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	result, e := s.importBatch(b)
	if e != nil || result.Created != 1 {
		t.Fatal(result, e)
	}
	b = applyAgain(s, b)
	if r, e := s.importBatch(b); e != nil || r.Unchanged != 1 {
		t.Fatal(r, e)
	}
	b.Files = map[string]string{"README.md": "# Changed", "../invalid.md": "must reject"}
	if _, e := s.importBatch(b); e == nil {
		t.Fatal("invalid batch accepted")
	}
	p, e := s.page("handbook/README.md")
	if e != nil || p.Text != "# Initial" {
		t.Fatal("partial batch committed", e)
	}
	reopened, e := s.reopen(s.Store.Dir())
	if e != nil {
		t.Fatal(e)
	}
	p, e = reopened.page("handbook/README.md")
	if e != nil || p.Text != "# Initial" {
		t.Fatal(e)
	}
}

func TestFailedAssetRepairDoesNotAdvanceObservation(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	b.Assets = map[string][]byte{"image.png": smallPNG(t)}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	previous := s.observedAt(b.SourceID)
	ref := s.db().Sources[b.SourceID].Assets["image.png"]
	filename := filepath.Join(s.Store.Dir(), "blobs", ref.SHA)
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filename, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.importBatch(b); err == nil {
		t.Fatal("failed repair reported success")
	}
	if !s.observedAt(b.SourceID).Equal(previous) {
		t.Fatal("failed repair advanced observation")
	}
}

func TestConcurrentImportsRequireCurrentGeneration(t *testing.T) {
	s := newFixture(t)
	initial := testBatch()
	if _, e := s.importBatch(initial); e != nil {
		t.Fatal(e)
	}
	b := applyAgain(s, initial)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			batch := b
			batch.Files = map[string]string{"README.md": "# Change " + strings.Repeat("x", i+1)}
			_, e := s.importBatch(batch)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if !errors.Is(e, domain.ErrConflict) {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent import overwrote a newer generation", successes)
	}
}

func TestImportConflictsPreserveBothVersionsAndResolve(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("handbook", "README.md", "# Local", 1); e != nil {
		t.Fatal(e)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote"}
	b.Commit = strings.Repeat("b", 40)
	result, e := s.importBatch(b)
	if e != nil || result.Conflicts != 1 {
		t.Fatal(result, e)
	}
	p, _ := s.page("handbook/README.md")
	if p.Text != "# Local" {
		t.Fatal("local edit overwritten")
	}
	conflicts := s.Conflicts()
	if len(conflicts) != 1 || conflicts[0].Conflict.Incoming != "# Remote" {
		t.Fatal("remote edit lost")
	}
	b = applyAgain(s, b)
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if len(s.Conflicts()) != 1 {
		t.Fatal("retry duplicated conflict")
	}
	if e := s.ResolveConflict("handbook", "README.md", 0, 1, "upstream"); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale conflict resolution accepted", e)
	}
	if e := s.ResolveConflict("handbook", "README.md", 0, 2, "upstream"); e != nil {
		t.Fatal(e)
	}
	p, _ = s.page("handbook/README.md")
	if p.Text != "# Remote" || len(s.Conflicts()) != 0 {
		t.Fatal("resolution failed")
	}
	reopened, e := s.reopen(s.Store.Dir())
	if e != nil {
		t.Fatal(e)
	}
	a, e := reopened.article("handbook/README.md")
	if e != nil || len(a.Versions) != 3 || a.Versions[1].Text != "# Local" {
		t.Fatal("history lost")
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Next remote"}
	if r, e := s.importBatch(b); e != nil || r.Updated != 1 {
		t.Fatal(r, e)
	}
}

func TestImportConflictSupersessionAndUpstreamRevert(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	s.importBatch(b)
	s.put("handbook", "README.md", "# Local", 1)
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote one"}
	s.importBatch(b)
	b = applyAgain(s, b)
	b.Commit = strings.Repeat("b", 40)
	s.importBatch(b)
	if len(s.db().Articles["handbook/README.md"].Conflicts) != 1 {
		t.Fatal("unchanged incoming duplicated")
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote two"}
	s.importBatch(b)
	if len(s.Conflicts()) != 1 || len(s.db().Articles["handbook/README.md"].Conflicts) != 2 {
		t.Fatal("prior incoming history lost or still pending")
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Initial"}
	s.importBatch(b)
	if len(s.Conflicts()) != 0 {
		t.Fatal("upstream revert retained obsolete conflict")
	}
	p, _ := s.page("handbook/README.md")
	if p.Text != "# Local" {
		t.Fatal("local edit overwritten on revert")
	}
}

func TestImportDeletionsPartialCoverageAndLocalConflict(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	b.Files["other.md"] = "# Other"
	s.importBatch(b)
	b = applyAgain(s, b)
	b.Files = map[string]string{}
	b.Complete = false
	b.Withheld = 1
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if len(s.livePages()) != 2 {
		t.Fatal("partial coverage deleted articles")
	}
	s.put("handbook", "README.md", "# Local edit", 1)
	b = applyAgain(s, b)
	b.Complete = true
	b.Withheld = 0
	r, e := s.importBatch(b)
	if e != nil || r.Deleted != 1 || r.Conflicts != 1 {
		t.Fatal(r, e)
	}
	if len(s.livePages()) != 1 {
		t.Fatal("tombstone visibility")
	}
	if e := s.ResolveConflict("handbook", "README.md", 0, 2, "local"); e != nil {
		t.Fatal(e)
	}
	b = applyAgain(s, b)
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if len(s.livePages()) != 1 {
		t.Fatal("locally retained article deleted")
	}
}

func TestPrivatePartialImportRetainsUnobservedImages(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	b.Visibility = "private"
	b.Assets = map[string][]byte{"images/unobserved.png": smallPNG(t)}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	b.Assets = map[string][]byte{}
	b.Complete, b.Withheld = false, 1
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.db().Sources["handbook"].Assets["images/unobserved.png"]; !ok {
		t.Fatal("partial observation removed an image")
	}
}

func TestLargeImportUsesVersionGate(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	b.Files["README.md"] = "# Large\n" + strings.Repeat("x", domain.LegacyArticleBytes+1)
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if s.db().Version != 13 {
		t.Fatal("large import did not upgrade store")
	}
	if _, err := s.reopen(s.Store.Dir()); err != nil {
		t.Fatal("large import could not reopen", err)
	}
}

// A remote proof carries the hooks that commit its connection state with the
// content; without them the pull would be applied but never recorded.
func TestApplyRejectsProofWithoutHooks(t *testing.T) {
	s := newFixture(t)
	storetest.Mutate(t, s.Store, func(db *domain.Database) {
		db.Connections = map[string]domain.GitHubConnection{"handbook": {ID: "handbook", ConfigRevision: 1}}
	})
	proof := func() *RemoteProof {
		return &RemoteProof{ConnectionID: "handbook", ConfigRevision: 1, Visibility: "private", Record: func(*domain.Database) {}}
	}
	missingRecord := proof()
	missingRecord.Record = nil
	missingAttach := proof()
	missingAttach.Adoption = &AdoptionProof{Context: context.Background()}
	for name, p := range map[string]*RemoteProof{"record": missingRecord, "attach": missingAttach} {
		if _, err := s.Apply(testBatch(), p, false); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("proof without %s hook accepted: %v", name, err)
		}
		if _, ok := s.db().Sources["handbook"]; ok {
			t.Fatalf("proof without %s hook committed content", name)
		}
	}
	recorded := false
	complete := proof()
	complete.Record = func(*domain.Database) { recorded = true }
	if _, err := s.Apply(testBatch(), complete, false); err != nil || !recorded {
		t.Fatal("proof with its hook rejected", err)
	}
}
