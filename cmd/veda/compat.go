package main

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/search"
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
	movedTranslation            = domain.MovedTranslation
	assetsPresent               = domain.AssetsPresent
	linksPresent                = domain.LinksPresent
	retainedPendingAssets       = domain.RetainedPendingAssets
	retainedPendingLinks        = domain.RetainedPendingLinks
	hasRootReference            = domain.HasRootReference
	repositoryTarget            = domain.RepositoryTarget
	directoryReadmeTarget       = domain.DirectoryReadmeTarget
)

// findArticles ranks against a snapshot taken under the store read lock;
// ranking runs after the lock is released.
func (s *Store) findArticles(query, sourceID string, public bool) []search.Hit {
	s.mu.RLock()
	candidates := search.Candidates(s.db, sourceID, public, time.Now())
	s.mu.RUnlock()
	return search.Rank(candidates, query)
}
