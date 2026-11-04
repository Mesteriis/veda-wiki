package knowledge

import (
	"context"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// publish makes g visible if snap is still active under the active
// fingerprint. The check, the publication and the job's end happen under
// publishMu, which reconciliation also holds while it supersedes and
// retires: a snapshot that became inactive is either refused here or
// retired by the reconciliation that follows the store change. A refused
// result stays stored (it is immutable) but is never published, and the
// job ends superseded, never over a fresher row.
func (s *Service) publish(ctx context.Context, job Job, g Graph) {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	snap := job.Snapshot
	active := false
	err := s.cfg.Store.View(func(db *domain.Database) error {
		_, active = ActiveSnapshotOf(*db, snap.Key)
		return nil
	})
	if err != nil {
		s.retry(ctx, job, err)
		return
	}
	if p, ok := s.Pipeline(); !active || !ok || snap.Fingerprint != p.Fingerprint {
		s.finish(ctx, snap, Finish{State: JobSuperseded})
		return
	}
	if err = s.cfg.Index.Publish(ctx, snap, g); err != nil {
		if ctx.Err() == nil {
			s.degrade(CodeIndexUnavailable, err)
		}
		s.outage(ctx, job, CodeIndexUnavailable)
		return
	}
	s.recover(CodeIndexUnavailable)
	s.mu.Lock()
	s.dropped = s.dropped.Add(g.Dropped)
	s.mu.Unlock()
	s.finish(ctx, snap, Finish{State: JobSucceeded})
}
