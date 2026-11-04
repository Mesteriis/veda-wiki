package syncer

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

// ReconcileRequest copies the articles of a source into a verified private
// GitHub destination source, at most Limit changes per call.
type ReconcileRequest struct {
	SourceID      string `json:"source_id"`
	DestinationID string `json:"destination_id"`
	Limit         int    `json:"limit,omitempty"`
}

// ReconcileResult counts what one reconciliation pass changed.
type ReconcileResult struct {
	Baselines int      `json:"baselines"`
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Unchanged int      `json:"unchanged"`
	Blocked   []string `json:"blocked_paths"`
	More      bool     `json:"more"`
}

// Connected sources must have a complete, recently verified private snapshot.
// Disconnected sources may be partial: individual unsafe paths are withheld.
func reconcileSourceReady(db *domain.Database, src domain.Source, dst domain.GitHubConnection) bool {
	if src.Visibility != "private" {
		return false
	}
	if src.ConnectionID == "" {
		return true
	}
	c, ok := db.Connections[src.ConnectionID]
	return ok && c.ID == src.ID && c.Private && !src.ImportPartial && domain.SourceSyncIssue(*db, src, time.Now()) == "" &&
		c.RepositoryID != 0 && dst.RepositoryID != 0 && (c.RepositoryID != dst.RepositoryID || c.Branch != dst.Branch)
}

func pendingReconcilePath(db *domain.Database, source, p string) bool {
	if domain.MoveDeletionLocks(*db, source, p) || domain.ConflictResolutionBlocked(*db, source, p) {
		return true
	}
	for _, j := range db.Outbox {
		if j.SourceID != source || j.State == "complete" || j.State == "superseded" {
			continue
		}
		if j.Path == p {
			return true
		}
		if _, ok := j.Bundle[p]; ok {
			return true
		}
		if _, ok := j.Links[p]; ok {
			return true
		}
	}
	return false
}

// tolerated errors block one path; any other error ends the pass.
func tolerated(err error) bool {
	return errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrNotFound)
}

// ReconcileSources performs bounded, idempotent transactions. It never
// deletes either side, never infers a baseline from unequal content, and only
// queues writes to a verified private GitHub destination. The caller must be
// an owner.
func (m *Manager) ReconcileSources(ctx context.Context, in ReconcileRequest) (ReconcileResult, error) {
	return m.reconcile(ctx, in, nil)
}

// reconciliation is one pass. The store write lock is held for the whole
// pass; each change is its own transaction.
type reconciliation struct {
	ctx      context.Context
	ex       *store.Exclusive
	transfer transfer.Service
	src, dst domain.Source
	limit    int
	changes  int
	result   ReconcileResult
}

