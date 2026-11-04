// Package jobs is the durable store of background knowledge indexing:
// <data>/knowledge.sqlite with its own migrations, the manifest of
// snapshots (one row per snapshot and pipeline fingerprint, which is also
// the job) and the immutable extraction results. It is separate from VEDA's
// state; the index and these jobs can be rebuilt from the store at any time.
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	_ "modernc.org/sqlite"
)

// File is the database file name in the data directory.
const File = "knowledge.sqlite"

// migrations are applied in order, each in one transaction, and recorded in
// the migrations table. Never edit a released migration; append a new one.
var migrations = []string{
	`CREATE TABLE manifest (
		source_id TEXT NOT NULL,
		path TEXT NOT NULL,
		sha TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('pending','running','succeeded','failed','superseded')),
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (source_id, path, sha, fingerprint)
	) WITHOUT ROWID;
	CREATE INDEX manifest_due ON manifest (fingerprint, state, next_attempt);
	CREATE TABLE results (
		source_id TEXT NOT NULL,
		path TEXT NOT NULL,
		sha TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		extraction TEXT NOT NULL,
		data BLOB NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (source_id, path, sha, fingerprint)
	) WITHOUT ROWID;
	CREATE INDEX results_extraction ON results (source_id, path, sha, extraction);`,
	`CREATE TABLE meta (
		name TEXT PRIMARY KEY,
		value BLOB NOT NULL,
		updated_at INTEGER NOT NULL
	) WITHOUT ROWID;`,
}

// DB is the open knowledge database. Its methods implement knowledge.Manifest.
type DB struct {
	db *sql.DB
}

var _ knowledge.Manifest = (*DB)(nil)

// Open opens or creates dir/knowledge.sqlite. dir must exist; the file is
// created 0600 and must stay a private regular file. Journaling matches the
// VEDA store (rollback journal, synchronous=EXTRA). A database migrated by a
// newer VEDA is refused.
func Open(dir string) (*DB, error) {
	p := filepath.Join(dir, File)
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		if err = f.Close(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("knowledge database must be a private regular file")
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=DELETE; PRAGMA synchronous=EXTRA; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the database; pending statements finish first.
func (d *DB) Close() error { return d.db.Close() }

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM migrations`).Scan(&current); err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("knowledge database version %d is newer than this VEDA (%d)", current, len(migrations))
	}
	for v := current + 1; v <= len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(migrations[v-1]); err == nil {
			_, err = tx.Exec(`INSERT INTO migrations (version, applied_at) VALUES (?, ?)`, v, time.Now().UnixMilli())
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("knowledge database migration %d: %w", v, err)
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Version is the applied schema version.
func (d *DB) Version(ctx context.Context) (int, error) {
	var v int
	err := d.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM migrations`).Scan(&v)
	return v, err
}

// ReadAliases reads the optional alias file dir/knowledge-aliases.json. A
// missing file gives no rules and an empty digest. The file must be a
// private regular file (no symlink): it is owner configuration.
func ReadAliases(dir string, s knowledge.Schema) (knowledge.Aliases, string, error) {
	p := filepath.Join(dir, knowledge.AliasesFile)
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return knowledge.Aliases{}, "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, "", errors.New("knowledge aliases must be a private regular file")
	}
	if info.Size() > 1<<20 {
		return nil, "", errors.New("knowledge aliases file is too large")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	return knowledge.ParseAliases(data, s)
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}
