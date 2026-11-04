package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// The backup of "veda migrate-revisions": a copy of state.sqlite and of
// state.json (the backend marker) for SQLite, or of state.json for JSON.
// Each copy is written as <name>.tmp, synced, reread and renamed, SQLite
// first and state.before-revisions.json last, so the final names exist only
// for complete copies and the JSON file completes the backup. A .tmp file is
// only ever a partial copy of ours; the migration removes it.
const (
	backupSQLite = "state.before-revisions.sqlite"
	backupJSON   = "state.before-revisions.json"
	backupTemp   = ".tmp"
)

var revisionBackups = []string{backupSQLite, backupJSON}

// revisionBackupPresent reports whether dir holds any part of a backup,
// including an interrupted copy; an entry that cannot be checked counts as
// present. Blob collection pauses while it does.
func revisionBackupPresent(dir string) bool {
	for _, name := range revisionBackups {
		for _, entry := range []string{name, name + backupTemp} {
			if _, err := os.Lstat(filepath.Join(dir, entry)); !os.IsNotExist(err) {
				return true
			}
		}
	}
	return false
}

// backupPresent reports whether a final backup file exists or cannot be
// checked.
func backupPresent(dir string) bool {
	for _, name := range revisionBackups {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

// removeBackupTemps removes the partial copies an interrupted backup left.
func removeBackupTemps(dir string) error {
	removed := false
	for _, name := range revisionBackups {
		err := os.Remove(filepath.Join(dir, name+backupTemp))
		if err == nil {
			removed = true
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if removed {
		return syncDir(dir)
	}
	return nil
}

// loadBackup loads the complete backup in dir read-only: state.before-
// revisions.json must exist; when it is the SQLite marker, state.before-
// revisions.sqlite must pass the integrity check and load, and otherwise must
// not exist. The backed-up store must be valid and below format 21.
func loadBackup(dir string) (domain.Database, bool, error) {
	var none domain.Database
	for _, name := range revisionBackups {
		if _, err := os.Lstat(filepath.Join(dir, name+backupTemp)); !os.IsNotExist(err) {
			return none, false, errors.New("an interrupted backup copy exists")
		}
	}
	b, err := readPrivateFile(filepath.Join(dir, backupJSON))
	if err != nil {
		return none, false, errors.New("state.before-revisions.json is missing or unreadable")
	}
	sqlitePath := filepath.Join(dir, backupSQLite)
	_, statErr := os.Lstat(sqlitePath)
	sqlite := sqliteMarkerValid(b)
	var db domain.Database
	if sqlite {
		if statErr != nil {
			return none, false, errors.New("state.before-revisions.sqlite is missing")
		}
		if err = integrityCheckFile(sqlitePath, true); err != nil {
			return none, false, err
		}
		if db, err = readSQLite(sqlitePath, true); err != nil {
			return none, false, errors.New("state.before-revisions.sqlite does not load")
		}
	} else {
		if !os.IsNotExist(statErr) {
			return none, false, errors.New("state.before-revisions.sqlite exists next to a JSON backup")
		}
		if err = json.Unmarshal(b, &db); err != nil {
			return none, false, errors.New("state.before-revisions.json does not load")
		}
	}
	if db.Version >= domain.RevisionsFormat {
		return none, false, errors.New("the backup is not a store below format 21")
	}
	if err = Validate(&db); err != nil {
		return none, false, errors.New("the backup is invalid")
	}
	return db, sqlite, nil
}

func readPrivateFile(name string) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("not a private regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxStoreBytes+1))
	if err == nil && len(b) > maxStoreBytes {
		err = errors.New("file too large")
	}
	return b, err
}

// backupStore writes the complete backup of the store in dir and requires it
// to load as original. On failure it removes every file it created; the store
// itself is never touched.
func backupStore(dir string, sqlite bool, original domain.Database) error {
	pairs := [][2]string{{"state.json", backupJSON}}
	if sqlite {
		pairs = [][2]string{{SQLiteFile, backupSQLite}, {"state.json", backupJSON}}
	}
	var created []string
	err := func() error {
		for _, pair := range pairs {
			final := filepath.Join(dir, pair[1])
			temp := final + backupTemp
			digest, err := copyNew(filepath.Join(dir, pair[0]), temp, &created)
			if err != nil {
				return err
			}
			if again, err := fileDigest(temp); err != nil || !bytes.Equal(again, digest) {
				return errors.New("backup copy does not match the store")
			}
			if _, err = os.Lstat(final); !os.IsNotExist(err) {
				return errors.New("backup file already exists")
			}
			if err = os.Rename(temp, final); err != nil {
				return err
			}
			created = append(created, final)
		}
		if err := syncDir(dir); err != nil {
			return err
		}
		backup, _, err := loadBackup(dir)
		if err != nil {
			return err
		}
		want, errA := json.Marshal(original)
		got, errB := json.Marshal(backup)
		if errA != nil || errB != nil || !bytes.Equal(want, got) {
			return errors.New("backup does not load as the store")
		}
		return nil
	}()
	if err != nil {
		for _, name := range created {
			os.Remove(name)
		}
		syncDir(dir)
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
