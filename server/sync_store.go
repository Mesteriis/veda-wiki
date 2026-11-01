package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SyncJob struct {
	ID             string `json:"id"`
	SourceID       string `json:"source_id"`
	Path           string `json:"path"`
	Revision       int    `json:"revision"`
	SHA            string `json:"sha256"`
	State          string `json:"state"`
	Created        string `json:"created_at"`
	Attempts       int    `json:"attempts"`
	LastError      string `json:"last_error,omitempty"`
	PreparedCommit string `json:"prepared_commit,omitempty"`
	ObservedCommit string `json:"observed_commit,omitempty"`
	NextAttempt    string `json:"next_attempt,omitempty"`
	Visibility     string `json:"visibility"`
}
type remoteProof struct {
	ConnectionID   string
	ConfigRevision int
	Visibility     string
	TreeSHA        string
}

func sourcePublic(db Database, src Source, now time.Time) bool {
	if src.Visibility != "public" {
		return false
	}
	if src.ConnectionID == "" {
		return true
	}
	c, ok := db.Connections[src.ConnectionID]
	verified, e := time.Parse(time.RFC3339Nano, c.VerifiedAt)
	return ok && !c.Paused && c.Verification == "verified" && !c.Private && e == nil && !verified.After(now.Add(time.Minute)) && now.Sub(verified) < 10*time.Minute
}
func (s *Store) retryConnection(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.db.Connections[id]
	if !ok {
		return ErrNotFound
	}
	if c.Paused {
		return ErrConflict
	}
	c.NextAttempt = ""
	c.State = "pending"
	db := s.clone()
	db.Connections[id] = c
	for key, j := range db.Outbox {
		if j.SourceID == id {
			j.NextAttempt = ""
			db.Outbox[key] = j
		}
	}
	return s.commit(db)
}

func (s *Store) sourceSyncState(id string) (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, code := "ready", ""
	for _, a := range s.db.Articles {
		if a.SourceID == id {
			for _, c := range a.Conflicts {
				if c.Resolution == "" {
					return "conflict", "source_conflict"
				}
			}
		}
	}
	for _, j := range s.db.Outbox {
		if j.SourceID != id {
			continue
		}
		switch j.State {
		case "conflict":
			return "conflict", "source_conflict"
		case "blocked":
			state, code = "blocked", j.LastError
		case "pending", "prepared", "retry":
			if state != "blocked" {
				state = "pending"
			}
		}
	}
	return state, code
}
func enqueueWrite(db *Database, a Article, rev Revision) {
	if db.Outbox == nil {
		db.Outbox = map[string]SyncJob{}
	}
	for id, job := range db.Outbox {
		if job.SourceID == a.SourceID && job.Path == a.Path && (job.State == "pending" || job.State == "retry") {
			job.State = "superseded"
			db.Outbox[id] = job
		}
	}
	id := a.SourceID + "/" + a.Path + "@" + strconv.Itoa(rev.Number)
	db.Outbox[id] = SyncJob{ID: id, SourceID: a.SourceID, Path: a.Path, Revision: rev.Number, SHA: rev.SHA, State: "pending", Created: rev.Created, Visibility: db.Sources[a.SourceID].Visibility}
}
func (s *Store) publishJob(id string, expected int, confirm bool) error {
	if !confirm {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.db.Outbox[id]
	if !ok {
		return ErrNotFound
	}
	a, ok := s.db.Articles[job.SourceID+"/"+job.Path]
	src := s.db.Sources[job.SourceID]
	if !ok || a.Deleted || expected != len(a.Versions) || job.Revision != expected || job.State == "complete" || job.State == "superseded" || !sourcePublic(s.db, src, time.Now()) {
		return ErrConflict
	}
	if secretContent.MatchString(a.Versions[expected-1].Text) {
		return ErrInvalid
	}
	job.Visibility = "public"
	job.State = "pending"
	job.LastError = ""
	job.NextAttempt = ""
	published := a.Versions[expected-1]
	published.Number = 0
	published.Created = time.Now().UTC().Format(time.RFC3339Nano)
	a.Published = &published
	a.PublishedLocal = true
	db := s.clone()
	db.Articles[job.SourceID+"/"+job.Path] = a
	db.Outbox[id] = job
	conn := db.Connections[src.ConnectionID]
	conn.NextAttempt = ""
	conn.State = "pending"
	db.Connections[conn.ID] = conn
	return s.commit(db)
}
func observedWrite(db *Database, sourceID, p, hash, commit string) bool {
	matched := 0
	for _, job := range db.Outbox {
		if job.SourceID == sourceID && job.Path == p && job.SHA == hash && job.Revision > matched && job.State != "complete" && job.State != "superseded" && job.State != "conflict" {
			matched = job.Revision
		}
	}
	if matched == 0 {
		return false
	}
	for id, job := range db.Outbox {
		if job.SourceID == sourceID && job.Path == p && job.Revision <= matched && job.State != "complete" {
			if job.Revision == matched {
				job.State = "complete"
				job.ObservedCommit = commit
			} else {
				job.State = "superseded"
			}
			job.LastError = ""
			db.Outbox[id] = job
		}
	}
	return true
}
func randomID() (string, error) {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func atomicPrivateFile(dir, name string, b []byte) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("private storage unavailable")
	}
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(f.Name(), filepath.Join(dir, name))
	}
	if e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) saveAccount(ctx context.Context, g *GitHubClient, id string, secret GitHubSecret) (GitHubAccount, error) {
	if !identifier.MatchString(id) {
		return GitHubAccount{}, ErrInvalid
	}
	login, e := g.verifyAccount(ctx, secret)
	if e != nil {
		return GitHubAccount{}, e
	}
	ref, e := randomID()
	if e != nil {
		return GitHubAccount{}, e
	}
	b, e := json.Marshal(secret)
	if e != nil {
		return GitHubAccount{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.db.Accounts[id]; exists {
		return GitHubAccount{}, ErrConflict
	}
	if e = atomicPrivateFile(filepath.Join(s.dir, "credentials"), ref+".json", b); e != nil {
		return GitHubAccount{}, e
	}
	account := GitHubAccount{ID: id, Kind: secret.Kind, Login: login, SecretRef: ref}
	db := s.clone()
	db.Accounts[id] = account
	if e = s.commit(db); e != nil {
		return GitHubAccount{}, e
	}
	account.SecretRef = ""
	return account, nil
}
func (s *Store) accounts() []GitHubAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []GitHubAccount{}
	for _, a := range s.db.Accounts {
		a.SecretRef = ""
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) secret(accountID string) (*GitHubSecret, GitHubAccount, error) {
	if accountID == "" {
		return nil, GitHubAccount{}, nil
	}
	s.mu.RLock()
	a, ok := s.db.Accounts[accountID]
	s.mu.RUnlock()
	if !ok || len(a.SecretRef) != 32 || strings.ContainsAny(a.SecretRef, "/\\.") {
		return nil, a, &remoteError{Code: "credentials_missing"}
	}
	filename := filepath.Join(s.dir, "credentials", a.SecretRef+".json")
	info, e := os.Lstat(filename)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, a, &remoteError{Code: "credentials_missing"}
	}
	f, e := os.Open(filename)
	if e != nil {
		return nil, a, &remoteError{Code: "credentials_missing"}
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 32769))
	if e != nil || len(b) > 32768 {
		return nil, a, ErrInvalid
	}
	var secret GitHubSecret
	if json.Unmarshal(b, &secret) != nil || secret.Kind != a.Kind {
		return nil, a, ErrInvalid
	}
	return &secret, a, nil
}
func (s *Store) connections() []GitHubConnection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []GitHubConnection{}
	for _, c := range s.db.Connections {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) jobs() []SyncJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SyncJob{}
	for _, j := range s.db.Outbox {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}
