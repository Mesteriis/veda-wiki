package main

import (
	"encoding/json"
	"errors"
)

var errLocalImportActive = errors.New("local importer is configured for source")

// adoptionSnapshotLocked captures exactly the local state whose baselines will
// change. Callers hold the store lock. Pending operations and tombstones need
// separate reconciliation before the source can be adopted safely.
func (s *Store) adoptionSnapshotLocked(id string) (string, error) {
	src, ok := s.DB().Sources[id]
	if s.InvalidLocked() || !ok || src.ConnectionID != "" || src.Visibility != "private" {
		return "", ErrConflict
	}
	if _, exists := s.DB().Connections[id]; exists {
		return "", ErrConflict
	}
	articles := map[string]Article{}
	for key, a := range s.DB().Articles {
		if a.SourceID != id {
			continue
		}
		if a.Deleted {
			return "", ErrConflict
		}
		for _, c := range a.Conflicts {
			if c.Resolution == "" {
				return "", ErrConflict
			}
		}
		articles[key] = a
	}
	for _, j := range s.DB().Outbox {
		if j.SourceID == id && j.State != "complete" && j.State != "superseded" {
			return "", ErrConflict
		}
	}
	for _, m := range s.DB().Moves {
		if m.SourceID != id && m.DestinationID != id {
			continue
		}
		switch m.State {
		case "complete", "cancelled", "source_changed", "destination_changed":
		default:
			return "", ErrConflict
		}
	}
	raw, err := json.Marshal(struct {
		Source   Source
		Articles map[string]Article
	}{src, articles})
	if err != nil {
		return "", errors.New("source snapshot unavailable")
	}
	return digest(raw), nil
}

func (s *Store) adoptionSnapshot(id string) (string, error) {
	s.RLock()
	defer s.RUnlock()
	return s.adoptionSnapshotLocked(id)
}