// reconcile is ReconcileSources for a mirror at mirrorRevision, or for a
// request when mirrorRevision is nil.
func (m *Manager) reconcile(ctx context.Context, in ReconcileRequest, mirrorRevision *int) (ReconcileResult, error) {
	r := &reconciliation{ctx: ctx, transfer: m.transfer, result: ReconcileResult{Blocked: []string{}}}
	if !domain.ValidID(in.SourceID) || !domain.ValidID(in.DestinationID) || in.SourceID == in.DestinationID {
		return r.result, domain.ErrInvalid
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Limit < 1 || in.Limit > 50 {
		return r.result, domain.ErrInvalid
	}
	r.limit = in.Limit
	err := m.store.Exclusive(func(ex *store.Exclusive) error {
		r.ex = ex
		return r.run(in, mirrorRevision)
	})
	return r.result, err
}

func (r *reconciliation) run(in ReconcileRequest, mirrorRevision *int) error {
	db := r.ex.DB()
	if mirrorRevision != nil {
		mirror, ok := db.SourceMirrors[in.DestinationID]
		if !ok || !mirror.Enabled || mirror.Revision != *mirrorRevision || mirror.SourceID != in.SourceID {
			return domain.ErrConflict
		}
	}
	var ok bool
	if r.src, ok = db.Sources[in.SourceID]; !ok {
		return domain.ErrNotFound
	}
	if r.dst, ok = db.Sources[in.DestinationID]; !ok {
		return domain.ErrNotFound
	}
	src, dst := r.src, r.dst
	conn, connected := db.Connections[dst.ConnectionID]
	if dst.Visibility != "private" ||
		src.Kind != dst.Kind ||
		!connected ||
		conn.ID != dst.ID ||
		!conn.Private ||
		conn.Verification != "verified" ||
		conn.Paused ||
		!conn.Write ||
		conn.LastPull == "" ||
		dst.ImportPartial ||
		!reconcileSourceReady(db, src, conn) {
		return domain.ErrConflict
	}
	paths := []string{}
	for _, a := range db.Articles {
		if a.SourceID == src.ID && !a.Deleted {
			paths = append(paths, a.Path)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		more, err := r.path(p)
		if err != nil {
			return err
		}
		if more {
			r.result.More = true
			break
		}
	}
	return nil
}

func (r *reconciliation) block(p string) { r.result.Blocked = append(r.result.Blocked, p) }

// path reconciles one source article. It reports more when the change limit
// is reached before the path could be changed.
func (r *reconciliation) path(p string) (bool, error) {
	src, dst := r.src, r.dst
	db := r.ex.DB()
	a := db.Articles[src.ID+"/"+p]
	if a.Deleted || domain.LatestRevision(a) == 0 {
		r.block(p)
		return false, nil
	}
	rev, _ := domain.LastRevision(a)
	unsafe := domain.CredentialLike(rev.Text)
	for _, c := range a.Conflicts {
		if c.Resolution == "" {
			unsafe = true
		}
	}
	if unsafe || (src.ConnectionID != "" && a.UpstreamHash != rev.SHA) || pendingReconcilePath(db, src.ID, p) || pendingReconcilePath(db, dst.ID, p) {
		r.block(p)
		return false, nil
	}
	target, exists := db.Articles[dst.ID+"/"+p]
	if exists {
		if target.Deleted || domain.LatestRevision(target) == 0 {
			r.block(p)
			return false, nil
		}
		for _, c := range target.Conflicts {
			if c.Resolution == "" {
				unsafe = true
			}
		}
		latest, _ := domain.LastRevision(target)
		if unsafe || target.UpstreamHash != latest.SHA {
			r.block(p)
			return false, nil
		}
		base := target.ReconcileBase
		if base != nil && (base.SourceID != src.ID || base.Path != p) {
			r.block(p)
			return false, nil
		}
		if latest.SHA == rev.SHA {
			return r.equal(p, rev, target, base)
		}
		if base == nil || base.SHA != latest.SHA {
			r.block(p)
			return false, nil
		}
	}
	if r.changes >= r.limit {
		return true, nil
	}
	expected := 0
	if exists {
		expected = domain.LatestRevision(target)
	}
	req := transfer.CopyRequest{SourceID: src.ID, SourcePath: p, ExpectedRevision: rev.Number, DestinationID: dst.ID, DestinationPath: p, ExpectedDestinationRevision: &expected}
	refused, err := r.stage(func(db *domain.Database) error { return r.copy(db, req, rev) })
	if err != nil {
		return false, err
	}
	if refused == nil {
		r.changes++
		if exists {
			r.result.Updated++
		} else {
			r.result.Created++
		}
		return false, nil
	}
	// A new root may carry a small, internally consistent linked graph.
	if !exists && (errors.Is(refused, domain.ErrConflict) || errors.Is(refused, domain.ErrInvalid)) {
		added, copied, err := r.graph(req)
		if err != nil {
			return false, err
		}
		if copied {
			r.changes += added
			r.result.Created += added
			return false, nil
		}
	}
	if tolerated(refused) {
		r.block(p)
		return false, nil
	}
	return false, refused
}

// stage runs one change as its own transaction. refused is the error with
// which change refused; err is a context or commit failure that ends the pass.
func (r *reconciliation) stage(change func(db *domain.Database) error) (refused, err error) {
	err = r.ex.Update(func(tx *store.Tx) error {
		db := tx.DB()
		if refused = change(db); refused != nil {
			return refused
		}
		if db.Version < 18 {
			db.Version = 18
		}
		return r.ctx.Err()
	})
	if refused != nil {
		return refused, nil
	}
	return nil, err
}

// copy copies one article and records the source revision it reconciles.
func (r *reconciliation) copy(db *domain.Database, req transfer.CopyRequest, rev domain.Revision) error {
	if _, err := r.transfer.PrepareCopy(db, req); err != nil {
		return err
	}
	key := req.DestinationID + "/" + req.DestinationPath
	article := db.Articles[key]
	article.ReconcileBase = &domain.ReconcileBase{SourceID: req.SourceID, Path: req.SourcePath, Revision: rev.Number, SHA: rev.SHA}
	db.Articles[key] = article
	return nil
}

// equal handles a destination article that already matches the source: it
// copies missing images or records the reconciled baseline.
func (r *reconciliation) equal(p string, rev domain.Revision, target domain.Article, base *domain.ReconcileBase) (bool, error) {
	src, dst := r.src, r.dst
	deps, depErr := r.transfer.ResolveDependencies(r.ex.DB(), rev.Text, p, p, src, dst)
	if depErr != nil {
		if tolerated(depErr) {
			r.block(p)
			return false, nil
		}
		return false, depErr
	}
	missingAsset := false
	for name, ref := range deps.Assets {
		if dst.Assets[name] != ref {
			missingAsset = true
			break
		}
	}
	if missingAsset {
		if r.changes >= r.limit {
			return true, nil
		}
		expected := domain.LatestRevision(target)
		req := transfer.CopyRequest{SourceID: src.ID, SourcePath: p, ExpectedRevision: rev.Number, DestinationID: dst.ID, DestinationPath: p, ExpectedDestinationRevision: &expected}
		refused, err := r.stage(func(db *domain.Database) error { return r.copy(db, req, rev) })
		if err != nil {
			return false, err
		}
		if refused != nil {
			if tolerated(refused) {
				r.block(p)
				return false, nil
			}
			return false, refused
		}
		r.changes++
		r.result.Updated++
		return false, nil
	}
	if base != nil && base.Revision == rev.Number && base.SHA == rev.SHA {
		r.result.Unchanged++
		return false, nil
	}
	if r.changes >= r.limit {
		return true, nil
	}
	_, err := r.stage(func(db *domain.Database) error {
		target.ReconcileBase = &domain.ReconcileBase{SourceID: src.ID, Path: p, Revision: rev.Number, SHA: rev.SHA}
		db.Articles[dst.ID+"/"+p] = target
		return nil
	})
	if err != nil {
		return false, err
	}
	r.changes++
	r.result.Baselines++
	return false, nil
}

// graph copies a new root with its missing linked articles when every one of
// them is safe and within the change limit. copied reports whether it did;
// err ends the pass.
func (r *reconciliation) graph(req transfer.CopyRequest) (added int, copied bool, err error) {
	src, dst := r.src, r.dst
	db := r.ex.DB()
	plan, planErr := transfer.BuildPlan(db, req)
	if planErr != nil && !tolerated(planErr) {
		return 0, false, planErr
	}
	safe := planErr == nil && len(plan.Articles) <= r.limit-r.changes
	if safe {
		for _, item := range plan.Articles {
			linked := db.Articles[src.ID+"/"+item.SourcePath]
			if item.CredentialLike ||
				item.DirectoryIndex ||
				item.Destination == "conflict" ||
				pendingReconcilePath(db, src.ID, item.SourcePath) ||
				pendingReconcilePath(db, dst.ID, item.DestinationPath) ||
				(src.ConnectionID != "" && linked.UpstreamHash != item.SHA) {
				safe = false
				break
			}
		}
	}
	if !safe {
		return 0, false, nil
	}
	refused, err := r.stage(func(db *domain.Database) error {
		if _, _, err := r.transfer.PrepareLinkedCopy(db, req); err != nil {
			return err
		}
		added = 0
		for _, item := range plan.Articles {
			if item.Destination != "missing" {
				continue
			}
			key := dst.ID + "/" + item.DestinationPath
			article := db.Articles[key]
			article.ReconcileBase = &domain.ReconcileBase{SourceID: src.ID, Path: item.SourcePath, Revision: item.Revision, SHA: item.SHA}
			db.Articles[key] = article
			added++
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	if refused != nil {
		if !tolerated(refused) {
			return 0, false, refused
		}
		return 0, false, nil
	}
	return added, true, nil
}
