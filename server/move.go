package main

import (
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Move records one explicit, revision-bound move; source history is retained.
type Move struct {
	ID                      string             `json:"id"`
	SourceID                string             `json:"source_id"`
	SourcePath              string             `json:"source_path"`
	SourceRevision          int                `json:"source_revision"`
	SourceConnectionID      string             `json:"source_connection_id,omitempty"`
	SourceRepositoryID      int64              `json:"source_repository_id,omitempty"`
	SourceBranch            string             `json:"source_branch,omitempty"`
	DestinationID           string             `json:"destination_id"`
	DestinationPath         string             `json:"destination_path"`
	DestinationConnectionID string             `json:"destination_connection_id,omitempty"`
	DestinationRepositoryID int64              `json:"destination_repository_id,omitempty"`
	DestinationBranch       string             `json:"destination_branch,omitempty"`
	SHA                     string             `json:"sha256"`
	State                   string             `json:"state"`
	PreparedDeletion        string             `json:"prepared_deletion,omitempty"`
	TranslationFingerprint  string             `json:"translation_fingerprint,omitempty"`
	Assets                  map[string]BlobRef `json:"assets,omitempty"`
	Links                   map[string]string  `json:"links,omitempty"`
	Created                 string             `json:"created_at"`
}

func translationFingerprint(tr *Translation) string {
	if tr == nil {
		return ""
	}
	b, _ := json.Marshal(tr)
	return digest(b)
}

func movedTranslation(tr *Translation) *Translation {
	if tr == nil {
		return nil
	}
	copy := *tr
	copy.PublicApproved = false
	return &copy
}

func assetsPresent(source Source, expected map[string]BlobRef) bool {
	for name, ref := range expected {
		if actual, ok := source.Assets[name]; !ok || actual != ref {
			return false
		}
	}
	return true
}

func linksPresent(db Database, sourceID string, expected map[string]string) bool {
	src, ok := db.Sources[sourceID]
	if !ok {
		return false
	}
	for name, sha := range expected {
		a, exists := db.Articles[sourceID+"/"+name]
		if !exists || a.Deleted || len(a.Versions) == 0 || a.Versions[len(a.Versions)-1].SHA != sha {
			return false
		}
		if src.ConnectionID != "" && a.UpstreamHash != sha {
			return false
		}
		if src.Visibility == "public" && (a.Published == nil || a.Published.SHA != sha) {
			return false
		}
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				return false
			}
		}
	}
	return true
}

func (s *Store) linkContents(sourceID string, expected map[string]string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !linksPresent(s.db, sourceID, expected) {
		return nil, ErrConflict
	}
	texts := make(map[string]string, len(expected))
	for name := range expected {
		a := s.db.Articles[sourceID+"/"+name]
		texts[name] = a.Versions[len(a.Versions)-1].Text
	}
	return texts, nil
}

func remoteAssetPath(name, kind string) bool {
	if !importablePath(name, kind) {
		return false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// Keep only images still referenced by a later revision of a queued article.
func retainedPendingAssets(value, articlePath string, candidates map[string]BlobRef) map[string]BlobRef {
	if len(candidates) == 0 {
		return nil
	}
	refs := map[string]BlobRef{}
	root := markdown.Parser().Parse(text.NewReader([]byte(value)))
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		image, ok := node.(*ast.Image)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(image.Destination))
		if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
			return ast.WalkContinue, nil
		}
		name := path.Clean(path.Join(path.Dir(articlePath), u.Path))
		if ref, found := candidates[name]; found {
			refs[name] = ref
		}
		return ast.WalkContinue, nil
	})
	return refs
}

func retainedPendingLinks(value, articlePath string, candidates map[string]string) map[string]string {
	if len(candidates) == 0 {
		return nil
	}
	refs := map[string]string{}
	root := markdown.Parser().Parse(text.NewReader([]byte(value)))
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		link, ok := node.(*ast.Link)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(link.Destination))
		if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
			return ast.WalkContinue, nil
		}
		name := path.Clean(path.Join(path.Dir(articlePath), u.Path))
		if sha, found := candidates[name]; found {
			refs[name] = sha
		}
		return ast.WalkContinue, nil
	})
	return refs
}

func (s *Store) readAsset(ref BlobRef) ([]byte, error) {
	if _, err := hexKey(ref.SHA); err != nil {
		return nil, ErrInvalid
	}
	filename := path.Join(s.dir, "blobs", ref.SHA)
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, ErrNotFound
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	verified, check := imageRef(content)
	if err != nil || check != nil || verified != ref {
		return nil, ErrInvalid
	}
	return content, nil
}

