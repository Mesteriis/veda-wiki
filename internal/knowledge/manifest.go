package knowledge

import (
	"context"
	"encoding/json"
	"time"
)

// JobState is the state of one snapshot under one fingerprint in the
// manifest. The manifest row is also the job: there is one per
// (snapshot, fingerprint).
//
//	pending -> running            claimed by a worker
//	running -> succeeded          graph published
//	running -> pending            retry (backoff) or extractor/index outage
//	running -> failed             permanent error or attempts exhausted
//	any     -> superseded         snapshot no longer active, or fingerprint changed
//	superseded|failed -> pending  snapshot active again / owner reindex
//	running -> pending            on start (a crash left it running)
//
// Finishing a job only changes a running row, so a slow worker can never
// overwrite a row reconciliation already superseded.
type JobState string

// Job states.
const (
	JobPending    JobState = "pending"
	JobRunning    JobState = "running"
	JobSucceeded  JobState = "succeeded"
	JobFailed     JobState = "failed"
	JobSuperseded JobState = "superseded"
)

// Error codes recorded on jobs and reported in status. They never carry
// article text, URLs or secrets.
const (
	CodeTooLarge             = "too_large"
	CodeRejected             = "extractor_rejected"
	CodeInvalidResult        = "invalid_result"
	CodeModelMismatch        = "model_mismatch"
	CodeExtractorUnavailable = "extractor_unavailable"
	CodeIndexUnavailable     = "index_unavailable"
	CodeManifestUnavailable  = "manifest_unavailable"
	// CodeExtractorReconfigured: a response reported other pipeline
	// settings than the active fingerprint; the result is discarded and
	// the pipeline is reloaded from /ready.
	CodeExtractorReconfigured = "extractor_reconfigured"
)

// Retry policy: at most MaxAttempts claims that end in an error counted
// against the job, with delays BackoffBase * 2^(attempt-1) up to BackoffMax.
// Outages (extractor or index unavailable) refund the attempt.
const (
	MaxAttempts = 6
	BackoffBase = 30 * time.Second
	BackoffMax  = 30 * time.Minute
)

// Backoff is the delay before retry number attempt (1-based).
func Backoff(attempt int) time.Duration {
	d := BackoffBase
	for i := 1; i < attempt && d < BackoffMax; i++ {
		d *= 2
	}
	return min(d, BackoffMax)
}

// Job is one manifest row.
type Job struct {
	Snapshot
	State       JobState
	Attempts    int
	NextAttempt time.Time
	LastError   string
	Updated     time.Time
}

// Finish is how a running job ends.
type Finish struct {
	State       JobState
	LastError   string
	NextAttempt time.Time
	// Refund gives the claim's attempt back (outages).
	Refund bool
}

// ReconcileChange is what Manifest.Reconcile changed.
type ReconcileChange struct {
	// Superseded rows: snapshots no longer active, or of another fingerprint.
	Superseded []Snapshot
	// Added rows and rows made pending again.
	Pending int
}

// Manifest is the durable job and result store (knowledge.sqlite).
type Manifest interface {
	// Reconcile makes the manifest match the active snapshots under fp in
	// one transaction: missing rows are added as pending, superseded rows
	// of active snapshots become pending, and rows of inactive snapshots or
	// of another fingerprint become superseded.
	Reconcile(ctx context.Context, fp Fingerprint, active []SnapshotKey, now time.Time) (ReconcileChange, error)
	// Requeue makes succeeded or failed rows of snaps pending with no
	// attempts spent (index lost them, or an owner reindex).
	Requeue(ctx context.Context, snaps []Snapshot, now time.Time) (int, error)
	// ResetRunning makes every running row pending; used once on start.
	ResetRunning(ctx context.Context, now time.Time) (int, error)
	// Claim marks up to n due pending rows under fp running, spends an
	// attempt on each and returns them, oldest due first.
	Claim(ctx context.Context, fp Fingerprint, now time.Time, n int) ([]Job, error)
	// Finish ends a running row; it reports false and changes nothing when
	// the row is no longer running.
	Finish(ctx context.Context, snap Snapshot, f Finish, now time.Time) (bool, error)
	// NextDue is the earliest NextAttempt of a pending row under fp.
	NextDue(ctx context.Context, fp Fingerprint) (time.Time, bool, error)
	// Jobs lists the rows of snapshot keys (every fingerprint), or all rows
	// when keys is empty.
	Jobs(ctx context.Context, keys []SnapshotKey) ([]Job, error)
	// Counts counts rows under fp by state.
	Counts(ctx context.Context, fp Fingerprint) (map[JobState]int, error)

	// SaveResult stores the extraction result of snap once; a result that
	// exists is never replaced. extraction identifies the extraction part
	// of the pipeline (Pipeline.Extraction).
	SaveResult(ctx context.Context, snap Snapshot, extraction string, res ExtractResult, now time.Time) error
	// Result loads the result of snap, or of the same snapshot key under
	// another fingerprint with the same extraction identity.
	Result(ctx context.Context, snap Snapshot, extraction string) (ExtractResult, bool, error)
	// ExtractorPipeline is the extractor pipeline object last seen in
	// /ready, so a restart while the extractor is down keeps serving and
	// reconciling under the last fingerprint.
	ExtractorPipeline(ctx context.Context) (json.RawMessage, bool, error)
	// SetExtractorPipeline records the extractor pipeline object.
	SetExtractorPipeline(ctx context.Context, pipeline json.RawMessage, now time.Time) error
	// Prune removes superseded rows and their results older than before,
	// except results still referenced by a non-superseded row.
	Prune(ctx context.Context, before time.Time) (int, error)
}

// Pipeline is the active configuration: schema, alias rules, the
// extractor's reported settings, the fingerprint every read and job uses,
// and the extraction identity, which leaves out resolution so an alias
// change re-resolves stored results without calling the extractor again.
type Pipeline struct {
	Schema      Schema
	Aliases     Aliases
	Fingerprint Fingerprint
	Extraction  string
	// Extractor is ExtractorDigest of ExtractorPipeline, the settings the
	// extractor reported in /ready; every response must report the same.
	Extractor         string
	ExtractorPipeline json.RawMessage
}

// NewPipeline derives the pipeline from the schema, the alias rules and
// their digest (ParseAliases; empty without an aliases file) and the
// extractor's pipeline object from /ready.
func NewPipeline(s Schema, aliases Aliases, aliasDigest string, extractor json.RawMessage) (Pipeline, error) {
	digest, err := ExtractorDigest(extractor)
	if err != nil {
		return Pipeline{}, err
	}
	resolve := ResolveVersion
	if aliasDigest != "" {
		resolve += "+" + aliasDigest
	}
	checkpoint := Checkpoint + "+" + digest
	return Pipeline{
		Schema:            s,
		Aliases:           aliases,
		Fingerprint:       PipelineFingerprint(s.Digest, PreprocessVersion, resolve, checkpoint),
		Extraction:        string(PipelineFingerprint(s.Digest, PreprocessVersion, "", checkpoint)),
		Extractor:         digest,
		ExtractorPipeline: append(json.RawMessage(nil), extractor...),
	}, nil
}
