package knowledge

import (
	"context"
	"errors"
	"slices"
)

// ArticleStatus is the knowledge state of the snapshot of an article a
// request reads. A caller learns it only for an article it may read.
type ArticleStatus string

// Article statuses. There is no "disabled" status: with the module off REST
// answers every knowledge read with 503 knowledge_disabled.
const (
	// ArticleProcessing: the snapshot is queued or being extracted, or the
	// pipeline is not known yet.
	ArticleProcessing ArticleStatus = "processing"
	// ArticleReady: the graph of exactly this snapshot is published.
	ArticleReady ArticleStatus = "ready"
	// ArticleStale: this text was indexed before (by an older pipeline, or
	// the index lost it) and is being rebuilt. Nothing old is shown.
	ArticleStale ArticleStatus = "stale"
	// ArticleUnavailable: the extractor, the index or the manifest is
	// unavailable, so the snapshot cannot be indexed or read now.
	ArticleUnavailable ArticleStatus = "unavailable"
	// ArticleTooLarge: the text exceeds the extraction limits.
	ArticleTooLarge ArticleStatus = "too_large"
	// ArticleFailed: extraction failed permanently; the owner gets the
	// error code.
	ArticleFailed ArticleStatus = "failed"
)

// ArticleKnowledge is the knowledge of the snapshot a request reads: the
// visible one (public: published revision; reader and owner: current) or the
// owner's publication copy when requested by SHA. Knowledge is set only
// when Status is ready; Error only for the owner of a failed snapshot.
type ArticleKnowledge struct {
	Key         SnapshotKey   `json:"key"`
	Fingerprint Fingerprint   `json:"pipeline_fingerprint,omitempty"`
	Status      ArticleStatus `json:"status"`
	Error       string        `json:"error,omitempty"`
	Knowledge   *ArticleView  `json:"knowledge,omitempty"`
}

// Article is the knowledge and status of one readable article. An article
// the request may not read, or a SHA that is not one of its readable
// snapshots, is ErrNotFound, the same as an unknown article.
func (s *Service) Article(ctx context.Context, rs ReadScope, ref ArticleRef, sha string, limit int) (ArticleKnowledge, error) {
	if !validRef(ref) || !validSHA(sha) || !validLimit(limit) {
		return ArticleKnowledge{}, ErrInvalidRequest
	}
	key, allowed, err := rs.pick(ref, sha)
	if err != nil {
		return ArticleKnowledge{}, err
	}
	out := ArticleKnowledge{Key: key}
	ctx, cancel, fp, err := s.readContext(ctx)
	defer cancel()
	if err != nil {
		out.Status = s.waiting()
		return out, nil
	}
	out.Fingerprint = fp
	view, err := s.cfg.Index.Article(ctx, allowed, fp, key, limit)
	switch err = indexError(ctx, err); {
	case err == nil:
		out.Status, out.Knowledge = ArticleReady, &view
		return out, nil
	case errors.Is(err, context.DeadlineExceeded):
		return ArticleKnowledge{}, err
	case !errors.Is(err, ErrNotFound):
		out.Status = ArticleUnavailable
		return out, nil
	}
	jobs, err := s.cfg.Manifest.Jobs(ctx, []SnapshotKey{key})
	if err != nil {
		if ctx.Err() != nil {
			return ArticleKnowledge{}, context.DeadlineExceeded
		}
		out.Status = ArticleUnavailable
		return out, nil
	}
	out.Status, out.Error = s.jobStatus(jobs, fp)
	if !rs.Owner || rs.Public {
		out.Error = ""
	}
	return out, nil
}

// jobStatus is the status of a snapshot the index does not hold under fp,
// from its manifest rows (every fingerprint).
func (s *Service) jobStatus(jobs []Job, fp Fingerprint) (ArticleStatus, string) {
	indexedBefore := slices.ContainsFunc(jobs, func(j Job) bool {
		return j.Fingerprint != fp && (j.State == JobSucceeded || j.State == JobSuperseded)
	})
	i := slices.IndexFunc(jobs, func(j Job) bool { return j.Fingerprint == fp })
	if i < 0 {
		if indexedBefore {
			return ArticleStale, ""
		}
		return s.waiting(), ""
	}
	switch j := jobs[i]; j.State {
	case JobFailed:
		if j.LastError == CodeTooLarge {
			return ArticleTooLarge, ""
		}
		return ArticleFailed, j.LastError
	case JobSucceeded:
		// Published before, but the index no longer holds it:
		// reconciliation republishes it from the stored result.
		return ArticleStale, ""
	}
	if indexedBefore {
		return ArticleStale, ""
	}
	return s.waiting(), ""
}

// waiting is processing, or unavailable during an outage that stops
// indexing.
func (s *Service) waiting() ArticleStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, code := range []string{CodeExtractorUnavailable, CodeIndexUnavailable, CodeManifestUnavailable, CodeModelMismatch} {
		if s.reasons[code] {
			return ArticleUnavailable
		}
	}
	return ArticleProcessing
}

func (s Schema) hasRelation(relation string) bool {
	return slices.ContainsFunc(s.Relations, func(r RelationType) bool { return r.Type == relation })
}
