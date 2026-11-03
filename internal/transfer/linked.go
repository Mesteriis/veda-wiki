package transfer

import (
	"os"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// PrepareLinkedCopy applies a linked-graph copy to db, which must be the
// caller's uncommitted transaction state. It stages all articles before
// validating references, so cycles are safe. On error db may be partly
// changed and must be discarded.
func (s Service) PrepareLinkedCopy(db *domain.Database, in CopyRequest) (domain.Revision, Dependencies, error) {
	plan, err := BuildPlan(db, in)
	if err != nil {
		return domain.Revision{}, Dependencies{}, err
	}
	src := db.Sources[in.SourceID]
	dst := db.Sources[in.DestinationID]
	if dst.ConnectionID == "" && dst.ImportGeneration != "" {
		return domain.Revision{}, Dependencies{}, domain.ErrInvalid
	}
	// The plan guarantees distinct source and destination IDs, so staging
	// destination articles in db never changes the source articles read here.
	observedAssets := dst.Assets
	now := time.Now()
	public := domain.SourcePublic(*db, dst, now)
	var root domain.Revision
	links := map[string]string{}
	remoteLinks := map[string]string{}
	bundle := map[string]string{}
	for _, item := range plan.Articles {
		if item.Destination == "conflict" || (item.SourcePath == in.SourcePath && item.Destination != "missing") {
			return domain.Revision{}, Dependencies{}, domain.ErrConflict
		}
		if item.SourcePath != in.SourcePath {
			links[item.DestinationPath] = item.SHA
			if item.Destination == "matching" {
				remoteLinks[item.DestinationPath] = item.SHA
			} else {
				bundle[item.DestinationPath] = item.SHA
			}
		}
		a := db.Articles[in.SourceID+"/"+item.SourcePath]
		rev := a.Versions[item.Revision-1]
		sourcePublicRevision := domain.SourcePublicReadable(*db, src, now) && (src.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
		if public && (domain.CredentialPattern.MatchString(rev.Text) || (!sourcePublicRevision && !in.ConfirmPublication)) {
			return domain.Revision{}, Dependencies{}, domain.ErrInvalid
		}
		if item.Destination == "matching" {
			continue
		}
		if a.Translation != nil && dst.Kind != "program" {
			return domain.Revision{}, Dependencies{}, domain.ErrInvalid
		}
		copied := domain.Revision{Number: 1, Text: rev.Text, SHA: rev.SHA, Created: now.UTC().Format(time.RFC3339Nano)}
		b := domain.Article{SourceID: in.DestinationID, Path: item.DestinationPath, Versions: []domain.Revision{copied}, CopiedFrom: &domain.CopyOrigin{SourceID: in.SourceID, Path: item.SourcePath, Revision: item.Revision, SHA: rev.SHA, DirectoryIndex: item.DirectoryIndex}, Translation: domain.MovedTranslation(a.Translation)}
		if item.DirectoryIndex && db.Version < 17 {
			db.Version = 17
		}
		if public {
			published := copied
			published.Number = 0
			b.Published = &published
			b.PublishedLocal = true
		}
		db.Articles[in.DestinationID+"/"+item.DestinationPath] = b
		if item.SourcePath == in.SourcePath {
			root = copied
		}
	}
	if root.Number == 0 {
		return domain.Revision{}, Dependencies{}, domain.ErrConflict
	}
	assets := make(map[string]domain.BlobRef, len(dst.Assets))
	for p, ref := range dst.Assets {
		assets[p] = ref
	}
	dst.Assets = assets
	validationSource := dst
	// Only the temporary graph is treated as local while checking its internal
	// links; real upstream and publication state are never fabricated in storage.
	validationSource.ConnectionID = ""
	validationSource.Visibility = "private"
	db.Sources[dst.ID] = validationSource
	batchAssets := map[string]domain.BlobRef{}
	verifiedAssets := map[domain.BlobRef]bool{}
	rootReferences := false
	directoryLinks := false
	var total int64
	for _, item := range plan.Articles {
		a := db.Articles[in.SourceID+"/"+item.SourcePath]
		rootReferences = rootReferences || domain.HasRootReference(a.Versions[item.Revision-1].Text)
		deps, e := s.ResolveDependencies(db, a.Versions[item.Revision-1].Text, item.SourcePath, item.DestinationPath, src, dst)
		if e != nil {
			return domain.Revision{}, Dependencies{}, e
		}
		directoryLinks = directoryLinks || deps.DirectoryLinks
		for p, ref := range deps.Assets {
			if !verifiedAssets[ref] {
				info, err := os.Lstat(s.blobPath(ref))
				if err != nil || !info.Mode().IsRegular() || total+info.Size() > 16<<20 {
					return domain.Revision{}, Dependencies{}, domain.ErrInvalid
				}
				total += info.Size()
				verifiedAssets[ref] = true
			}
			assets[p] = ref
			batchAssets[p] = ref
			if len(batchAssets) > 2000 {
				return domain.Revision{}, Dependencies{}, domain.ErrInvalid
			}
		}
	}
	if dst.ConnectionID != "" {
		// The asset index reflects only the last observed remote tree.
		dst.Assets = observedAssets
		db.Sources[dst.ID] = dst
		a := db.Articles[in.DestinationID+"/"+in.DestinationPath]
		domain.EnqueueWrite(db, a, root, time.Now())
		id := a.SourceID + "/" + a.Path + "@1"
		job := db.Outbox[id]
		job.Assets, job.Links, job.Bundle = batchAssets, remoteLinks, bundle
		db.Outbox[id] = job
		version := 7
		if len(remoteLinks) > 0 {
			version = 8
		}
		if len(bundle) > 0 {
			version = 9
		}
		if db.Version < version {
			db.Version = version
		}
		if rootReferences && (len(batchAssets) > 0 || len(remoteLinks) > 0 || len(bundle) > 0) && db.Version < 12 {
			db.Version = 12
		}
	} else {
		db.Sources[dst.ID] = dst
	}
	if directoryLinks && db.Version < 16 {
		db.Version = 16
	}
	return root, Dependencies{Assets: batchAssets, Links: links, DirectoryLinks: directoryLinks}, nil
}
