package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// After a successful conversion MigrateRevisions writes a completion record,
// state.before-revisions.done, with the counts and the SHA-256 of the store
// file (state.sqlite, or state.json for JSON) as converted. A store whose file
// differs from the record was written after the migration: restoring the
// backup would discard those writes, so no message then advises a restore.
// Only without a completion record, and only when verification fails, do the
// messages advise restoring, always with the warning that restoring discards
// every change made since the migration.
const backupDone = "state.before-revisions.done"

const (
	verifyAdvice   = `run "veda migrate-revisions --verify --data DIR": only a passing verify means the migration is good, and verify states whether to restore`
	restoreSteps   = "stop VEDA and all clients, copy state.before-revisions.sqlite over state.sqlite (when it exists) and state.before-revisions.json over state.json, then delete all state.before-revisions.* files; or restore the PBS snapshot"
	discardWarning = "restoring discards every change made to the store since the migration"
	keepAdvice     = "do not restore; remove the state.before-revisions.* files once you no longer need the rollback point"
)

var (
	errChangedSinceMigration = errors.New("the migration completed and the store has changed since; restoring the backup would discard those changes: " + keepAdvice)
	errBackupJournal         = errors.New("a state.before-revisions.* backup exists and the store has an SQLite journal from an unclean stop; nothing may write to the store until it is recovered: stop all clients, start the current VEDA binary once with no clients connected (opening the store rolls the journal back; on an unconverted store it then refuses to start, which is expected), stop it, then run migrate-revisions again or migrate-revisions --verify")
	// errConvertedWithoutRecord marks a converted store whose completion
	// record is missing; the caller verifies it and records completion.
	errConvertedWithoutRecord = errors.New("converted store without a completion record")
)

type completionRecord struct {
	Articles         int    `json:"articles"`
	KeptRevisions    int    `json:"kept_revisions"`
	RemovedRevisions int    `json:"removed_revisions"`
	StoreSHA256      string `json:"store_sha256"`
}

func storeFile(dir string, sqlite bool) string {
	if sqlite {
		return filepath.Join(dir, SQLiteFile)
	}
	return filepath.Join(dir, "state.json")
}

// writeCompletion records a successful conversion durably.
func writeCompletion(dir string, sqlite bool, r MigrationReport) error {
	digest, err := fileDigest(storeFile(dir, sqlite))
	if err != nil {
		return err
	}
	b, err := json.Marshal(completionRecord{r.Articles, r.KeptRevisions, r.RemovedRevisions, hex.EncodeToString(digest)})
	if err != nil {
		return err
	}
	return WritePrivateFile(dir, backupDone, b)
}

func completionPresent(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, backupDone))
	return !os.IsNotExist(err)
}

// storeUnchanged reports whether the store file still has the digest the
// completion record holds and no journal shows a later write.
func storeUnchanged(dir string) (completionRecord, bool) {
	var record completionRecord
	b, err := readPrivateFile(filepath.Join(dir, backupDone))
	if err != nil || json.Unmarshal(b, &record) != nil || journalPresent(dir) {
		return record, false
	}
	marker, err := readPrivateFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return record, false
	}
	digest, err := fileDigest(storeFile(dir, sqliteMarkerValid(marker)))
	return record, err == nil && hex.EncodeToString(digest) == record.StoreSHA256
}

func (r completionRecord) report() MigrationReport {
	return MigrationReport{Articles: r.Articles, KeptRevisions: r.KeptRevisions, RemovedRevisions: r.RemovedRevisions}
}

// completedMigration explains a rerun after a completed migration.
func completedMigration(dir string) error {
	if _, unchanged := storeUnchanged(dir); !unchanged {
		return errChangedSinceMigration
	}
	return errors.New("the migration completed and the store is unchanged since: nothing to do; " + verifyAdvice + "; remove the state.before-revisions.* files once you no longer need the rollback point (" + discardWarning + ")")
}

// backupMatchesStore decides a rerun that finds a backup without a
// completion record. The store is changed only after a complete backup
// exists, and only to format 21, so a store below format 21 is unconverted:
// a complete backup identical to it is reused; an incomplete or obsolete one
// must be deleted first.
func backupMatchesStore(dir string) (bool, error) {
	s, found, err := read(dir)
	if err != nil || !found {
		return false, errors.New("a state.before-revisions.* backup exists and the store cannot be read: do not start the service and do not delete anything; " + verifyAdvice)
	}
	_, sqlite, backupErr := loadBackup(dir)
	if s.db.Version >= domain.RevisionsFormat {
		if backupErr != nil {
			return false, errors.New("state.before-revisions.* is incomplete or damaged and the store is already converted: do not start the service and do not delete anything; restore the PBS snapshot (" + discardWarning + ")")
		}
		return false, errConvertedWithoutRecord
	}
	if backupErr != nil {
		return false, fmt.Errorf("state.before-revisions.* is incomplete (an interrupted migrate-revisions); the store was not converted (still format v%d): delete the state.before-revisions.* files and run migrate-revisions again", s.db.Version)
	}
	same := sqlite == s.sqlite
	pairs := map[string]string{"state.json": backupJSON}
	if sqlite {
		pairs[SQLiteFile] = backupSQLite
	}
	for from, to := range pairs {
		a, errA := fileDigest(filepath.Join(dir, from))
		b, errB := fileDigest(filepath.Join(dir, to))
		same = same && errA == nil && errB == nil && bytes.Equal(a, b)
	}
	if !same {
		return false, fmt.Errorf("the store was not converted (still format v%d) but was written after state.before-revisions.* was taken, so that backup is obsolete: delete the state.before-revisions.* files and run migrate-revisions again", s.db.Version)
	}
	return true, nil
}

