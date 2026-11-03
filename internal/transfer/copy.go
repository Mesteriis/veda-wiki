// Package transfer copies and moves articles between sources, with their
// relative images and linked articles, and plans linked-graph transfers.
// It is the only area that changes moves; the move rules shared with other
// areas live in domain.
package transfer

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Service runs transfers against Store. Status, when set, reports a source's
// status for transfer plans; it runs under the store read lock.
type Service struct {
	Store  *store.Store
	Status func(db domain.Database, sourceID string, src domain.Source) domain.SourceStatus
}

// CopyRequest asks to copy one article, or with IncludeLinked its linked
// graph, to another source.
type CopyRequest struct {
	SourceID                    string `json:"source_id"`
	SourcePath                  string `json:"source_path"`
	ExpectedRevision            int    `json:"expected_revision"`
	ExpectedDestinationRevision *int   `json:"expected_destination_revision,omitempty"`
	DestinationID               string `json:"destination_id"`
	DestinationPath             string `json:"destination_path"`
	ConfirmPublication          bool   `json:"confirm_publication"`
	IncludeLinked               bool   `json:"include_linked,omitempty"`
}

// PrepareCopy applies a single-article copy to db, which must be the
// caller's uncommitted transaction state. On error db may be partly changed
// and must be discarded.
func (s Service) PrepareCopy(db *domain.Database, in CopyRequest) (domain.Revision, error) {
	if in.ExpectedRevision < 1 || in.SourceID == in.DestinationID {
		return domain.Revision{}, domain.ErrInvalid
	}
	source, ok := db.Sources[in.SourceID]
	if !ok {
		return domain.Revision{}, domain.ErrNotFound
	}
	destination, ok := db.Sources[in.DestinationID]
	if !ok {
		return domain.Revision{}, domain.ErrNotFound
	}
	if !domain.ArticlePath(in.SourcePath, source.Kind) || !domain.ArticlePath(in.DestinationPath, destination.Kind) {
		return domain.Revision{}, domain.ErrInvalid
	}
	a, ok := db.Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || a.Deleted {
		return domain.Revision{}, domain.ErrNotFound
	}
	if len(a.Versions) != in.ExpectedRevision {
		return domain.Revision{}, domain.ErrConflict
	}
	for _, c := range a.Conflicts {
		if c.Resolution == "" {
			return domain.Revision{}, domain.ErrConflict
		}
	}
	key := in.DestinationID + "/" + in.DestinationPath
	b, exists := db.Articles[key]
	if exists {
		if in.ExpectedDestinationRevision == nil || *in.ExpectedDestinationRevision < 1 || b.Deleted || len(b.Versions) != *in.ExpectedDestinationRevision || domain.MoveDeletionLocks(*db, in.DestinationID, in.DestinationPath) || domain.ConflictResolutionBlocked(*db, in.DestinationID, in.DestinationPath) {
			return domain.Revision{}, domain.ErrConflict
		}
		for _, conflict := range b.Conflicts {
			if conflict.Resolution == "" {
				return domain.Revision{}, domain.ErrConflict
			}
		}
	} else if in.ExpectedDestinationRevision != nil && *in.ExpectedDestinationRevision != 0 {
		return domain.Revision{}, domain.ErrConflict
	}
	rev := a.Versions[len(a.Versions)-1]
	dependencies, err := s.ResolveDependencies(db, rev.Text, in.SourcePath, in.DestinationPath, source, destination)
	if err != nil {
		return domain.Revision{}, err
	}
	now := time.Now()
	targetPublic := domain.SourcePublic(*db, destination, now)
	sourcePublicRevision := domain.SourcePublicReadable(*db, source, now) && (source.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
	if targetPublic && (domain.CredentialPattern.MatchString(rev.Text) || (!sourcePublicRevision && !in.ConfirmPublication)) {
		return domain.Revision{}, domain.ErrInvalid
	}
	copied := domain.Revision{Number: len(b.Versions) + 1, Text: rev.Text, SHA: rev.SHA, Created: now.UTC().Format(time.RFC3339Nano)}
	b.SourceID, b.Path, b.Deleted = in.DestinationID, in.DestinationPath, false
	b.Versions = append(append([]domain.Revision(nil), b.Versions...), copied)
	b.CopiedFrom = &domain.CopyOrigin{SourceID: in.SourceID, Path: in.SourcePath, Revision: in.ExpectedRevision, SHA: rev.SHA}
	if a.Translation != nil {
		if destination.Kind != "program" {
			return domain.Revision{}, domain.ErrInvalid
		}
		b.Translation = domain.MovedTranslation(a.Translation)
	}
	if targetPublic {
		published := copied
		published.Number = 0
		b.Published = &published
		b.PublishedLocal = true
	} else {
		b.PublishedLocal = false
	}
	if dependencies.DirectoryLinks && db.Version < 16 {
		db.Version = 16
	}
	if len(dependencies.Assets) > 0 && destination.ConnectionID == "" {
		previous := destination.Assets
		destination.Assets = make(map[string]domain.BlobRef, len(previous)+len(dependencies.Assets))
		for p, ref := range previous {
			destination.Assets[p] = ref
		}
		for p, ref := range dependencies.Assets {
			destination.Assets[p] = ref
		}
		db.Sources[destination.ID] = destination
	}
	db.Articles[key] = b
	// An active move may depend on the previous destination revision.
	// Reconcile it in the same transaction before queuing the new write.
	domain.ReconcileMoves(db)
	if destination.ConnectionID != "" {
		domain.EnqueueWrite(db, b, copied, time.Now())
		if len(dependencies.Assets) > 0 || len(dependencies.Links) > 0 {
			if err := queueDependencies(db, b, copied, dependencies, rev.Text); err != nil {
				return domain.Revision{}, err
			}
		}
	}
	return copied, nil
}

// queueDependencies adds the copy's images and links to its outbox job.
func queueDependencies(db *domain.Database, b domain.Article, copied domain.Revision, dependencies Dependencies, text string) error {
	if err := domain.AddWriteDependencies(db, b, copied, dependencies.Assets, dependencies.Links); err != nil {
		return err
	}
	version := 7
	if len(dependencies.Links) > 0 {
		version = 8
	}
	if db.Version < version {
		db.Version = version
	}
	if domain.HasRootReference(text) && db.Version < 12 {
		db.Version = 12
	}
	return nil
}

// Copy copies an article, or its linked graph, in one transaction.
func (s Service) Copy(in CopyRequest) (domain.Revision, error) {
	var revision domain.Revision
	err := s.Store.Update(func(tx *store.Tx) error {
		var err error
		if in.IncludeLinked {
			if in.ExpectedDestinationRevision != nil && *in.ExpectedDestinationRevision != 0 {
				return domain.ErrInvalid
			}
			revision, _, err = s.PrepareLinkedCopy(tx.DB(), in)
			return err
		}
		revision, err = s.PrepareCopy(tx.DB(), in)
		return err
	})
	if err != nil {
		return domain.Revision{}, err
	}
	return revision, nil
}
