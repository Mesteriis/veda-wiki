package knowledge

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Snapshots is read access to VEDA's store; *store.Store satisfies it. The
// service reads under View only and never writes articles.
type Snapshots interface {
	View(fn func(db *domain.Database) error) error
}

// Worker and reconciliation bounds.
const (
	MaxWorkers               = 4
	MinReconcileInterval     = 30 * time.Second
	DefaultReconcileInterval = 5 * time.Minute
	// SupersededRetention keeps superseded rows and their results this
	// long, so a revert to a recent text reuses its stored result.
	SupersededRetention = 7 * 24 * time.Hour
	// CleanupTimeout bounds manifest writes made after shutdown began.
	CleanupTimeout = 2 * time.Second
)

// Config wires a Service. Store, Manifest, Index and Extractor are required.
// The pipeline is derived from Schema, Aliases and AliasDigest and from the
// settings the extractor reports in /ready.
type Config struct {
	Store       Snapshots
	Manifest    Manifest
	Index       Index
	Extractor   Extractor
	Schema      Schema
	Aliases     Aliases
	AliasDigest string
	// Workers is the number of concurrent jobs, 1..MaxWorkers (default 1).
	Workers int
	// ReconcileInterval is the periodic full reconciliation (default 5m).
	ReconcileInterval time.Duration
	// SignalDelay batches bursts of commit signals before reconciling
	// (default 1s).
	SignalDelay time.Duration
	// ProbeBase and ProbeMax bound the wait between availability probes
	// during an outage (defaults 5s and 5m).
	ProbeBase, ProbeMax time.Duration
	// ReadTimeout bounds one index query of the read service (default
	// DefaultReadTimeout).
	ReadTimeout time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Logf receives operational messages without article text or secrets.
	Logf func(format string, args ...any)
}

// Service runs background indexing: reconciliation of the manifest with the
// store, a bounded queue fed from the manifest, and workers that extract,
// build and publish snapshot graphs. VEDA never waits for it: the store only
// sends a non-blocking Signal after commits.
type Service struct {
	cfg       Config
	signal    chan struct{}
	wake      chan struct{}
	queue     chan Job
	publishMu sync.Mutex
	// refreshMu serializes reloads of the extractor settings.
	refreshMu sync.Mutex
	// reindexMu admits one reindex operation at a time.
	reindexMu sync.Mutex
	started   atomic.Bool

	mu            sync.Mutex
	reasons       map[string]bool
	outageUntil   time.Time
	outageDelay   time.Duration
	lastReconcile time.Time
	dropped       GraphDropped
	lastReindex   *ReindexOperation
	// pipeline is nil until the extractor's settings are known (from the
	// manifest or /ready); nothing is reconciled or processed before.
	pipeline *Pipeline
}

// NewService checks cfg and fills defaults.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Manifest == nil || cfg.Index == nil || cfg.Extractor == nil || cfg.Schema.Digest == "" {
		return nil, errors.New("knowledge: incomplete service configuration")
	}
	if cfg.Workers == 0 {
		cfg.Workers = 1
	}
	if cfg.Workers < 1 || cfg.Workers > MaxWorkers {
		return nil, errors.New("knowledge: workers must be 1 to 4")
	}
	if cfg.ReconcileInterval == 0 {
		cfg.ReconcileInterval = DefaultReconcileInterval
	}
	if cfg.SignalDelay == 0 {
		cfg.SignalDelay = time.Second
	}
	if cfg.ProbeBase == 0 {
		cfg.ProbeBase = 5 * time.Second
	}
	if cfg.ProbeMax == 0 {
		cfg.ProbeMax = 5 * time.Minute
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = DefaultReadTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Service{cfg: cfg, signal: make(chan struct{}, 1), wake: make(chan struct{}, 1), queue: make(chan Job, cfg.Workers), reasons: map[string]bool{}}, nil
}

