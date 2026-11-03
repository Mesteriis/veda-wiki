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
	src, ok := s.db.Sources[id]
	if s.invalid || !ok || src.ConnectionID != "" || src.Visibility != "private" {
		return "", ErrConflict
	}
	if _, exists := s.db.Connections[id]; exists {
		return "", ErrConflict
	}
	articles := map[string]Article{}
	for key, a := range s.db.Articles {
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
	for _, j := range s.db.Outbox {
		if j.SourceID == id && j.State != "complete" && j.State != "superseded" {
			return "", ErrConflict
		}
	}
	for _, m := range s.db.Moves {
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.adoptionSnapshotLocked(id)
}