func (s *Store) connectionStatus(id, state, verification, code string, info *repositoryInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.db.Connections[id]
	if !ok {
		return ErrNotFound
	}
	if c.Paused {
		return ErrConflict
	}
	c.State = state
	c.LastError = code
	if verification != "" {
		c.Verification = verification
	}
	if info != nil {
		if c.RepositoryID != info.ID {
			return ErrConflict
		}
		c.Private = *info.Private
		c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if code != "" {
		c.Attempts++
		delay := time.Duration(1<<min(c.Attempts, 8)) * time.Second
		c.NextAttempt = time.Now().Add(delay).UTC().Format(time.RFC3339Nano)
	} else {
		c.Attempts = 0
		c.NextAttempt = ""
	}
	db := s.clone()
	db.Connections[id] = c
	if info != nil && *info.Private {
		if src, found := db.Sources[id]; found {
			src.Visibility = "private"
			db.Sources[id] = src
		}
	}
	return s.commit(db)
}
func (s *Store) setJob(id, state, code, prepared, observed string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.db.Outbox[id]
	if !ok {
		return ErrNotFound
	}
	j.State = state
	j.LastError = code
	if prepared != "" {
		j.PreparedCommit = prepared
	}
	if observed != "" {
		j.ObservedCommit = observed
	}
	if code != "" {
		j.Attempts++
		j.NextAttempt = time.Now().Add(time.Duration(1<<min(j.Attempts, 8)) * time.Second).UTC().Format(time.RFC3339Nano)
	} else {
		j.NextAttempt = ""
	}
	db := s.clone()
	db.Outbox[id] = j
	return s.commit(db)
}
func (s *Store) pauseConnection(id string, paused bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.db.Connections[id]
	if !ok {
		return ErrNotFound
	}
	c.Paused = paused
	c.ConfigRevision++
	if paused {
		c.State = "paused"
		c.Verification = "unknown"
	} else {
		c.State = "pending"
		c.NextAttempt = ""
	}
	db := s.clone()
	db.Connections[id] = c
	return s.commit(db)
}
