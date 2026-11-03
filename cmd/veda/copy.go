package main

import (
	"strconv"
	"time"
)

type copyRequest struct {
	SourceID                    string `json:"source_id"`
	SourcePath                  string `json:"source_path"`
	ExpectedRevision            int    `json:"expected_revision"`
	ExpectedDestinationRevision *int   `json:"expected_destination_revision,omitempty"`
	DestinationID               string `json:"destination_id"`
	DestinationPath             string `json:"destination_path"`
	ConfirmPublication          bool   `json:"confirm_publication"`
	IncludeLinked               bool   `json:"include_linked,omitempty"`
}

// Caller holds the store mutex. The returned transaction is not yet committed.
func (s *Store) prepareCopy(in copyRequest) (Database, Revision, error) {
	if in.ExpectedRevision < 1 || in.SourceID == in.DestinationID {
		return Database{}, Revision{}, ErrInvalid
	}
	source, ok := s.db.Sources[in.SourceID]
	if !ok {
		return Database{}, Revision{}, ErrNotFound
	}
	destination, ok := s.db.Sources[in.DestinationID]
	if !ok {
		return Database{}, Revision{}, ErrNotFound
	}
	if !articlePath(in.SourcePath, source.Kind) || !articlePath(in.DestinationPath, destination.Kind) {
		return Database{}, Revision{}, ErrInvalid
	}
	a, ok := s.db.Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || a.Deleted {
		return Database{}, Revision{}, ErrNotFound
	}
	if len(a.Versions) != in.ExpectedRevision {
		return Database{}, Revision{}, ErrConflict
	}
	for _, c := range a.Conflicts {
		if c.Resolution == "" {
			return Database{}, Revision{}, ErrConflict
		}
	}
	key := in.DestinationID + "/" + in.DestinationPath
	b, exists := s.db.Articles[key]
	if exists {
		if in.ExpectedDestinationRevision == nil || *in.ExpectedDestinationRevision < 1 || b.Deleted || len(b.Versions) != *in.ExpectedDestinationRevision || moveDeletionLocks(s.db, in.DestinationID, in.DestinationPath) || conflictResolutionBlocked(s.db, in.DestinationID, in.DestinationPath) {
			return Database{}, Revision{}, ErrConflict
		}
		for _, conflict := range b.Conflicts {
			if conflict.Resolution == "" {
				return Database{}, Revision{}, ErrConflict
			}
		}
	} else if in.ExpectedDestinationRevision != nil && *in.ExpectedDestinationRevision != 0 {
		return Database{}, Revision{}, ErrConflict
	}
	rev := a.Versions[len(a.Versions)-1]
	dependencies, err := s.transferAssets(rev.Text, in.SourcePath, in.DestinationPath, source, destination)
	if err != nil {
		return Database{}, Revision{}, err
	}
	now := time.Now()
	targetPublic := sourcePublic(s.db, destination, now)
	sourcePublicRevision := sourcePublicReadable(s.db, source, now) && (source.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
	if targetPublic && (secretContent.MatchString(rev.Text) || (!sourcePublicRevision && !in.ConfirmPublication)) {
		return Database{}, Revision{}, ErrInvalid
	}
	copied := Revision{Number: len(b.Versions) + 1, Text: rev.Text, SHA: rev.SHA, Created: now.UTC().Format(time.RFC3339Nano)}
	b.SourceID, b.Path, b.Deleted = in.DestinationID, in.DestinationPath, false
	b.Versions = append(append([]Revision(nil), b.Versions...), copied)
	b.CopiedFrom = &CopyOrigin{SourceID: in.SourceID, Path: in.SourcePath, Revision: in.ExpectedRevision, SHA: rev.SHA}
	if a.Translation != nil {
		if destination.Kind != "program" {
			return Database{}, Revision{}, ErrInvalid
		}
		b.Translation = movedTranslation(a.Translation)
	}
	if targetPublic {
		published := copied
		published.Number = 0
		b.Published = &published
		b.PublishedLocal = true
	} else {
		b.PublishedLocal = false
	}
	db := s.clone()
	if dependencies.DirectoryLinks && db.Version < 16 {
		db.Version = 16
	}
	if len(dependencies.Assets) > 0 && destination.ConnectionID == "" {
		destination.Assets = make(map[string]BlobRef, len(destination.Assets)+len(dependencies.Assets))
		for p, ref := range s.db.Sources[in.DestinationID].Assets {
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
	reconcileMoves(&db)
	if destination.ConnectionID != "" {
		enqueueWrite(&db, b, copied, time.Now())
		if len(dependencies.Assets) > 0 || len(dependencies.Links) > 0 {
			id := b.SourceID + "/" + b.Path + "@" + strconv.Itoa(copied.Number)
			job := db.Outbox[id]
			if job.Assets == nil {
				job.Assets = map[string]BlobRef{}
			}
			for name, ref := range dependencies.Assets {
				if old, ok := job.Assets[name]; ok && old != ref {
					return Database{}, Revision{}, ErrConflict
				}
				job.Assets[name] = ref
			}
			if job.Links == nil {
				job.Links = map[string]string{}
			}
			for name, sha := range dependencies.Links {
				if old, ok := job.Links[name]; ok && old != sha {
					return Database{}, Revision{}, ErrConflict
				}
				job.Links[name] = sha
			}
			db.Outbox[id] = job
			version := 7
			if len(dependencies.Links) > 0 {
				version = 8
			}
			if db.Version < version {
				db.Version = version
			}
			if hasRootReference(rev.Text) && db.Version < 12 {
				db.Version = 12
			}
		}
	}
	return db, copied, nil
}
func (s *Store) copyArticle(in copyRequest) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.IncludeLinked {
		if in.ExpectedDestinationRevision != nil && *in.ExpectedDestinationRevision != 0 {
			return Revision{}, ErrInvalid
		}
		db, revision, _, err := s.prepareCopyLinkedLocal(in)
		if err != nil {
			return Revision{}, err
		}
		if err = s.commit(db); err != nil {
			return Revision{}, err
		}
		return revision, nil
	}
	db, revision, err := s.prepareCopy(in)
	if err != nil {
		return Revision{}, err
	}
	if err = s.commit(db); err != nil {
		return Revision{}, err
	}
	return revision, nil
}
