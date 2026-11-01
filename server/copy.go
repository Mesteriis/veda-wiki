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

func (s *Store) copyArticle(in copyRequest) (Revision, error) {
	if in.ExpectedRevision < 1 || in.SourceID == in.DestinationID {
		return Revision{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.db.Sources[in.SourceID]
	if !ok {
		return Revision{}, ErrNotFound
	}
	destination, ok := s.db.Sources[in.DestinationID]
	if !ok {
		return Revision{}, ErrNotFound
	}
	if !articlePath(in.SourcePath, source.Kind) || !articlePath(in.DestinationPath, destination.Kind) {
		return Revision{}, ErrInvalid
	}
	a, ok := s.db.Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || a.Deleted {
		return Revision{}, ErrNotFound
	}
	if len(a.Versions) != in.ExpectedRevision {
		return Revision{}, ErrConflict
	}
	for _, c := range a.Conflicts {
		if c.Resolution == "" {
			return Revision{}, ErrConflict
		}
	}
	key := in.DestinationID + "/" + in.DestinationPath
	if _, exists := s.db.Articles[key]; exists {
		return Revision{}, ErrConflict
	}
	rev := a.Versions[len(a.Versions)-1]
	now := time.Now()
	targetPublic := sourcePublic(s.db, destination, now)
	sourcePublicRevision := sourcePublic(s.db, source, now) && (source.ConnectionID == "" || (a.Published != nil && a.Published.SHA == rev.SHA))
	if targetPublic && !sourcePublicRevision && (!in.ConfirmPublication || secretContent.MatchString(rev.Text)) {
		return Revision{}, ErrInvalid
	}
	copied := Revision{Number: 1, Text: rev.Text, SHA: rev.SHA, Created: now.UTC().Format(time.RFC3339Nano)}
	b := Article{SourceID: in.DestinationID, Path: in.DestinationPath, Versions: []Revision{copied}, CopiedFrom: &CopyOrigin{SourceID: in.SourceID, Path: in.SourcePath, Revision: in.ExpectedRevision, SHA: rev.SHA}}
	if targetPublic {
		published := copied
		published.Number = 0
		b.Published = &published
		b.PublishedLocal = true
	}
	db := s.clone()
	db.Articles[key] = b
	if destination.ConnectionID != "" {
		enqueueWrite(&db, b, copied)
	}
	if err := s.commit(db); err != nil {
		return Revision{}, err
	}
	return copied, nil
}
