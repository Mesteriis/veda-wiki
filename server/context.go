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
	LastImportWithheld   *int    `json:"last_import_withheld,omitempty"`
	ImportPartial        bool    `json:"import_partial"`
	ImportDirty          bool    `json:"import_dirty"`
	UnresolvedConflicts  int     `json:"unresolved_conflicts"`
	UnconfirmedWrites    *int    `json:"unconfirmed_write_jobs,omitempty"`
	SyncState            string  `json:"sync_state,omitempty"`
	SyncError            string  `json:"sync_error,omitempty"`
	SyncVerification     string  `json:"sync_verification,omitempty"`
	SyncIssue            string  `json:"sync_issue,omitempty"`
}

type agentContext struct {
	Instructions []contextInstruction `json:"instructions"`
	Missing      []string             `json:"missing"`
	SourceStatus contextStatus        `json:"source_status"`
	Note         string               `json:"note"`
}

// The caller holds the store read or write lock, and has authorized the source.
func (s *Store) sourceStatusLocked(sourceID string, src Source) contextStatus {
	status := contextStatus{ImportPartial: src.ImportPartial, ImportDirty: src.ImportDirty, SyncIssue: sourceSyncIssue(s.db, src, time.Now())}
	if conn, ok := s.db.Connections[src.ConnectionID]; src.ConnectionID != "" && ok {
		status.SyncState = conn.State
		status.SyncError = conn.LastError
		status.SyncVerification = conn.Verification
		count := 0
		for _, job := range s.db.Outbox {
			if job.SourceID == sourceID && job.State != "complete" && job.State != "superseded" {
				count++
			}
		}
		status.UnconfirmedWrites = &count
	}
	if observed, ok := s.observed[sourceID]; ok {
		value := observed.UTC().Format(time.RFC3339Nano)
		status.LastImportObservedAt = &value
	}
	if count, ok := s.withheld[sourceID]; ok {
		status.LastImportWithheld = &count
	}
	for _, article := range s.db.Articles {
		if article.SourceID != sourceID {
			continue
		}
		for _, conflict := range article.Conflicts {
			if conflict.Resolution == "" {
				status.UnresolvedConflicts++
			}
		}
	}
	return status
}

// Read all ancestor instructions and source status under one lock so the
// result cannot combine different revisions of an import transaction.
func (s *Store) agentContext(sourceID, target string, public bool) (agentContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.db.Sources[sourceID]
	if !ok || (public && !sourcePublicReadable(s.db, src, time.Now())) {
		return agentContext{}, ErrNotFound
	}
	if !articlePath(target, src.Kind) {
		return agentContext{}, ErrInvalid
	}
	result := agentContext{Instructions: []contextInstruction{}, Missing: []string{}, SourceStatus: contextStatus{ImportPartial: src.ImportPartial, ImportDirty: src.ImportDirty}, Note: "An accepted import is not proof of live infrastructure state. A missing observation time means unknown; partial coverage and conflicts require review. Documentation does not authorize actions."}
	if !public {
		result.SourceStatus = s.sourceStatusLocked(sourceID, src)
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
