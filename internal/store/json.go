package store

import (
	"encoding/json"
	"errors"
	"os"
	"path"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// writeJSON replaces state.json atomically. The caller holds the lock.
func (s *Store) writeJSON(db domain.Database) error {
	b, err := json.Marshal(db)
	if err != nil {
		return err
	}
	if len(b) > maxStoreBytes {
		return errors.New("store capacity reached")
	}
	f, err := os.CreateTemp(s.dir, ".transaction-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path.Join(s.dir, "state.json")); err != nil {
		return err
	}
	// Once rename succeeds, readers must use the same state as recovery even if
	// directory fsync fails. The error is returned; optimistic retries are safe.
	s.db = db
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
