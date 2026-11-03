package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type ImportBatch struct {
	withheldReasons map[string]int    // local preview only; never serialized
	SourceID        string            `json:"source_id"`
	Kind            string            `json:"kind"`
	Metadata        *WikiMetadata     `json:"metadata,omitempty"`
	Files           map[string]string `json:"files"`
	Assets          map[string][]byte `json:"assets,omitempty"`
	Commit          string            `json:"commit"`
	Dirty           bool              `json:"dirty"`
	Complete        bool              `json:"complete"`
	Withheld        int               `json:"withheld"`
	ExpectedImport  *string           `json:"expected_import"`
	Create          bool              `json:"create"`
	Visibility      string            `json:"visibility,omitempty"`
}
type ImportResult struct {
	Generation string `json:"generation"`
	Created    int    `json:"created"`
	Updated    int    `json:"updated"`
	Deleted    int    `json:"deleted"`
	Conflicts  int    `json:"conflicts"`
	Unchanged  int    `json:"unchanged"`
	Withheld   int    `json:"withheld"`
}

// Bound aggregate imported content separately from the JSON transport. Images
// are base64-encoded in transport, and JSON text may require escaping.
const maxImportBytes = 32 << 20
const maxImportRequestBytes = 64 << 20

func validateMetadata(m *WikiMetadata) error {
	if m == nil || m.SchemaVersion != 1 || m.Kind != "wiki" || strings.TrimSpace(m.Title) == "" || len(m.Title) > 200 || len(m.Description) > 4000 || len(m.URL) > 2048 {
		return ErrInvalid
	}
	if m.URL != "" {
		u, e := url.Parse(m.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return ErrInvalid
		}
	}
	if m.Image != "" && !safeRelative(m.Image) {
		return ErrInvalid
	}
	return nil
}
func parseMetadata(b []byte) (*WikiMetadata, error) {
	if len(b) > 16384 || !utf8.Valid(b) || secretContent.Match(b) {
		return nil, ErrInvalid
	}
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if e := dec.Decode(&node); e != nil {
		return nil, ErrInvalid
	}
	var check func(*yaml.Node) bool
	check = func(n *yaml.Node) bool {
		if n.Alias != nil || n.Anchor != "" || (n.Tag != "" && n.Tag != "!!map" && n.Tag != "!!str" && n.Tag != "!!int") {
			return false
		}
		for _, c := range n.Content {
			if !check(c) {
				return false
			}
		}
		return true
	}
	if !check(&node) {
		return nil, ErrInvalid
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	var m WikiMetadata
	if e := decoder.Decode(&m); e != nil {
		return nil, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	if e := validateMetadata(&m); e != nil {
		return nil, e
	}
	return &m, nil
}

// prepareCheckout never follows symlinks or evaluates repository hooks. Its
// result is a bounded working-tree observation, not a claim of a clean commit.
func prepareCheckout(directory, sourceID string) (ImportBatch, error) {
	result := ImportBatch{SourceID: sourceID, Files: map[string]string{}, Assets: map[string][]byte{}, Complete: true, withheldReasons: map[string]int{}}
	withhold := func(reason string) {
		result.Withheld++
		result.Complete = false
		result.withheldReasons[reason]++
	}
	if !identifier.MatchString(sourceID) {
		return result, ErrInvalid
	}
	info, e := os.Lstat(directory)
	if e != nil || !info.IsDir() {
		return result, ErrInvalid
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		return result, e
	}
	defer root.Close()
	read := func(name string, limit int64) ([]byte, error) {
		f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			return nil, ErrInvalid
		}
		b, e := io.ReadAll(io.LimitReader(f, limit+1))
		if e != nil || int64(len(b)) > limit {
			return nil, ErrInvalid
		}
		return b, nil
	}
	git := func(args ...string) (string, error) {
		argv := append([]string{"-C", directory, "-c", "core.fsmonitor=false"}, args...)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", argv...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
		var out boundedGitOutput
		cmd.Stdout = &out
		e := cmd.Run()
		value := out.String()
		if len(args) > 0 && args[0] == "ls-files" {
			return value, e
		}
		return strings.TrimSpace(value), e
	}
	top, e := git("rev-parse", "--show-toplevel")
	if e != nil {
		return result, errors.New("selected directory is not a Git checkout")
	}
	selected, e := filepath.EvalSymlinks(directory)
	if e != nil {
		return result, e
	}
	selected, e = filepath.Abs(selected)
	if e != nil {
		return result, e
	}
	top, e = filepath.EvalSymlinks(top)
	if e != nil || selected != top {
		return result, errors.New("select the Git checkout root")
	}
	head, e := git("rev-parse", "--verify", "HEAD")
	if e != nil || !commitID.MatchString(head) {
		return result, errors.New("checkout requires an existing Git commit")
	}
	result.Commit = head
	status, e := git("status", "--porcelain", "--untracked-files=no")
	if e != nil {
		return result, e
	}
	result.Dirty = status != ""
	unmergedText, e := git("ls-files", "--unmerged", "-z")
	if e != nil {
		return result, e
	}
	unmerged := map[string]bool{}
	for _, record := range strings.Split(unmergedText, "\x00") {
		if record == "" {
			continue
		}
		_, name, ok := strings.Cut(record, "\t")
		if !ok || name == "" {
			return result, ErrInvalid
		}
		unmerged[name] = true
	}
	if unmerged["veda.yaml"] {
		return result, ErrConflict
	}
	trackedText, e := git("ls-files", "-z", "--cached")
	if e != nil {
		return result, e
	}
	tracked := map[string]bool{}
	directories := map[string]bool{".": true}
	for _, name := range strings.Split(trackedText, "\x00") {
		if name == "" {
			continue
		}
		tracked[name] = true
		if len(tracked) > 20000 {
			//lint:ignore ST1005 "Git" is a proper noun; the message text is observable and must not change
			return result, errors.New("Git index exceeds import limits")
		}
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			directories[dir] = true
		}
	}
	if info, e := fs.Lstat(root.FS(), "veda.yaml"); e == nil {
		if !info.Mode().IsRegular() || !tracked["veda.yaml"] {
			return result, ErrInvalid
		}
		b, e := read("veda.yaml", 16384)
		if e != nil {
			return result, e
		}
		result.Metadata, e = parseMetadata(b)
		if e != nil {
			return result, e
		}
		result.Kind = "wiki"
	} else if !errors.Is(e, fs.ErrNotExist) {
		return result, e
	} else {
		info, e := fs.Lstat(root.FS(), "docs")
		if errors.Is(e, fs.ErrNotExist) {
			return result, ErrNoDocs
		}
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, ErrInvalid
		}
		if !directories["docs"] {
			return result, ErrNoDocs
		}
		result.Kind = "program"
	}
	start := "."
	if result.Kind == "program" {
		start = "docs"
	}
	// Git can leave an unmerged path absent from the working tree. Count it
	// before traversal so a partial import cannot mistake it for a deletion.
	for name := range unmerged {
		if !importablePath(name, result.Kind) {
			continue
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".md", ".markdown", ".png", ".jpg", ".jpeg", ".webp", ".gif":
			withhold("unmerged")
		}
	}
	total := 0
	count := 0
	entries := 0
	e = fs.WalkDir(root.FS(), start, func(name string, entry fs.DirEntry, walkErr error) error {
		entries++
		if entries > 20000 {
			return errors.New("checkout traversal limit exceeded")
		}
		if walkErr != nil {
			return walkErr
		}
		if name == "." || name == "docs" {
			return nil
		}
		if entry.IsDir() {
			if !directories[name] {
				return fs.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor" || entry.Name() == "dist" {
				return fs.SkipDir
			}
			return nil
		}
		if !tracked[name] || !safeRelative(name) {
			return nil
		}
		if unmerged[name] {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			withhold("symlink")
			return nil
		}
		if !entry.Type().IsRegular() {
			return ErrInvalid
		}
		ext := strings.ToLower(path.Ext(name))
		markdown := ext == ".md" || ext == ".markdown"
		image := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"
		if !markdown && !image {
			return nil
		}
		limit := int64(maxArticleBytes)
		if image {
			limit = 4 << 20
		}
		b, e := read(name, limit)
		if e != nil {
			return e
		}
		count++
		total += len(b)
		if count > 2000 || total > maxImportBytes {
			return errors.New("checkout exceeds import limits")
		}
		if markdown {
			if !utf8.Valid(b) {
				withhold("invalid_utf8")
				return nil
			}
			if secretContent.Match(b) {
				withhold("sensitive_pattern")
				return nil
			}
			result.Files[name] = string(b)
		} else {
			if _, e := imageRef(b); e != nil {
				withhold("unsupported_image")
				return nil
			}
			result.Assets[name] = b
		}
		return nil
	})
	if e != nil {
		return result, e
	}
	if result.Metadata != nil && result.Metadata.Image != "" {
		if _, ok := result.Assets[result.Metadata.Image]; !ok {
			return result, errors.New("metadata image is unavailable or unsafe")
		}
	}
	after, e := git("rev-parse", "--verify", "HEAD")
	if e != nil || after != head {
		return result, ErrConflict
	}
	statusAfter, e := git("status", "--porcelain", "--untracked-files=no")
	if e != nil || statusAfter != status {
		return result, ErrConflict
	}
	unmergedAfter, e := git("ls-files", "--unmerged", "-z")
	if e != nil || unmergedAfter != unmergedText {
		return result, ErrConflict
	}
	return result, nil
}

