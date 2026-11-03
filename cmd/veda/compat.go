package main

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/search"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
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
)

var (
	ErrInvalid  = domain.ErrInvalid
	ErrConflict = domain.ErrConflict
	ErrNoDocs   = domain.ErrNoDocs
	ErrNotFound = domain.ErrNotFound

	identifier    = domain.IDPattern
	commitID      = domain.CommitIDPattern
	secretContent = domain.CredentialPattern
	markdown      = domain.Markdown
)

var (
	safePath                    = domain.SafeArticlePath
	safeRelative                = domain.SafeRelative
	articlePath                 = domain.ArticlePath
	digest                      = domain.Digest
	hexKey                      = domain.HexKey
	title                       = domain.Title
	sourcePublic                = domain.SourcePublic
	sourcePublicReadable        = domain.SourcePublicReadable
	sourceSyncIssue             = domain.SourceSyncIssue
	enqueueWrite                = domain.EnqueueWrite
	reconcileMoves              = domain.ReconcileMoves
	moveDeletionLocks           = domain.MoveDeletionLocks
	moveDeletionLocksConnection = domain.MoveDeletionLocksConnection
	bundleLocks                 = domain.BundleLocks
	moveOperationLocks          = domain.MoveOperationLocks
	conflictResolutionBlocked   = domain.ConflictResolutionBlocked
	displayedRevision           = domain.DisplayedRevision
	translationFingerprint      = domain.TranslationFingerprint
	linksPresent                = domain.LinksPresent
	importablePath              = domain.ImportablePath
	imageRef                    = domain.ImageRef
	migrateSQLite               = store.MigrateSQLite
	atomicPrivateFile           = store.WritePrivateFile
	privateDataDir              = store.PrivateDataDir
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

// transfer returns the copy and move service for this store. Plans report
// source statuses as the agent context does.
func (s *Store) transfer() transfer.Service {
	return transfer.Service{Store: s.Store, Status: func(_ *Database, sourceID string, src Source) any {
		return s.sourceStatusLocked(sourceID, src)
	}}
}

// The source reconciler still runs under the transitional store lock. These
// adapters stage a transfer on a clone of the locked state; the caller commits.

func (s *Store) prepareCopy(in copyRequest) (Database, Revision, error) {
	db := s.Clone()
	rev, err := s.transfer().PrepareCopy(&db, in)
	if err != nil {
		return Database{}, Revision{}, err
	}
	return db, rev, nil
}

func (s *Store) prepareCopyLinkedLocal(in copyRequest) (Database, Revision, transfer.Dependencies, error) {
	db := s.Clone()
	rev, deps, err := s.transfer().PrepareLinkedCopy(&db, in)
	if err != nil {
		return Database{}, Revision{}, transfer.Dependencies{}, err
	}
	return db, rev, deps, nil
}

func (s *Store) planTransferLocked(in copyRequest) (transfer.Plan, error) {
	return transfer.BuildPlan(s.DB(), in)
}

func (s *Store) transferAssets(value, sourcePath, targetPath string, src, target Source) (transfer.Dependencies, error) {
	return s.transfer().ResolveDependencies(s.DB(), value, sourcePath, targetPath, src, target)
}
