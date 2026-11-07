// Package syncer synchronizes sources with GitHub: the background loop,
// connections and their status, credentials of GitHub accounts, pulls into
// the store, the write outbox and move deletions, publication approval,
// source mirrors and source reconciliation.
//
// It is the only package that changes the Connections, Accounts and
// SourceMirrors collections and the only one that edits outbox jobs itself.
// Other packages change the Outbox only through domain rules: saving or
// copying an article queues writes (domain.EnqueueWrite, EnqueueBundle,
// AddWriteDependencies) and an import settles the writes it observes
// (domain.ObservedWrite). The store only persists the collections.
package syncer

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

// Bounds of one background job.
const (
	connectionJobTimeout = 5 * time.Minute
	mirrorJobTimeout     = 90 * time.Second
	// syncWorkers bounds concurrent connection steps, and with them import
	// batches in memory (up to importer.MaxBytes each).
	syncWorkers = 2
)

// Manager runs the synchronization of one store. Its operations that do not
// reach GitHub also work without a client.
type Manager struct {
	store    *store.Store
	github   *github.Client
	importer *importer.Service
	transfer transfer.Service

	// LocalImportSource is the source the privileged local importer may
	// update; it cannot be adopted by a GitHub connection.
	LocalImportSource string

	mu   sync.Mutex
	wake chan struct{}

	// partialRetried holds the connections whose partial import this process
	// has already reread at an unchanged head (see retryPartial).
	partialRetried sync.Map

	// syncConnection runs one synchronization step of a due connection; it
	// is syncSource. Tests replace it to observe scheduling.
	syncConnection func(context.Context, domain.GitHubConnection)
}

// New returns a manager for st that reaches GitHub through gh and applies
// pulls through imp.
func New(st *store.Store, gh *github.Client, imp *importer.Service) *Manager {
	m := &Manager{store: st, github: gh, importer: imp, transfer: transfer.Service{Store: st}, wake: make(chan struct{}, 1)}
	m.syncConnection = m.syncSource
	return m
}

// Wake schedules a synchronization pass without waiting for the interval.
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Woken reports whether a wake-up is pending that Run has not taken yet, and
// takes it.
//
// Test seam: httpapi handler tests use it to prove that a request woke the
// manager. Production code must not call it, because it would consume a
// wake-up meant for Run.
func (m *Manager) Woken() bool {
	select {
	case <-m.wake:
		return true
	default:
		return false
	}
}

// Run synchronizes every interval and whenever woken until ctx ends.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	m.Wake()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
		m.Once(ctx)
	}
}

// Once synchronizes the due connections, then each enabled mirror. A ctx
// that ends starts no further work; Once still waits for running steps.
func (m *Manager) Once(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store.Invalid() {
		return
	}
	m.syncConnections(ctx)
	if ctx.Err() != nil {
		return
	}
	for _, mirror := range m.Mirrors() {
		if ctx.Err() != nil {
			return
		}
		if !mirror.Enabled {
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, mirrorJobTimeout)
		result, err := m.reconcile(jobCtx, ReconcileRequest{SourceID: mirror.SourceID, DestinationID: mirror.DestinationID, Limit: 20}, &mirror.Revision)
		cancel()
		if saveErr := m.recordMirrorResult(mirror, result, err); saveErr != nil {
			log.Print("source mirror status could not be saved")
		}
		if err == nil && (result.More || result.Created > 0 || result.Updated > 0) {
			m.Wake()
		}
	}
}

// syncConnections synchronizes the due connections of up to syncWorkers
// GitHub identities at a time and returns when all of them have finished.
// Connections of one identity run one after another in ID order, because a
// rate limit puts the whole identity on cooldown: each is reloaded just before
// its step, so it sees a cooldown recorded by the previous one.
//
// Concurrent steps share only state that is safe for it: the store and its
// blob writes take their own locks and every status write rechecks the
// connection's configuration revision; the GitHub client is stateless; import
// observations have their own mutex; cached Git objects are written to a
// fresh temporary file and renamed. Operations that must serialize a remote
// update with local state (move deletions) hold the store write lock.
func (m *Manager) syncConnections(ctx context.Context) {
	var identities []string
	byIdentity := map[string][]string{}
	m.read(func(db *domain.Database) {
		ids := make([]string, 0, len(db.Connections))
		for id := range db.Connections {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			key := identity(db, db.Connections[id].AccountID)
			if _, seen := byIdentity[key]; !seen {
				identities = append(identities, key)
			}
			byIdentity[key] = append(byIdentity[key], id)
		}
	})
	slots := make(chan struct{}, syncWorkers)
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, key := range identities {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		if ctx.Err() != nil {
			return
		}
		wg.Go(func() {
			defer func() { <-slots }()
			for _, id := range byIdentity[key] {
				if ctx.Err() != nil {
					return
				}
				m.syncDue(ctx, key, id)
			}
		})
	}
}

// syncDue runs one step of connection id if it is still due and still uses
// the GitHub identity key.
func (m *Manager) syncDue(ctx context.Context, key, id string) {
	// A previous connection may have put this identity on rate-limit
	// cooldown. Reload rather than using the snapshot taken before the pass.
	var c domain.GitHubConnection
	var exists bool
	var current string
	m.read(func(db *domain.Database) {
		c, exists = db.Connections[id]
		current = identity(db, c.AccountID)
	})
	if !exists || c.Paused {
		return
	}
	if current != key {
		// Reconfigured to an identity whose worker may be running:
		// synchronize it in the next pass instead of beside that worker.
		m.Wake()
		return
	}
	if due, e := time.Parse(time.RFC3339Nano, c.NextAttempt); e == nil && due.After(time.Now()) {
		return
	}
	jobCtx, cancel := context.WithTimeout(ctx, connectionJobTimeout)
	defer cancel()
	m.syncConnection(jobCtx, c)
}

// SyncSource runs one synchronization step of connection c at once, as Once
// does for each due connection. The step's status and import checks reject a
// connection that was paused or reconfigured since c was read.
//
// Test seam: httpapi handler tests call it; production synchronizes through
// Run and Once.
func (m *Manager) SyncSource(ctx context.Context, c domain.GitHubConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncSource(ctx, c)
}

// retryPartial reports whether a partial import of connection id should be
// reread although the branch head has not changed: once per process, so
// rules that changed with a new VEDA version (what is withheld) apply to
// existing sources without waiting for a new commit.
func (m *Manager) retryPartial(id string) bool {
	_, done := m.partialRetried.LoadOrStore(id, true)
	return !done
}
