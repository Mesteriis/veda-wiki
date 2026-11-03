package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxStoreBytes = 64 << 20
const legacyArticleBytes = 256000
const maxArticleBytes = 1 << 20

// One server process owns the data directory. All writes are serialized and
// persisted before becoming visible. Whole-state transactions keep article
// history and the future sync outbox in one durability boundary.
type Store struct {
	mu         sync.RWMutex
	dir        string
	db         Database
	observed   map[string]time.Time
	withheld   map[string]int
	sqlite     bool
	invalid    bool
	passwordMu sync.Mutex
}

func privateDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("data directory must be private (0700)")
	}
	return nil
}
func openStore(dir string) (*Store, error) {
	if err := privateDataDir(dir); err != nil {
		return nil, err
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
	if s.db.Version == 14 {
		if err = s.loadSQLite(b); err != nil {
			return nil, err
		}
	}
	return s.validate()
}

func (s *Store) validate() (*Store, error) {
	if (s.db.Version < 1 || s.db.Version > 20) || s.db.Version == 14 || s.db.Sources == nil || s.db.Articles == nil {
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
	if s.db.SourceMirrors == nil {
		s.db.SourceMirrors = map[string]SourceMirror{}
	}
	for id, mirror := range s.db.SourceMirrors {
		if s.db.Version < 19 || id != mirror.DestinationID || mirror.SourceID == id || !identifier.MatchString(mirror.SourceID) || !identifier.MatchString(id) || mirror.Revision < 1 {
			return nil, errors.New("invalid source mirror")
		}
		if _, ok := s.db.Sources[mirror.SourceID]; !ok {
			return nil, errors.New("mirror source unavailable")
		}
		if _, ok := s.db.Sources[id]; !ok {
			return nil, errors.New("mirror destination unavailable")
		}
		validState := mirror.State == "pending" || mirror.State == "ready" || mirror.State == "blocked" || mirror.State == "syncing" || mirror.State == "paused" || mirror.State == "error"
		validError := mirror.LastError == "" || mirror.LastError == "source_unavailable" || mirror.LastError == "interrupted" || mirror.LastError == "storage_unavailable"
		if !validState || !validError || mirror.Blocked < 0 || (mirror.Enabled && mirror.State == "paused") || (!mirror.Enabled && mirror.State != "paused") {
			return nil, errors.New("invalid mirror status")
		}
		if len(mirror.BlockedPaths) > 50 || len(mirror.BlockedPaths) > mirror.Blocked {
			return nil, errors.New("invalid mirror blocked paths")
		}
		for _, p := range mirror.BlockedPaths {
			if !articlePath(p, s.db.Sources[mirror.SourceID].Kind) {
				return nil, errors.New("invalid mirror blocked path")
			}
		}
		if mirror.LastRun != "" {
			if _, err := time.Parse(time.RFC3339Nano, mirror.LastRun); err != nil {
				return nil, errors.New("invalid mirror observation time")
			}
		}
	}
	if s.db.Version == 1 && len(s.db.Moves) > 0 {
		return nil, errors.New("move records require store version 2")
	}
	if s.db.Version < 4 && len(s.db.AccessKeys) > 0 {
		return nil, errors.New("access keys require store version 4")
	}
	for id, key := range s.db.AccessKeys {
		if err := validateAccessKey(id, key, s.db.Sources); err != nil {
			return nil, err
		}
	}
	if err := validatePeople(s.db); err != nil {
		return nil, err
	}
	for id, a := range s.db.Accounts {
		if !identifier.MatchString(id) || a.ID != id || (a.Kind != "token" && a.Kind != "app") || !github.ValidName(a.Login) || len(a.SecretRef) != 32 {
			return nil, errors.New("invalid stored GitHub account")
		}
		if _, e := hex.DecodeString(a.SecretRef); e != nil {
			return nil, errors.New("invalid credential reference")
		}
	}
	for id, c := range s.db.Connections {
		repo, e := github.RepositoryName(c.Repository)
		if e != nil || repo != c.Repository || !identifier.MatchString(id) || id != c.ID || c.RepositoryID < 1 || !github.ValidBranch(c.Branch) || c.ConfigRevision < 1 {
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
		if b := article.ReconcileBase; b != nil {
			origin, found := s.db.Articles[b.SourceID+"/"+b.Path]
			if s.db.Version < 18 || b.SourceID == article.SourceID || !found || b.Revision < 1 || b.Revision > len(origin.Versions) || origin.Versions[b.Revision-1].SHA != b.SHA {
				return nil, errors.New("invalid reconciliation baseline")
			}
		}
		for i, revision := range article.Versions {
			sum := sha256.Sum256([]byte(revision.Text))
			if revision.Number != i+1 || len(revision.Text) > maxArticleBytes || (len(revision.Text) > legacyArticleBytes && s.db.Version < 13) || !utf8.ValidString(revision.Text) || revision.SHA != hex.EncodeToString(sum[:]) {
				return nil, errors.New("invalid stored revision")
			}
		}
		if article.Translation != nil {
			tr := article.Translation
			if tr.Origin != "" && (tr.Origin != "local_model" || s.db.Version < 11) {
				return nil, errors.New("invalid translation origin")
			}
			if src.Kind != "program" || tr.Language != "ru" || len(tr.Text) == 0 || len(tr.Text) > maxArticleBytes || (len(tr.Text) > legacyArticleBytes && s.db.Version < 13) || !utf8.ValidString(tr.Text) || digest([]byte(tr.Text)) != tr.SHA || len(tr.OriginalSHA) != 64 {
				return nil, errors.New("invalid stored translation")
			}
			if _, e := hex.DecodeString(tr.OriginalSHA); e != nil {
				return nil, errors.New("invalid translation provenance")
			}
		}
		if article.CopiedFrom != nil {
			origin := article.CopiedFrom
			if origin.DirectoryIndex && (s.db.Version < 17 || path.Base(origin.Path) != "index.md" || path.Base(article.Path) != "README.md") {
				return nil, errors.New("invalid directory copy provenance")
			}
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
			if len(p.Text) > maxArticleBytes || (len(p.Text) > legacyArticleBytes && s.db.Version < 13) || !utf8.ValidString(p.Text) || digest([]byte(p.Text)) != p.SHA {
				return nil, errors.New("invalid publication snapshot")
			}
		}
		for _, c := range article.Conflicts {
			if !commitID.MatchString(c.Commit) || (c.Deleted && (c.Incoming != "" || c.IncomingSHA != "")) || (!c.Deleted && (digest([]byte(c.Incoming)) != c.IncomingSHA || len(c.Incoming) > maxArticleBytes || (len(c.Incoming) > legacyArticleBytes && s.db.Version < 13))) {
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
		if m.TranslationFingerprint != "" {
			if s.db.Version < 5 || len(m.TranslationFingerprint) != 64 {
				return nil, errors.New("invalid move translation fingerprint")
			}
			if _, e := hex.DecodeString(m.TranslationFingerprint); e != nil {
				return nil, errors.New("invalid move translation fingerprint")
			}
		}
		if len(m.Assets) > 2000 {
			return nil, errors.New("too many move assets")
		}
		if len(m.Assets) > 0 && s.db.Version < 6 {
			return nil, errors.New("move assets require store version 6")
		}
		for p, ref := range m.Assets {
			if !safeRelative(p) || (s.db.Sources[m.DestinationID].Kind == "program" && !strings.HasPrefix(p, "docs/")) {
				return nil, errors.New("invalid move asset path")
			}
			if _, e := hexKey(ref.SHA); e != nil {
				return nil, errors.New("invalid move asset digest")
			}
			switch ref.MIME {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return nil, errors.New("invalid move asset type")
			}
		}
		if len(m.Links) > 2000 || (len(m.Links) > 0 && s.db.Version < 8) {
			return nil, errors.New("invalid move links")
		}
		for p, sha := range m.Links {
			if p == m.DestinationPath || !articlePath(p, s.db.Sources[m.DestinationID].Kind) {
				return nil, errors.New("invalid move link path")
			}
			if len(sha) != 64 {
				return nil, errors.New("invalid move link digest")
			}
			if _, e := hex.DecodeString(sha); e != nil {
				return nil, errors.New("invalid move link digest")
			}
		}
		if _, e := hex.DecodeString(id); e != nil {
			return nil, errors.New("invalid stored move")
		}
		if m.SourceConnectionID == "" {
			if m.SourceRepositoryID != 0 || m.SourceBranch != "" {
				return nil, errors.New("invalid move source identity")
			}
		} else if s.db.Version < 3 || !identifier.MatchString(m.SourceConnectionID) || m.SourceRepositoryID < 1 || !github.ValidBranch(m.SourceBranch) {
			return nil, errors.New("invalid move source identity")
		}
		if m.DestinationConnectionID == "" {
			if m.DestinationRepositoryID != 0 || m.DestinationBranch != "" {
				return nil, errors.New("invalid move destination identity")
			}
		} else if !identifier.MatchString(m.DestinationConnectionID) || m.DestinationRepositoryID < 1 || !github.ValidBranch(m.DestinationBranch) {
			return nil, errors.New("invalid move destination identity")
		}
		switch m.State {
		case "waiting_destination", "complete", "source_changed", "destination_changed", "cancelled":
		case "waiting_source_delete":
			if m.SourceConnectionID == "" || s.db.Version < 3 {
				return nil, errors.New("invalid move state")
			}
		case "deleting_source":
			if m.SourceConnectionID == "" || s.db.Version < 3 || !commitID.MatchString(m.PreparedDeletion) {
				return nil, errors.New("invalid prepared deletion")
			}
		default:
			return nil, errors.New("invalid move state")
		}
	}
	for id, j := range s.db.Outbox {
		if len(j.Bundle) > 2000 || (len(j.Bundle) > 0 && s.db.Version < 9) {
			return nil, errors.New("invalid queued bundle")
		}
		for p, sha := range j.Bundle {
			a, exists := s.db.Articles[j.SourceID+"/"+p]
			if p == j.Path || !articlePath(p, s.db.Sources[j.SourceID].Kind) || len(sha) != 64 || !exists || len(a.Versions) == 0 || a.Versions[0].SHA != sha {
				return nil, errors.New("invalid queued bundle article")
			}
			if _, e := hex.DecodeString(sha); e != nil {
				return nil, errors.New("invalid queued bundle digest")
			}
		}
		if len(j.Links) > 2000 || (len(j.Links) > 0 && s.db.Version < 8) {
			return nil, errors.New("invalid queued links")
		}
		for p, sha := range j.Links {
			if p == j.Path || !articlePath(p, s.db.Sources[j.SourceID].Kind) {
				return nil, errors.New("invalid queued link path")
			}
			if len(sha) != 64 {
				return nil, errors.New("invalid queued link digest")
			}
			if _, e := hex.DecodeString(sha); e != nil {
				return nil, errors.New("invalid queued link digest")
			}
		}
		if len(j.Assets) > 2000 || (len(j.Assets) > 0 && s.db.Version < 7) {
			return nil, errors.New("invalid queued assets")
		}
		for p, ref := range j.Assets {
			if !remoteAssetPath(p, s.db.Sources[j.SourceID].Kind) {
				return nil, errors.New("invalid queued asset path")
			}
			if _, e := hexKey(ref.SHA); e != nil {
				return nil, errors.New("invalid queued asset digest")
			}
			switch ref.MIME {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return nil, errors.New("invalid queued asset type")
			}
		}
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
	return s.commitWithArticle(db, "")
}

// articleKey is used only by put: that operation changes one article and may
// update moves and outbox jobs. Other mutations must use a full transaction.
func (s *Store) commitWithArticle(db Database, articleKey string) error {
	var targeted func(Database) error
	if articleKey != "" {
		targeted = func(db Database) error { return s.commitSQLiteArticle(db, articleKey) }
	}
	return s.commitTargeted(db, targeted)
}

// commitConnections is for mutations that change only GitHub connection
// records, such as sync status. It must not be used for any other category.
func (s *Store) commitConnections(db Database) error {
	return s.commitTargeted(db, s.commitSQLiteConnections)
}

// A nil targeted writer persists the whole SQLite state.
func (s *Store) commitTargeted(db Database, targeted func(Database) error) error {
	if s.invalid {
		return errors.New("store unavailable")
	}
	// Old binaries reject the upgraded store before reading a large revision.
	if db.Version < 13 {
		for _, article := range db.Articles {
			large := article.Published != nil && len(article.Published.Text) > legacyArticleBytes
			large = large || article.Translation != nil && len(article.Translation.Text) > legacyArticleBytes
			for _, revision := range article.Versions {
				large = large || len(revision.Text) > legacyArticleBytes
			}
			for _, conflict := range article.Conflicts {
				large = large || len(conflict.Incoming) > legacyArticleBytes
			}
			if large {
				db.Version = 13
				break
			}
		}
	}
	db.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	if s.sqlite {
		var err error
		if targeted != nil {
			err = targeted(db)
		} else {
			err = s.commitSQLite(db)
		}
		if err != nil {
			// Commit errors can be ambiguous. Reload the authoritative database;
			// if recovery fails, fail closed until the process is restarted.
			recovered := &Store{dir: s.dir}
			if recovered.loadSQLite([]byte(`{"version":14,"backend":"sqlite"}`)) != nil {
				s.invalid = true
			} else if _, validationErr := recovered.validate(); validationErr != nil {
				s.invalid = true
			} else {
				s.db = recovered.db
			}
			return err
		}
		s.db = db
		return nil
	}
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
	db.SourceMirrors = make(map[string]SourceMirror, len(s.db.SourceMirrors))
	for k, v := range s.db.SourceMirrors {
		v.BlockedPaths = append([]string(nil), v.BlockedPaths...)
		db.SourceMirrors[k] = v
	}
	db.AccessKeys = make(map[string]AccessKey, len(s.db.AccessKeys))
	for k, v := range s.db.AccessKeys {
		db.AccessKeys[k] = v
	}
	db.Users = make(map[string]WikiUser, len(s.db.Users))
	for k, v := range s.db.Users {
		db.Users[k] = v
	}
	db.Teams = make(map[string]WikiTeam, len(s.db.Teams))
	for k, v := range s.db.Teams {
		db.Teams[k] = v
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
func (s *Store) put(sourceID, p, text string, expected int) (Revision, error) {
	if len(text) > maxArticleBytes || !utf8.ValidString(text) || expected < 0 {
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
	if moveDeletionLocks(s.db, sourceID, p) {
		return Revision{}, ErrConflict
	}
	article := s.db.Articles[key]
	if len(article.Versions) != expected {
		return Revision{}, ErrConflict
	}
	for _, conflict := range article.Conflicts {
		if conflict.Resolution == "" {
			return Revision{}, ErrConflict
		}
	}
	publicNow := sourcePublic(s.db, src, time.Now())
	if publicNow && secretContent.MatchString(text) {
		return Revision{}, ErrInvalid
	}
	sum := sha256.Sum256([]byte(text))
	rev := Revision{Number: expected + 1, Text: text, SHA: hex.EncodeToString(sum[:]), Created: time.Now().UTC().Format(time.RFC3339Nano)}
	versions := append([]Revision(nil), article.Versions...)
	versions = append(versions, rev)
	db := s.clone()
	article.SourceID, article.Path, article.Versions, article.Deleted = sourceID, p, versions, false
	if publicNow {
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
		enqueueWrite(&db, article, rev, time.Now())
	}
	// Reconciliation can delete another article when a pending move completes.
	// Such writes must persist the whole state, not only this article.
	articleKey := key
	for _, move := range s.db.Moves {
		switch move.State {
		case "complete", "cancelled", "source_changed", "destination_changed":
			continue
		default:
			articleKey = ""
		}
		break
	}
	if err := s.commitWithArticle(db, articleKey); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

func (s *Store) sources(public bool) []SourceView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SourceView{}
	now := time.Now()
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
		if !public || sourcePublicReadable(s.db, src, now) {
			if public {
				src.ConnectionID = ""
			}
			view := SourceView{Source: src}
			if !public {
				view.SyncIssue = sourceSyncIssue(s.db, src, now)
				if observed, ok := s.observed[src.ID]; ok {
					view.LastImportObservedAt = observed.UTC().Format(time.RFC3339Nano)
				}
				if count, ok := s.withheld[src.ID]; ok {
					view.LastImportWithheld = &count
				}
				view.UnresolvedConflicts = conflicts[src.ID]
			}
			out = append(out, view)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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
		if public && !sourcePublicReadable(s.db, src, time.Now()) {
			continue
		}
		rev := a.Versions[len(a.Versions)-1]
		if public && src.ConnectionID != "" {
			if a.Published == nil {
				continue
			}
			rev = *a.Published
		}
		original := rev.Text
		rev, _ = displayedRevision(a, rev, public)
		if public && rev.Created > snap.Generated {
			snap.Generated = rev.Created
		}
		snap.Pages = append(snap.Pages, Page{Path: key, Title: title(rev.Text, a.Path), Environment: src.ID, SHA: rev.SHA, Updated: rev.Created, SearchOriginal: original, SearchDisplayed: rev.Text, SearchSourceTitle: src.Title})
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
	if public && !sourcePublicReadable(s.db, src, time.Now()) {
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
	origin, translationState := "", ""
	unresolved := false
	if !public {
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				unresolved = true
				break
			}
		}
	}
	if src.Kind == "program" {
		translationState = "unavailable"
		if translated {
			translationState = "current"
			origin = a.Translation.Origin
		} else if !public && a.Translation != nil && a.Translation.OriginalSHA != originalSHA {
			// Public readers must not learn whether an unpublished translation exists.
			translationState = "stale"
		}
	}
	blocked := unresolved && !public && conflictResolutionBlocked(s.db, a.SourceID, a.Path)
	return Page{Path: key, Title: title(r.Text, a.Path), Environment: src.ID, SHA: r.SHA, Updated: r.Created, Text: r.Text, OriginalSHA: originalSHA, Translated: translated, TranslationOrigin: origin, TranslationState: translationState, ProgramDocumentation: src.Kind == "program", UnresolvedConflict: unresolved, ConflictResolutionBlocked: blocked}, nil
}
func (s *Store) article(sourceID, p string, public bool) (Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.db.Articles[sourceID+"/"+p]
	if !ok || a.Deleted {
		return Article{}, ErrNotFound
	}
	src := s.db.Sources[a.SourceID]
	if public && !sourcePublicReadable(s.db, src, time.Now()) {
		return Article{}, ErrNotFound
	}
	a.Versions = append([]Revision(nil), a.Versions...)
	if public && src.ConnectionID != "" {
		if a.Published == nil {
			return Article{}, ErrNotFound
		}
		a.Versions = []Revision{*a.Published}
	}
	if public {
		a.Translation = nil
		a.CopiedFrom = nil
		a.ReconcileBase = nil
		a.Conflicts = nil
		a.UpstreamHash = ""
		a.Versions = a.Versions[len(a.Versions)-1:]
		a.Versions[0].Number = 0
		// The visible revision is already in Versions; do not expose internal
		// publication bookkeeping or a different stored publication snapshot.
		a.Published = nil
		a.PublishedLocal = false
	}
	return a, nil
}
