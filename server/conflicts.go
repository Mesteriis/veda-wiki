package main

import (
	"sort"
	"time"
)

type ConflictRecord struct {
	SourceID string         `json:"source_id"`
	Path     string         `json:"path"`
	Index    int            `json:"index"`
	Revision int            `json:"revision"`
	Conflict ImportConflict `json:"conflict"`
}

func supersedeImportConflicts(a Article) Article {
	a.Conflicts = append([]ImportConflict(nil), a.Conflicts...)
	for i := range a.Conflicts {
		if a.Conflicts[i].Resolution == "" {
			a.Conflicts[i].Resolution = "superseded"
		}
	}
	return a
}
func hasImportConflict(a Article, hash string, deleted bool) bool {
	for i := len(a.Conflicts) - 1; i >= 0; i-- {
		c := a.Conflicts[i]
		if c.Resolution == "" {
			return c.IncomingSHA == hash && c.Deleted == deleted
		}
	}
	return false
}

func (s *Store) conflicts() []ConflictRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ConflictRecord{}
	for _, a := range s.db.Articles {
		for i, c := range a.Conflicts {
			if c.Resolution == "" {
				out = append(out, ConflictRecord{SourceID: a.SourceID, Path: a.Path, Index: i, Revision: len(a.Versions), Conflict: c})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Index < out[j].Index
	})
	return out
}
func (s *Store) resolveConflict(sourceID, p string, index, expected int, resolution string) error {
	if index < 0 || expected < 1 || (resolution != "local" && resolution != "upstream") {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sourceID + "/" + p
	a, ok := s.db.Articles[key]
	if !ok {
		return ErrNotFound
	}
	if expected != len(a.Versions) || index >= len(a.Conflicts) || a.Conflicts[index].Resolution != "" {
		return ErrConflict
	}
	for _, c := range a.Conflicts[index+1:] {
		if c.Resolution == "" {
			return ErrConflict
		}
	}
	conflicts := append([]ImportConflict(nil), a.Conflicts...)
	incoming := conflicts[index]
	for i := 0; i <= index; i++ {
		if conflicts[i].Resolution == "" {
			conflicts[i].Resolution = "superseded"
		}
	}
	conflicts[index].Resolution = resolution
	a.Conflicts = conflicts
	a.UpstreamHash = incoming.IncomingSHA
	if resolution == "upstream" {
		a.Deleted = incoming.Deleted
		if !incoming.Deleted {
			a.Versions = append(append([]Revision(nil), a.Versions...), Revision{Number: len(a.Versions) + 1, Text: incoming.Incoming, SHA: incoming.IncomingSHA, Created: time.Now().UTC().Format(time.RFC3339Nano)})
		}
	}
	db := s.clone()
	db.Articles[key] = a
	if s.db.Sources[sourceID].ConnectionID != "" {
		if resolution == "local" {
			enqueueWrite(&db, a, a.Versions[len(a.Versions)-1])
		} else {
			for id, job := range db.Outbox {
				if job.SourceID == sourceID && job.Path == p && job.State != "complete" {
					job.State = "superseded"
					db.Outbox[id] = job
				}
			}
		}
	}
	return s.commit(db)
}
