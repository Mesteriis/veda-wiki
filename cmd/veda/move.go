package main

import (
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func (s *Store) linkContents(sourceID string, expected map[string]string) (map[string]string, error) {
	s.RLock()
	defer s.RUnlock()
	if !linksPresent(*s.DB(), sourceID, expected) {
		return nil, ErrConflict
	}
	texts := make(map[string]string, len(expected))
	for name := range expected {
		a := s.DB().Articles[sourceID+"/"+name]
		texts[name] = a.Versions[len(a.Versions)-1].Text
	}
	return texts, nil
}

func (s *Store) readAsset(ref BlobRef) ([]byte, error) {
	content, err := s.ReadBlob(ref.SHA)
	if err != nil {
		return nil, err
	}
	if verified, check := imageRef(content); check != nil || verified != ref {
		return nil, ErrInvalid
	}
	return content, nil
}

func (s *Store) verifyAsset(ref BlobRef) error { return s.VerifyBlob(ref) }

func (s *Store) verifyMoveAssets(assets map[string]BlobRef) error {
	seen := map[BlobRef]bool{}
	for _, ref := range assets {
		if seen[ref] {
			continue
		}
		if err := s.verifyAsset(ref); err != nil {
			return err
		}
		seen[ref] = true
	}
	return nil
}

type moveRequest struct {
	SourceID           string `json:"source_id"`
	SourcePath         string `json:"source_path"`
	ExpectedRevision   int    `json:"expected_revision"`
	DestinationID      string `json:"destination_id"`
	DestinationPath    string `json:"destination_path"`
	ConfirmMove        bool   `json:"confirm_move"`
	ConfirmPublication bool   `json:"confirm_publication"`
	IncludeLinked      bool   `json:"include_linked,omitempty"`
}

// Resolve relative images and verify linked articles at the destination.
// Embedded HTML and links without an identical destination remain blocked.
// Caller holds the store lock.
type transferDependencies struct {
	Assets         map[string]BlobRef
	Links          map[string]string
	DirectoryLinks bool
}

func (s *Store) transferAssets(value, sourcePath, targetPath string, src, target Source) (transferDependencies, error) {
	document := []byte(value)
	doc := markdown.Parser().Parse(text.NewReader(document))
	assets := map[string]BlobRef{}
	links := map[string]string{}
	verified := map[BlobRef]bool{}
	total := int64(0)
	var failure error
	directoryLinks := false
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest []byte
		image := false
		switch n := node.(type) {
		case *ast.Link:
			dest = n.Destination
		case *ast.Image:
			dest = n.Destination
			image = true
		case *ast.RawHTML, *ast.HTMLBlock:
			failure = ErrInvalid
			return ast.WalkStop, nil
		default:
			return ast.WalkContinue, nil
		}
		parsed, e := url.Parse(string(dest))
		if e != nil {
			failure = ErrInvalid
			return ast.WalkStop, nil
		}
		if parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" {
			return ast.WalkContinue, nil
		}
		if target.ConnectionID == "" && target.ImportGeneration != "" {
			failure = ErrInvalid
			return ast.WalkStop, nil
		}
		from, fromOK := repositoryTarget(sourcePath, parsed.Path)
		to, toOK := repositoryTarget(targetPath, parsed.Path)
		if !image && (!fromOK || !articlePath(from, src.Kind)) {
			from, fromOK, _ = s.directoryTransferSource(src.ID, sourcePath, parsed.Path)
			to, toOK = directoryReadmeTarget(targetPath, parsed.Path)
			directoryLinks = true
		}
		if !fromOK || !toOK || (target.Kind == "program" && !strings.HasPrefix(to, "docs/")) {
			failure = ErrInvalid
			return ast.WalkStop, nil
		}
		if !image {
			if from == sourcePath {
				if to == targetPath {
					return ast.WalkContinue, nil
				}
				failure = ErrInvalid
				return ast.WalkStop, nil
			}
			if !articlePath(from, src.Kind) || !articlePath(to, target.Kind) || to == targetPath {
				failure = ErrInvalid
				return ast.WalkStop, nil
			}
			original, ok := s.DB().Articles[src.ID+"/"+from]
			if !ok || original.Deleted || len(original.Versions) == 0 {
				failure = ErrInvalid
				return ast.WalkStop, nil
			}
			for _, conflict := range original.Conflicts {
				if conflict.Resolution == "" {
					failure = ErrConflict
					return ast.WalkStop, nil
				}
			}
			sha := original.Versions[len(original.Versions)-1].SHA
			if !linksPresent(*s.DB(), target.ID, map[string]string{to: sha}) {
				failure = ErrConflict
				return ast.WalkStop, nil
			}
			links[to] = sha
			if len(links) > 2000 {
				failure = ErrInvalid
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		}
		if target.ConnectionID != "" && !remoteAssetPath(to, target.Kind) {
			failure = ErrInvalid
			return ast.WalkStop, nil
		}
		ref, ok := src.Assets[from]
		if !ok {
			failure = ErrInvalid
			return ast.WalkStop, nil
		}
		if existing, ok := target.Assets[to]; ok && existing != ref {
			failure = ErrConflict
			return ast.WalkStop, nil
		}
		if existing, ok := assets[to]; ok && existing != ref {
			failure = ErrConflict
			return ast.WalkStop, nil
		}
		if !verified[ref] {
			if _, err := hexKey(ref.SHA); err != nil {
				failure = ErrInvalid
				return ast.WalkStop, nil
			}
			info, err := os.Lstat(path.Join(s.Dir(), "blobs", ref.SHA))
			if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > 4<<20 || total+info.Size() > 16<<20 {
				failure = ErrInvalid
				return ast.WalkStop, nil
			}
			if err := s.verifyAsset(ref); err != nil {
				failure = err
				return ast.WalkStop, nil
			}
			total += info.Size()
			verified[ref] = true
		}
		assets[to] = ref
		if len(assets) > 2000 {
			failure = ErrInvalid
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return transferDependencies{Assets: assets, Links: links, DirectoryLinks: directoryLinks}, failure
}
func (s *Store) moveArticle(in moveRequest) (Move, error) {
	if !in.ConfirmMove {
		return Move{}, ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	if moveDeletionLocks(*s.DB(), in.SourceID, in.SourcePath) {
		return Move{}, ErrConflict
	}
	src, ok := s.DB().Sources[in.SourceID]
	if !ok {
		return Move{}, ErrNotFound
	}
	// A local import may be repopulated by an independent synchronizer.
	if src.ConnectionID == "" && src.ImportGeneration != "" {
		return Move{}, ErrInvalid
	}
	a, ok := s.DB().Articles[in.SourceID+"/"+in.SourcePath]
	if !ok || a.Deleted {
		return Move{}, ErrNotFound
	}
	if in.ExpectedRevision != len(a.Versions) || in.ExpectedRevision < 1 {
		return Move{}, ErrConflict
	}
	if src.ConnectionID != "" {
		c, connected := s.DB().Connections[src.ConnectionID]
		if !connected || !c.Write || c.Paused || a.UpstreamHash != a.Versions[in.ExpectedRevision-1].SHA {
			return Move{}, ErrConflict
		}
		// Different source IDs may point at the very same GitHub branch. A
		// copy to the same physical path followed by deletion would erase both.
		if dst, exists := s.DB().Sources[in.DestinationID]; exists && dst.ConnectionID != "" {
			other, connected := s.DB().Connections[dst.ConnectionID]
			if connected && c.RepositoryID == other.RepositoryID && c.Branch == other.Branch && in.SourcePath == in.DestinationPath {
				return Move{}, ErrConflict
			}
		}
	}
	// Translations belong to program documents; never discard one when moving.
	if a.Translation != nil && s.DB().Sources[in.DestinationID].Kind != "program" {
		return Move{}, ErrInvalid
	}
	for _, m := range s.DB().Moves {
		if (m.State == "waiting_destination" || m.State == "waiting_source_delete" || m.State == "deleting_source") && m.SourceID == in.SourceID && m.SourcePath == in.SourcePath {
			return Move{}, ErrConflict
		}
	}
	copyIn := copyRequest{SourceID: in.SourceID, SourcePath: in.SourcePath, ExpectedRevision: in.ExpectedRevision, DestinationID: in.DestinationID, DestinationPath: in.DestinationPath, ConfirmPublication: in.ConfirmPublication}
	var db Database
	var rev Revision
	var dependencies transferDependencies
	var e error
	if in.IncludeLinked {
		db, rev, dependencies, e = s.prepareCopyLinkedLocal(copyIn)
	} else {
		dependencies, e = s.transferAssets(a.Versions[len(a.Versions)-1].Text, in.SourcePath, in.DestinationPath, src, s.DB().Sources[in.DestinationID])
		if e == nil {
			db, rev, e = s.prepareCopy(copyIn)
		}
	}
	if e != nil {
		return Move{}, e
	}
	if a.Translation != nil && db.Version < 5 {
		// Old binaries must refuse stores containing these new move invariants.
		db.Version = 5
	}
	if len(dependencies.Assets) > 0 {
		version := 6
		if s.DB().Sources[in.DestinationID].ConnectionID != "" {
			version = 7
		}
		if db.Version < version {
			db.Version = version
		}
	}
	if len(dependencies.Links) > 0 && db.Version < 8 {
		db.Version = 8
	}
	if dependencies.DirectoryLinks && db.Version < 16 {
		db.Version = 16
	}
	if in.IncludeLinked && db.Sources[in.DestinationID].ConnectionID != "" && db.Version < 10 {
		// Previous binaries do not understand graph moves awaiting a bundle.
		db.Version = 10
	}
	id, e := randomID()
	if e != nil {
		return Move{}, e
	}
	if db.Moves == nil {
		db.Moves = map[string]Move{}
	}
	m := Move{ID: id, SourceID: in.SourceID, SourcePath: in.SourcePath, SourceRevision: in.ExpectedRevision, DestinationID: in.DestinationID, DestinationPath: in.DestinationPath, SHA: rev.SHA, TranslationFingerprint: translationFingerprint(a.Translation), Assets: dependencies.Assets, Links: dependencies.Links, State: "waiting_destination", Created: time.Now().UTC().Format(time.RFC3339Nano)}
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
	if e = s.Commit(db); e != nil {
		return Move{}, e
	}
	return m, nil
}
func (s *Store) moves() []Move {
	s.RLock()
	defer s.RUnlock()
	out := make([]Move, 0, len(s.DB().Moves))
	for _, m := range s.DB().Moves {
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

// Cancellation retains both copies and never attempts a remote deletion.
func (s *Store) cancelMove(id string, confirm bool) error {
	if !confirm {
		return ErrInvalid
	}
	s.Lock()
	defer s.Unlock()
	m, ok := s.DB().Moves[id]
	if !ok {
		return ErrNotFound
	}
	if m.State != "waiting_destination" && m.State != "waiting_source_delete" {
		return ErrConflict
	}
	m.State = "cancelled"
	db := s.Clone()
	db.Moves[id] = m
	return s.Commit(db)
}
