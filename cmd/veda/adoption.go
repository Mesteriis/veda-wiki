package main

import (
	"errors"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

var errLocalImportActive = errors.New("local importer is configured for source")

// adoptionSnapshotLocked captures exactly the local state whose baselines will
// change. Callers hold the store lock.
func (s *Store) adoptionSnapshotLocked(id string) (string, error) {
	if s.InvalidLocked() {
		return "", ErrConflict
	}
	return domain.AdoptionSnapshot(*s.DB(), id)
}

func (s *Store) adoptionSnapshot(id string) (string, error) {
	s.RLock()
	defer s.RUnlock()
	return s.adoptionSnapshotLocked(id)
}
