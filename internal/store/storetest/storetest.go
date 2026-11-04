// Package storetest builds stores with data for tests in other packages.
package storetest

import (
	"database/sql"
	"maps"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	_ "modernc.org/sqlite"
)

// New opens an empty JSON store in a private temporary directory.
func New(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// Source commits a disconnected wiki source.
func Source(t testing.TB, st *store.Store, id, visibility string) {
	t.Helper()
	Mutate(t, st, func(db *domain.Database) {
		db.Sources[id] = domain.Source{ID: id, Title: id, Kind: "wiki", Visibility: visibility}
	})
}

// Mutate applies fn to a clone of the state and commits the whole state.
func Mutate(t testing.TB, st *store.Store, fn func(db *domain.Database)) {
	t.Helper()
	if err := st.Update(func(tx *store.Tx) error { fn(tx.DB()); return nil }); err != nil {
		t.Fatal(err)
	}
}

// Renumber commits article key with its revisions numbered consecutively
// from first, as a history whose older revisions were pruned looks. It
// remaps only outbox jobs of the article (revision number and ID); moves,
// reconciliation baselines and copy origins that name its revisions are left
// unchanged, so callers must not renumber articles they reference.
func Renumber(t testing.TB, st *store.Store, key string, first int) {
	t.Helper()
	Mutate(t, st, func(db *domain.Database) {
		a, ok := db.Articles[key]
		if !ok {
			t.Fatal("no article to renumber")
		}
		numbers := map[int]int{}
		versions := make([]domain.Revision, len(a.Versions))
		for i, r := range a.Versions {
			numbers[r.Number] = first + i
			r.Number = first + i
			versions[i] = r
		}
		a.Versions = versions
		db.Articles[key] = a
		for id, j := range db.Outbox {
			if j.SourceID+"/"+j.Path != key {
				continue
			}
			delete(db.Outbox, id)
			j.Revision = numbers[j.Revision]
			j.ID = key + "@" + strconv.Itoa(j.Revision)
			db.Outbox[j.ID] = j
		}
	})
}

// Read returns a copy of the committed state whose collections the caller may
// modify. Slices and pointers inside records are shared and must be replaced,
// not mutated in place.
func Read(t testing.TB, st *store.Store) domain.Database {
	t.Helper()
	var out domain.Database
	if err := st.View(func(db *domain.Database) error {
		out = *db
		out.Sources = maps.Clone(db.Sources)
		out.Articles = maps.Clone(db.Articles)
		out.Accounts = maps.Clone(db.Accounts)
		out.Connections = maps.Clone(db.Connections)
		out.Outbox = maps.Clone(db.Outbox)
		out.Moves = maps.Clone(db.Moves)
		out.SourceMirrors = maps.Clone(db.SourceMirrors)
		out.AccessKeys = maps.Clone(db.AccessKeys)
		out.Users = maps.Clone(db.Users)
		out.Teams = maps.Clone(db.Teams)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// SQLite opens the SQLite file of a migrated data directory directly, so a
// test can inject faults. The caller closes it.
func SQLite(t testing.TB, dir string) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, store.SQLiteFile), RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

// Corrupt runs fn on the committed in-memory state under the write lock and
// persists nothing. It exists only for tests that change live records in place
// to prove that readers copy them or to race readers against a writer; every
// other test uses Mutate.
func Corrupt(st *store.Store, fn func(db *domain.Database)) {
	_ = st.Exclusive(func(ex *store.Exclusive) error { fn(ex.DB()); return nil })
}
