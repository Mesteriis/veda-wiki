package main

import (
	"context"
	"errors"
	"sort"
	"time"
)

// Connected sources must have a complete, recently verified private snapshot.
// Disconnected sources may be partial: individual unsafe paths are withheld.
func reconcileSourceReady(db Database, src Source, dst GitHubConnection) bool {
	if src.Visibility != "private" {
		return false
	}
	if src.ConnectionID == "" {
		return true
	}
	c, ok := db.Connections[src.ConnectionID]
	return ok && c.ID == src.ID && c.Private && !src.ImportPartial && sourceSyncIssue(db, src, time.Now()) == "" &&
		c.RepositoryID != 0 && dst.RepositoryID != 0 && (c.RepositoryID != dst.RepositoryID || c.Branch != dst.Branch)
}

// ReconcileBase is an owner-only, versioned observation shared by two sources.
// It is not copy provenance: equal pre-existing articles can establish a base.
type ReconcileBase struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Revision int    `json:"revision"`
	SHA      string `json:"sha256"`
}

type reconcileRequest struct {
	SourceID      string `json:"source_id"`
	DestinationID string `json:"destination_id"`
	Limit         int    `json:"limit,omitempty"`
}
type reconcileResult struct {
	Baselines int      `json:"baselines"`
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Unchanged int      `json:"unchanged"`
	Blocked   []string `json:"blocked_paths"`
	More      bool     `json:"more"`
}

func pendingReconcilePath(db Database, source, p string) bool {
	if moveDeletionLocks(db, source, p) || conflictResolutionBlocked(db, source, p) {
		return true
	}
	for _, j := range db.Outbox {
		if j.SourceID != source || j.State == "complete" || j.State == "superseded" {
			continue
		}
		if j.Path == p {
			return true
		}
		if _, ok := j.Bundle[p]; ok {
			return true
		}
		if _, ok := j.Links[p]; ok {
			return true
		}
	}
	return false
}

// reconcileSources performs bounded, idempotent transactions. It never deletes
// either side, never infers a baseline from unequal content, and only queues
// writes to a verified private GitHub destination. Caller must be an owner.
func (s *Store) reconcileSources(ctx context.Context, in reconcileRequest) (reconcileResult, error) {
	return s.reconcileSourcesExpected(ctx, in, nil)
}

