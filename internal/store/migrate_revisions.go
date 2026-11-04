package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// MigrationReport counts what MigrateRevisions kept and removed. It holds
// counts only: no paths, titles, texts or digests.
type MigrationReport struct {
	Articles, KeptRevisions, RemovedRevisions int
}

// revisionBackups are the files MigrateRevisions writes before it changes the
// store: a copy of state.sqlite and of state.json (the backend marker) for
// SQLite, or of state.json for JSON. Restoring copies each back to state.*.
var revisionBackups = []string{"state.before-revisions.sqlite", "state.before-revisions.json"}

var (
	errMigrationInUse   = errors.New("data directory already in use: stop the VEDA service before migrate-revisions")
	errMigrationBackup  = errors.New("state.before-revisions.* already exists: a previous migrate-revisions completed or was interrupted. If the service starts, the migration is done and the backup may be removed once it is no longer needed; otherwise restore the store from the backup (copy each state.before-revisions.X back to state.X) or from the PBS snapshot")
	errMigrationDone    = errors.New("store is already format v21: nothing to migrate")
	errMigrationNoStore = errors.New("no store in the data directory")
	errMigrationLayout  = errors.New("store is in an inconsistent state (revision tables do not match the store format): restore it from state.before-revisions.* or the PBS snapshot")
	errMigrationJournal = errors.New("SQLite journal present: the store was not closed cleanly; nothing was changed")
)

// migrateRevisionsFault lets tests fail a step after the backup is written:
// "apply" before the store is changed, "verify" after it is committed.
var migrateRevisionsFault = func(step string) error { return nil }

// MigrateRevisions converts a stopped data directory below format 21 to
// format 21 (domain.RevisionsFormat): it binds images to revisions, prunes
// history (domain.PruneAll) and, on SQLite, moves revision texts into their
// own rows, all in one transaction. It refuses an active writer, an existing
// backup, an invalid, inconsistent or already converted store, and changes
// nothing before a durable backup exists. After committing it rereads the
// store from disk and checks it; a failure then leaves the backup in place
// for the operator to restore. It never deletes or restores a backup and does
// not collect blobs: collection stays paused while the backup exists.
func MigrateRevisions(dir string) (MigrationReport, error) {
	var none MigrationReport
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return none, errors.New("data directory must be private")
	}
	lock, err := os.OpenFile(filepath.Join(dir, "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return none, errors.New("writer lock unavailable: " + scrub(err))
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return none, errMigrationInUse
	}
	if revisionBackupPresent(dir) {
		return none, errMigrationBackup
	}
	s, found, err := read(dir)
	switch {
	case errors.Is(err, errSQLiteLayout):
		return none, errMigrationLayout
	case err != nil:
		return none, errors.New("store cannot be loaded, nothing was changed: " + scrub(err))
	case !found:
		return none, errMigrationNoStore
	case s.db.Version >= domain.RevisionsFormat:
		return none, errMigrationDone
	}
	if err = Validate(&s.db); err != nil {
		return none, errors.New("store is invalid, nothing was changed: " + scrub(err))
	}
	if s.sqlite && journalPresent(dir) {
		return none, errMigrationJournal
	}
	original := s.db
	plan, err := planRevisions(original, time.Now())
	if err != nil {
		return none, errors.New("cannot convert the store, nothing was changed: " + err.Error())
	}
	if err = verifyRevisionBlobs(s, plan.db); err != nil {
		return none, errors.New("a revision references a missing or damaged image, nothing was changed")
	}
	if err = backupStore(dir, s.sqlite); err != nil {
		return none, errors.New("backup failed, the store was not changed: " + scrub(err))
	}
	if err = migrateRevisionsFault("apply"); err == nil {
		if s.sqlite {
			err = migrateSQLiteRows(dir, plan.db, original.Version)
		} else {
			err = s.writeJSON(plan.db)
		}
	}
	if err == nil {
		err = migrateRevisionsFault("verify")
	}
	if err == nil {
		err = verifyMigration(dir, s.sqlite, original, plan)
	}
	if err == nil {
		err = syncDir(dir)
	}
	if err != nil {
		return none, fmt.Errorf("migrate-revisions failed after the backup was written: %s. The previous store is intact in state.before-revisions.*; do not start the service until the store is restored from it (copy each state.before-revisions.X back to state.X) or from the PBS snapshot", scrub(err))
	}
	return plan.report, nil
}

// scrub returns err's message without the file path an OS error carries.
func scrub(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Op + ": " + pathErr.Err.Error()
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Op + ": " + linkErr.Err.Error()
	}
	return err.Error()
}

// journalPresent reports whether SQLite left a rollback or write-ahead
// journal next to the store; reading the store rolls back a hot journal, so
// one that remains means the copy would not be consistent.
func journalPresent(dir string) bool {
	for _, suffix := range []string{"-journal", "-wal"} {
		if _, err := os.Lstat(filepath.Join(dir, SQLiteFile+suffix)); !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

// backupStore copies the store files to their state.before-revisions.*
// names, syncs them and the directory, and rereads each copy. A failure
// removes the copies this call created; the store itself is never touched.
func backupStore(dir string, sqlite bool) error {
	pairs := [][2]string{{"state.json", "state.before-revisions.json"}}
	if sqlite {
		pairs = append(pairs, [2]string{SQLiteFile, "state.before-revisions.sqlite"})
	}
	var created []string
	err := func() error {
		for _, pair := range pairs {
			target := filepath.Join(dir, pair[1])
			digest, err := copyNew(filepath.Join(dir, pair[0]), target, &created)
			if err != nil {
				return err
			}
			if again, err := fileDigest(target); err != nil || !bytes.Equal(again, digest) {
				return errors.New("backup copy does not match the store")
			}
		}
		return syncDir(dir)
	}()
	if err != nil {
		for _, name := range created {
			os.Remove(name)
		}
	}
	return err
}

// copyNew copies the private regular file src to a new file dst, syncs dst
// and returns the digest of the bytes copied.
func copyNew(src, dst string, created *[]string) ([]byte, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("store file must be a private regular file")
	}
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	*created = append(*created, dst)
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, hash), in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

func fileDigest(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}
