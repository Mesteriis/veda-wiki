// Package syncer synchronizes sources with GitHub: the background loop,
// connections and their status, credentials of GitHub accounts, pulls into
// the store, the write outbox and move deletions, publication approval,
// source mirrors and source reconciliation.
//
// It is the only package that changes the Outbox, Connections, Accounts and
// SourceMirrors collections, apart from shared rules in domain.
package syncer

import (
	"context"
	"log"
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
}

// New returns a manager for st that reaches GitHub through gh and applies
// pulls through imp.
func New(st *store.Store, gh *github.Client, imp *importer.Service) *Manager {
	return &Manager{store: st, github: gh, importer: imp, transfer: transfer.Service{Store: st}, wake: make(chan struct{}, 1)}
}

// Wake schedules a synchronization pass without waiting for the interval.
func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Woken reports whether a wake-up is pending that Run has not taken yet, and
// takes it. Handlers that wake the manager use it to prove they did.
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

// Once synchronizes each due connection and then each enabled mirror.
func (m *Manager) Once(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store.Invalid() {
		return
	}
	for _, initial := range m.Connections() {
		if ctx.Err() != nil {
			return
		}
		// A previous source may have put this account on rate-limit cooldown.
		// Reload rather than using the snapshot taken before the loop.
		var c domain.GitHubConnection
		var exists bool
		m.read(func(db *domain.Database) { c, exists = db.Connections[initial.ID] })
		if !exists {
			continue
		}
		if c.Paused {
			continue
		}
		if due, e := time.Parse(time.RFC3339Nano, c.NextAttempt); e == nil && due.After(time.Now()) {
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, connectionJobTimeout)
		m.syncSource(jobCtx, c)
		cancel()
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

// SyncSource runs one synchronization step of connection c at once, as Once
// does for each due connection. The step's status and import checks reject a
// connection that was paused or reconfigured since c was read.
func (m *Manager) SyncSource(ctx context.Context, c domain.GitHubConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncSource(ctx, c)
}
