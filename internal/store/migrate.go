package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// MigrateSQLite runs only while no VEDA process owns the data directory. The
// JSON backup and SQLite snapshot are durable before the marker switches the
// active store; old binaries reject that marker instead of opening stale data.
func MigrateSQLite(dir string) error {
	lock, err := lockDataDir(dir)
	if errors.Is(err, errMigrationInUse) {
		return errors.New("data directory already in use")
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	statePath := filepath.Join(dir, "state.json")
	info, err := os.Lstat(statePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("JSON store must be a private regular file")
	}
	s, err := Open(dir)
	if err != nil {
		return err
	}
	if s.sqlite {
		return errors.New("store already uses SQLite")
	}
	for _, name := range []string{SQLiteFile, "state.before-sqlite.json"} {
		if _, err = os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			return errors.New("migration artifact already exists or cannot be checked")
		}
	}
	original, err := os.Open(statePath)
	if err != nil {
		return err
	}
	defer original.Close()
	backup, err := os.OpenFile(filepath.Join(dir, "state.before-sqlite.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(backup, original)
	if err == nil {
		err = backup.Sync()
	}
	closeErr := backup.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = initSQLite(dir); err != nil {
		return err
	}
	if err = s.commitSQLite(s.db); err != nil {
		return err
	}
	check := &Store{dir: dir}
	marker := []byte(`{"version":14,"backend":"sqlite"}`)
	if err = check.loadSQLite(marker); err != nil {
		return err
	}
	if err = Validate(&check.db); err != nil {
		return err
	}
	a, err := json.Marshal(s.db)
	if err != nil {
		return err
	}
	b, err := json.Marshal(check.db)
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return errors.New("SQLite migration verification mismatch")
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr = directory.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return WritePrivateFile(dir, "state.json", marker)
}
