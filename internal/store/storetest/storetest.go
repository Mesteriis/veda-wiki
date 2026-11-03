// Package storetest builds stores with data for tests in other packages.
package storetest

import (
	"database/sql"
	"maps"
	"net/url"
	"path/filepath"
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
