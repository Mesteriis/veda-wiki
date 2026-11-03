package importer

import (
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// RemoveLocalImportedSource removes only the configured disconnected private
// source and its articles, and returns how many articles it removed. It
// refuses removal if any retained record depends on that source. The
// root-only caller is responsible for stopping the importer first.
func (s Service) RemoveLocalImportedSource(id, expectedGeneration string) (int, error) {
	count := 0
	err := s.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		src, ok := db.Sources[id]
		if !ok {
			return domain.ErrNotFound
		}
		if src.Visibility != "private" || src.ConnectionID != "" || src.ImportGeneration != expectedGeneration || expectedGeneration == "" {
			return domain.ErrConflict
		}
		delete(db.Sources, id)
		for key, article := range db.Articles {
			if article.SourceID == id {
				delete(db.Articles, key)
				count++
			}
		}
		// Validation catches references from moves, outbox, scopes, teams, copies,
		// mirrors, and reconciliation baselines without editing those records.
		if err := store.Validate(db); err != nil {
			return domain.ErrConflict
		}
		tx.AfterCommit(func() { s.Observations.Forget(id) })
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
