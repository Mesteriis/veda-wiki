package knowledge

import (
	"context"
	"errors"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// errModelMismatch reports a response from another checkpoint than the
// pinned one; it is treated as an outage of the extractor.
var errModelMismatch = errors.New("knowledge: extractor runs another model checkpoint")

// process runs one claimed job: copy the snapshot text under View, prepare
// it, reuse a stored result or call the extractor with no lock held, store
// the immutable result, build the graph and publish it if the snapshot is
// still active.
func (s *Service) process(ctx context.Context, job Job) {
	snap := job.Snapshot
	p, ok := s.Pipeline()
	if !ok || snap.Fingerprint != p.Fingerprint {
		s.finish(ctx, snap, Finish{State: JobSuperseded})
		return
	}
	text, active, err := s.activeText(snap.Key)
	if err != nil {
		s.retry(ctx, job, err)
		return
	}
	if !active {
		s.finish(ctx, snap, Finish{State: JobSuperseded})
		return
	}
	blocks, err := Prepare(text)
	if err != nil {
		code := CodeInvalidResult
		if errors.Is(err, ErrTooLarge) {
			code = CodeTooLarge
		}
		s.finish(ctx, snap, Finish{State: JobFailed, LastError: code})
		return
	}
	res, found, err := s.cfg.Manifest.Result(ctx, snap, p.Extraction)
	if err != nil {
		s.degrade(CodeManifestUnavailable, err)
		s.outage(ctx, job, CodeManifestUnavailable)
		return
	}
	if !found {
		if res, err = s.extract(ctx, p, snap, blocks); err != nil {
			s.extractFailed(ctx, job, err)
			return
		}
		s.recover(CodeExtractorUnavailable)
		s.recover(CodeModelMismatch)
		if err = s.cfg.Manifest.SaveResult(ctx, snap, p.Extraction, res, s.cfg.Now()); err != nil {
			s.degrade(CodeManifestUnavailable, err)
			s.outage(ctx, job, CodeManifestUnavailable)
			return
		}
	}
	g, err := BuildGraph(snap, p.Schema, blocks, res, p.Aliases)
	if err != nil {
		s.finish(ctx, snap, Finish{State: JobFailed, LastError: CodeInvalidResult})
		return
	}
	s.publish(ctx, job, g)
}

// activeText copies the text of key if it is still an active snapshot.
func (s *Service) activeText(key SnapshotKey) (string, bool, error) {
	var text string
	var ok bool
	err := s.cfg.Store.View(func(db *domain.Database) error {
		var a ActiveSnapshot
		if a, ok = ActiveSnapshotOf(*db, key); ok {
			text = strings.Clone(a.Text)
		}
		return nil
	})
	return text, ok, err
}

// extract sends the snapshot in batches and merges the checked responses.
func (s *Service) extract(ctx context.Context, p Pipeline, snap Snapshot, blocks []Block) (ExtractResult, error) {
	reqs, err := Requests(snap, p.Schema, blocks)
	if err != nil {
		return ExtractResult{}, err
	}
	results := make([]ExtractResult, 0, len(reqs))
	for _, req := range reqs {
		res, err := s.cfg.Extractor.Extract(ctx, req)
		if err != nil {
			return ExtractResult{}, err
		}
		if err = ValidateResult(req, res); err != nil {
			return ExtractResult{}, err
		}
		if err = checkResultPipeline(p, res); err != nil {
			return ExtractResult{}, err
		}
		res.RequestID = ""
		results = append(results, res)
	}
	if len(results) == 0 {
		return ExtractResult{SchemaVersion: ExtractSchemaVersion, Model: ModelInfo{ID: ModelID, Revision: ModelRevision, Pipeline: p.ExtractorPipeline}, Blocks: []BlockResult{}}, nil
	}
	return MergeResults(results)
}

func (s *Service) extractFailed(ctx context.Context, job Job, err error) {
	switch {
	case ctx.Err() != nil:
		s.outage(ctx, job, "")
	case errors.Is(err, ErrBlockTooLarge):
		s.finish(ctx, job.Snapshot, Finish{State: JobFailed, LastError: CodeTooLarge})
	case errors.Is(err, ErrRejected):
		s.finish(ctx, job.Snapshot, Finish{State: JobFailed, LastError: CodeRejected})
	case errors.Is(err, errReconfigured):
		// Discard the result, reload the settings (which switches the
		// fingerprint and reconciles) and leave the job to be superseded.
		s.refreshPipeline(ctx)
		s.outage(ctx, job, CodeExtractorReconfigured)
	case errors.Is(err, errModelMismatch):
		s.degrade(CodeModelMismatch, err)
		s.outage(ctx, job, CodeModelMismatch)
	case errors.Is(err, ErrUnavailable):
		s.degrade(CodeExtractorUnavailable, err)
		s.outage(ctx, job, CodeExtractorUnavailable)
	default:
		s.retry(ctx, job, err)
	}
}

// retry spends the job's attempt: pending with backoff, or failed once
// MaxAttempts claims have failed.
func (s *Service) retry(ctx context.Context, job Job, err error) {
	if ctx.Err() != nil {
		s.outage(ctx, job, "")
		return
	}
	code := CodeInvalidResult
	if errors.Is(err, ErrUnavailable) {
		code = CodeExtractorUnavailable
	}
	if job.Attempts >= MaxAttempts {
		s.finish(ctx, job.Snapshot, Finish{State: JobFailed, LastError: code})
		return
	}
	s.finish(ctx, job.Snapshot, Finish{State: JobPending, LastError: code, NextAttempt: s.cfg.Now().Add(Backoff(job.Attempts))})
}

// outage returns the job to pending without spending its attempt, due at
// the next availability probe (or at once after shutdown began).
func (s *Service) outage(ctx context.Context, job Job, code string) {
	s.finish(ctx, job.Snapshot, Finish{State: JobPending, LastError: code, NextAttempt: s.nextProbe(), Refund: true})
}

// finish records the end of a running job. After shutdown began it still
// writes, bounded by CleanupTimeout, so the row does not stay running.
func (s *Service) finish(ctx context.Context, snap Snapshot, f Finish) {
	if ctx.Err() != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
		defer cancel()
		ctx = cleanup
	}
	if _, err := s.cfg.Manifest.Finish(ctx, snap, f, s.cfg.Now()); err != nil {
		s.degrade(CodeManifestUnavailable, err)
	}
}
