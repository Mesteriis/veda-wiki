package main

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"
)

type mirrorRequest struct {
	SourceID         string `json:"source_id"`
	DestinationID    string `json:"destination_id"`
	Enabled          *bool  `json:"enabled"`
	ExpectedRevision *int   `json:"expected_revision"`
}

func (s *Store) mirrors() []SourceMirror {
	s.RLock()
	defer s.RUnlock()
	result := make([]SourceMirror, 0, len(s.DB().SourceMirrors))
	for _, m := range s.DB().SourceMirrors {
		m.BlockedPaths = slices.Clone(m.BlockedPaths)
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DestinationID < result[j].DestinationID })
	return result
}

func (s *Store) hasEnabledMirror(sourceID string) bool {
	s.RLock()
	defer s.RUnlock()
	for _, mirror := range s.DB().SourceMirrors {
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
	s.Lock()
	defer s.Unlock()
	if err := ctx.Err(); err != nil {
		return SourceMirror{}, err
	}
	old, exists := s.DB().SourceMirrors[in.DestinationID]
	if exists && (old.Revision != *in.ExpectedRevision || old.SourceID != in.SourceID) || !exists && *in.ExpectedRevision != 0 {
		return SourceMirror{}, ErrConflict
	}
	src, srcOK := s.DB().Sources[in.SourceID]
	dst, dstOK := s.DB().Sources[in.DestinationID]
	if !srcOK || !dstOK {
		return SourceMirror{}, ErrNotFound
	}
	if *in.Enabled {
		c, ok := s.DB().Connections[dst.ConnectionID]
		if dst.Visibility != "private" || src.Kind != dst.Kind || !ok || c.ID != dst.ID || !c.Private || c.Verification != "verified" || c.Paused || !c.Write || c.LastPull == "" || dst.ImportPartial || !reconcileSourceReady(*s.DB(), src, c) || mirrorWouldCycle(*s.DB(), src.ID, dst.ID) {
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
	db := s.Clone()
	db.SourceMirrors[in.DestinationID] = mirror
	if db.Version < 19 {
		db.Version = 19
	}
	if err := ctx.Err(); err != nil {
		return SourceMirror{}, err
	}
	if err := s.Commit(db); err != nil {
		return SourceMirror{}, err
	}
	return mirror, nil
}
func (s *Store) recordMirrorResult(m SourceMirror, r reconcileResult, err error) error {
	s.Lock()
	defer s.Unlock()
	current, ok := s.DB().SourceMirrors[m.DestinationID]
	if !ok || !current.Enabled || current.Revision != m.Revision || current.SourceID != m.SourceID {
		return nil
	}
	current.LastRun = time.Now().UTC().Format(time.RFC3339Nano)
	current.Blocked = len(r.Blocked)
	current.BlockedPaths = slices.Clone(r.Blocked[:min(len(r.Blocked), 50)])
	current.More = r.More
	current.LastError = ""
	pendingWrites := false
	for _, job := range s.DB().Outbox {
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
	db := s.Clone()
	db.SourceMirrors[current.DestinationID] = current
	return s.Commit(db)
}
