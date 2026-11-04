package knowledge

import (
	"context"
	"errors"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Reconcile makes the manifest and the index match the store:
//
//  1. Read the active snapshot keys under View (current and published copy
//     of every article that is not deleted).
//  2. In one manifest transaction: add missing (key, fingerprint) rows as
//     pending, make superseded rows of active keys pending again, and
//     supersede rows of keys that are no longer active (deleted, removed,
//     moved, replaced by a new revision) or of another fingerprint.
//  3. Retire from the index every superseded snapshot and every published
//     snapshot that is not active under the active fingerprint, so an old
//     generation is removed as soon as the fingerprint changes. Reads filter
//     by the active fingerprint, so generations never mix even before that.
//  4. Requeue succeeded rows whose snapshot the index no longer holds (the
//     index was rebuilt or a publication was lost); they are republished
//     from the stored result without calling the extractor.
//  5. Prune superseded rows older than SupersededRetention.
//
// Steps 2–4 hold publishMu, so a worker cannot publish between the
// decision and the retirement.
func (s *Service) Reconcile(ctx context.Context) error {
	p, ok := s.Pipeline()
	if !ok {
		return errNoPipeline
	}
	var active []SnapshotKey
	if err := s.cfg.Store.View(func(db *domain.Database) error {
		for _, a := range ActiveSnapshots(*db) {
			active = append(active, a.Key)
		}
		return nil
	}); err != nil {
		return err
	}
	fp := p.Fingerprint
	now := s.cfg.Now()
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	change, err := s.cfg.Manifest.Reconcile(ctx, fp, active, now)
	if err != nil {
		s.degrade(CodeManifestUnavailable, err)
		return err
	}
	s.recover(CodeManifestUnavailable)
	isActive := make(map[SnapshotKey]bool, len(active))
	for _, k := range active {
		isActive[k] = true
	}
	published, err := s.cfg.Index.Snapshots(ctx)
	if err != nil {
		s.degrade(CodeIndexUnavailable, err)
		return err
	}
	retire := map[Snapshot]bool{}
	for _, snap := range change.Superseded {
		retire[snap] = true
	}
	inIndex := map[Snapshot]bool{}
	for _, snap := range published {
		inIndex[snap] = true
		if !isActive[snap.Key] || snap.Fingerprint != fp {
			retire[snap] = true
		}
	}
	jobs, err := s.cfg.Manifest.Jobs(ctx, nil)
	if err != nil {
		s.degrade(CodeManifestUnavailable, err)
		return err
	}
	var lost []Snapshot
	for _, j := range jobs {
		if j.State == JobSucceeded && j.Fingerprint == fp && !inIndex[j.Snapshot] {
			lost = append(lost, j.Snapshot)
		}
	}
	if len(retire) > 0 {
		list := make([]Snapshot, 0, len(retire))
		for snap := range retire {
			list = append(list, snap)
		}
		if err = s.cfg.Index.Retire(ctx, list); err != nil {
			s.degrade(CodeIndexUnavailable, err)
			return err
		}
	}
	s.recover(CodeIndexUnavailable)
	if _, err = s.cfg.Manifest.Requeue(ctx, lost, now); err != nil {
		return err
	}
	if _, err = s.cfg.Manifest.Prune(ctx, now.Add(-SupersededRetention)); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastReconcile = now
	s.mu.Unlock()
	s.wakeFeeder()
	return nil
}

// Reindex requeues the succeeded and failed snapshots of a source, or of one
// article when path is set, under the active fingerprint. A requeued
// snapshot is rebuilt from its stored result when one exists. It returns
// the number of requeued jobs.
func (s *Service) Reindex(ctx context.Context, sourceID, path string) (int, error) {
	if sourceID == "" {
		return 0, errors.New("knowledge: reindex needs a source")
	}
	p, ok := s.Pipeline()
	if !ok {
		return 0, errNoPipeline
	}
	jobs, err := s.cfg.Manifest.Jobs(ctx, nil)
	if err != nil {
		return 0, err
	}
	var snaps []Snapshot
	for _, j := range jobs {
		if j.Fingerprint == p.Fingerprint && j.Key.SourceID == sourceID && (path == "" || j.Key.Path == path) && (j.State == JobSucceeded || j.State == JobFailed) {
			snaps = append(snaps, j.Snapshot)
		}
	}
	n, err := s.cfg.Manifest.Requeue(ctx, snaps, s.cfg.Now())
	s.wakeFeeder()
	return n, err
}
