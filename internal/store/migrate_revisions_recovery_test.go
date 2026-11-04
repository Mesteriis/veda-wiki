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

func TestMigrateRevisionsRerunAfterSuccessIsRefused(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	before := snapshot(t, dir)
	if _, err := MigrateRevisions(dir); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, dir)
	if _, err := MigrateRevisions(dir); err == nil || !strings.Contains(err.Error(), `run "veda migrate-revisions --verify --data DIR"`) || strings.Contains(err.Error(), "service starts") {
		t.Fatal("rerun was not refused by the backup", err)
	}
	backupsHold(t, dir, before)
	sameFiles(t, dir, after)
	if report, err := VerifyRevisions(dir); err != nil || report != v20Report {
		t.Fatal("verify after success", report, err)
	}
	sameFiles(t, dir, after)
	// Without the backup the converted store is refused as already v21.
	for _, b := range revisionBackups {
		os.Remove(filepath.Join(dir, b))
	}
	if _, err := MigrateRevisions(dir); !errors.Is(err, errMigrationDone) {
		t.Fatal("rerun of a converted store", err)
	}
	sameFiles(t, dir, after)
}

// A failure after the backup leaves a complete backup and tells the operator
// to verify; verify passes only for a committed conversion. Restoring the
// backup and deleting it gives back the original store, which a new
// migration converts.
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
			if err == nil || !strings.Contains(err.Error(), "failed after the backup was written: injected failure. Do not start the service") || !strings.Contains(err.Error(), "--verify") {
				t.Fatal("failure message", err)
			}
			backupsHold(t, dir, before)
			if _, _, err = loadBackup(dir); err != nil {
				t.Fatal("backup incomplete", err)
			}
			if !tc.converted {
				sameFiles(t, dir, before)
			}
			if report, err := VerifyRevisions(dir); (err == nil) != tc.converted || tc.converted && report != v20Report {
				t.Fatal("verify after a failure", report, err)
			}
			if _, err = MigrateRevisions(dir); err == nil || !strings.Contains(err.Error(), "--verify") {
				t.Fatal("rerun after a failure", err)
			}
			restoreBackups(t, dir)
			sameFiles(t, dir, before)
			if _, err = Open(dir); err == nil || !strings.Contains(err.Error(), "veda migrate-revisions") {
				t.Fatal("restored store", err)
			}
			migrateRevisionsFault = func(string) error { return nil }
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

func TestVerifyRevisionsFailsForAChangedStore(t *testing.T) {
	dir := copyFixture(t, v20Fixture)
	if _, err := VerifyRevisions(dir); err == nil || !strings.Contains(err.Error(), "no complete state.before-revisions.* backup") {
		t.Fatal("verify without a backup", err)
	}
	if _, err := MigrateRevisions(dir); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	update(t, s, func(db *domain.Database) { appendRevision(db, "notes", "short.md", "# Changed") })
	if report, err := VerifyRevisions(dir); err == nil || report != v20Report {
		t.Fatal("verify accepted a store written after the migration", report, err)
	}
	holdWriterLock(t, dir)
	if _, err := VerifyRevisions(dir); !errors.Is(err, errMigrationInUse) {
		t.Fatal("verify with an active writer", err)
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