// Signal asks for a reconciliation soon. It never blocks: it is called by
// the store after a commit, under the store's write lock.
func (s *Service) Signal() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *Service) wakeFeeder() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Index is the index the service publishes to, for the read service.
func (s *Service) Index() Index { return s.cfg.Index }

// Run resets jobs a previous process left running, then reconciles on
// start, every ReconcileInterval and after signals, and processes jobs
// until ctx ends. It returns after every goroutine it started has stopped;
// jobs still queued go back to pending.
func (s *Service) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("knowledge: service already running")
	}
	s.loadPipeline(ctx)
	s.refreshPipeline(ctx)
	if n, err := s.cfg.Manifest.ResetRunning(ctx, s.cfg.Now()); err != nil {
		s.degrade(CodeManifestUnavailable, err)
	} else if n > 0 {
		s.cfg.Logf("knowledge: %d interrupted jobs requeued", n)
	}
	var wg sync.WaitGroup
	wg.Add(2 + s.cfg.Workers)
	go func() { defer wg.Done(); s.reconcileLoop(ctx) }()
	go func() { defer wg.Done(); s.feed(ctx) }()
	for range s.cfg.Workers {
		go func() { defer wg.Done(); s.work(ctx) }()
	}
	wg.Wait()
	cleanup, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
	defer cancel()
	for len(s.queue) > 0 {
		job := <-s.queue
		_, _ = s.cfg.Manifest.Finish(cleanup, job.Snapshot, Finish{State: JobPending, NextAttempt: s.cfg.Now(), Refund: true}, s.cfg.Now())
	}
	return nil
}

func (s *Service) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.ReconcileInterval)
	defer ticker.Stop()
	for {
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			s.cfg.Logf("knowledge: reconciliation incomplete: %s", reasonOf(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.signal:
			if !sleep(ctx, s.cfg.SignalDelay) {
				return
			}
		}
	}
}

func (s *Service) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.queue:
			s.process(ctx, job)
			s.wakeFeeder()
		}
	}
}

