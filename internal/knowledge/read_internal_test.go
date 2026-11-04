package knowledge

import (
	"context"
	"errors"
	"testing"
)

// Article status of a snapshot the index does not hold, from its manifest
// rows under every fingerprint, and the outage reasons.
func TestJobStatus(t *testing.T) {
	const active, old Fingerprint = "kg1-new", "kg1-old"
	row := func(fp Fingerprint, state JobState, code string) Job {
		return Job{Snapshot: Snapshot{Fingerprint: fp}, State: state, LastError: code}
	}
	cases := []struct {
		name    string
		jobs    []Job
		reasons []string
		status  ArticleStatus
		detail  string
	}{
		{"no row yet", nil, nil, ArticleProcessing, ""},
		{"no row during an extractor outage", nil, []string{CodeExtractorUnavailable}, ArticleUnavailable, ""},
		{"pending", []Job{row(active, JobPending, "")}, nil, ArticleProcessing, ""},
		{"running", []Job{row(active, JobRunning, "")}, nil, ArticleProcessing, ""},
		{"pending during an index outage", []Job{row(active, JobPending, "")}, []string{CodeIndexUnavailable}, ArticleUnavailable, ""},
		{"pending with a manifest outage", []Job{row(active, JobPending, "")}, []string{CodeManifestUnavailable}, ArticleUnavailable, ""},
		{"pending, reconfigured only", []Job{row(active, JobPending, "")}, []string{CodeExtractorReconfigured}, ArticleProcessing, ""},
		{"superseded under the active fingerprint", []Job{row(active, JobSuperseded, "")}, nil, ArticleProcessing, ""},
		{"rebuilding after a pipeline change", []Job{row(old, JobSuperseded, ""), row(active, JobPending, "")}, nil, ArticleStale, ""},
		{"old pipeline only", []Job{row(old, JobSucceeded, "")}, nil, ArticleStale, ""},
		{"old failure does not count", []Job{row(old, JobFailed, CodeTooLarge)}, nil, ArticleProcessing, ""},
		{"succeeded but not in the index", []Job{row(active, JobSucceeded, "")}, nil, ArticleStale, ""},
		{"too large", []Job{row(active, JobFailed, CodeTooLarge)}, nil, ArticleTooLarge, ""},
		{"failed", []Job{row(active, JobFailed, CodeInvalidResult)}, nil, ArticleFailed, CodeInvalidResult},
		{"failed despite an older success", []Job{row(old, JobSucceeded, ""), row(active, JobFailed, CodeRejected)}, nil, ArticleFailed, CodeRejected},
	}
	for _, c := range cases {
		s := &Service{reasons: map[string]bool{}}
		for _, r := range c.reasons {
			s.reasons[r] = true
		}
		status, detail := s.jobStatus(c.jobs, active)
		if status != c.status || detail != c.detail {
			t.Errorf("%s: %s %q, want %s %q", c.name, status, detail, c.status, c.detail)
		}
	}
}

func TestIndexErrorHidesInternals(t *testing.T) {
	ctx := context.Background()
	if err := indexError(ctx, errors.New("bolt://user:secret@host failed")); !errors.Is(err, ErrReadUnavailable) || err.Error() != ErrReadUnavailable.Error() {
		t.Fatalf("index error leaked: %v", err)
	}
	if err := indexError(ctx, ErrNotFound); err != ErrNotFound {
		t.Fatalf("not found: %v", err)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if err := indexError(done, errors.New("i/o")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if err := (ReadScope{}).access(func(readAccess) error { return nil }); !errors.Is(err, ErrReadUnavailable) {
		t.Fatalf("a read without a request store must fail closed: %v", err)
	}
}
