package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// holdWriterLock takes writer.lock as a running service does.
func holdWriterLock(t *testing.T, dir string) {
	t.Helper()
	lock, err := os.OpenFile(filepath.Join(dir, "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
}

// restoreBackups is the operator's documented recovery: copy each
// state.before-revisions.X back to state.X and remove the backups.
func restoreBackups(t *testing.T, dir string) {
	t.Helper()
	for _, backup := range revisionBackups {
		data, err := os.ReadFile(filepath.Join(dir, backup))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "state."+strings.TrimPrefix(backup, "state.before-revisions.")), string(data))
		if err = os.Remove(filepath.Join(dir, backup)); err != nil {
			t.Fatal(err)
		}
	}
}

func failAt(t *testing.T, step string) {
	t.Helper()
	previous := migrateRevisionsFault
	migrateRevisionsFault = func(s string) error {
		if s == step {
			return errors.New("injected failure")
		}
		return nil
	}
	t.Cleanup(func() { migrateRevisionsFault = previous })
}

// noRestoreAdvice requires err to say the store changed since a completed
// migration and never to advise restoring.
func noRestoreAdvice(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, errChangedSinceMigration) || strings.Contains(err.Error(), "copy state.before-revisions") || strings.Contains(err.Error(), "PBS") {
		t.Fatal("changed store got restore advice", err)
	}
}

// After a completed migration the service writes; a rerun or verify must
// then never advise restoring the backup, which would discard those writes.
func TestCompletedMigrationThenWritesNeverAdvisesRestore(t *testing.T) {
	for _, backend := range []string{"sqlite", "json"} {
		t.Run(backend, func(t *testing.T) {
			dir := copyFixture(t, v20Fixture)
			if backend == "json" {
				toJSON(t, dir)
			}
			before := snapshot(t, dir)
			if _, err := MigrateRevisions(dir); err != nil {
				t.Fatal(err)
			}
			if !present(filepath.Join(dir, backupDone)) {
				t.Fatal("no completion record")
			}
			record, err := os.ReadFile(filepath.Join(dir, backupDone))
			if err != nil || strings.Contains(string(record), "notes") || strings.Contains(string(record), "Synthetic") {
				t.Fatal("completion record", string(record), err)
			}
			after := snapshot(t, dir)
			if _, err = MigrateRevisions(dir); err == nil || !strings.Contains(err.Error(), "completed and the store is unchanged since: nothing to do") {
				t.Fatal("rerun after success", err)
			}
			if report, err := VerifyRevisions(dir); err != nil || report != v20Report {
				t.Fatal("verify after success", report, err)
			}
			backupsHold(t, dir, before)
			sameFiles(t, dir, after)
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			update(t, s, func(db *domain.Database) { appendRevision(db, "notes", "short.md", "# Written after the migration") })
			changed := snapshot(t, dir)
			_, err = MigrateRevisions(dir)
			noRestoreAdvice(t, err)
			report, err := VerifyRevisions(dir)
			noRestoreAdvice(t, err)
			if report != v20Report {
				t.Fatal("verify report", report)
			}
			sameFiles(t, dir, changed)
			// Without any backup file the converted store is refused as v21.
			for _, b := range append(revisionBackups, backupDone) {
				os.Remove(filepath.Join(dir, b))
			}
			if _, err := MigrateRevisions(dir); !errors.Is(err, errMigrationDone) {
				t.Fatal("rerun of a converted store", err)
			}
		})
	}
}

// Without a completion record, a failed verify advises restoring and warns
// that restoring discards every change since the migration.
func TestVerifyFailureWithoutCompletionAdvisesRestore(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	failAt(t, "verify")
	if _, err := MigrateRevisions(dir); err == nil {
		t.Fatal("injected failure ignored")
	}
	if present(filepath.Join(dir, backupDone)) {
		t.Fatal("completion record after a failure")
	}
	s, _, err := read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(s.BlobPath(s.db.Sources["notes"].Assets["img/c.png"].SHA)); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyRevisions(dir)
	if err == nil || !strings.Contains(err.Error(), "restore: stop VEDA and all clients, copy state.before-revisions.sqlite over state.sqlite") || !strings.Contains(err.Error(), "restoring discards every change made to the store since the migration") {
		t.Fatal("verify failure advice", err)
	}
}

