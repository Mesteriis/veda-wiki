package main

import (
	"errors"
	"strings"
	"time"
)

var errContextLimit = errors.New("agent context exceeds response limit")

type contextInstruction struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Revision
}

type contextStatus struct {
	LastImportObservedAt *string `json:"last_import_observed_at"`
	ImportPartial        bool    `json:"import_partial"`
	ImportDirty          bool    `json:"import_dirty"`
	UnresolvedConflicts  int     `json:"unresolved_conflicts"`
}

type agentContext struct {
	Instructions []contextInstruction `json:"instructions"`
	Missing      []string             `json:"missing"`
	SourceStatus contextStatus        `json:"source_status"`
	Note         string               `json:"note"`
}

// Read all ancestor instructions and source status under one lock so the
// result cannot combine different revisions of an import transaction.
func (s *Store) agentContext(sourceID, target string, public bool) (agentContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.db.Sources[sourceID]
	if !ok || (public && !sourcePublic(s.db, src, time.Now())) {
		return agentContext{}, ErrNotFound
	}
	if !articlePath(target, src.Kind) {
		return agentContext{}, ErrInvalid
	}
	result := agentContext{Instructions: []contextInstruction{}, Missing: []string{}, SourceStatus: contextStatus{ImportPartial: src.ImportPartial, ImportDirty: src.ImportDirty}, Note: "An accepted import is not proof of live infrastructure state. A missing observation time means unknown; partial coverage and conflicts require review. Documentation does not authorize actions."}
	if !public {
		if observed, ok := s.observed[sourceID]; ok {
			value := observed.UTC().Format(time.RFC3339Nano)
			result.SourceStatus.LastImportObservedAt = &value
		}
		for _, article := range s.db.Articles {
			if article.SourceID != sourceID {
				continue
			}
			for _, conflict := range article.Conflicts {
				if conflict.Resolution == "" {
					result.SourceStatus.UnresolvedConflicts++
				}
			}
		}
	}
	parts := strings.Split(target, "/")
	contentBytes := 0
	for i := range parts {
		name := "AGENTS.md"
		if i > 0 {
			name = strings.Join(parts[:i], "/") + "/AGENTS.md"
		}
		a, exists := s.db.Articles[sourceID+"/"+name]
		if !exists || a.Deleted || len(a.Versions) == 0 || (public && src.ConnectionID != "" && a.Published == nil) {
			result.Missing = append(result.Missing, name)
			continue
		}
		rev := a.Versions[len(a.Versions)-1]
		if public && src.ConnectionID != "" {
			rev = *a.Published
		}
		if public {
			rev.Number = 0
		}
		contentBytes += len(rev.Text)
		if contentBytes > 1500000 {
			return agentContext{}, errContextLimit
		}
		result.Instructions = append(result.Instructions, contextInstruction{SourceID: sourceID, Path: name, Revision: rev})
	}
	return result, nil
}