func (s *Store) reconcileSourcesExpected(ctx context.Context, in reconcileRequest, mirrorRevision *int) (reconcileResult, error) {
	result := reconcileResult{Blocked: []string{}}
	if !identifier.MatchString(in.SourceID) || !identifier.MatchString(in.DestinationID) || in.SourceID == in.DestinationID {
		return result, ErrInvalid
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Limit < 1 || in.Limit > 50 {
		return result, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if mirrorRevision != nil {
		mirror, ok := s.db.SourceMirrors[in.DestinationID]
		if !ok || !mirror.Enabled || mirror.Revision != *mirrorRevision || mirror.SourceID != in.SourceID {
			return result, ErrConflict
		}
	}
	src, ok := s.db.Sources[in.SourceID]
	if !ok {
		return result, ErrNotFound
	}
	dst, ok := s.db.Sources[in.DestinationID]
	if !ok {
		return result, ErrNotFound
	}
	conn, connected := s.db.Connections[dst.ConnectionID]
	if dst.Visibility != "private" || src.Kind != dst.Kind || !connected || conn.ID != dst.ID || !conn.Private || conn.Verification != "verified" || conn.Paused || !conn.Write || conn.LastPull == "" || dst.ImportPartial || !reconcileSourceReady(s.db, src, conn) {
		return result, ErrConflict
	}
	paths := []string{}
	for _, a := range s.db.Articles {
		if a.SourceID == src.ID && !a.Deleted {
			paths = append(paths, a.Path)
		}
	}
	sort.Strings(paths)
	changes := 0
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		a := s.db.Articles[src.ID+"/"+p]
		if a.Deleted || len(a.Versions) == 0 {
			result.Blocked = append(result.Blocked, p)
			continue
		}
		rev := a.Versions[len(a.Versions)-1]
		unsafe := secretContent.MatchString(rev.Text)
		for _, c := range a.Conflicts {
			if c.Resolution == "" {
				unsafe = true
			}
		}
		if unsafe || (src.ConnectionID != "" && a.UpstreamHash != rev.SHA) || pendingReconcilePath(s.db, src.ID, p) || pendingReconcilePath(s.db, dst.ID, p) {
			result.Blocked = append(result.Blocked, p)
			continue
		}
		target, exists := s.db.Articles[dst.ID+"/"+p]
		if exists {
			if target.Deleted || len(target.Versions) == 0 {
				result.Blocked = append(result.Blocked, p)
				continue
			}
			for _, c := range target.Conflicts {
				if c.Resolution == "" {
					unsafe = true
				}
			}
			latest := target.Versions[len(target.Versions)-1]
			if unsafe || target.UpstreamHash != latest.SHA {
				result.Blocked = append(result.Blocked, p)
				continue
			}
			base := target.ReconcileBase
			if base != nil && (base.SourceID != src.ID || base.Path != p) {
				result.Blocked = append(result.Blocked, p)
				continue
			}
			if latest.SHA == rev.SHA {
				deps, depErr := s.transferAssets(rev.Text, p, p, src, dst)
				if depErr != nil {
					if errors.Is(depErr, ErrInvalid) || errors.Is(depErr, ErrConflict) || errors.Is(depErr, ErrNotFound) {
						result.Blocked = append(result.Blocked, p)
						continue
					}
					return result, depErr
				}
				missingAsset := false
				for name, ref := range deps.Assets {
					if dst.Assets[name] != ref {
						missingAsset = true
						break
					}
				}
				if missingAsset {
					if changes >= in.Limit {
						result.More = true
						break
					}
					expected := len(target.Versions)
					db, _, err := s.prepareCopy(copyRequest{SourceID: src.ID, SourcePath: p, ExpectedRevision: rev.Number, DestinationID: dst.ID, DestinationPath: p, ExpectedDestinationRevision: &expected})
					if err != nil {
						if errors.Is(err, ErrInvalid) || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
							result.Blocked = append(result.Blocked, p)
							continue
						}
						return result, err
					}
					article := db.Articles[dst.ID+"/"+p]
					article.ReconcileBase = &ReconcileBase{SourceID: src.ID, Path: p, Revision: rev.Number, SHA: rev.SHA}
					db.Articles[dst.ID+"/"+p] = article
					if db.Version < 18 {
						db.Version = 18
					}
					if err := ctx.Err(); err != nil {
						return result, err
					}
					if err = s.commit(db); err != nil {
						return result, err
					}
					changes++
					result.Updated++
					continue
				}
				if base != nil && base.Revision == rev.Number && base.SHA == rev.SHA {
					result.Unchanged++
					continue
				}
				if changes >= in.Limit {
					result.More = true
					break
				}
				db := s.clone()
				target.ReconcileBase = &ReconcileBase{SourceID: src.ID, Path: p, Revision: rev.Number, SHA: rev.SHA}
				db.Articles[dst.ID+"/"+p] = target
				if db.Version < 18 {
					db.Version = 18
				}
				if err := ctx.Err(); err != nil {
					return result, err
				}
				if err := s.commit(db); err != nil {
					return result, err
				}
				changes++
				result.Baselines++
				continue
			}
			if base == nil || base.SHA != latest.SHA {
				result.Blocked = append(result.Blocked, p)
				continue
			}
		}
		if changes >= in.Limit {
			result.More = true
			break
		}
		expected := 0
		if exists {
			expected = len(target.Versions)
		}
		req := copyRequest{SourceID: src.ID, SourcePath: p, ExpectedRevision: rev.Number, DestinationID: dst.ID, DestinationPath: p, ExpectedDestinationRevision: &expected}
		db, _, err := s.prepareCopy(req)
		if err != nil {
			// A new root may carry a small, internally consistent linked graph.
			if !exists && (errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid)) {
				plan, planErr := s.planTransferLocked(req)
				if planErr != nil && !errors.Is(planErr, ErrConflict) && !errors.Is(planErr, ErrInvalid) && !errors.Is(planErr, ErrNotFound) {
					return result, planErr
				}
				safe := planErr == nil && len(plan.Articles) <= in.Limit-changes
				if safe {
					for _, item := range plan.Articles {
						linked := s.db.Articles[src.ID+"/"+item.SourcePath]
						if item.CredentialLike || item.DirectoryIndex || item.Destination == "conflict" || pendingReconcilePath(s.db, src.ID, item.SourcePath) || pendingReconcilePath(s.db, dst.ID, item.DestinationPath) || (src.ConnectionID != "" && linked.UpstreamHash != item.SHA) {
							safe = false
							break
						}
					}
				}
				if safe {
					var graphErr error
					db, _, _, graphErr = s.prepareCopyLinkedLocal(req)
					if graphErr != nil && !errors.Is(graphErr, ErrConflict) && !errors.Is(graphErr, ErrInvalid) && !errors.Is(graphErr, ErrNotFound) {
						return result, graphErr
					}
					if graphErr == nil {
						added := 0
						for _, item := range plan.Articles {
							if item.Destination != "missing" {
								continue
							}
							article := db.Articles[dst.ID+"/"+item.DestinationPath]
							article.ReconcileBase = &ReconcileBase{SourceID: src.ID, Path: item.SourcePath, Revision: item.Revision, SHA: item.SHA}
							db.Articles[dst.ID+"/"+item.DestinationPath] = article
							added++
						}
						if db.Version < 18 {
							db.Version = 18
						}
						if err := ctx.Err(); err != nil {
							return result, err
						}
						if e := s.commit(db); e != nil {
							return result, e
						}
						changes += added
						result.Created += added
						continue
					}
				}
			}
			if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
				result.Blocked = append(result.Blocked, p)
				continue
			}
			return result, err
		}
		article := db.Articles[dst.ID+"/"+p]
		article.ReconcileBase = &ReconcileBase{SourceID: src.ID, Path: p, Revision: rev.Number, SHA: rev.SHA}
		db.Articles[dst.ID+"/"+p] = article
		if db.Version < 18 {
			db.Version = 18
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err = s.commit(db); err != nil {
			return result, err
		}
		changes++
		if exists {
			result.Updated++
		} else {
			result.Created++
		}
	}
	return result, nil
}
