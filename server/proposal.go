package main

// proposalBase snapshots the original revision and source status together.
// Callers must authorize and scope the source before using it.
func (s *Store) proposalBase(sourceID, path string) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.db.Sources[sourceID]
	if !ok {
		return nil, ErrNotFound
	}
	if !articlePath(path, src.Kind) {
		return nil, ErrInvalid
	}
	a, ok := s.db.Articles[sourceID+"/"+path]
	if !ok || a.Deleted || len(a.Versions) == 0 {
		return nil, ErrNotFound
	}
	unresolved := false
	for _, conflict := range a.Conflicts {
		if conflict.Resolution == "" {
			unresolved = true
			break
		}
	}
	blocked := false
	for _, job := range s.db.Outbox {
		if job.SourceID == sourceID && job.Path == path && job.State != "complete" && job.State != "superseded" && (job.PreparedCommit != "" || job.scopePrepared || len(job.Bundle) != 0 || job.scopeBundle) {
			blocked = true
			break
		}
	}
	rev := a.Versions[len(a.Versions)-1]
	return map[string]any{"revision": rev.Number, "sha256": rev.SHA, "unresolved_conflict": unresolved, "conflict_resolution_blocked_by_write": blocked, "source_status": s.sourceStatusLocked(sourceID, src)}, nil
}

// newProposalBase observes the visible path and source status under one lock.
// Tombstoned history stays hidden; a later write may still conflict.
func (s *Store) newProposalBase(sourceID, path string) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.db.Sources[sourceID]
	if !ok {
		return nil, ErrNotFound
	}
	if !articlePath(path, src.Kind) {
		return nil, ErrInvalid
	}
	if a, exists := s.db.Articles[sourceID+"/"+path]; exists && !a.Deleted && len(a.Versions) > 0 {
		return nil, ErrConflict
	}
	return map[string]any{"source_status": s.sourceStatusLocked(sourceID, src)}, nil
}