// feed claims due jobs into the queue, at most its free capacity, and
// pauses claiming during an outage until a probe succeeds.
func (s *Service) feed(ctx context.Context) {
	for ctx.Err() == nil {
		if wait := s.outageWait(); wait > 0 {
			if !sleep(ctx, wait) {
				return
			}
			s.probe(ctx)
			continue
		}
		p, ok := s.Pipeline()
		if !ok {
			s.degrade(CodeExtractorUnavailable, ErrUnavailable)
			continue
		}
		fp := p.Fingerprint
		if free := cap(s.queue) - len(s.queue); free > 0 {
			jobs, err := s.cfg.Manifest.Claim(ctx, fp, s.cfg.Now(), free)
			if err != nil {
				if ctx.Err() == nil {
					s.degrade(CodeManifestUnavailable, err)
				}
				continue
			}
			s.recover(CodeManifestUnavailable)
			for _, j := range jobs {
				s.queue <- j
			}
			if len(jobs) == free {
				continue
			}
		}
		wait := s.cfg.ReconcileInterval
		if due, ok, err := s.cfg.Manifest.NextDue(ctx, fp); err == nil && ok {
			wait = min(wait, max(due.Sub(s.cfg.Now()), 0)+10*time.Millisecond)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// probe checks the extractor (reloading its pipeline) and the index
// after an outage.
func (s *Service) probe(ctx context.Context) {
	ok := s.refreshPipeline(ctx)
	if err := s.cfg.Index.Status(ctx); err != nil {
		ok = false
		s.degrade(CodeIndexUnavailable, err)
	} else {
		s.recover(CodeIndexUnavailable)
	}
	if ok {
		s.mu.Lock()
		s.outageUntil, s.outageDelay = time.Time{}, 0
		s.mu.Unlock()
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Status is the owner view of background indexing. Reasons are codes only.
type Status struct {
	Enabled       bool             `json:"enabled"`
	State         string           `json:"state"`
	Reasons       []string         `json:"reasons,omitempty"`
	Fingerprint   Fingerprint      `json:"pipeline_fingerprint"`
	Jobs          map[JobState]int `json:"jobs"`
	LastReconcile *time.Time       `json:"last_reconcile,omitempty"`
	// Dropped sums, since start, the model output left out in published
	// graphs by the extractor and by Go-side rules.
	Dropped GraphDropped `json:"dropped"`
	// LastReindex is the most recent reindex operation, if any.
	LastReindex *ReindexOperation `json:"last_reindex,omitempty"`
}

// Status states.
const (
	StateReady      = "ready"
	StateProcessing = "processing"
	StateDegraded   = "degraded"
)

// Status reports the job counts under the active fingerprint, the outage
// reasons and the last completed reconciliation.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{Enabled: true, Jobs: map[JobState]int{}}
	if p, ok := s.Pipeline(); ok {
		st.Fingerprint = p.Fingerprint
		// A failed read is reported in this answer only; it does not
		// change the service's state.
		counts, err := s.cfg.Manifest.Counts(ctx, st.Fingerprint)
		if err != nil {
			st.Reasons = append(st.Reasons, CodeManifestUnavailable)
		} else {
			st.Jobs = counts
		}
	}
	s.mu.Lock()
	for r := range s.reasons {
		if r != CodeManifestUnavailable || !slices.Contains(st.Reasons, r) {
			st.Reasons = append(st.Reasons, r)
		}
	}
	st.Dropped = s.dropped
	if s.lastReindex != nil {
		op := *s.lastReindex
		st.LastReindex = &op
	}
	if !s.lastReconcile.IsZero() {
		t := s.lastReconcile
		st.LastReconcile = &t
	}
	s.mu.Unlock()
	sort.Strings(st.Reasons)
	switch {
	case len(st.Reasons) > 0:
		st.State = StateDegraded
	case st.Fingerprint == "" || st.Jobs[JobPending]+st.Jobs[JobRunning] > 0 || st.LastReconcile == nil:
		st.State = StateProcessing
	default:
		st.State = StateReady
	}
	return st
}

// States is the job state of each key under the active fingerprint; keys
// without a row are absent.
func (s *Service) States(ctx context.Context, keys []SnapshotKey) (map[SnapshotKey]JobState, error) {
	out := map[SnapshotKey]JobState{}
	p, ok := s.Pipeline()
	if !ok {
		return out, nil
	}
	jobs, err := s.cfg.Manifest.Jobs(ctx, keys)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j.Fingerprint == p.Fingerprint {
			out[j.Key] = j.State
		}
	}
	return out, nil
}

// degrade records an outage reason; an extractor or index outage also
// pauses claiming for a growing probe delay.
func (s *Service) degrade(code string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.reasons[code] {
		s.cfg.Logf("knowledge: degraded: %s (%s)", code, reasonOf(err))
	}
	s.reasons[code] = true
	if code == CodeExtractorUnavailable || code == CodeIndexUnavailable || code == CodeModelMismatch || code == CodeManifestUnavailable {
		s.outageDelay = min(max(s.outageDelay*2, s.cfg.ProbeBase), s.cfg.ProbeMax)
		s.outageUntil = s.cfg.Now().Add(s.outageDelay)
	}
}

func (s *Service) recover(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reasons[code] {
		s.cfg.Logf("knowledge: recovered: %s", code)
		delete(s.reasons, code)
	}
}

func (s *Service) outageWait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outageUntil.IsZero() {
		return 0
	}
	if d := s.outageUntil.Sub(s.cfg.Now()); d > 0 {
		return d
	}
	return time.Nanosecond
}

func (s *Service) nextProbe() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outageUntil.IsZero() {
		return s.cfg.Now()
	}
	return s.outageUntil
}

// reasonOf names an error class without its message, which may hold a URL.
func reasonOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "timeout or cancelled"
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	case errors.Is(err, ErrRejected):
		return "rejected"
	case errors.Is(err, ErrInvalidResponse):
		return "invalid response"
	}
	return "error"
}
