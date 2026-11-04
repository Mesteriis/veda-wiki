package syncer

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// MirrorRequest enables or pauses the mirror into a destination source at
// its expected revision.
type MirrorRequest struct {
	SourceID         string `json:"source_id"`
	DestinationID    string `json:"destination_id"`
	Enabled          *bool  `json:"enabled"`
	ExpectedRevision *int   `json:"expected_revision"`
}

// Mirrors lists the source mirrors by destination.
func (m *Manager) Mirrors() []domain.SourceMirror {
	var result []domain.SourceMirror
	m.read(func(db *domain.Database) {
		result = make([]domain.SourceMirror, 0, len(db.SourceMirrors))
		for _, mirror := range db.SourceMirrors {
			mirror.BlockedPaths = slices.Clone(mirror.BlockedPaths)
			result = append(result, mirror)
		}
	})
	sort.Slice(result, func(i, j int) bool { return result[i].DestinationID < result[j].DestinationID })
	return result
}

// HasEnabledMirror reports whether an enabled mirror reads from sourceID.
func (m *Manager) HasEnabledMirror(sourceID string) bool {
	enabled := false
	m.read(func(db *domain.Database) {
		for _, mirror := range db.SourceMirrors {
			if mirror.Enabled && mirror.SourceID == sourceID {
				enabled = true
				return
			}
		}
	})
	return enabled
}

// Each destination has at most one incoming edge; reject cycles before saving.
func mirrorWouldCycle(db *domain.Database, sourceID, destinationID string) bool {
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

// ConfigureMirror enables or pauses a mirror. Enabling requires a verified,
// writable private destination connection and a reconcilable source.
func (m *Manager) ConfigureMirror(ctx context.Context, in MirrorRequest) (domain.SourceMirror, error) {
	if !domain.ValidID(in.SourceID) || !domain.ValidID(in.DestinationID) || in.SourceID == in.DestinationID || in.Enabled == nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 0 {
		return domain.SourceMirror{}, domain.ErrInvalid
	}
	var mirror domain.SourceMirror
	err := m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		if err := ctx.Err(); err != nil {
			return err
		}
		old, exists := db.SourceMirrors[in.DestinationID]
		if exists && (old.Revision != *in.ExpectedRevision || old.SourceID != in.SourceID) || !exists && *in.ExpectedRevision != 0 {
			return domain.ErrConflict
		}
		src, srcOK := db.Sources[in.SourceID]
		dst, dstOK := db.Sources[in.DestinationID]
		if !srcOK || !dstOK {
			return domain.ErrNotFound
		}
		if *in.Enabled {
			c, ok := db.Connections[dst.ConnectionID]
			if dst.Visibility != "private" ||
				src.Kind != dst.Kind ||
				!ok ||
				c.ID != dst.ID ||
				!c.Private ||
				c.Verification != "verified" ||
				c.Paused ||
				!c.Write ||
				c.LastPull == "" ||
				dst.ImportPartial ||
				!reconcileSourceReady(db, src, c) ||
				mirrorWouldCycle(db, src.ID, dst.ID) {
				return domain.ErrConflict
			}
		}
		mirror = old
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
		db.SourceMirrors[in.DestinationID] = mirror
		return ctx.Err()
	})
	if err != nil {
		return domain.SourceMirror{}, err
	}
	return mirror, nil
}

// recordMirrorResult stores the outcome of one mirror pass, unless the
// mirror was paused or reconfigured meanwhile.
func (m *Manager) recordMirrorResult(mirror domain.SourceMirror, r ReconcileResult, err error) error {
	return m.update(func(tx *store.Tx) error {
		db := tx.DB()
		current, ok := db.SourceMirrors[mirror.DestinationID]
		if !ok || !current.Enabled || current.Revision != mirror.Revision || current.SourceID != mirror.SourceID {
			return errUnchanged
		}
		current.LastRun = time.Now().UTC().Format(time.RFC3339Nano)
		current.Blocked = len(r.Blocked)
		current.BlockedPaths = slices.Clone(r.Blocked[:min(len(r.Blocked), 50)])
		current.More = r.More
		current.LastError = ""
		pendingWrites := false
		for _, job := range db.Outbox {
			if job.SourceID == current.DestinationID && (job.State == "pending" || job.State == "retry" || job.State == "prepared") {
				pendingWrites = true
				break
			}
		}
		switch {
		case err != nil:
			current.State = "error"
			if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) {
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
		db.SourceMirrors[current.DestinationID] = current
		return nil
	})
}
