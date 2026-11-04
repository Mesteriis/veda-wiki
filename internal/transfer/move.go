package transfer

import (
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// MoveRequest asks to move one article, or with IncludeLinked to copy its
// linked graph and move the requested article, to another source.
type MoveRequest struct {
	SourceID           string `json:"source_id"`
	SourcePath         string `json:"source_path"`
	ExpectedRevision   int    `json:"expected_revision"`
	DestinationID      string `json:"destination_id"`
	DestinationPath    string `json:"destination_path"`
	ConfirmMove        bool   `json:"confirm_move"`
	ConfirmPublication bool   `json:"confirm_publication"`
	IncludeLinked      bool   `json:"include_linked,omitempty"`
}

// Move copies the article and records a move that deletes the source once
// the destination is confirmed, all in one transaction.
func (s Service) Move(in MoveRequest) (domain.Move, error) {
	if !in.ConfirmMove {
		return domain.Move{}, domain.ErrInvalid
	}
	var m domain.Move
	err := s.Store.Update(func(tx *store.Tx) error {
		var err error
		m, err = s.move(tx.DB(), in)
		return err
	})
	if err != nil {
		return domain.Move{}, err
	}
	return m, nil
}

// move runs inside the move transaction.
func (s Service) move(db *domain.Database, in MoveRequest) (domain.Move, error) {
	if domain.MoveDeletionLocks(*db, in.SourceID, in.SourcePath) {
		return domain.Move{}, domain.ErrConflict
	}
	src, ok := db.Sources[in.SourceID]
	if !ok {
		return domain.Move{}, domain.ErrNotFound
	}
	// A local import may be repopulated by an independent synchronizer.
	if src.ConnectionID == "" && src.ImportGeneration != "" {
		return domain.Move{}, domain.ErrInvalid
	}
	a, ok := db.Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || a.Deleted {
		return domain.Move{}, domain.ErrNotFound
	}
	if in.ExpectedRevision != domain.LatestRevision(a) || in.ExpectedRevision < 1 {
		return domain.Move{}, domain.ErrConflict
	}
	if src.ConnectionID != "" {
		c, connected := db.Connections[src.ConnectionID]
		expectedRev, _ := domain.RevisionByNumber(a, in.ExpectedRevision)
		if !connected || !c.Write || c.Paused || a.UpstreamHash != expectedRev.SHA {
			return domain.Move{}, domain.ErrConflict
		}
		// Different source IDs may point at the very same GitHub branch. A
		// copy to the same physical path followed by deletion would erase both.
		if dst, exists := db.Sources[in.DestinationID]; exists && dst.ConnectionID != "" {
			other, connected := db.Connections[dst.ConnectionID]
			if connected && c.RepositoryID == other.RepositoryID && c.Branch == other.Branch && in.SourcePath == in.DestinationPath {
				return domain.Move{}, domain.ErrConflict
			}
		}
	}
	destinationSource := db.Sources[in.DestinationID]
	// Translations belong to program documents; never discard one when moving.
	if a.Translation != nil && destinationSource.Kind != "program" {
		return domain.Move{}, domain.ErrInvalid
	}
	for _, m := range db.Moves {
		if (m.State == "waiting_destination" || m.State == "waiting_source_delete" || m.State == "deleting_source") && m.SourceID == in.SourceID && m.SourcePath == in.SourcePath {
			return domain.Move{}, domain.ErrConflict
		}
	}
	copyIn := CopyRequest{
		SourceID:           in.SourceID,
		SourcePath:         in.SourcePath,
		ExpectedRevision:   in.ExpectedRevision,
		DestinationID:      in.DestinationID,
		DestinationPath:    in.DestinationPath,
		ConfirmPublication: in.ConfirmPublication,
	}
	var rev domain.Revision
	var dependencies Dependencies
	var e error
	if in.IncludeLinked {
		rev, dependencies, e = s.PrepareLinkedCopy(db, copyIn)
	} else {
		current, _ := domain.LastRevision(a)
		dependencies, e = s.ResolveDependencies(db, current.Text, in.SourcePath, in.DestinationPath, src, destinationSource)
		if e == nil {
			rev, e = s.PrepareCopy(db, copyIn)
		}
	}
	if e != nil {
		return domain.Move{}, e
	}
	id, e := store.RandomID()
	if e != nil {
		return domain.Move{}, e
	}
	if db.Moves == nil {
		db.Moves = map[string]domain.Move{}
	}
	m := domain.Move{
		ID:                     id,
		SourceID:               in.SourceID,
		SourcePath:             in.SourcePath,
		SourceRevision:         in.ExpectedRevision,
		DestinationID:          in.DestinationID,
		DestinationPath:        in.DestinationPath,
		SHA:                    rev.SHA,
		TranslationFingerprint: domain.TranslationFingerprint(a.Translation),
		Assets:                 dependencies.Assets,
		Links:                  dependencies.Links,
		State:                  "waiting_destination",
		Created:                time.Now().UTC().Format(time.RFC3339Nano),
	}
	if src.ConnectionID != "" {
		conn := db.Connections[src.ConnectionID]
		m.SourceConnectionID, m.SourceRepositoryID, m.SourceBranch = src.ConnectionID, conn.RepositoryID, conn.Branch
	}
	destination := db.Sources[in.DestinationID]
	if destination.ConnectionID != "" {
		conn, ok := db.Connections[destination.ConnectionID]
		if !ok {
			return domain.Move{}, domain.ErrInvalid
		}
		m.DestinationConnectionID = destination.ConnectionID
		m.DestinationRepositoryID = conn.RepositoryID
		m.DestinationBranch = conn.Branch
	}
	db.Moves[id] = m
	domain.ReconcileMoves(db)
	return db.Moves[id], nil
}

// Moves lists all moves, oldest first.
func (s Service) Moves() []domain.Move {
	var out []domain.Move
	_ = s.Store.View(func(db *domain.Database) error {
		out = make([]domain.Move, 0, len(db.Moves))
		for _, m := range db.Moves {
			out = append(out, m)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created < out[j].Created
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// CancelMove cancels a move that has not started deleting its source.
// Cancellation retains both copies and never attempts a remote deletion.
func (s Service) CancelMove(id string, confirm bool) error {
	if !confirm {
		return domain.ErrInvalid
	}
	return s.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		m, ok := db.Moves[id]
		if !ok {
			return domain.ErrNotFound
		}
		if m.State != "waiting_destination" && m.State != "waiting_source_delete" {
			return domain.ErrConflict
		}
		m.State = "cancelled"
		db.Moves[id] = m
		return nil
	})
}

// LinkContents returns the current text of each linked destination article
// when every one still has the expected digest.
func (s Service) LinkContents(sourceID string, expected map[string]string) (map[string]string, error) {
	var texts map[string]string
	err := s.Store.View(func(db *domain.Database) error {
		if !domain.LinksPresent(*db, sourceID, expected) {
			return domain.ErrConflict
		}
		texts = make(map[string]string, len(expected))
		for name := range expected {
			a := db.Articles[sourceID+"/"+name]
			current, _ := domain.LastRevision(a)
			texts[name] = current.Text
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return texts, nil
}
