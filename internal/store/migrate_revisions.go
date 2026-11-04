package store

import (
	"errors"
	"fmt"
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
	// CompletionRecorded is set when a run found a converted store without
	// its completion record, verified it and recorded completion.
	CompletionRecorded bool
}

var (
	errMigrationInUse   = errors.New("data directory already in use: stop the VEDA service before migrate-revisions")
	errMigrationDone    = errors.New("store is already format v21 and has no state.before-revisions.* backup: nothing to migrate")
	errMigrationNoStore = errors.New("no store in the data directory")
	errMigrationLayout  = errors.New("store is in an inconsistent state (revision tables do not match the store format), nothing was changed: restore the PBS snapshot")
	errMigrationJournal = errors.New("the service did not stop cleanly (SQLite journal present), nothing was changed: stop all clients, start the current VEDA binary once with no clients connected (opening the store rolls the journal back; on an unconverted store it then refuses to start, which is expected), stop it, then retry")
)

// migrateRevisionsFault lets tests fail a step after the backup is written:
// "apply" before the store is changed, "commit" inside the SQLite
// transaction after the rows are written, "verify" after the commit and
// "record" before the completion record is written.
var migrateRevisionsFault = func(step string) error { return nil }

// lockDataDir takes the writer lock of the private data directory dir.
func lockDataDir(dir string) (*os.File, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("data directory must be private")
	}
	lock, err := os.OpenFile(filepath.Join(dir, "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("writer lock unavailable: " + scrub(err))
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errMigrationInUse
	}
	return lock, nil
}

// MigrateRevisions converts a stopped data directory below format 21 to
// format 21 (domain.RevisionsFormat): it binds images to revisions, prunes
// history (domain.PruneAll) and, on SQLite, moves revision texts into their
// own rows, all in one transaction. It refuses an active writer, an unclean
// stop, an invalid, inconsistent or already converted store and missing
// images, and changes nothing before a complete, durable backup exists; a
// complete backup an interrupted run left is reused only while the store is
// unconverted and byte-identical to it. After committing it rereads the store
// and checks it (see VerifyRevisions), then writes the completion record (see
// migrate_revisions_advice.go). It never restores or deletes a backup and
// does not collect blobs: collection stays paused while any
// state.before-revisions.* file exists.
func MigrateRevisions(dir string) (MigrationReport, error) {
	var none MigrationReport
	lock, err := lockDataDir(dir)
	if err != nil {
		return none, err
	}
	defer lock.Close()
	if err = removeBackupTemps(dir); err != nil {
		return none, errors.New("cannot remove an interrupted backup copy, nothing was changed: " + scrub(err))
	}
	reuse := false
	switch {
	case completionPresent(dir):
		return none, completedMigration(dir)
	case backupPresent(dir) && journalPresent(dir):
		return none, errBackupJournal
	case backupPresent(dir):
		if reuse, err = backupMatchesStore(dir); errors.Is(err, errConvertedWithoutRecord) {
			return recordVerified(dir)
		}
		if err != nil {
			return none, err
		}
	case journalPresent(dir):
		return none, errMigrationJournal
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
	original := s.db
	plan, err := planRevisions(original, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return none, errors.New("cannot convert the store, nothing was changed: " + err.Error())
	}
	if p := checkRevisionBlobs(s, plan.db); p.articles > 0 {
		return none, fmt.Errorf("revisions of %d articles reference images that are missing (%d) or damaged (%d), nothing was changed", p.articles, p.missing, p.damaged)
	}
	if !reuse {
		if err = backupStore(dir, s.sqlite, original); err != nil {
			return none, errors.New("backup failed, the store was not changed: " + scrub(err))
		}
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
		err = verifyMigration(dir, s.sqlite, false, original, plan)
	}
	if err == nil {
		err = syncDir(dir)
	}
	if err == nil {
		err = migrateRevisionsFault("record")
	}
	if err == nil {
		err = writeCompletion(dir, s.sqlite, plan.report)
	}
	if err != nil {
		return none, failedAfterBackup(dir, err)
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
// journal next to the store. In the DELETE journal mode VEDA uses, opening
// the store would roll a hot journal back, a benign recovery, but it writes
// the original before any backup exists; the migration refuses instead.
func journalPresent(dir string) bool {
	for _, suffix := range []string{"-journal", "-wal"} {
		if _, err := os.Lstat(filepath.Join(dir, SQLiteFile+suffix)); !os.IsNotExist(err) {
			return true
		}
	}
	return false
}
