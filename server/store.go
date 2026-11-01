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
	ID         string `json:"id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Visibility string `json:"visibility"`
}
type Revision struct {
	Number  int    `json:"revision"`
	Text    string `json:"text"`
	SHA     string `json:"sha256"`
	Created string `json:"created_at"`
}
type Article struct {
	SourceID string     `json:"source_id"`
	Path     string     `json:"path"`
	Versions []Revision `json:"versions"`
}
type Database struct {
	Version  int                `json:"version"`
	Updated  string             `json:"updated_at"`
	Sources  map[string]Source  `json:"sources"`
	Articles map[string]Article `json:"articles"`
}

// One server process owns the data directory. All writes are serialized and
// persisted before becoming visible. Whole-state transactions keep article
// history and the future sync outbox in one durability boundary.
type Store struct {
	mu  sync.RWMutex
	dir string
	db  Database
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
	if s.db.Version != 1 || s.db.Sources == nil || s.db.Articles == nil {
		return nil, errors.New("unsupported store")
	}
	for id, src := range s.db.Sources {
		if id != src.ID || !identifier.MatchString(id) || (src.Kind != "wiki" && src.Kind != "program") || (src.Visibility != "public" && src.Visibility != "private") {
			return nil, errors.New("invalid stored source")
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
	if !safePath(p) || len(p) > 512 || strings.ContainsAny(p, "\\\x00") {
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
	db.Articles[key] = Article{SourceID: sourceID, Path: p, Versions: versions}
	if err := s.commit(db); err != nil {
		return Revision{}, err
	}
	return rev, nil
}
func (s *Store) sources(public bool) []Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Source{}
	for _, src := range s.db.Sources {
		if !public || src.Visibility == "public" {
			out = append(out, src)
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
		src := s.db.Sources[a.SourceID]
		if public && src.Visibility != "public" {
			continue
		}
		rev := a.Versions[len(a.Versions)-1]
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
	if !ok {
		return Page{}, ErrNotFound
	}
	src := s.db.Sources[a.SourceID]
	if public && src.Visibility != "public" {
		return Page{}, ErrNotFound
	}
	r := a.Versions[len(a.Versions)-1]
	return Page{Path: key, Title: title(r.Text, a.Path), Environment: src.ID, SHA: r.SHA, Text: r.Text}, nil
}
func (s *Store) article(sourceID, p string, public bool) (Article, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.db.Articles[sourceID+"/"+p]
	if !ok {
		return Article{}, ErrNotFound
	}
	if public && s.db.Sources[a.SourceID].Visibility != "public" {
		return Article{}, ErrNotFound
	}
	a.Versions = append([]Revision(nil), a.Versions...)
	return a, nil
}
