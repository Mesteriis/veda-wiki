// Package store owns the VEDA data directory: one lock over the in-memory
// database and durable commits to the JSON or SQLite backend. Readers use
// View; writers use Update, which commits a validated clone or nothing.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

const maxStoreBytes = 64 << 20

// One server process owns the data directory. All writes are serialized and
// persisted before becoming visible. Whole-state transactions keep article
// history and the sync outbox in one durability boundary.
type Store struct {
	mu       sync.RWMutex
	dir      string
	db       domain.Database
	sqlite   bool
	invalid  bool
	readOnly bool
	// blobMu serializes WriteBlobs with a blob collection sweep.
	blobMu sync.Mutex
	// blobsCollected is when blob collection last ran; clock replaces
	// time.Now in tests.
	blobsCollected time.Time
	clock          func() time.Time
}

var errReadOnly = errors.New("read-only store")

// PrivateDataDir creates dir if needed and requires it to be private (0700).
func PrivateDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("data directory must be private (0700)")
	}
	return nil
}

// Open loads the store in dir, creating a private empty directory if needed.
//
// Open has a side effect: when it loads an existing v21 store, it runs a
// blob collection, which deletes unreferenced image files (see CollectBlobs).
// The opened store is writable, so callers must hold the data directory's
// writer lock (writer.lock, as internal/app and MigrateSQLite do) before
// calling Open; collection then runs only in the process that owns the
// directory. Tests open private temporary directories.
//
// Open refuses a store below format 21 (domain.RevisionsFormat): such a store
// is converted offline by "veda migrate-revisions" (MigrateRevisions).
func Open(dir string) (*Store, error) {
	s, found, err := read(dir)
	if err != nil || !found {
		return s, err
	}
	if v := s.db.Version; v >= 1 && v < domain.RevisionsFormat {
		return nil, fmt.Errorf("store format v%d requires \"veda migrate-revisions\"", v)
	}
	if err = Validate(&s.db); err != nil {
		return nil, err
	}
	s.collectBlobsDue()
	return s, nil
}

// read loads the store in dir in any supported format without validating it
// or collecting blobs. Without state.json it returns an empty v21 store and
// found is false.
func read(dir string) (s *Store, found bool, err error) {
	if err = PrivateDataDir(dir); err != nil {
		return nil, false, err
	}
	s = &Store{dir: dir, db: domain.Database{Version: domain.RevisionsFormat, Sources: map[string]domain.Source{}, Articles: map[string]domain.Article{}}}
	if info, e := os.Lstat(path.Join(dir, "state.json")); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, false, errors.New("store must be a private regular file")
	}
	f, err := os.OpenFile(path.Join(dir, "state.json"), os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return s, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxStoreBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxStoreBytes {
		return nil, false, errors.New("store too large")
	}
	if err = json.Unmarshal(b, &s.db); err != nil {
		return nil, false, err
	}
	if s.db.Version == 14 {
		if err = s.loadSQLite(b); err != nil {
			return nil, false, err
		}
	}
	return s, true, nil
}

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }

// IsSQLite reports whether the store persists to SQLite records.
func (s *Store) IsSQLite() bool { return s.sqlite }

// Invalid reports whether a failed SQLite commit could not be recovered; the
// store then rejects every commit until the process restarts.
func (s *Store) Invalid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.invalid
}

// clone copies every collection map; values are shared, so callers replace
// slices and pointers instead of mutating them in place.
func clone(src *domain.Database) domain.Database {
	db := *src
	db.Sources = make(map[string]domain.Source, len(src.Sources))
	db.Articles = make(map[string]domain.Article, len(src.Articles))
	db.Accounts = make(map[string]domain.GitHubAccount, len(src.Accounts))
	for k, v := range src.Accounts {
		db.Accounts[k] = v
	}
	db.Connections = make(map[string]domain.GitHubConnection, len(src.Connections))
	for k, v := range src.Connections {
		db.Connections[k] = v
	}
	db.Outbox = make(map[string]domain.SyncJob, len(src.Outbox))
	for k, v := range src.Outbox {
		db.Outbox[k] = v
	}
	db.Moves = make(map[string]domain.Move, len(src.Moves))
	for k, v := range src.Moves {
		db.Moves[k] = v
	}
	db.SourceMirrors = make(map[string]domain.SourceMirror, len(src.SourceMirrors))
	for k, v := range src.SourceMirrors {
		v.BlockedPaths = append([]string(nil), v.BlockedPaths...)
		db.SourceMirrors[k] = v
	}
	db.AccessKeys = make(map[string]domain.AccessKey, len(src.AccessKeys))
	for k, v := range src.AccessKeys {
		db.AccessKeys[k] = v
	}
	db.Users = make(map[string]domain.WikiUser, len(src.Users))
	for k, v := range src.Users {
		db.Users[k] = v
	}
	db.Teams = make(map[string]domain.WikiTeam, len(src.Teams))
	for k, v := range src.Teams {
		db.Teams[k] = v
	}
	for k, v := range src.Sources {
		db.Sources[k] = v
	}
	for k, v := range src.Articles {
		db.Articles[k] = v
	}
	return db
}

// WritePrivateFile atomically replaces dir/name with b, creating dir with
// private permissions if needed, and syncs the directory.
func WritePrivateFile(dir, name string, b []byte) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("private storage unavailable")
	}
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), filepath.Join(dir, name))
	}
	if e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
