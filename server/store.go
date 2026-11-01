package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxStoreBytes = 64 << 20

var ErrInvalid = errors.New("invalid input")
var ErrConflict = errors.New("revision conflict")
var ErrNotFound = errors.New("not found")
var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Source struct {
	ID               string             `json:"id"`
	Title            string             `json:"title"`
	Kind             string             `json:"kind"`
	Visibility       string             `json:"visibility"`
	Metadata         *WikiMetadata      `json:"metadata,omitempty"`
	Assets           map[string]BlobRef `json:"assets,omitempty"`
	ImportGeneration string             `json:"import_generation,omitempty"`
	ImportCommit     string             `json:"import_commit,omitempty"`
	ImportDirty      bool               `json:"import_dirty,omitempty"`
	ImportPartial    bool               `json:"import_partial,omitempty"`
	ConnectionID     string             `json:"connection_id,omitempty"`
}

// SourceView adds ephemeral, owner-visible sync evidence without persisting a
// timestamp on every unchanged snapshot import.
type SourceView struct {
	Source
	LastImportObservedAt string `json:"last_import_observed_at,omitempty"`
	UnresolvedConflicts  int    `json:"unresolved_conflicts,omitempty"`
}
type Revision struct {
	Number  int    `json:"revision"`
	Text    string `json:"text"`
	SHA     string `json:"sha256"`
	Created string `json:"created_at"`
}
type Article struct {
	Translation    *Translation     `json:"translation,omitempty"`
	CopiedFrom     *CopyOrigin      `json:"copied_from,omitempty"`
	SourceID       string           `json:"source_id"`
	Path           string           `json:"path"`
	Versions       []Revision       `json:"versions"`
	UpstreamHash   string           `json:"upstream_sha256,omitempty"`
	Deleted        bool             `json:"deleted,omitempty"`
	Conflicts      []ImportConflict `json:"conflicts,omitempty"`
	Published      *Revision        `json:"published,omitempty"`
	PublishedLocal bool             `json:"published_local,omitempty"`
}
type Database struct {
	Version     int                         `json:"version"`
	Updated     string                      `json:"updated_at"`
	Sources     map[string]Source           `json:"sources"`
	Articles    map[string]Article          `json:"articles"`
	Accounts    map[string]GitHubAccount    `json:"github_accounts,omitempty"`
	Connections map[string]GitHubConnection `json:"github_connections,omitempty"`
	Outbox      map[string]SyncJob          `json:"outbox,omitempty"`
	Moves       map[string]Move             `json:"moves,omitempty"`
}