func imageRef(b []byte) (BlobRef, error) {
	if len(b) == 0 || len(b) > 4<<20 {
		return BlobRef{}, ErrInvalid
	}
	mime := http.DetectContentType(b)
	switch mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return BlobRef{SHA: digest(b), MIME: mime}, nil
	}
	return BlobRef{}, ErrInvalid
}

func (s *Store) importBatch(batch ImportBatch) (ImportResult, error) {
	return s.applyImport(batch, nil, false)
}

// Write verified immutable blobs before advancing the observation or metadata.
// A failed metadata commit may leave an unreferenced blob, but never a partial
// file at a referenced digest. The caller holds the store lock.
func (s *Store) storeImportAssets(refs map[string]BlobRef, assets map[string][]byte) error {
	if len(refs) == 0 {
		return nil
	}
	directory := path.Join(s.dir, "blobs")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	changed := false
	for name, ref := range refs {
		if s.verifyAsset(ref) == nil {
			continue
		}
		f, err := os.CreateTemp(directory, ".blob-")
		if err != nil {
			return err
		}
		_, err = f.Write(assets[name])
		if err == nil {
			err = f.Sync()
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(f.Name(), path.Join(directory, ref.SHA))
		}
		os.Remove(f.Name())
		if err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) applyImport(batch ImportBatch, proof *remoteProof, privateLocalOnly bool) (ImportResult, error) {
	result := ImportResult{Withheld: batch.Withheld}
	if !identifier.MatchString(batch.SourceID) || (batch.Kind != "wiki" && batch.Kind != "program") || !commitID.MatchString(batch.Commit) || batch.ExpectedImport == nil || batch.Withheld < 0 || len(batch.Files)+len(batch.Assets) > 2000 {
		return result, ErrInvalid
	}
	if batch.Kind == "wiki" {
		if e := validateMetadata(batch.Metadata); e != nil {
			return result, e
		}
	} else if batch.Metadata != nil {
		return result, ErrInvalid
	}
	total := 0
	for p, text := range batch.Files {
		if !articlePath(p, batch.Kind) || len(text) > maxArticleBytes || !utf8.ValidString(text) || secretContent.MatchString(text) {
			return result, ErrInvalid
		}
		total += len(text)
	}
	refs := map[string]BlobRef{}
	for name, data := range batch.Assets {
		if !safeRelative(name) || (batch.Kind == "program" && !strings.HasPrefix(name, "docs/")) {
			return result, ErrInvalid
		}
		ref, e := imageRef(data)
		if e != nil {
			return result, e
		}
		refs[name] = ref
		total += len(data)
	}
	if total > maxImportBytes {
		return result, ErrInvalid
	}
	if batch.Metadata != nil && batch.Metadata.Image != "" {
		if _, ok := refs[batch.Metadata.Image]; !ok {
			return result, ErrInvalid
		}
	}
	canonical, _ := json.Marshal(struct {
		Files    map[string]string
		Assets   map[string]BlobRef
		Commit   string
		Metadata *WikiMetadata
		Dirty    bool
		Partial  bool
	}{batch.Files, refs, batch.Commit, batch.Metadata, batch.Dirty, !batch.Complete || batch.Withheld > 0})
	generation := digest(canonical)
	s.mu.Lock()
	defer s.mu.Unlock()
	src, exists := s.db.Sources[batch.SourceID]
	// The privileged local ingest route can update only a pre-existing,
	// disconnected private source. Check this inside the import transaction.
	if privateLocalOnly && (!exists || src.Visibility != "private" || src.ConnectionID != "" || batch.Create) {
		return result, ErrInvalid
	}
	if !exists {
		if !batch.Create || (batch.Visibility != "public" && batch.Visibility != "private") {
			return result, ErrNotFound
		}
		src = Source{ID: batch.SourceID, Title: batch.SourceID, Kind: batch.Kind, Visibility: batch.Visibility}
	} else if batch.Create {
		return result, ErrConflict
	}
	wasPrivate := src.Visibility != "public"
	if proof == nil && src.ConnectionID != "" {
		return result, ErrInvalid
	}
	if proof != nil {
		if proof.Adoption != nil {
			adopt := proof.Adoption
			conn := adopt.Connection
			current, err := s.adoptionSnapshotLocked(batch.SourceID)
			account, accountOK := s.db.Accounts[conn.AccountID]
			if err != nil || !exists || current != adopt.Snapshot || !accountOK || account != adopt.Account || conn.AccountID == "" || conn.ID != batch.SourceID || conn.ID != proof.ConnectionID || conn.ConfigRevision != proof.ConfigRevision || !conn.Private || proof.Visibility != "private" || !batch.Complete || batch.Withheld != 0 {
				return result, ErrConflict
			}
			for name, old := range src.Assets {
				if incoming, ok := refs[name]; !ok || incoming != old {
					return result, ErrConflict
				}
			}
			src.ConnectionID = conn.ID
		} else {
			conn, ok := s.db.Connections[proof.ConnectionID]
			if !ok || conn.Paused || conn.ConfigRevision != proof.ConfigRevision || conn.ID != batch.SourceID || (exists && src.ConnectionID != conn.ID) {
				return result, ErrConflict
			}
			src.ConnectionID = conn.ID
		}
		src.Visibility = proof.Visibility
	}
	if src.Kind != batch.Kind || src.ImportGeneration != *batch.ExpectedImport {
		return result, ErrConflict
	}
	if e := s.storeImportAssets(refs, batch.Assets); e != nil {
		return result, e
	}
	if proof == nil && exists && src.ImportGeneration == generation {
		for _, ref := range src.Assets {
			if s.verifyAsset(ref) != nil {
				return ImportResult{}, errImportedAssetUnavailable
			}
		}
		result.Generation = generation
		result.Unchanged = len(batch.Files)
		s.markImportObserved(batch.SourceID)
		s.markImportWithheld(batch.SourceID, batch.Withheld)
		return result, nil
	}
	db := s.clone()
	if proof != nil && proof.Adoption != nil {
		// A local checkout's old import base is not a GitHub merge base. Clear
		// it so differing files become conflicts and absent files stay local.
		for key, article := range db.Articles {
			if article.SourceID == batch.SourceID {
				article.UpstreamHash = ""
				db.Articles[key] = article
			}
		}
		conn := proof.Adoption.Connection
		db.Connections[conn.ID] = conn
	}
	if proof != nil && proof.Visibility == "public" {
		for key, a := range db.Articles {
			if a.SourceID == batch.SourceID && (wasPrivate || !a.PublishedLocal) {
				a.Published = nil
				a.PublishedLocal = false
				db.Articles[key] = a
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	paths := make([]string, 0, len(batch.Files))
	for p := range batch.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		text := batch.Files[p]
		hash := digest([]byte(text))
		key := batch.SourceID + "/" + p
		a, found := db.Articles[key]
		if proof != nil && proof.Visibility == "public" && !a.PublishedLocal {
			published := Revision{Text: text, SHA: hash, Created: now}
			if previous := s.db.Articles[key].Published; previous != nil && previous.SHA == hash {
				published = *previous
			}
			a.Published = &published
			db.Articles[key] = a
		}
		if proof != nil && observedWrite(&db, batch.SourceID, p, hash, batch.Commit, refs, batch.Files, batch.Complete && batch.Withheld == 0) {
			a.UpstreamHash = hash
			a = supersedeImportConflicts(a)
			db.Articles[key] = a
			result.Unchanged++
			continue
		}
		if found && a.UpstreamHash == hash && !a.Deleted {
			db.Articles[key] = supersedeImportConflicts(a)
			result.Unchanged++
			continue
		}
		latest := ""
		if found {
			latest = a.Versions[len(a.Versions)-1].SHA
		}
		if found && !a.Deleted && latest != a.UpstreamHash && (latest != hash || (proof != nil && preparedWriteDiffers(db, batch.SourceID, p, hash))) {
			if hasImportConflict(a, hash, false) {
				result.Conflicts++
				continue
			}
			a = supersedeImportConflicts(a)
			a.Conflicts = append(append([]ImportConflict(nil), a.Conflicts...), ImportConflict{Incoming: text, IncomingSHA: hash, BaseSHA: a.UpstreamHash, Commit: batch.Commit, Created: now})
			db.Articles[key] = a
			result.Conflicts++
			continue
		}
		if found && latest == hash && !a.Deleted {
			a = supersedeImportConflicts(a)
			a.UpstreamHash = hash
			db.Articles[key] = a
			result.Unchanged++
			continue
		}
		a = supersedeImportConflicts(a)
		a.SourceID, a.Path, a.Deleted, a.UpstreamHash = batch.SourceID, p, false, hash
		a.Versions = append(append([]Revision(nil), a.Versions...), Revision{Number: len(a.Versions) + 1, Text: text, SHA: hash, Created: now})
		db.Articles[key] = a
		if found {
			result.Updated++
		} else {
			result.Created++
		}
	}
	if batch.Complete && batch.Withheld == 0 {
		for key, a := range db.Articles {
			if a.SourceID != batch.SourceID || a.Deleted || a.UpstreamHash == "" {
				continue
			}
			if _, present := batch.Files[a.Path]; present {
				continue
			}
			if proof != nil && proof.Visibility == "public" && !a.PublishedLocal {
				a.Published = nil
				db.Articles[key] = a
			}
			latest := a.Versions[len(a.Versions)-1].SHA
			if latest != a.UpstreamHash {
				if hasImportConflict(a, "", true) {
					result.Conflicts++
					continue
				}
				a = supersedeImportConflicts(a)
				a.Conflicts = append(append([]ImportConflict(nil), a.Conflicts...), ImportConflict{BaseSHA: a.UpstreamHash, Commit: batch.Commit, Deleted: true, Created: now})
				result.Conflicts++
			} else {
				a = supersedeImportConflicts(a)
				a.Deleted = true
				result.Deleted++
			}
			db.Articles[key] = a
		}
	}
	// Partial observations cannot prove that an old image was removed. A
	// locally changed article also keeps its missing referenced images while
	// an upstream deletion remains unresolved. Never carry private assets into
	// a newly public source; publication needs a fresh, verified snapshot.
	if exists && wasPrivate && src.Visibility == "private" {
		if !batch.Complete || batch.Withheld > 0 {
			for name, ref := range src.Assets {
				if _, present := refs[name]; !present {
					refs[name] = ref
				}
			}
		}
		for _, article := range db.Articles {
			if article.SourceID != batch.SourceID || article.Deleted || len(article.Versions) == 0 {
				continue
			}
			latest := article.Versions[len(article.Versions)-1]
			if article.UpstreamHash != "" && latest.SHA == article.UpstreamHash {
				continue
			}
			retained := retainedPendingAssets(latest.Text, article.Path, src.Assets)
			if len(retained) > 0 && hasRootReference(latest.Text) && db.Version < 12 {
				db.Version = 12
			}
			for name, ref := range retained {
				if _, present := refs[name]; !present {
					refs[name] = ref
				}
			}
		}
	}
	// Retained images may not be present in the new batch and therefore cannot
	// be repaired from it. Do not commit or report a successful observation if
	// any image that would remain referenced is missing or corrupt.
	for _, ref := range refs {
		if s.verifyAsset(ref) != nil {
			return ImportResult{}, errImportedAssetUnavailable
		}
	}
	src.Metadata = batch.Metadata
	src.Assets = refs
	src.ImportCommit = batch.Commit
	src.ImportDirty = batch.Dirty
	src.ImportPartial = !batch.Complete || batch.Withheld > 0
	if batch.Metadata != nil {
		src.Title = batch.Metadata.Title
	}
	src.ImportGeneration = generation
	result.Generation = src.ImportGeneration
	db.Sources[src.ID] = src
	// A prepared ref update may have succeeded even if its response was lost.
	// Only a complete verified pull can establish that the source is absent.
	if proof != nil && batch.Complete && batch.Withheld == 0 {
		for id, move := range db.Moves {
			if move.State != "deleting_source" || move.SourceConnectionID != proof.ConnectionID {
				continue
			}
			if _, present := batch.Files[move.SourcePath]; present {
				continue
			}
			original := db.Articles[move.SourceID+"/"+move.SourcePath]
			if !original.Deleted {
				continue
			}
			target, exists := db.Articles[move.DestinationID+"/"+move.DestinationPath]
			destination, dstOK := db.Sources[move.DestinationID]
			valid := exists && dstOK && !target.Deleted && len(target.Versions) == 1 && target.Versions[0].SHA == move.SHA && destination.ConnectionID == move.DestinationConnectionID && translationFingerprint(original.Translation) == move.TranslationFingerprint && translationFingerprint(target.Translation) == translationFingerprint(movedTranslation(original.Translation)) && assetsPresent(destination, move.Assets) && linksPresent(db, move.DestinationID, move.Links)
			if s.verifyMoveAssets(move.Assets) != nil {
				valid = false
			}
			for _, conflict := range target.Conflicts {
				if conflict.Resolution == "" {
					valid = false
				}
			}
			if move.DestinationConnectionID != "" {
				connection, ok := db.Connections[move.DestinationConnectionID]
				valid = valid && ok && connection.RepositoryID == move.DestinationRepositoryID && connection.Branch == move.DestinationBranch
			}
			if !valid {
				move.State = "destination_changed"
			} else {
				move.State = "complete"
			}
			db.Moves[id] = move
		}
	}
	reconcileMoves(&db)
	if proof != nil {
		conn := db.Connections[proof.ConnectionID]
		conn.LastPull = batch.Commit
		conn.TreeSHA = proof.TreeSHA
		db.Connections[conn.ID] = conn
	}
	if proof != nil && proof.Adoption != nil {
		if err := proof.Adoption.Context.Err(); err != nil {
			return ImportResult{}, err
		}
	}
	if e := s.commit(db); e != nil {
		return ImportResult{}, e
	}
	s.markImportObserved(batch.SourceID)
	s.markImportWithheld(batch.SourceID, batch.Withheld)
	return result, nil
}

// Git commands have bounded output even for adversarial repository indexes.
type boundedGitOutput struct{ bytes.Buffer }

func (b *boundedGitOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		//lint:ignore ST1005 "Git" is a proper noun; the message text is observable and must not change
		return 0, errors.New("Git output exceeds import limit")
	}
	return b.Buffer.Write(p)
}

// Caller holds the store mutex. Counts are only known after a full import
// attempt, not after a head-only check, and are not retained across restart.
func (s *Store) markImportWithheld(sourceID string, count int) {
	if s.withheld == nil {
		s.withheld = map[string]int{}
	}
	s.withheld[sourceID] = count
}

// Caller holds the store mutex; failed imports must never advance this clock.
func (s *Store) markImportObserved(sourceID string) {
	if s.observed == nil {
		s.observed = map[string]time.Time{}
	}
	s.observed[sourceID] = time.Now().UTC()
}