// failedAfterBackup explains a failure after the backup was written.
func failedAfterBackup(dir string, cause error) error {
	s, found, err := read(dir)
	if err == nil && found && s.db.Version < domain.RevisionsFormat {
		if _, _, err = loadBackup(dir); err == nil {
			return fmt.Errorf("migrate-revisions failed: %s; the store was not converted (still format v%d) and the backup matches it: run migrate-revisions again", scrub(cause), s.db.Version)
		}
		return fmt.Errorf("migrate-revisions failed: %s; the store was not converted (still format v%d): delete the state.before-revisions.* files and run migrate-revisions again", scrub(cause), s.db.Version)
	}
	return fmt.Errorf("migrate-revisions failed after converting the store: %s. Do not start the service; %s", scrub(cause), verifyAdvice)
}

// VerifyRevisions checks that the store in dir is the conversion of its
// state.before-revisions.* backup: it rebuilds the expected state from the
// backup and runs the checks MigrateRevisions runs after its commit. It takes
// writer.lock, the lock file the service itself creates, so that no writer
// changes the store while it is read, and opens the store and the backup
// read-only. Its only write is the completion record, written when the check
// passes and no record exists yet (a migration that stopped between its
// commit and the record). A store changed since a completed migration fails
// without restore advice; only a failure without a completion record advises
// restoring. The report is filled when known.
func VerifyRevisions(dir string) (MigrationReport, error) {
	var none MigrationReport
	lock, err := lockDataDir(dir)
	if err != nil {
		return none, err
	}
	defer lock.Close()
	completed := completionPresent(dir)
	if completed {
		record, unchanged := storeUnchanged(dir)
		if !unchanged {
			return record.report(), errChangedSinceMigration
		}
	}
	if journalPresent(dir) {
		return none, errBackupJournal
	}
	if completed {
		report, _, checked, err := verifyAgainstBackup(dir)
		if checked && err != nil {
			err = fmt.Errorf("verify failed: %s; the store file is unchanged since the completed migration, so investigate before restoring anything", scrub(err))
		}
		return report, err
	}
	return recordVerified(dir)
}

// recordVerified verifies a store without a completion record against its
// backup and, when it passes, records completion. The caller holds the lock.
func recordVerified(dir string) (MigrationReport, error) {
	report, sqlite, checked, err := verifyAgainstBackup(dir)
	switch {
	case checked && err != nil:
		return report, fmt.Errorf("verify failed: %s; restore: %s (%s)", scrub(err), restoreSteps, discardWarning)
	case err != nil:
		return report, err
	}
	if err = migrateRevisionsFault("record"); err == nil {
		err = writeCompletion(dir, sqlite, report)
	}
	if err != nil {
		return report, errors.New("the migration verified but its completion record could not be written: " + scrub(err) + "; do not start the service; run migrate-revisions --verify again")
	}
	report.CompletionRecorded = true
	return report, nil
}

// verifyAgainstBackup runs the post-migration checks of the store in dir,
// read-only, against the conversion of its backup. checked reports whether
// the checks ran; an error with checked false explains why they could not.
func verifyAgainstBackup(dir string) (report MigrationReport, sqlite, checked bool, err error) {
	original, sqlite, err := loadBackup(dir)
	if err != nil {
		return report, false, false, errors.New("no complete state.before-revisions.* backup: " + err.Error())
	}
	current, found, err := readStore(dir, true)
	if err == nil && found && current.db.Version < domain.RevisionsFormat {
		return report, sqlite, false, fmt.Errorf("the store was not converted (still format v%d): nothing to restore; run migrate-revisions", current.db.Version)
	}
	if err != nil || !found || current.sqlite != sqlite || current.db.Version != domain.RevisionsFormat {
		return report, sqlite, true, errors.New("the store is not the format 21 conversion of the backup")
	}
	plan, err := planRevisions(original, current.db.Updated)
	if err != nil {
		return report, sqlite, true, errors.New("cannot convert the backup: " + err.Error())
	}
	return plan.report, sqlite, true, verifyMigration(dir, sqlite, true, original, plan)
}
