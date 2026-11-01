package main

import (
	"net/url"
	"sort"
	"time"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Move records one explicit, revision-bound move; source history is retained.
type Move struct {
	ID                      string `json:"id"`
	SourceID                string `json:"source_id"`
	SourcePath              string `json:"source_path"`
	SourceRevision          int    `json:"source_revision"`
	SourceConnectionID      string `json:"source_connection_id,omitempty"`
	SourceRepositoryID      int64  `json:"source_repository_id,omitempty"`
	SourceBranch            string `json:"source_branch,omitempty"`
	DestinationID           string `json:"destination_id"`
	DestinationPath         string `json:"destination_path"`
	DestinationConnectionID string `json:"destination_connection_id,omitempty"`
	DestinationRepositoryID int64  `json:"destination_repository_id,omitempty"`
	DestinationBranch       string `json:"destination_branch,omitempty"`
	SHA                     string `json:"sha256"`
	State                   string `json:"state"`
	PreparedDeletion        string `json:"prepared_deletion,omitempty"`
	Created                 string `json:"created_at"`
}

// A prepared deletion may have reached GitHub even when the response was lost.
// Keep both local copies stable until a complete remote pull resolves it.
func moveDeletionLocks(db Database, sourceID, p string) bool {
	for _, m := range db.Moves {
		if m.State == "deleting_source" && ((m.SourceID == sourceID && m.SourcePath == p) || (m.DestinationID == sourceID && m.DestinationPath == p)) {
			return true
		}
	}
	return false
}

func moveDeletionLocksConnection(db Database, id string) bool {
	for _, m := range db.Moves {
		if m.State == "deleting_source" && (m.SourceConnectionID == id || m.DestinationConnectionID == id) {
			return true
		}
	}
	return false
}

type moveRequest struct {
	SourceID           string `json:"source_id"`
	SourcePath         string `json:"source_path"`
	ExpectedRevision   int    `json:"expected_revision"`
	DestinationID      string `json:"destination_id"`
	DestinationPath    string `json:"destination_path"`
	ConfirmMove        bool   `json:"confirm_move"`
	ConfirmPublication bool   `json:"confirm_publication"`
}

// Relative references can depend on source-only pages/assets. Do not move them
// until dependency copying and rewriting are implemented.
func hasRelativeDependencies(value string) bool {
	source := []byte(value)
	doc := markdown.Parser().Parse(text.NewReader(source))
	found := false
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest []byte
		switch n := node.(type) {
		case *ast.Link:
			dest = n.Destination
		case *ast.Image:
			dest = n.Destination
		default:
			return ast.WalkContinue, nil
		}
		parsed, e := url.Parse(string(dest))
		if e != nil || (parsed.Scheme == "" && parsed.Host == "" && parsed.Path != "") {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}
func (s *Store) moveArticle(in moveRequest) (Move, error) {
	if !in.ConfirmMove {
		return Move{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.db.Sources[in.SourceID]
	if !ok {
		return Move{}, ErrNotFound
	}
	// A local import may be repopulated by an independent synchronizer.
	if src.ConnectionID == "" && src.ImportGeneration != "" {
		return Move{}, ErrInvalid
	}
	a, ok := s.db.Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || a.Deleted {
		return Move{}, ErrNotFound
	}
	if in.ExpectedRevision != len(a.Versions) || in.ExpectedRevision < 1 {
		return Move{}, ErrConflict
	}
	if src.ConnectionID != "" {
		c, connected := s.db.Connections[src.ConnectionID]
		if !connected || !c.Write || c.Paused || a.UpstreamHash != a.Versions[in.ExpectedRevision-1].SHA {
			return Move{}, ErrConflict
		}
		// Different source IDs may point at the very same GitHub branch. A
		// copy to the same physical path followed by deletion would erase both.
		if dst, exists := s.db.Sources[in.DestinationID]; exists && dst.ConnectionID != "" {
			other, connected := s.db.Connections[dst.ConnectionID]
			if connected && c.RepositoryID == other.RepositoryID && c.Branch == other.Branch && in.SourcePath == in.DestinationPath {
				return Move{}, ErrConflict
			}
		}
	}
	// A derived translation must not become inaccessible by silently moving only its original.
	if a.Translation != nil {
		return Move{}, ErrInvalid
	}
	if hasRelativeDependencies(a.Versions[len(a.Versions)-1].Text) {
		return Move{}, ErrInvalid
	}
	for _, m := range s.db.Moves {
		if (m.State == "waiting_destination" || m.State == "waiting_source_delete" || m.State == "deleting_source") && m.SourceID == in.SourceID && m.SourcePath == in.SourcePath {
			return Move{}, ErrConflict
		}
	}
	db, rev, e := s.prepareCopy(copyRequest{SourceID: in.SourceID, SourcePath: in.SourcePath, ExpectedRevision: in.ExpectedRevision, DestinationID: in.DestinationID, DestinationPath: in.DestinationPath, ConfirmPublication: in.ConfirmPublication})
	if e != nil {
		return Move{}, e
	}
	id, e := randomID()
	if e != nil {
		return Move{}, e
	}
	if db.Moves == nil {
		db.Moves = map[string]Move{}
	}
	m := Move{ID: id, SourceID: in.SourceID, SourcePath: in.SourcePath, SourceRevision: in.ExpectedRevision, DestinationID: in.DestinationID, DestinationPath: in.DestinationPath, SHA: rev.SHA, State: "waiting_destination", Created: time.Now().UTC().Format(time.RFC3339Nano)}
	if src.ConnectionID != "" {
		conn := db.Connections[src.ConnectionID]
		m.SourceConnectionID, m.SourceRepositoryID, m.SourceBranch = src.ConnectionID, conn.RepositoryID, conn.Branch
	}
	destination := db.Sources[in.DestinationID]
	if destination.ConnectionID != "" {
		conn, ok := db.Connections[destination.ConnectionID]
		if !ok {
			return Move{}, ErrInvalid
		}
		m.DestinationConnectionID = destination.ConnectionID
		m.DestinationRepositoryID = conn.RepositoryID
		m.DestinationBranch = conn.Branch
	}
	// Never downgrade a store with newer features to an older format.
	if src.ConnectionID != "" && db.Version < 3 {
		db.Version = 3
	} else if db.Version < 2 {
		db.Version = 2
	}
	db.Moves[id] = m
	reconcileMoves(&db)
	m = db.Moves[id]
	if e = s.commit(db); e != nil {
		return Move{}, e
	}
	return m, nil
}
func (s *Store) moves() []Move {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Move, 0, len(s.db.Moves))
	for _, m := range s.db.Moves {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created < out[j].Created
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Called only inside the same transaction that changes source or destination.
// Connected destinations must be observed in GitHub before source removal.
func reconcileMoves(db *Database) {
	for id, m := range db.Moves {
		if m.State != "waiting_destination" && m.State != "waiting_source_delete" && m.State != "deleting_source" {
			continue
		}
		source, exists := db.Articles[m.SourceID+"/"+m.SourcePath]
		src, srcOK := db.Sources[m.SourceID]
		if !exists || !srcOK || source.Deleted || source.Translation != nil || len(source.Versions) != m.SourceRevision || source.Versions[m.SourceRevision-1].SHA != m.SHA || src.ConnectionID != m.SourceConnectionID || (m.SourceConnectionID == "" && src.ImportGeneration != "") {
			m.State = "source_changed"
			db.Moves[id] = m
			continue
		}
		if m.SourceConnectionID != "" {
			conn, ok := db.Connections[m.SourceConnectionID]
			if !ok || conn.RepositoryID != m.SourceRepositoryID || conn.Branch != m.SourceBranch || source.UpstreamHash != m.SHA {
				m.State = "source_changed"
				db.Moves[id] = m
				continue
			}
			for _, conflict := range source.Conflicts {
				if conflict.Resolution == "" {
					m.State = "source_changed"
					db.Moves[id] = m
					break
				}
			}
			if m.State == "source_changed" {
				continue
			}
		}
		target, exists := db.Articles[m.DestinationID+"/"+m.DestinationPath]
		dst, dstOK := db.Sources[m.DestinationID]
		if !exists || !dstOK || target.Deleted || len(target.Versions) != 1 || target.Versions[0].SHA != m.SHA {
			m.State = "destination_changed"
			db.Moves[id] = m
			continue
		}
		conflicting := false
		for _, c := range target.Conflicts {
			if c.Resolution == "" {
				conflicting = true
				break
			}
		}
		if conflicting {
			m.State = "destination_changed"
			db.Moves[id] = m
			continue
		}
		if dst.ConnectionID != m.DestinationConnectionID {
			m.State = "destination_changed"
			db.Moves[id] = m
			continue
		}
		if m.DestinationConnectionID != "" {
			conn, ok := db.Connections[m.DestinationConnectionID]
			if !ok || conn.RepositoryID != m.DestinationRepositoryID || conn.Branch != m.DestinationBranch {
				m.State = "destination_changed"
				db.Moves[id] = m
				continue
			}
			jobID := m.DestinationID + "/" + m.DestinationPath + "@1"
			job, ok := db.Outbox[jobID]
			if !ok || job.State != "complete" || job.SHA != m.SHA {
				continue
			}
		}
		if m.SourceConnectionID != "" {
			if m.State != "deleting_source" {
				m.State = "waiting_source_delete"
			}
			db.Moves[id] = m
			continue
		}
		source.Deleted = true
		db.Articles[m.SourceID+"/"+m.SourcePath] = source
		m.State = "complete"
		db.Moves[id] = m
	}
}

// Cancellation retains both copies and never attempts a remote deletion.
func (s *Store) cancelMove(id string, confirm bool) error {
	if !confirm {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.db.Moves[id]
	if !ok {
		return ErrNotFound
	}
	if m.State != "waiting_destination" && m.State != "waiting_source_delete" {
		return ErrConflict
	}
	m.State = "cancelled"
	db := s.clone()
	db.Moves[id] = m
	return s.commit(db)
}