// One server process owns the data directory. All writes are serialized and
// persisted before becoming visible. Whole-state transactions keep article
// history and the future sync outbox in one durability boundary.
type Store struct {
	mu       sync.RWMutex
	dir      string
	db       Database
	observed map[string]time.Time
}

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("data directory must be private (0700)")
	}
	s := &Store{dir: dir, db: Database{Version: 1, Sources: map[string]Source{}, Articles: map[string]Article{}}}
	if info, e := os.Lstat(path.Join(dir, "state.json")); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("store must be a private regular file")
	}
	f, err := os.OpenFile(path.Join(dir, "state.json"), os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxStoreBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStoreBytes {
		return nil, errors.New("store too large")
	}
	if err = json.Unmarshal(b, &s.db); err != nil {
		return nil, err
	}
	if (s.db.Version != 1 && s.db.Version != 2 && s.db.Version != 3) || s.db.Sources == nil || s.db.Articles == nil {
		return nil, errors.New("unsupported store")
	}
	if s.db.Accounts == nil {
		s.db.Accounts = map[string]GitHubAccount{}
	}
	if s.db.Connections == nil {
		s.db.Connections = map[string]GitHubConnection{}
	}
	if s.db.Outbox == nil {
		s.db.Outbox = map[string]SyncJob{}
	}
	if s.db.Moves == nil {
		s.db.Moves = map[string]Move{}
	}
	if s.db.Version == 1 && len(s.db.Moves) > 0 {
		return nil, errors.New("move records require store version 2")
	}
	for id, a := range s.db.Accounts {
		if !identifier.MatchString(id) || a.ID != id || (a.Kind != "token" && a.Kind != "app") || !githubPart.MatchString(a.Login) || len(a.SecretRef) != 32 {
			return nil, errors.New("invalid stored GitHub account")
		}
		if _, e := hex.DecodeString(a.SecretRef); e != nil {
			return nil, errors.New("invalid credential reference")
		}
	}
	for id, c := range s.db.Connections {
		repo, e := repositoryName(c.Repository)
		if e != nil || repo != c.Repository || !identifier.MatchString(id) || id != c.ID || c.RepositoryID < 1 || !validBranch(c.Branch) || c.ConfigRevision < 1 {
			return nil, errors.New("invalid stored GitHub connection")
		}
		if c.AccountID != "" {
			if _, ok := s.db.Accounts[c.AccountID]; !ok {
				return nil, errors.New("connection account unavailable")
			}
		}
	}
	for id, src := range s.db.Sources {
		if id != src.ID || !identifier.MatchString(id) || (src.Kind != "wiki" && src.Kind != "program") || (src.Visibility != "public" && src.Visibility != "private") {
			return nil, errors.New("invalid stored source")
		}
		if src.Metadata != nil {
			if e := validateMetadata(src.Metadata); e != nil {
				return nil, errors.New("invalid stored metadata")
			}
		}
		for name, ref := range src.Assets {
			if !safeRelative(name) {
				return nil, errors.New("invalid stored asset")
			}
			if _, e := hexKey(ref.SHA); e != nil {
				return nil, errors.New("invalid stored asset digest")
			}
			switch ref.MIME {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return nil, errors.New("invalid stored asset type")
			}
		}
	}
	for key, article := range s.db.Articles {
		src, exists := s.db.Sources[article.SourceID]
		if !exists || key != article.SourceID+"/"+article.Path || !articlePath(article.Path, src.Kind) || len(article.Versions) == 0 {
			return nil, errors.New("invalid stored article")
		}
		for i, revision := range article.Versions {
			sum := sha256.Sum256([]byte(revision.Text))
			if revision.Number != i+1 || len(revision.Text) > 256000 || !utf8.ValidString(revision.Text) || revision.SHA != hex.EncodeToString(sum[:]) {
				return nil, errors.New("invalid stored revision")
			}
		}
		if article.Translation != nil {
			tr := article.Translation
			if src.Kind != "program" || tr.Language != "ru" || len(tr.Text) == 0 || len(tr.Text) > 256000 || !utf8.ValidString(tr.Text) || digest([]byte(tr.Text)) != tr.SHA || len(tr.OriginalSHA) != 64 {
				return nil, errors.New("invalid stored translation")
			}
			if _, e := hex.DecodeString(tr.OriginalSHA); e != nil {
				return nil, errors.New("invalid translation provenance")
			}
		}
		if article.CopiedFrom != nil {
			origin := article.CopiedFrom
			src, exists := s.db.Sources[origin.SourceID]
			if !exists || !articlePath(origin.Path, src.Kind) || origin.Revision < 1 || len(origin.SHA) != 64 {
				return nil, errors.New("invalid copy provenance")
			}
			if _, e := hex.DecodeString(origin.SHA); e != nil {
				return nil, errors.New("invalid copy provenance")
			}
		}
		if article.Published != nil {
			p := article.Published
			if len(p.Text) > 256000 || !utf8.ValidString(p.Text) || digest([]byte(p.Text)) != p.SHA {
				return nil, errors.New("invalid publication snapshot")
			}
		}
		for _, c := range article.Conflicts {
			if !commitID.MatchString(c.Commit) || (c.Deleted && (c.Incoming != "" || c.IncomingSHA != "")) || (!c.Deleted && (digest([]byte(c.Incoming)) != c.IncomingSHA || len(c.Incoming) > 256000)) {
				return nil, errors.New("invalid stored conflict")
			}
		}
	}
	for id, m := range s.db.Moves {
		source, srcOK := s.db.Articles[m.SourceID+"/"+m.SourcePath]
		target, dstOK := s.db.Articles[m.DestinationID+"/"+m.DestinationPath]
		if len(id) != 32 || id != m.ID || m.SourceID == m.DestinationID || !srcOK || !dstOK || m.SourceRevision < 1 || m.SourceRevision > len(source.Versions) || len(target.Versions) < 1 || source.Versions[m.SourceRevision-1].SHA != m.SHA || target.Versions[0].SHA != m.SHA {
			return nil, errors.New("invalid stored move")
		}
		if _, e := hex.DecodeString(id); e != nil {
			return nil, errors.New("invalid stored move")
		}
		if m.SourceConnectionID == "" {
			if m.SourceRepositoryID != 0 || m.SourceBranch != "" {
				return nil, errors.New("invalid move source identity")
			}
		} else if s.db.Version < 3 || !identifier.MatchString(m.SourceConnectionID) || m.SourceRepositoryID < 1 || !validBranch(m.SourceBranch) {
			return nil, errors.New("invalid move source identity")
		}
		if m.DestinationConnectionID == "" {
			if m.DestinationRepositoryID != 0 || m.DestinationBranch != "" {
				return nil, errors.New("invalid move destination identity")
			}
		} else if !identifier.MatchString(m.DestinationConnectionID) || m.DestinationRepositoryID < 1 || !validBranch(m.DestinationBranch) {
			return nil, errors.New("invalid move destination identity")
		}
		switch m.State {
		case "waiting_destination", "complete", "source_changed", "destination_changed", "cancelled":
		case "waiting_source_delete":
			if m.SourceConnectionID == "" || s.db.Version < 3 {
				return nil, errors.New("invalid move state")
			}
		default:
			return nil, errors.New("invalid move state")
		}
	}
	for id, j := range s.db.Outbox {
		if j.Visibility == "" {
			j.Visibility = "private"
			s.db.Outbox[id] = j
		}
		if j.Visibility != "private" && j.Visibility != "public" {
			return nil, errors.New("invalid queued publication scope")
		}
		a, ok := s.db.Articles[j.SourceID+"/"+j.Path]
		if !ok || j.ID != id || j.Revision < 1 || j.Revision > len(a.Versions) || a.Versions[j.Revision-1].SHA != j.SHA {
			return nil, errors.New("invalid outbox revision")
		}
		switch j.State {
		case "pending", "prepared", "retry", "blocked", "conflict", "complete", "superseded":
		default:
			return nil, errors.New("invalid outbox state")
		}
	}
	return s, nil
}
func (s *Store) commit(db Database) error {
	db.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(db)
	if err != nil {
		return err
	}
	if len(b) > maxStoreBytes {
		return errors.New("store capacity reached")
	}
	f, err := os.CreateTemp(s.dir, ".transaction-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path.Join(s.dir, "state.json")); err != nil {
		return err
	}
	// Once rename succeeds, readers must use the same state as recovery even if
	// directory fsync fails. The error is returned; optimistic retries are safe.
	s.db = db
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) clone() Database {
	db := s.db
	db.Sources = make(map[string]Source, len(s.db.Sources))
	db.Articles = make(map[string]Article, len(s.db.Articles))
	db.Accounts = make(map[string]GitHubAccount, len(s.db.Accounts))
	for k, v := range s.db.Accounts {
		db.Accounts[k] = v
	}
	db.Connections = make(map[string]GitHubConnection, len(s.db.Connections))
	for k, v := range s.db.Connections {
		db.Connections[k] = v
	}
	db.Outbox = make(map[string]SyncJob, len(s.db.Outbox))
	for k, v := range s.db.Outbox {
		db.Outbox[k] = v
	}
	db.Moves = make(map[string]Move, len(s.db.Moves))
	for k, v := range s.db.Moves {
		db.Moves[k] = v
	}
	for k, v := range s.db.Sources {
		db.Sources[k] = v
	}
	for k, v := range s.db.Articles {
		db.Articles[k] = v
	}
	return db
}
func (s *Store) addSource(src Source) error {
	if !identifier.MatchString(src.ID) || strings.TrimSpace(src.Title) == "" || len(src.Title) > 200 || (src.Kind != "wiki" && src.Kind != "program") || (src.Visibility != "private" && src.Visibility != "public") {
		return ErrInvalid
	}
	if src.ConnectionID != "" || src.Metadata != nil || len(src.Assets) != 0 || src.ImportGeneration != "" || src.ImportCommit != "" || src.ImportDirty || src.ImportPartial {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.db.Sources[src.ID]; ok {
		return ErrConflict
	}
	db := s.clone()
	db.Sources[src.ID] = src
	return s.commit(db)
}
func articlePath(p string, kind string) bool {
	if !utf8.ValidString(p) || !safePath(p) || len(p) > 512 || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return kind != "program" || strings.HasPrefix(p, "docs/")
}
func (s *Store) put(sourceID, p, text string, expected int) (Revision, error) {
	if len(text) > 256000 || !utf8.ValidString(text) || expected < 0 {
		return Revision{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.db.Sources[sourceID]
	if !ok {
		return Revision{}, ErrNotFound
	}
	if !articlePath(p, src.Kind) {
		return Revision{}, ErrInvalid
	}
	key := sourceID + "/" + p
	article := s.db.Articles[key]
	if len(article.Versions) != expected {
		return Revision{}, ErrConflict
	}
	sum := sha256.Sum256([]byte(text))
	rev := Revision{Number: expected + 1, Text: text, SHA: hex.EncodeToString(sum[:]), Created: time.Now().UTC().Format(time.RFC3339Nano)}
	versions := append([]Revision(nil), article.Versions...)
	versions = append(versions, rev)
	db := s.clone()
	article.SourceID, article.Path, article.Versions, article.Deleted = sourceID, p, versions, false
	if sourcePublic(s.db, src, time.Now()) {
		published := rev
		published.Number = 0
		article.Published = &published
		article.PublishedLocal = true
	} else {
		article.PublishedLocal = false
	}
	db.Articles[key] = article
	reconcileMoves(&db)
	if src.ConnectionID != "" {
		enqueueWrite(&db, article, rev)
	}
	if err := s.commit(db); err != nil {
		return Revision{}, err
	}
	return rev, nil
}
func (s *Store) sources(public bool) []SourceView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SourceView{}
	conflicts := map[string]int{}
	if !public {
		for _, a := range s.db.Articles {
			for _, c := range a.Conflicts {
				if c.Resolution == "" {
					conflicts[a.SourceID]++
				}
			}
		}
	}
	for _, src := range s.db.Sources {
		if !public || sourcePublic(s.db, src, time.Now()) {
			if public {
				src.ConnectionID = ""
			}
			view := SourceView{Source: src}
			if !public {
				if observed, ok := s.observed[src.ID]; ok {
					view.LastImportObservedAt = observed.UTC().Format(time.RFC3339Nano)
				}
				view.UnresolvedConflicts = conflicts[src.ID]
			}
			out = append(out, view)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func title(text, p string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return path.Base(p)
}
func (s *Store) snapshot(public bool) Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{Pages: []Page{}}
	if !public {
		snap.Generated = s.db.Updated
	}
	for key, a := range s.db.Articles {
		if a.Deleted {
			continue
		}
		src := s.db.Sources[a.SourceID]
		if public && !sourcePublic(s.db, src, time.Now()) {
			continue
		}
		rev := a.Versions[len(a.Versions)-1]
		if public && src.ConnectionID != "" {
			if a.Published == nil {
				continue
			}
			rev = *a.Published
		}
		rev, _ = displayedRevision(a, rev, public)
		if public && rev.Created > snap.Generated {
			snap.Generated = rev.Created
		}
		snap.Pages = append(snap.Pages, Page{Path: key, Title: title(rev.Text, a.Path), Environment: src.ID, SHA: rev.SHA})
	}
	sort.Slice(snap.Pages, func(i, j int) bool { return snap.Pages[i].Path < snap.Pages[j].Path })
	return snap
}
func (s *Store) page(key string, public bool) (Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.db.Articles[key]
	if !ok || a.Deleted {
		return Page{}, ErrNotFound
	}
	src := s.db.Sources[a.SourceID]
	if public && !sourcePublic(s.db, src, time.Now()) {
		return Page{}, ErrNotFound
	}
	r := a.Versions[len(a.Versions)-1]
	if public && src.ConnectionID != "" {
		if a.Published == nil {
			return Page{}, ErrNotFound
		}
		r = *a.Published
	}
	originalSHA := r.SHA
	r, translated := displayedRevision(a, r, public)
	return Page{Path: key, Title: title(r.Text, a.Path), Environment: src.ID, SHA: r.SHA, Text: r.Text, OriginalSHA: originalSHA, Translated: translated}, nil
}
func (s *Store) article(sourceID, p string, public bool) (Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.db.Articles[sourceID+"/"+p]
	if !ok || a.Deleted {
		return Article{}, ErrNotFound
	}
	src := s.db.Sources[a.SourceID]
	if public && !sourcePublic(s.db, src, time.Now()) {
		return Article{}, ErrNotFound
	}
	a.Versions = append([]Revision(nil), a.Versions...)
	if public && src.ConnectionID != "" {
		if a.Published == nil {
			return Article{}, ErrNotFound
		}
		a.Versions = []Revision{*a.Published}
		a.Conflicts = nil
		a.UpstreamHash = ""
		a.PublishedLocal = false
	}
	if public {
		a.Translation = nil
		a.CopiedFrom = nil
		a.Versions = a.Versions[len(a.Versions)-1:]
		a.Versions[0].Number = 0
		if a.Published != nil {
			published := *a.Published
			published.Number = 0
			a.Published = &published
		}
		a.PublishedLocal = false
	}
	return a, nil
}
