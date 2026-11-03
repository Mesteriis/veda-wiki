package main

import "github.com/Mesteriis/veda-wiki/internal/store"

// removeLocalImportedSource removes only the configured disconnected private
// source. It refuses removal if any retained record depends on that source.
// The root-only caller is responsible for stopping the importer first.
func (s *Store) removeLocalImportedSource(id, expectedGeneration string) (int, error) {
	s.Lock()
	defer s.Unlock()
	src, ok := s.DB().Sources[id]
	if !ok {
		return 0, ErrNotFound
	}
	if src.Visibility != "private" || src.ConnectionID != "" || src.ImportGeneration != expectedGeneration || expectedGeneration == "" {
		return 0, ErrConflict
	}
	db := s.Clone()
	delete(db.Sources, id)
	count := 0
	for key, article := range db.Articles {
		if article.SourceID == id {
			delete(db.Articles, key)
			count++
		}
	}
	// Validation catches references from moves, outbox, scopes, teams, copies,
	// mirrors, and reconciliation baselines without editing those records.
	if err := store.Validate(&db); err != nil {
		return 0, ErrConflict
	}
	if err := s.Commit(db); err != nil {
		return 0, err
	}
	s.ForgetImportObservation(id)
	return count, nil
}
