package main

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/agentctx"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/search"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

// Temporary aliases while the backend moves into internal packages. Each
// restructure task removes the entries it no longer needs; Task 15 deletes
// this file.

type (
	Source           = domain.Source
	SourceView       = domain.SourceView
	Revision         = domain.Revision
	Article          = domain.Article
	Database         = domain.Database
	Translation      = domain.Translation
	CopyOrigin       = domain.CopyOrigin
	ReconcileBase    = domain.ReconcileBase
	ImportConflict   = domain.ImportConflict
	BlobRef          = domain.BlobRef
	WikiMetadata     = domain.WikiMetadata
	SyncJob          = domain.SyncJob
	Move             = domain.Move
	AccessKey        = domain.AccessKey
	WikiUser         = domain.WikiUser
	WikiTeam         = domain.WikiTeam
	GitHubAccount    = domain.GitHubAccount
	GitHubConnection = domain.GitHubConnection
	SourceMirror     = domain.SourceMirror
	Page             = domain.Page
	contextStatus    = domain.SourceStatus
	agentContext     = agentctx.Result
)

var (
	ErrInvalid  = domain.ErrInvalid
	ErrConflict = domain.ErrConflict
	ErrNoDocs   = domain.ErrNoDocs
	ErrNotFound = domain.ErrNotFound

	identifier    = domain.IDPattern
	secretContent = domain.CredentialPattern
)

var (
	safePath                  = domain.SafeArticlePath
	safeRelative              = domain.SafeRelative
	articlePath               = domain.ArticlePath
	digest                    = domain.Digest
	hexKey                    = domain.HexKey
	title                     = domain.Title
	sourcePublic              = domain.SourcePublic
	sourcePublicReadable      = domain.SourcePublicReadable
	sourceSyncIssue           = domain.SourceSyncIssue
	enqueueWrite              = domain.EnqueueWrite
	reconcileMoves            = domain.ReconcileMoves
	moveDeletionLocks         = domain.MoveDeletionLocks
	conflictResolutionBlocked = domain.ConflictResolutionBlocked
	displayedRevision         = domain.DisplayedRevision
	imageRef                  = domain.ImageRef
	migrateSQLite             = store.MigrateSQLite
	privateDataDir            = store.PrivateDataDir
)

const (
	maxArticleBytes    = domain.MaxArticleBytes
	legacyArticleBytes = domain.LegacyArticleBytes
	sqliteFile         = store.SQLiteFile
)

// findArticles ranks against a snapshot taken under the store read lock;
// ranking runs after the lock is released.
func (s *Store) findArticles(query, sourceID string, public bool) []search.Hit {
	s.RLock()
	candidates := search.Candidates(*s.DB(), sourceID, public, time.Now())
	s.RUnlock()
	return search.Rank(candidates, query)
}

type copyRequest = transfer.CopyRequest

type (
	translationRequest   = translation.Request
	translationCandidate = translation.Candidate
)

// translation returns the translation service for this store. Validation
// reports the source status as the agent context does.
func (s *Store) translation() translation.Service {
	return translation.Service{Store: s.Store, Status: s.sourceStatus}
}

// transfer returns the copy and move service for this store. Plans report
// source statuses as the agent context does.
func (s *Store) transfer() transfer.Service {
	return transfer.Service{Store: s.Store, Status: s.sourceStatus}
}

// sourceStatus reports a source from the given snapshot and this store's
// import observations.
func (s *Store) sourceStatus(db domain.Database, sourceID string, src Source) domain.SourceStatus {
	return agentctx.Status(db, s.observations, sourceID, src, time.Now())
}

// agentContext, proposalBase and newProposalBase adapt the agent context
// package to this store until the HTTP layer moves.
func (s *Store) agentContext(sourceID, target string, public bool) (agentctx.Result, error) {
	return agentctx.Context(s.Store, s.observations, sourceID, target, public)
}

func (s *Store) proposalBase(sourceID, path string) (map[string]any, error) {
	return agentctx.ProposalBase(s.Store, s.observations, sourceID, path)
}

func (s *Store) newProposalBase(sourceID, path string) (map[string]any, error) {
	return agentctx.NewProposalBase(s.Store, s.observations, sourceID, path)
}

// syncer returns the synchronization manager for this store. Operations that
// do not reach GitHub accept a nil client.
func (s *Store) syncer(g *github.Client) *syncer.Manager {
	imp := s.importer()
	return syncer.New(s.Store, g, &imp)
}

// syncer returns the running manager, or one without a GitHub client for
// mirror and reconciliation requests to a handler that runs no manager.
func (w Wiki) syncer() *syncer.Manager {
	if w.Sync != nil {
		return w.Sync
	}
	return w.Store.syncer(nil)
}
