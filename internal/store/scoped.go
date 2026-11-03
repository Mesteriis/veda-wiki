package store

import (
	"maps"
	"slices"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Scoped returns an immutable snapshot containing only the given sources, for
// a reader restricted to them. Update and commits on the snapshot fail.
//
// Test seam: tests in other packages build snapshots with it. Production code
// uses ScopedWhere, which decides the scope and takes the snapshot atomically.
func (s *Store) Scoped(sources []string) *Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scopedLocked(sources)
}

// ScopedWhere evaluates scope and builds the snapshot of the sources it
// returns under one read lock, so an authorization decision and the data it
// grants come from the same state. A nil scope grants nothing: the result is
// (nil, false, nil). scope must not modify db or retain any part of it.
func (s *Store) ScopedWhere(scope func(db *domain.Database) []string) (*Store, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sources := scope(&s.db)
	if sources == nil {
		return nil, false, nil
	}
	return s.scopedLocked(sources), true, nil
}

// scopedLocked builds an immutable snapshot of the selected sources for a
// restricted reader. It holds no import baselines, commit IDs, revisions or
// link hashes beyond what reader views need. The caller holds the read lock,
// keeping authorization and snapshot atomic.
func (s *Store) scopedLocked(scope []string) *Store {
	selected := make(map[string]bool, len(scope))
	db := domain.Database{
		Version:     s.db.Version,
		Sources:     map[string]domain.Source{},
		Articles:    map[string]domain.Article{},
		Connections: map[string]domain.GitHubConnection{},
		Outbox:      map[string]domain.SyncJob{},
		Moves:       map[string]domain.Move{},
	}
	for _, id := range scope {
		selected[id] = true
		if source, ok := s.db.Sources[id]; ok {
			if source.Metadata != nil {
				metadata := *source.Metadata
				source.Metadata = &metadata
			}
			source.Assets = maps.Clone(source.Assets)
			db.Sources[id] = source
			if conn, ok := s.db.Connections[source.ConnectionID]; ok {
				db.Connections[conn.ID] = conn
			}
		}
	}
	for key, article := range s.db.Articles {
		if !selected[article.SourceID] {
			continue
		}
		// Strings are immutable values; copy every mutable field while the
		// source lock still protects the coherent authorization snapshot.
		article.Versions = slices.Clone(article.Versions)
		article.Conflicts = slices.Clone(article.Conflicts)
		article.ReconcileBase = nil
		if article.Translation != nil {
			translation := *article.Translation
			article.Translation = &translation
		}
		if article.CopiedFrom != nil {
			origin := *article.CopiedFrom
			article.CopiedFrom = &origin
		}
		if article.Published != nil {
			published := *article.Published
			article.Published = &published
		}
		db.Articles[key] = article
	}
	// Copy only fields needed for source counts and the selected article's
	// blocking status; no commit IDs, revisions, contents or link hashes.
	for id, job := range s.db.Outbox {
		if selected[job.SourceID] && job.State != "complete" && job.State != "superseded" {
			paths := map[string]bool{}
			if len(job.Bundle) != 0 || job.ScopeBundle {
				for p := range job.Bundle {
					paths[p] = true
				}
				for p := range job.Links {
					paths[p] = true
				}
				for p := range job.ScopeBundlePaths {
					paths[p] = true
				}
			}
			db.Outbox[id] = domain.SyncJob{
				SourceID:         job.SourceID,
				Path:             job.Path,
				State:            job.State,
				ScopePrepared:    job.PreparedCommit != "" || job.ScopePrepared,
				ScopeBundle:      len(job.Bundle) != 0 || job.ScopeBundle,
				ScopeBundlePaths: paths,
			}
		}
	}
	// Preserve only the path locks visible within the authorized sources; the
	// other endpoint, move ID, commit and dependency hashes are not exposed.
	for id, move := range s.db.Moves {
		if move.State != "deleting_source" {
			continue
		}
		partial := domain.Move{State: move.State}
		if selected[move.SourceID] {
			partial.SourceID, partial.SourcePath = move.SourceID, move.SourcePath
		}
		if selected[move.DestinationID] {
			partial.DestinationID, partial.DestinationPath = move.DestinationID, move.DestinationPath
			partial.Links = map[string]string{}
			for p := range move.Links {
				partial.Links[p] = ""
			}
		}
		if partial.SourceID != "" || partial.DestinationID != "" {
			db.Moves[id] = partial
		}
	}
	return &Store{dir: s.dir, db: db, readOnly: true}
}
