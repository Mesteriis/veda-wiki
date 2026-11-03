package main

import (
	"github.com/Mesteriis/veda-wiki/internal/store"
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
	src := s.DB().Sources[in.SourceID]
	dst := s.DB().Sources[in.DestinationID]
	if dst.ConnectionID == "" && dst.ImportGeneration != "" {
		return Database{}, Revision{}, transferDependencies{}, ErrInvalid
	}
	shadow := &Store{Store: store.Detached(s.Dir(), s.Clone())}
	now := time.Now()
	public := sourcePublic(*s.DB(), dst, now)
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
		a := s.DB().Articles[in.SourceID+"/"+item.SourcePath]
		rev := a.Versions[item.Revision-1]
		sourcePublicRevision := sourcePublicReadable(*s.DB(), src, now) && (src.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
		if public && (secretContent.MatchString(rev.Text) || (!sourcePublicRevision && !in.ConfirmPublication)) {
			return Database{}, Revision{}, transferDependencies{}, ErrInvalid
		}
		if item.Destination == "matching" {
			continue
		}
		if a.Translation != nil && dst.Kind != "program" {
			return Database{}, Revision{}, transferDependencies{}, ErrInvalid
		}
		copied := Revision{Number: 1, Text: rev.Text, SHA: rev.SHA, Created: now.UTC().Format(time.RFC3339Nano)}
		b := Article{SourceID: in.DestinationID, Path: item.DestinationPath, Versions: []Revision{copied}, CopiedFrom: &CopyOrigin{SourceID: in.SourceID, Path: item.SourcePath, Revision: item.Revision, SHA: rev.SHA, DirectoryIndex: item.DirectoryIndex}, Translation: movedTranslation(a.Translation)}
		if item.DirectoryIndex && shadow.DB().Version < 17 {
			shadow.DB().Version = 17
		}
		if public {
			published := copied
			published.Number = 0
			b.Published = &published
			b.PublishedLocal = true
		}
		shadow.DB().Articles[in.DestinationID+"/"+item.DestinationPath] = b
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
	shadow.DB().Sources[dst.ID] = validationSource
	batchAssets := map[string]BlobRef{}
	verifiedAssets := map[BlobRef]bool{}
	rootReferences := false
	directoryLinks := false
	var total int64
	for _, item := range plan.Articles {
		a := s.DB().Articles[in.SourceID+"/"+item.SourcePath]
		rootReferences = rootReferences || hasRootReference(a.Versions[item.Revision-1].Text)
		deps, e := shadow.transferAssets(a.Versions[item.Revision-1].Text, item.SourcePath, item.DestinationPath, src, dst)
		if e != nil {
			return Database{}, Revision{}, transferDependencies{}, e
		}
		directoryLinks = directoryLinks || deps.DirectoryLinks
		for p, ref := range deps.Assets {
			if !verifiedAssets[ref] {
				info, err := os.Lstat(path.Join(s.Dir(), "blobs", ref.SHA))
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
		dst.Assets = s.DB().Sources[dst.ID].Assets
		shadow.DB().Sources[dst.ID] = dst
		a := shadow.DB().Articles[in.DestinationID+"/"+in.DestinationPath]
		enqueueWrite(shadow.DB(), a, root, time.Now())
		id := a.SourceID + "/" + a.Path + "@1"
		job := shadow.DB().Outbox[id]
		job.Assets, job.Links, job.Bundle = batchAssets, remoteLinks, bundle
		shadow.DB().Outbox[id] = job
		version := 7
		if len(remoteLinks) > 0 {
			version = 8
		}
		if len(bundle) > 0 {
			version = 9
		}
		if shadow.DB().Version < version {
			shadow.DB().Version = version
		}
		if rootReferences && (len(batchAssets) > 0 || len(remoteLinks) > 0 || len(bundle) > 0) && shadow.DB().Version < 12 {
			shadow.DB().Version = 12
		}
	} else {
		shadow.DB().Sources[dst.ID] = dst
	}
	if directoryLinks && shadow.DB().Version < 16 {
		shadow.DB().Version = 16
	}
	return *shadow.DB(), root, transferDependencies{Assets: batchAssets, Links: links, DirectoryLinks: directoryLinks}, nil
}
