package knowledge

import (
	"context"
	"errors"
	"fmt"
)

// errReconfigured reports a response produced with other extractor
// settings than the active pipeline.
var errReconfigured = errors.New("knowledge: extractor was reconfigured")

// errNoPipeline: the extractor's settings are not known yet.
var errNoPipeline = errors.New("knowledge: extractor settings unknown")

// Pipeline is the active pipeline, if the extractor's settings are known.
// Reads must use its fingerprint and nothing else.
func (s *Service) Pipeline() (Pipeline, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pipeline == nil {
		return Pipeline{}, false
	}
	return *s.pipeline, true
}

// loadPipeline restores the pipeline of the extractor settings last seen,
// so a restart during an extractor outage keeps its fingerprint.
func (s *Service) loadPipeline(ctx context.Context) {
	raw, ok, err := s.cfg.Manifest.ExtractorPipeline(ctx)
	if err != nil {
		s.degrade(CodeManifestUnavailable, err)
		return
	}
	if !ok {
		return
	}
	p, err := NewPipeline(s.cfg.Schema, s.cfg.Aliases, s.cfg.AliasDigest, raw)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.pipeline = &p
	s.mu.Unlock()
}

// refreshPipeline asks the extractor for its model and settings. When they
// differ from the active pipeline it records them, switches the fingerprint
// and asks for a reconciliation, which supersedes every row of the old
// fingerprint and retires its graphs. It reports whether the extractor is
// ready with the pinned checkpoint.
func (s *Service) refreshPipeline(ctx context.Context) bool {
	info, err := s.cfg.Extractor.Ready(ctx)
	if err == nil && (info.ID != ModelID || info.Revision != ModelRevision) {
		s.degrade(CodeModelMismatch, errModelMismatch)
		return false
	}
	if err != nil {
		s.degrade(CodeExtractorUnavailable, err)
		return false
	}
	p, err := NewPipeline(s.cfg.Schema, s.cfg.Aliases, s.cfg.AliasDigest, info.Pipeline)
	if err != nil {
		s.degrade(CodeExtractorUnavailable, err)
		return false
	}
	s.recover(CodeExtractorUnavailable)
	s.recover(CodeModelMismatch)
	if current, ok := s.Pipeline(); ok && current.Fingerprint == p.Fingerprint {
		return true
	}
	if err = s.cfg.Manifest.SetExtractorPipeline(ctx, p.ExtractorPipeline, s.cfg.Now()); err != nil {
		s.degrade(CodeManifestUnavailable, err)
		return false
	}
	s.mu.Lock()
	changed := s.pipeline != nil
	s.pipeline = &p
	s.mu.Unlock()
	if changed {
		s.cfg.Logf("knowledge: extractor settings changed; reprocessing under a new fingerprint")
	}
	s.Signal()
	return true
}

// checkResultPipeline verifies that a response was produced with the
// settings of the active pipeline.
func checkResultPipeline(p Pipeline, res ExtractResult) error {
	if res.Model.ID != ModelID || res.Model.Revision != ModelRevision {
		return errModelMismatch
	}
	digest, err := ExtractorDigest(res.Model.Pipeline)
	if err != nil {
		return err
	}
	if digest != p.Extractor {
		return fmt.Errorf("%w: %s", errReconfigured, "pipeline settings differ")
	}
	return nil
}
