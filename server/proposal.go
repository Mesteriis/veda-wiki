package main

// proposalBase snapshots the original revision and conflict status together.
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
	rev := a.Versions[len(a.Versions)-1]
	return map[string]any{"revision": rev.Number, "sha256": rev.SHA, "unresolved_conflict": unresolved}, nil
}