// A failure after the backup leaves a complete backup. Before the store is
// converted, a rerun reuses the identical backup; after it, verify decides.
// Restoring the backup and deleting it gives back the original store.
func TestMigrateRevisionsFailureKeepsBackupForRestore(t *testing.T) {
	for _, tc := range []struct {
		step, backend string
		converted     bool
	}{
		{"apply", "sqlite", false}, {"apply", "json", false},
		{"commit", "sqlite", false},
		{"verify", "sqlite", true}, {"verify", "json", true},
	} {
		t.Run(tc.step+"/"+tc.backend, func(t *testing.T) {
			dir := copyFixture(t, v20Fixture)
			if tc.backend == "json" {
				toJSON(t, dir)
			}
			original, before := legacyState(t, dir), snapshot(t, dir)
			failAt(t, tc.step)
			_, err := MigrateRevisions(dir)
			want := "the store was not converted (still format v20) and the backup matches it: run migrate-revisions again"
			if tc.converted {
				want = "failed after converting the store: injected failure. Do not start the service; run \"veda migrate-revisions --verify"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatal("failure message", err)
			}
			backupsHold(t, dir, before)
			if _, _, err = loadBackup(dir); err != nil {
				t.Fatal("backup incomplete", err)
			}
			if present(filepath.Join(dir, backupDone)) {
				t.Fatal("completion record after a failure")
			}
			report, err := VerifyRevisions(dir)
			if (err == nil) != tc.converted || tc.converted && report != v20Report {
				t.Fatal("verify after a failure", report, err)
			}
			migrateRevisionsFault = func(string) error { return nil }
			if !tc.converted {
				if !strings.Contains(err.Error(), "nothing to restore") {
					t.Fatal("verify of an unconverted store", err)
				}
				sameFiles(t, dir, before)
				if report, err = MigrateRevisions(dir); err != nil || report != v20Report {
					t.Fatal("rerun did not reuse the backup", report, err)
				}
				backupsHold(t, dir, before)
				checkMigrated(t, dir, original, v20Kept)
				return
			}
			if _, err = MigrateRevisions(dir); err == nil || !strings.Contains(err.Error(), "converted the store but did not complete") {
				t.Fatal("rerun after a failure", err)
			}
			restoreBackups(t, dir)
			sameFiles(t, dir, before)
			if _, err = Open(dir); err == nil || !strings.Contains(err.Error(), "veda migrate-revisions") {
				t.Fatal("restored store", err)
			}
			if _, err = MigrateRevisions(dir); err != nil {
				t.Fatal("migration after restore", err)
			}
			checkMigrated(t, dir, original, v20Kept)
		})
	}
}

// Partial copies an interrupted backup left are ours: the migration removes
// them and proceeds.
func TestMigrateRevisionsRemovesInterruptedBackupCopies(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	for _, name := range revisionBackups {
		writeFile(t, filepath.Join(dir, name+backupTemp), "partial")
	}
	if _, _, err := loadBackup(dir); err == nil {
		t.Fatal("a partial copy counts as a complete backup")
	}
	if report, err := MigrateRevisions(dir); err != nil || report != v20Report {
		t.Fatal(report, err)
	}
	for _, name := range revisionBackups {
		if present(filepath.Join(dir, name+backupTemp)) {
			t.Fatal("partial copy left")
		}
	}
}

func TestVerifyRevisionsNeedsBackupAndLock(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	if _, err := VerifyRevisions(dir); err == nil || !strings.Contains(err.Error(), "no complete state.before-revisions.* backup") {
		t.Fatal("verify without a backup", err)
	}
	if _, err := MigrateRevisions(dir); err != nil {
		t.Fatal(err)
	}
	holdWriterLock(t, dir)
	if _, err := VerifyRevisions(dir); !errors.Is(err, errMigrationInUse) {
		t.Fatal("verify with an active writer", err)
	}
}

// Verify opens the store and backup read-only: no store or backup file
// changes, not even its modification time, and no journal appears.
func TestVerifyRevisionsWritesNothing(t *testing.T) {
	for _, backend := range []string{"sqlite", "json"} {
		t.Run(backend, func(t *testing.T) {
			dir := copyFixture(t, v20Fixture)
			if backend == "json" {
				toJSON(t, dir)
			}
			if _, err := MigrateRevisions(dir); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour).Truncate(time.Second)
			names := append([]string{"state.json", SQLiteFile, backupDone}, revisionBackups...)
			stats := map[string]os.FileInfo{}
			for _, name := range names {
				p := filepath.Join(dir, name)
				if !present(p) {
					continue
				}
				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				stats[name] = info
			}
			before := snapshot(t, dir)
			if _, err := VerifyRevisions(dir); err != nil {
				t.Fatal(err)
			}
			sameFiles(t, dir, before)
			for name, info := range stats {
				now, err := os.Stat(filepath.Join(dir, name))
				if err != nil || !now.ModTime().Equal(info.ModTime()) || now.Size() != info.Size() {
					t.Fatal("verify touched", name)
				}
			}
			for _, suffix := range []string{"-journal", "-wal", "-shm"} {
				if present(filepath.Join(dir, SQLiteFile+suffix)) || present(filepath.Join(dir, backupSQLite+suffix)) {
					t.Fatal("verify left a journal")
				}
			}
		})
	}
}

