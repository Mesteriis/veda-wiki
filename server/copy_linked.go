package main

import (
	"os"
	"path"
	"time"
)

// prepareCopyLinkedLocal stages all articles before validating references, so
// cycles are safe. The caller owns the lock; no data is committed here.
func (s *Store) prepareCopyLinkedLocal(in copyRequest) (Database, Revision, transferDependencies, error) {
	plan, err := s.planTransferLocked(in)
	if err != nil {
		return Database{}, Revision{}, transferDependencies{}, err
	}
	src := s.db.Sources[in.SourceID]
	dst := s.db.Sources[in.DestinationID]
	if dst.ConnectionID == "" && dst.ImportGeneration != "" {
		return Database{}, Revision{}, transferDependencies{}, ErrInvalid
	}
	shadow := &Store{dir: s.dir, db: s.clone()}
	now := time.Now()
	public := sourcePublic(s.db, dst, now)
	var root Revision
	links := map[string]string{}
	remoteLinks := map[string]string{}
	bundle := map[string]string{}
	for _, item := range plan.Articles {
		if item.Destination == "conflict" || (item.SourcePath == in.SourcePath && item.Destination != "missing") {
			return Database{}, Revision{}, transferDependencies{}, ErrConflict
		}
		if item.SourcePath != in.SourcePath {
			links[item.DestinationPath] = item.SHA
			if item.Destination == "matching" {
				remoteLinks[item.DestinationPath] = item.SHA
			} else {
				bundle[item.DestinationPath] = item.SHA
			}
		}
		a := s.db.Articles[in.SourceID+"/"+item.SourcePath]
		rev := a.Versions[item.Revision-1]
		sourcePublicRevision := sourcePublic(s.db, src, now) && (src.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
		if public && !sourcePublicRevision && (!in.ConfirmPublication || secretContent.MatchString(rev.Text)) {
			return Database{}, Revision{}, transferDependencies{}, ErrInvalid
		}
		if item.Destination == "matching" {
			continue
		}
		if a.Translation != nil && dst.Kind != "program" {
			return Database{}, Revision{}, transferDependencies{}, ErrInvalid
		}
		copied := Revision{Number: 1, Text: rev.Text, SHA: rev.SHA, Created: now.UTC().Format(time.RFC3339Nano)}
		b := Article{SourceID: in.DestinationID, Path: item.DestinationPath, Versions: []Revision{copied}, CopiedFrom: &CopyOrigin{SourceID: in.SourceID, Path: item.SourcePath, Revision: item.Revision, SHA: rev.SHA}, Translation: movedTranslation(a.Translation)}
		if public {
			published := copied
			published.Number = 0
			b.Published = &published
			b.PublishedLocal = true
		}
		shadow.db.Articles[in.DestinationID+"/"+item.DestinationPath] = b
		if item.SourcePath == in.SourcePath {
			root = copied
		}
	}
	if root.Number == 0 {
		return Database{}, Revision{}, transferDependencies{}, ErrConflict
	}
	assets := make(map[string]BlobRef, len(dst.Assets))
	for p, ref := range dst.Assets {
		assets[p] = ref
	}
	dst.Assets = assets
	validationSource := dst
	// Only the temporary graph is treated as local while checking its internal
	// links; real upstream and publication state are never fabricated in storage.
	validationSource.ConnectionID = ""
	validationSource.Visibility = "private"
	shadow.db.Sources[dst.ID] = validationSource
	batchAssets := map[string]BlobRef{}
	verifiedAssets := map[BlobRef]bool{}
	var total int64
	for _, item := range plan.Articles {
		a := s.db.Articles[in.SourceID+"/"+item.SourcePath]
		deps, e := shadow.transferAssets(a.Versions[item.Revision-1].Text, item.SourcePath, item.DestinationPath, src, dst)
		if e != nil {
			return Database{}, Revision{}, transferDependencies{}, e
		}
		for p, ref := range deps.Assets {
			if !verifiedAssets[ref] {
				info, err := os.Lstat(path.Join(s.dir, "blobs", ref.SHA))
				if err != nil || !info.Mode().IsRegular() || total+info.Size() > 16<<20 {
					return Database{}, Revision{}, transferDependencies{}, ErrInvalid
				}
				total += info.Size()
				verifiedAssets[ref] = true
			}
			assets[p] = ref
			batchAssets[p] = ref
			if len(batchAssets) > 2000 {
				return Database{}, Revision{}, transferDependencies{}, ErrInvalid
			}
		}
	}
	if dst.ConnectionID != "" {
		// The asset index reflects only the last observed remote tree.
		dst.Assets = s.db.Sources[dst.ID].Assets
		shadow.db.Sources[dst.ID] = dst
		a := shadow.db.Articles[in.DestinationID+"/"+in.DestinationPath]
		enqueueWrite(&shadow.db, a, root)
		id := a.SourceID + "/" + a.Path + "@1"
		job := shadow.db.Outbox[id]
		job.Assets, job.Links, job.Bundle = batchAssets, remoteLinks, bundle
		shadow.db.Outbox[id] = job
		version := 7
		if len(remoteLinks) > 0 {
			version = 8
		}
		if len(bundle) > 0 {
			version = 9
		}
		if shadow.db.Version < version {
			shadow.db.Version = version
		}
	} else {
		shadow.db.Sources[dst.ID] = dst
	}
	return shadow.db, root, transferDependencies{Assets: batchAssets, Links: links}, nil
}
