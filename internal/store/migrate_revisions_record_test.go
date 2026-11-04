package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// A migration that stopped between its commit and the completion record:
// a rerun verifies the converted store and records completion, after which
// writes by the service make every later run refuse to advise a restore.
func TestRerunRecordsCompletionAfterCrashBeforeRecord(t *testing.T) {
	for _, path := range []string{"rerun", "verify"} {
		t.Run(path, func(t *testing.T) {
			dir := copyFixture(t, v20Fixture)
			failAt(t, "record")
			if _, err := MigrateRevisions(dir); err == nil || !strings.Contains(err.Error(), "failed after converting the store") {
				t.Fatal("crash before the record", err)
			}
			if present(filepath.Join(dir, backupDone)) {
				t.Fatal("record written despite the failure")
			}
			migrateRevisionsFault = func(string) error { return nil }
			converted := snapshot(t, dir)
			run := MigrateRevisions
			if path == "verify" {
				run = VerifyRevisions
			}
			want := v20Report
			want.CompletionRecorded = true
			if report, err := run(dir); err != nil || report != want || !present(filepath.Join(dir, backupDone)) {
				t.Fatal("completion not recorded", report, err)
			}
			sameFiles(t, dir, converted)
			if _, err := MigrateRevisions(dir); err == nil || !strings.Contains(err.Error(), "nothing to do") {
				t.Fatal("rerun after recording", err)
			}
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			update(t, s, func(db *domain.Database) { appendRevision(db, "notes", "short.md", "# Written after the migration") })
			_, err = MigrateRevisions(dir)
			noRestoreAdvice(t, err)
			_, err = VerifyRevisions(dir)
			noRestoreAdvice(t, err)
		})
	}
}

// A failed record check after recovery keeps the restore advice and writes
// no record.
func TestRerunOfDamagedConvertedStoreRecordsNothing(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	failAt(t, "record")
	if _, err := MigrateRevisions(dir); err == nil {
		t.Fatal("injected failure ignored")
	}
	migrateRevisionsFault = func(string) error { return nil }
	s, _, err := read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(s.BlobPath(s.db.Sources["notes"].Assets["img/c.png"].SHA)); err != nil {
		t.Fatal(err)
	}
	_, err = MigrateRevisions(dir)
	if err == nil || !strings.Contains(err.Error(), "verify failed") || !strings.Contains(err.Error(), "restoring discards every change") || present(filepath.Join(dir, backupDone)) {
		t.Fatal("damaged converted store", err)
	}
}

// A converted store without a record and with a journal: the advice names
// the current binary, which opens a converted store; the previous binary
// refuses it.
func TestConvertedStoreWithJournalAdvisesCurrentBinary(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	failAt(t, "record")
	if _, err := MigrateRevisions(dir); err == nil {
		t.Fatal("injected failure ignored")
	}
	migrateRevisionsFault = func(string) error { return nil }
	hotJournal(t, dir)
	before := snapshot(t, dir)
	for name, run := range map[string]func(string) (MigrationReport, error){"rerun": MigrateRevisions, "verify": VerifyRevisions} {
		_, err := run(dir)
		if !errors.Is(err, errBackupJournal) || !strings.Contains(err.Error(), "stop all clients, start the current VEDA binary once with no clients connected") || strings.Contains(err.Error(), "previous binary") {
			t.Fatal(name, err)
		}
	}
	sameFiles(t, dir, before)
	if present(filepath.Join(dir, backupDone)) {
		t.Fatal("record written next to a journal")
	}
}