func TestMigrateRevisionsBackupFailureChangesNothing(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	before := snapshot(t, dir)
	previous := syncDir
	syncDir = func(string) error { return errors.New("injected sync failure") }
	defer func() { syncDir = previous }()
	if _, err := MigrateRevisions(dir); err == nil || !strings.Contains(err.Error(), "backup failed, the store was not changed") {
		t.Fatal("backup failure", err)
	}
	sameFiles(t, dir, before)
	noBackups(t, dir)
}

// Blob collection stays paused while a migration backup exists, so restoring
// the backup finds every image its records reference.
func TestBlobCollectionPausedWhileRevisionBackupExists(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	if _, err := MigrateRevisions(dir); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stray := agedBlob(t, s, 9, now, 48*time.Hour)
	for _, blob := range []string{stray.SHA, s.db.Sources["notes"].Assets["img/c.png"].SHA} {
		setAge(t, s.BlobPath(blob), now, 48*time.Hour)
	}
	if removed, err := s.CollectBlobs(now); !errors.Is(err, errCollectionPaused) || removed != 0 || !present(s.BlobPath(stray.SHA)) {
		t.Fatal("collection ran with a backup present", removed, err)
	}
	for _, b := range revisionBackups {
		if err = os.Rename(filepath.Join(dir, b), filepath.Join(dir, b+backupTemp)); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := s.CollectBlobs(now); !errors.Is(err, errCollectionPaused) || removed != 0 {
		t.Fatal("collection ran with a partial backup copy present", removed, err)
	}
	if _, err = Open(dir); err != nil || !present(s.BlobPath(stray.SHA)) {
		t.Fatal("Open collected with a backup present", err)
	}
	for _, b := range revisionBackups {
		if err = os.Remove(filepath.Join(dir, b+backupTemp)); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := s.CollectBlobs(now); !errors.Is(err, errCollectionPaused) || removed != 0 {
		t.Fatal("collection ran with the completion record present", removed, err)
	}
	if err = os.Remove(filepath.Join(dir, backupDone)); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.CollectBlobs(now); err != nil || removed != 1 || present(s.BlobPath(stray.SHA)) {
		t.Fatal("collection after the backup was removed", removed, err)
	}
	if !present(s.BlobPath(s.db.Sources["notes"].Assets["img/c.png"].SHA)) {
		t.Fatal("referenced blob removed")
	}
}

// Verification rereads the committed store: damage between the commit and
// the check fails the migration and keeps the backup.
func TestMigrateRevisionsVerificationCatchesDamage(t *testing.T) {
	for name, tc := range map[string]struct {
		json   bool
		damage func(t *testing.T, dir string)
		text   string
	}{
		"missing image": {damage: func(t *testing.T, dir string) {
			s, _, err := read(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(s.BlobPath(s.db.Sources["notes"].Assets["img/b.png"].SHA)); err != nil {
				t.Fatal(err)
			}
		}, text: "reference images that are missing (1) or damaged (0)"},
		"changed metadata": {damage: func(t *testing.T, dir string) {
			execSQL(t, dir, `UPDATE records SET data='{"version":21,"updated_at":"2000-01-01T00:00:00Z"}' WHERE category='meta'`)
		}, text: "differs from the planned state"},
		"lost revision": {json: true, damage: func(t *testing.T, dir string) {
			s, _, err := read(dir)
			if err != nil {
				t.Fatal(err)
			}
			a := s.db.Articles["notes/short.md"]
			a.Versions = a.Versions[1:]
			s.db.Articles["notes/short.md"] = a
			if err = s.writeJSON(s.db); err != nil {
				t.Fatal(err)
			}
		}, text: "kept other revisions than planned"},
		"store gone": {damage: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, SQLiteFile)); err != nil {
				t.Fatal(err)
			}
		}, text: "converted store cannot be read"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := copyFixture(t, v20Fixture)
			if tc.json {
				toJSON(t, dir)
			}
			before := snapshot(t, dir)
			previous := migrateRevisionsFault
			migrateRevisionsFault = func(step string) error {
				if step == "verify" {
					tc.damage(t, dir)
				}
				return nil
			}
			defer func() { migrateRevisionsFault = previous }()
			_, err := MigrateRevisions(dir)
			if err == nil || !strings.Contains(err.Error(), tc.text) || !strings.Contains(err.Error(), "Do not start the service") {
				t.Fatal("damage not detected", err)
			}
			// Verify takes the timestamp from the store, so only the
			// changed timestamp passes it; every other damage fails it.
			if _, err = VerifyRevisions(dir); (err == nil) != (name == "changed metadata") {
				t.Fatal("verify of a damaged store", err)
			}
			backupsHold(t, dir, before)
		})
	}
}
