package agentctx

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// ProposalBase snapshots the original revision and source status together.
// Callers must authorize and scope the source before using it.
func ProposalBase(st *store.Store, obs *importer.Observations, sourceID, path string) (map[string]any, error) {
	var out map[string]any
	err := st.View(func(db *domain.Database) error {
		src, ok := db.Sources[sourceID]
		if !ok {
			return domain.ErrNotFound
		}
		if !domain.ArticlePath(path, src.Kind) {
			return domain.ErrInvalid
		}
		a, ok := db.Articles[sourceID+"/"+path]
		if !ok || a.Deleted || domain.LatestRevision(a) == 0 {
			return domain.ErrNotFound
		}
		unresolved := false
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				unresolved = true
				break
			}
		}
		blocked := domain.ConflictResolutionBlocked(*db, sourceID, path)
		rev, _ := domain.LastRevision(a)
		out = map[string]any{
			"revision":                             rev.Number,
			"sha256":                               rev.SHA,
			"unresolved_conflict":                  unresolved,
			"conflict_resolution_blocked_by_write": blocked,
			"write_blocked_by_move":                domain.MoveOperationLocks(*db, sourceID, path),
			"write_blocked_by_bundle":              domain.BundleLocks(*db, sourceID, path),
			"source_status":                        Status(*db, obs, sourceID, src, time.Now()),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NewProposalBase observes the visible path and source status under one
// snapshot. Tombstoned history stays hidden; a later write may still conflict.
func NewProposalBase(st *store.Store, obs *importer.Observations, sourceID, path string) (map[string]any, error) {
	var out map[string]any
	err := st.View(func(db *domain.Database) error {
		src, ok := db.Sources[sourceID]
		if !ok {
			return domain.ErrNotFound
		}
		if !domain.ArticlePath(path, src.Kind) {
			return domain.ErrInvalid
		}
		if a, exists := db.Articles[sourceID+"/"+path]; exists && !a.Deleted && domain.LatestRevision(a) > 0 {
			return domain.ErrConflict
		}
		out = map[string]any{"source_status": Status(*db, obs, sourceID, src, time.Now())}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