func (s *Store) verifyAsset(ref BlobRef) error { _, err := s.readAsset(ref); return err }

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

// A prepared deletion may have reached GitHub even when the response was lost.
// Keep both local copies stable until a complete remote pull resolves it.
func moveDeletionLocks(db Database, sourceID, p string) bool {
	for _, job := range db.Outbox {
		if len(job.Bundle) == 0 || job.SourceID != sourceID || job.State == "complete" || job.State == "superseded" {
			continue
		}
		if job.Path == p {
			return true
		}
		if _, ok := job.Bundle[p]; ok {
			return true
		}
		if _, ok := job.Links[p]; ok {
			return true
		}
	}
	for _, m := range db.Moves {
		if m.State == "deleting_source" {
			if (m.SourceID == sourceID && m.SourcePath == p) || (m.DestinationID == sourceID && m.DestinationPath == p) {
				return true
			}
			if m.DestinationID == sourceID {
				if _, dependency := m.Links[p]; dependency {
					return true
				}
			}
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
	IncludeLinked      bool   `json:"include_linked,omitempty"`
}

// Resolve relative images and verify linked articles at the destination.
// Embedded HTML and links without an identical destination remain blocked.
// Caller holds the store lock.
type transferDependencies struct {
	Assets map[string]BlobRef
	Links  map[string]string
}

func (s *Store) transferAssets(value, sourcePath, targetPath string, src, target Source) (transferDependencies, error) {
	document := []byte(value)
	doc := markdown.Parser().Parse(text.NewReader(document))
	assets := map[string]BlobRef{}
	links := map[string]string{}
	verified := map[BlobRef]bool{}
	total := int64(0)
	var failure error
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
		if strings.HasPrefix(parsed.Path, "/") || (target.ConnectionID == "" && target.ImportGeneration != "") {
			failure = ErrInvalid
			return ast.WalkStop, nil
		}
		from := path.Clean(path.Join(path.Dir(sourcePath), parsed.Path))
		to := path.Clean(path.Join(path.Dir(targetPath), parsed.Path))
		if !safeRelative(from) || !safeRelative(to) || (target.Kind == "program" && !strings.HasPrefix(to, "docs/")) {
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
			original, ok := s.db.Articles[src.ID+"/"+from]
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
			if !linksPresent(s.db, target.ID, map[string]string{to: sha}) {
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
			info, err := os.Lstat(path.Join(s.dir, "blobs", ref.SHA))
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
	return transferDependencies{Assets: assets, Links: links}, failure
}
func (s *Store) moveArticle(in moveRequest) (Move, error) {
	if !in.ConfirmMove {
		return Move{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if moveDeletionLocks(s.db, in.SourceID, in.SourcePath) {
		return Move{}, ErrConflict
	}
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
	// Translations belong to program documents; never discard one when moving.
	if a.Translation != nil && s.db.Sources[in.DestinationID].Kind != "program" {
		return Move{}, ErrInvalid
	}
	for _, m := range s.db.Moves {
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
		// A graph move requires a separate remote-observation invariant; until
		// then only copy may enqueue a multi-article destination bundle.
		if dst, ok := s.db.Sources[in.DestinationID]; ok && dst.ConnectionID != "" {
			return Move{}, ErrInvalid
		}
		db, rev, dependencies, e = s.prepareCopyLinkedLocal(copyIn)
	} else {
		dependencies, e = s.transferAssets(a.Versions[len(a.Versions)-1].Text, in.SourcePath, in.DestinationPath, src, s.db.Sources[in.DestinationID])
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
		if s.db.Sources[in.DestinationID].ConnectionID != "" {
			version = 7
		}
		if db.Version < version {
			db.Version = version
		}
	}
	if len(dependencies.Links) > 0 && db.Version < 8 {
		db.Version = 8
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
		if !exists || !srcOK || source.Deleted || translationFingerprint(source.Translation) != m.TranslationFingerprint || len(source.Versions) != m.SourceRevision || source.Versions[m.SourceRevision-1].SHA != m.SHA || src.ConnectionID != m.SourceConnectionID || (m.SourceConnectionID == "" && src.ImportGeneration != "") {
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
		if !exists || !dstOK || target.Deleted || len(target.Versions) != 1 || target.Versions[0].SHA != m.SHA || translationFingerprint(target.Translation) != translationFingerprint(movedTranslation(source.Translation)) || (m.DestinationConnectionID == "" && !assetsPresent(dst, m.Assets)) || !linksPresent(*db, m.DestinationID, m.Links) {
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
			if !assetsPresent(dst, m.Assets) {
				m.State = "destination_changed"
				db.Moves[id] = m
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
