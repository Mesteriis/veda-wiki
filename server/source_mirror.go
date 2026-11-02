package main

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"
)

// SourceMirror stores owner-controlled scheduling and sanitized status only.
// Article baselines and the durable GitHub outbox remain the source of truth.
type SourceMirror struct {
	SourceID      string   `json:"source_id"`
	DestinationID string   `json:"destination_id"`
	Enabled       bool     `json:"enabled"`
	Revision      int      `json:"revision"`
	State         string   `json:"state"`
	LastRun       string   `json:"last_run,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
	Blocked       int      `json:"blocked"`
	BlockedPaths  []string `json:"blocked_paths,omitempty"`
	More          bool     `json:"more"`
}
type mirrorRequest struct {
	SourceID         string `json:"source_id"`
	DestinationID    string `json:"destination_id"`
	Enabled          *bool  `json:"enabled"`
	ExpectedRevision *int   `json:"expected_revision"`
}

func (s *Store) mirrors() []SourceMirror {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SourceMirror, 0, len(s.db.SourceMirrors))
	for _, m := range s.db.SourceMirrors {
		m.BlockedPaths = slices.Clone(m.BlockedPaths)
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DestinationID < result[j].DestinationID })
	return result
}

func (s *Store) hasEnabledMirror(sourceID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, mirror := range s.db.SourceMirrors {
		if mirror.Enabled && mirror.SourceID == sourceID {
			return true
		}
	}
	return false
}

// Each destination has at most one incoming edge; reject cycles before saving.
func mirrorWouldCycle(db Database, sourceID, destinationID string) bool {
	seen := map[string]bool{}
	for sourceID != "" {
		if sourceID == destinationID || seen[sourceID] {
			return true
		}
		seen[sourceID] = true
		m, ok := db.SourceMirrors[sourceID]
		if !ok || !m.Enabled {
			break
		}
		sourceID = m.SourceID
	}
	return false
}
func (s *Store) configureMirror(ctx context.Context, in mirrorRequest) (SourceMirror, error) {
	if !identifier.MatchString(in.SourceID) || !identifier.MatchString(in.DestinationID) || in.SourceID == in.DestinationID || in.Enabled == nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 0 {
		return SourceMirror{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SourceMirror{}, err
	}
	old, exists := s.db.SourceMirrors[in.DestinationID]
	if exists && (old.Revision != *in.ExpectedRevision || old.SourceID != in.SourceID) || !exists && *in.ExpectedRevision != 0 {
		return SourceMirror{}, ErrConflict
	}
	src, srcOK := s.db.Sources[in.SourceID]
	dst, dstOK := s.db.Sources[in.DestinationID]
	if !srcOK || !dstOK {
		return SourceMirror{}, ErrNotFound
	}
	if *in.Enabled {
		c, ok := s.db.Connections[dst.ConnectionID]
		if dst.Visibility != "private" || src.Kind != dst.Kind || !ok || c.ID != dst.ID || !c.Private || c.Verification != "verified" || c.Paused || !c.Write || c.LastPull == "" || dst.ImportPartial || !reconcileSourceReady(s.db, src, c) || mirrorWouldCycle(s.db, src.ID, dst.ID) {
			return SourceMirror{}, ErrConflict
		}
	}
	mirror := old
	mirror.SourceID = in.SourceID
	mirror.DestinationID = in.DestinationID
	mirror.Enabled = *in.Enabled
	mirror.Revision = *in.ExpectedRevision + 1
	mirror.LastRun = ""
	mirror.LastError = ""
	mirror.Blocked = 0
	mirror.BlockedPaths = nil
	mirror.More = false
	if mirror.Enabled {
		mirror.State = "pending"
	} else {
		mirror.State = "paused"
	}
	db := s.clone()
	db.SourceMirrors[in.DestinationID] = mirror
	if db.Version < 19 {
		db.Version = 19
	}
	if err := ctx.Err(); err != nil {
		return SourceMirror{}, err
	}
	if err := s.commit(db); err != nil {
		return SourceMirror{}, err
	}
	return mirror, nil
}
func (s *Store) recordMirrorResult(m SourceMirror, r reconcileResult, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.db.SourceMirrors[m.DestinationID]
	if !ok || !current.Enabled || current.Revision != m.Revision || current.SourceID != m.SourceID {
		return nil
	}
	current.LastRun = time.Now().UTC().Format(time.RFC3339Nano)
	current.Blocked = len(r.Blocked)
	current.BlockedPaths = slices.Clone(r.Blocked[:min(len(r.Blocked), 50)])
	current.More = r.More
	current.LastError = ""
	pendingWrites := false
	for _, job := range s.db.Outbox {
		if job.SourceID == current.DestinationID && (job.State == "pending" || job.State == "retry" || job.State == "prepared") {
			pendingWrites = true
			break
		}
	}
	switch {
	case err != nil:
		current.State = "error"
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
			current.LastError = "source_unavailable"
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			current.LastError = "interrupted"
		} else {
			current.LastError = "storage_unavailable"
		}
	case len(r.Blocked) > 0 && pendingWrites:
		current.State = "syncing"
	case len(r.Blocked) > 0:
		current.State = "blocked"
	case r.More || r.Created > 0 || r.Updated > 0:
		current.State = "syncing"
	default:
		current.State = "ready"
	}
	db := s.clone()
	db.SourceMirrors[current.DestinationID] = current
	return s.commit(db)
}
