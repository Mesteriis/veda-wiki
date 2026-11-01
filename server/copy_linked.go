package main

import "time"

// copyLinkedLocal stages all articles before validating references, so cycles
// are safe. The caller owns the store lock and commits only the whole batch.
func (s *Store) copyLinkedLocal(in copyRequest) (Revision, error) {
	plan, err := s.planTransferLocked(in)
	if err != nil {
		return Revision{}, err
	}
	src := s.db.Sources[in.SourceID]
	dst := s.db.Sources[in.DestinationID]
	// Connected destinations need a durable multi-file outbox transaction, which
	// cannot be simulated by independent jobs without exposing broken references.
	if dst.ConnectionID != "" || dst.ImportGeneration != "" {
		return Revision{}, ErrInvalid
	}
	shadow := &Store{dir: s.dir, db: s.clone()}
	now := time.Now()
	public := sourcePublic(s.db, dst, now)
	var root Revision
	for _, item := range plan.Articles {
		if item.Destination == "conflict" || (item.SourcePath == in.SourcePath && item.Destination != "missing") {
			return Revision{}, ErrConflict
		}
		a := s.db.Articles[in.SourceID+"/"+item.SourcePath]
		rev := a.Versions[item.Revision-1]
		sourcePublicRevision := sourcePublic(s.db, src, now) && (src.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
		if public && !sourcePublicRevision && (!in.ConfirmPublication || secretContent.MatchString(rev.Text)) {
			return Revision{}, ErrInvalid
		}
		if item.Destination == "matching" {
			continue
		}
		if a.Translation != nil && dst.Kind != "program" {
			return Revision{}, ErrInvalid
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
		return Revision{}, ErrConflict
	}
	assets := make(map[string]BlobRef, len(dst.Assets))
	for p, ref := range dst.Assets {
		assets[p] = ref
	}
	dst.Assets = assets
	shadow.db.Sources[dst.ID] = dst
	for _, item := range plan.Articles {
		a := s.db.Articles[in.SourceID+"/"+item.SourcePath]
		deps, e := shadow.transferAssets(a.Versions[item.Revision-1].Text, item.SourcePath, item.DestinationPath, src, shadow.db.Sources[dst.ID])
		if e != nil {
			return Revision{}, e
		}
		for p, ref := range deps.Assets {
			assets[p] = ref
		}
	}
	if err = s.commit(shadow.db); err != nil {
		return Revision{}, err
	}
	return root, nil
}
