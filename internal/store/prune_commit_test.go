package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// backendStore opens an empty store with source "local" on the named backend.
func backendStore(t *testing.T, backend string) *store.Store {
	t.Helper()
	st := storetest.New(t)
	storetest.Source(t, st, "local", "private")
	if backend == "json" {
		return st
	}
	if err := store.MigrateSQLite(st.Dir()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// writeRevision appends the next revision of local/<p> in a transaction
// limited to that article, as an article write does.
func writeRevision(t *testing.T, st *store.Store, p string) {
	t.Helper()
	key := "local/" + p
	if err := st.Update(func(tx *store.Tx) error {
		a := tx.DB().Articles[key]
		a.SourceID, a.Path = "local", p
		n := domain.LatestRevision(a) + 1
		text := "# Revision " + strconv.Itoa(n)
		a.Versions = append(slices.Clone(a.Versions), domain.Revision{Number: n, Text: text, SHA: domain.Digest([]byte(text))})
		tx.DB().Articles[key] = a
		tx.OnlyArticle(key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func revisionNumbers(a domain.Article) []int {
	var out []int
	for _, r := range a.Versions {
		out = append(out, r.Number)
	}
	return out
}

// numbersInMemoryAndOnDisk returns the revision numbers of key in the
// committed state and after reopening the directory; they must agree.
func numbersInMemoryAndOnDisk(t *testing.T, st *store.Store, key string) []int {
	t.Helper()
	memory := revisionNumbers(storetest.Read(t, st).Articles[key])
	reopened, err := store.Open(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if disk := revisionNumbers(storetest.Read(t, reopened).Articles[key]); !slices.Equal(memory, disk) {
		t.Fatalf("memory %v differs from disk %v", memory, disk)
	}
	return memory
}

func TestNewStoreUsesFormat21(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			st := backendStore(t, backend)
			if v := storetest.Read(t, st).Version; v != 21 {
				t.Fatal("new store version", v)
			}
			reopened, err := store.Open(st.Dir())
			if err != nil || storetest.Read(t, reopened).Version != 21 {
				t.Fatal("reopened store is not v21", err)
			}
		})
	}
	if err := store.Validate(&domain.Database{Version: 22, Sources: map[string]domain.Source{}, Articles: map[string]domain.Article{}}); err == nil {
		t.Fatal("future store format accepted")
	}
}

func TestCommitKeepsThreeNewestRevisions(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			st := backendStore(t, backend)
			for range 5 {
				writeRevision(t, st, "guide.md")
			}
			if got := numbersInMemoryAndOnDisk(t, st, "local/guide.md"); !slices.Equal(got, []int{3, 4, 5}) {
				t.Fatal("kept revisions", got)
			}
			storetest.Mutate(t, st, func(db *domain.Database) {
				appendTo(db, "local/guide.md", 6)
			})
			if got := numbersInMemoryAndOnDisk(t, st, "local/guide.md"); !slices.Equal(got, []int{4, 5, 6}) {
				t.Fatal("full commit kept", got)
			}
		})
	}
}

func appendTo(db *domain.Database, key string, n int) {
	a := db.Articles[key]
	text := "# Revision " + strconv.Itoa(n)
	a.Versions = append(slices.Clone(a.Versions), domain.Revision{Number: n, Text: text, SHA: domain.Digest([]byte(text))})
	db.Articles[key] = a
}

// protectedHistory gives local/held.md five revisions with revision 1 held by
// an active outbox job, so pruning keeps 1, 3, 4, 5.
func protectedHistory(t *testing.T, st *store.Store) {
	t.Helper()
	writeRevision(t, st, "held.md")
	storetest.Mutate(t, st, func(db *domain.Database) {
		r := db.Articles["local/held.md"].Versions[0]
		db.Outbox["held"] = domain.SyncJob{ID: "held", SourceID: "local", Path: "held.md", Revision: 1, SHA: r.SHA, State: "pending"}
	})
	for range 4 {
		writeRevision(t, st, "held.md")
	}
	if got := numbersInMemoryAndOnDisk(t, st, "local/held.md"); !slices.Equal(got, []int{1, 3, 4, 5}) {
		t.Fatal("protected history", got)
	}
}

// A narrow commit prunes only its article: another article whose protection
// the same transaction lifts keeps its history until its own next write.
func TestArticleCommitPrunesOnlyItsArticle(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			st := backendStore(t, backend)
			protectedHistory(t, st)
			completeHeldJob(t, st)
			if got := numbersInMemoryAndOnDisk(t, st, "local/held.md"); !slices.Equal(got, []int{1, 3, 4, 5}) {
				t.Fatal("narrow commit pruned another article", got)
			}
			writeRevision(t, st, "held.md")
			if got := numbersInMemoryAndOnDisk(t, st, "local/held.md"); !slices.Equal(got, []int{4, 5, 6}) {
				t.Fatal("next write kept", got)
			}
		})
	}
}

func TestFullCommitPrunesRevisionOfCompletedJob(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			st := backendStore(t, backend)
			protectedHistory(t, st)
			storetest.Mutate(t, st, func(db *domain.Database) {
				job := db.Outbox["held"]
				job.State = "complete"
				db.Outbox["held"] = job
			})
			if got := numbersInMemoryAndOnDisk(t, st, "local/held.md"); !slices.Equal(got, []int{3, 4, 5}) {
				t.Fatal("completed job still protects", got)
			}
		})
	}
}

// completeHeldJob completes the job protecting local/held.md in a commit
// limited to another article, which leaves held.md unpruned.
func completeHeldJob(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.Update(func(tx *store.Tx) error {
		job := tx.DB().Outbox["held"]
		job.State = "complete"
		tx.DB().Outbox["held"] = job
		appendTo(tx.DB(), "local/other.md", 1)
		a := tx.DB().Articles["local/other.md"]
		a.SourceID, a.Path = "local", "other.md"
		tx.DB().Articles["local/other.md"] = a
		tx.OnlyArticle("local/other.md")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionsCommitDoesNotPrune(t *testing.T) {
	st := backendStore(t, "sqlite")
	protectedHistory(t, st)
	completeHeldJob(t, st)
	if err := st.Update(func(tx *store.Tx) error {
		tx.DB().Connections["handbook"] = domain.GitHubConnection{ID: "handbook", Repository: "org/handbook", RepositoryID: 1, Branch: "main", State: "idle", ConfigRevision: 1}
		tx.OnlyConnections()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := numbersInMemoryAndOnDisk(t, st, "local/held.md"); !slices.Equal(got, []int{1, 3, 4, 5}) {
		t.Fatal("connections commit pruned an article", got)
	}
}

// Stores below format 21 are not pruned by commits; migrate-revisions
// converts them.
func TestCommitDoesNotPruneOlderFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(domain.Database{Version: 20, Sources: map[string]domain.Source{"local": {ID: "local", Title: "local", Kind: "wiki", Visibility: "private"}}, Articles: map[string]domain.Article{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		writeRevision(t, st, "guide.md")
	}
	storetest.Mutate(t, st, func(*domain.Database) {})
	if got := numbersInMemoryAndOnDisk(t, st, "local/guide.md"); !slices.Equal(got, []int{1, 2, 3, 4, 5}) {
		t.Fatal("v20 store was pruned", got)
	}
	if v := storetest.Read(t, st).Version; v != 20 {
		t.Fatal("commit changed the store format", v)
	}
}
