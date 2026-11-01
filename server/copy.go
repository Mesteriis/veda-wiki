package main

import "time"

// CopyOrigin is retained on the destination for owner audit and is never
// returned on the anonymous public surface.
type CopyOrigin struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Revision int    `json:"revision"`
	SHA      string `json:"sha256"`
}
type copyRequest struct {
	SourceID           string `json:"source_id"`
	SourcePath         string `json:"source_path"`
	ExpectedRevision   int    `json:"expected_revision"`
	DestinationID      string `json:"destination_id"`
	DestinationPath    string `json:"destination_path"`
	ConfirmPublication bool   `json:"confirm_publication"`
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
	if _, exists := s.db.Articles[key]; exists {
		return Database{}, Revision{}, ErrConflict
	}
	rev := a.Versions[len(a.Versions)-1]
	dependencies, err := s.transferAssets(rev.Text, in.SourcePath, in.DestinationPath, source, destination)
	if err != nil {
		return Database{}, Revision{}, err
	}
	now := time.Now()
	targetPublic := sourcePublic(s.db, destination, now)
	sourcePublicRevision := sourcePublic(s.db, source, now) && (source.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
	if targetPublic && !sourcePublicRevision && (!in.ConfirmPublication || secretContent.MatchString(rev.Text)) {
		return Database{}, Revision{}, ErrInvalid
	}
	copied := Revision{Number: 1, Text: rev.Text, SHA: rev.SHA, Created: now.UTC().Format(time.RFC3339Nano)}
	b := Article{SourceID: in.DestinationID, Path: in.DestinationPath, Versions: []Revision{copied}, CopiedFrom: &CopyOrigin{SourceID: in.SourceID, Path: in.SourcePath, Revision: in.ExpectedRevision, SHA: rev.SHA}}
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
	}
	db := s.clone()
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
	if destination.ConnectionID != "" {
		enqueueWrite(&db, b, copied)
		if len(dependencies.Assets) > 0 || len(dependencies.Links) > 0 {
			id := b.SourceID + "/" + b.Path + "@1"
			job := db.Outbox[id]
			job.Assets = dependencies.Assets
			job.Links = dependencies.Links
			db.Outbox[id] = job
			version := 7
			if len(dependencies.Links) > 0 {
				version = 8
			}
			if db.Version < version {
				db.Version = version
			}
		}
	}
	return db, copied, nil
}
func (s *Store) copyArticle(in copyRequest) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db, revision, err := s.prepareCopy(in)
	if err != nil {
		return Revision{}, err
	}
	if err = s.commit(db); err != nil {
		return Revision{}, err
	}
	return revision, nil
}
