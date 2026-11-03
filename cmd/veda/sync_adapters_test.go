package main

import (
	"context"

	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
)

// Synchronization names for the cmd tests that remain until their areas move.

type (
	SyncManager      = syncer.Manager
	connectRequest   = syncer.ConnectRequest
	mirrorRequest    = syncer.MirrorRequest
	reconcileRequest = syncer.ReconcileRequest
)

func newSyncManager(s *Store, g *github.Client) *SyncManager { return s.syncer(g) }

func (s *Store) connections() []GitHubConnection { return s.syncer(nil).Connections() }
func (s *Store) jobs() []SyncJob                 { return s.syncer(nil).Jobs() }
func (s *Store) mirrors() []SourceMirror         { return s.syncer(nil).Mirrors() }
func (s *Store) retryConnection(id string) error { return s.syncer(nil).RetryConnection(id) }

func (s *Store) pauseConnection(id string, paused bool) error {
	return s.syncer(nil).PauseConnection(id, paused)
}

func (s *Store) saveAccount(ctx context.Context, g *github.Client, id string, secret github.Secret) (GitHubAccount, error) {
	return s.syncer(g).SaveAccount(ctx, id, secret)
}

func (s *Store) configureMirror(ctx context.Context, in mirrorRequest) (SourceMirror, error) {
	return s.syncer(nil).ConfigureMirror(ctx, in)
}

func (s *Store) reconcileSources(ctx context.Context, in reconcileRequest) (syncer.ReconcileResult, error) {
	return s.syncer(nil).ReconcileSources(ctx, in)
}
