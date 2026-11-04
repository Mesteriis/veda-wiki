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
}

const (
	verifyAdvice  = `run "veda migrate-revisions --verify --data DIR": only a passing verify means the migration is good`
	restoreAdvice = "if verify fails, stop VEDA, copy state.before-revisions.sqlite over state.sqlite (when it exists) and state.before-revisions.json over state.json, then delete both state.before-revisions.* files, or restore the PBS snapshot"
)

var (
	errMigrationInUse   = errors.New("data directory already in use: stop the VEDA service before migrate-revisions")
	errMigrationDone    = errors.New("store is already format v21 and has no state.before-revisions.* backup: nothing to migrate")
	errMigrationNoStore = errors.New("no store in the data directory")
	errMigrationLayout  = errors.New("store is in an inconsistent state (revision tables do not match the store format), nothing was changed: restore the PBS snapshot")
	errMigrationJournal = errors.New("the service did not stop cleanly (SQLite journal present), nothing was changed: start and stop VEDA's previous binary once, then retry")
)

// migrateRevisionsFault lets tests fail a step after the backup is written:
// "apply" before the store is changed, "commit" inside the SQLite
// transaction after the rows are written, "verify" after the commit.
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
// stop, an existing backup, an invalid, inconsistent or already converted
// store and missing images, and changes nothing before a complete, durable
// backup exists. After committing it rereads the store from disk and checks
// it (see VerifyRevisions); a failure leaves the backup for the operator. It
// never restores or deletes a complete backup and does not collect blobs:
// collection stays paused while a backup exists.
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
	if journalPresent(dir) {
		return none, errMigrationJournal
	}
	if backupPresent(dir) {
		return none, backupExists(dir)
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
	if err = backupStore(dir, s.sqlite, original); err != nil {
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
		return none, failedAfterBackup(dir, err)
	}
	return plan.report, nil
}

// VerifyRevisions checks, under the writer lock and without writing, that the
// store in dir is the conversion of its state.before-revisions.* backup: it
// rebuilds the expected state from the backup and runs the checks
// MigrateRevisions runs after its commit. Only a store that no service has
// written since the migration passes. The report is filled when the backup
// could be converted, also when the check fails.
func VerifyRevisions(dir string) (MigrationReport, error) {
	var none MigrationReport
	lock, err := lockDataDir(dir)
	if err != nil {
		return none, err
	}
	defer lock.Close()
	original, sqlite, err := loadBackup(dir)
	if err != nil {
		return none, errors.New("no complete state.before-revisions.* backup: " + err.Error())
	}
	if journalPresent(dir) {
		return none, errMigrationJournal
	}
	current, found, err := read(dir)
	if err != nil || !found || current.sqlite != sqlite || current.db.Version != domain.RevisionsFormat {
		return none, errors.New("the store is not the format 21 conversion of the backup")
	}
	plan, err := planRevisions(original, current.db.Updated)
	if err != nil {
		return none, errors.New("cannot convert the backup: " + err.Error())
	}
	return plan.report, verifyMigration(dir, sqlite, original, plan)
}

// backupExists explains a backup found before a migration.
func backupExists(dir string) error {
	if _, _, err := loadBackup(dir); err != nil {
		return incompleteBackup(dir)
	}
	return errors.New("state.before-revisions.* exists from a previous migrate-revisions, nothing was changed: " + verifyAdvice + "; " + restoreAdvice)
}

// failedAfterBackup explains a failure after the backup was written.
func failedAfterBackup(dir string, cause error) error {
	if _, _, err := loadBackup(dir); err != nil {
		return fmt.Errorf("migrate-revisions failed: %s; %w", scrub(cause), incompleteBackup(dir))
	}
	return fmt.Errorf("migrate-revisions failed after the backup was written: %s. Do not start the service; %s; %s", scrub(cause), verifyAdvice, restoreAdvice)
}

// incompleteBackup explains a backup that is incomplete or does not load. The
// store is changed only after a complete backup exists, and only to format
// 21, so a store still below format 21 is the original.
func incompleteBackup(dir string) error {
	if s, found, err := read(dir); err == nil && found && s.db.Version < domain.RevisionsFormat {
		return errors.New("state.before-revisions.* is incomplete (an interrupted migrate-revisions); the original store was not modified: delete the state.before-revisions.* files and run migrate-revisions again")
	}
	return errors.New("state.before-revisions.* is incomplete or damaged and the store may already be converted: do not start the service and do not delete anything; restore the PBS snapshot")
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
