package main

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type SyncManager struct {
	store  *Store
	github *GitHubClient
	mu     sync.Mutex
	wake   chan struct{}
}

func newSyncManager(s *Store, g *GitHubClient) *SyncManager {
	return &SyncManager{store: s, github: g, wake: make(chan struct{}, 1)}
}
func (m *SyncManager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *SyncManager) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	m.Wake()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
		m.Once(ctx)
	}
}
func (m *SyncManager) Once(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.store.connections() {
		if ctx.Err() != nil {
			return
		}
		if c.Paused {
			continue
		}
		if due, e := time.Parse(time.RFC3339Nano, c.NextAttempt); e == nil && due.After(time.Now()) {
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		m.syncSource(jobCtx, c)
		cancel()
	}
}

type connectRequest struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	AccountID  string `json:"account_id,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Write      bool   `json:"write"`
}
type configureRequest struct {
	ID        string  `json:"id"`
	AccountID *string `json:"account_id"`
	Write     *bool   `json:"write"`
	Expected  *int    `json:"expected_config_revision"`
}

func (m *SyncManager) configure(ctx context.Context, in configureRequest) (GitHubConnection, error) {
	if in.Expected == nil || (in.AccountID == nil && in.Write == nil) {
		return GitHubConnection{}, ErrInvalid
	}
	m.store.mu.RLock()
	c, ok := m.store.db.Connections[in.ID]
	m.store.mu.RUnlock()
	if !ok {
		return c, ErrNotFound
	}
	if c.ConfigRevision != *in.Expected {
		return c, ErrConflict
	}
	accountID, write := c.AccountID, c.Write
	if in.AccountID != nil {
		accountID = *in.AccountID
	}
	if in.Write != nil {
		write = *in.Write
	}
	var info *repositoryInfo
	if write || accountID != c.AccountID {
		secret, account, e := m.store.secret(accountID)
		if e != nil {
			return c, e
		}
		if write && secret == nil {
			return c, ErrInvalid
		}
		if secret != nil && secret.Kind == "app" && !strings.EqualFold(account.Login, strings.Split(c.Repository, "/")[0]) {
			return c, ErrInvalid
		}
		token, e := m.github.token(ctx, secret, c.Repository, write)
		if e != nil {
			return c, e
		}
		repository, e := m.github.repository(ctx, c.Repository, token)
		if e != nil {
			return c, e
		}
		if repository.ID != c.RepositoryID || write && repository.Archived {
			return c, ErrConflict
		}
		info = &repository
	}
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	if moveDeletionLocksConnection(m.store.db, c.ID) {
		return c, ErrConflict
	}
	current, ok := m.store.db.Connections[c.ID]
	if !ok || current.ConfigRevision != *in.Expected {
		return c, ErrConflict
	}
	c = current
	c.AccountID = accountID
	c.Write = write
	c.ConfigRevision++
	c.State = "pending"
	c.NextAttempt = ""
	c.LastPull = ""
	c.TreeSHA = ""
	db := m.store.clone()
	if info != nil {
		c.Private = *info.Private
		c.Verification = "verified"
		c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if c.Private {
			src, found := db.Sources[c.ID]
			if found {
				src.Visibility = "private"
				db.Sources[c.ID] = src
			}
		}
	}
	db.Connections[c.ID] = c
	for id, j := range db.Outbox {
		if j.SourceID == c.ID {
			j.NextAttempt = ""
			if j.State == "blocked" {
				j.State = "pending"
				j.LastError = ""
			}
			db.Outbox[id] = j
		}
	}
	if e := m.store.commit(db); e != nil {
		return c, e
	}
	m.Wake()
	return c, nil
}

func (m *SyncManager) connect(ctx context.Context, in connectRequest) (GitHubConnection, error) {
	if !identifier.MatchString(in.ID) {
		return GitHubConnection{}, ErrInvalid
	}
	repo, e := repositoryName(in.Repository)
	if e != nil {
		return GitHubConnection{}, e
	}
	secret, account, e := m.store.secret(in.AccountID)
	if e != nil {
		return GitHubConnection{}, e
	}
	if secret != nil && secret.Kind == "app" && !strings.EqualFold(account.Login, strings.Split(repo, "/")[0]) {
		return GitHubConnection{}, ErrInvalid
	}
	if in.Write && secret == nil {
		return GitHubConnection{}, ErrInvalid
	}
	token, e := m.github.token(ctx, secret, repo, in.Write)
	if e != nil {
		return GitHubConnection{}, e
	}
	info, e := m.github.repository(ctx, repo, token)
	if e != nil {
		return GitHubConnection{}, e
	}
	branch := in.Branch
	if branch == "" {
		branch = info.DefaultBranch
	}
	if !validBranch(branch) || info.Archived && in.Write {
		return GitHubConnection{}, ErrInvalid
	}
	if _, e = m.github.head(ctx, repo, branch, token); e != nil {
		return GitHubConnection{}, e
	}
	c := GitHubConnection{ID: in.ID, Repository: info.FullName, RepositoryID: info.ID, Branch: branch, AccountID: in.AccountID, Write: in.Write, Private: *info.Private, Verification: "verified", VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano), State: "pending", ConfigRevision: 1}
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	if _, ok := m.store.db.Connections[c.ID]; ok {
		return c, ErrConflict
	}
	if _, ok := m.store.db.Sources[c.ID]; ok {
		return c, ErrConflict
	}
	db := m.store.clone()
	db.Connections[c.ID] = c
	if e = m.store.commit(db); e != nil {
		return c, e
	}
	m.Wake()
	return c, nil
}
func gitBlobDigest(data []byte, length int) string {
	prefix := []byte(fmt.Sprintf("blob %d\x00", len(data)))
	if length == 40 {
		h := sha1.New()
		h.Write(prefix)
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}
	h := sha256.New()
	h.Write(prefix)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
func (m *SyncManager) cachedBlob(ctx context.Context, c GitHubConnection, entry gitTreeEntry, token string) ([]byte, error) {
	if !commitID.MatchString(entry.SHA) {
		return nil, ErrInvalid
	}
	dir := filepath.Join(m.store.dir, "git-objects", fmt.Sprint(c.RepositoryID))
	filename := filepath.Join(dir, entry.SHA)
	if info, e := os.Lstat(filename); e == nil && info.Mode().IsRegular() && info.Size() <= 4<<20 {
		b, e := os.ReadFile(filename)
		if e == nil && gitBlobDigest(b, len(entry.SHA)) == entry.SHA {
			return b, nil
		}
	}
	b, e := m.github.blob(ctx, c.Repository, entry.SHA, token)
	if e != nil {
		return nil, e
	}
	if gitBlobDigest(b, len(entry.SHA)) != entry.SHA {
		return nil, &remoteError{Code: "blob_digest_mismatch"}
	}
	if e = atomicPrivateFile(dir, entry.SHA, b); e != nil {
		return nil, e
	}
	return b, nil
}
func importablePath(p, kind string) bool {
	if !safeRelative(p) || (kind == "program" && !strings.HasPrefix(p, "docs/")) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "node_modules" || part == "vendor" || part == "dist" {
			return false
		}
	}
	return true
}
func (m *SyncManager) remoteBatch(ctx context.Context, c GitHubConnection, head, token string, tree gitTree) (ImportBatch, error) {
	batch := ImportBatch{SourceID: c.ID, Kind: "program", Files: map[string]string{}, Assets: map[string][]byte{}, Commit: head, Complete: true}
	entries := map[string]gitTreeEntry{}
	hasDocs := false
	for _, entry := range tree.Tree {
		if _, exists := entries[entry.Path]; exists {
			return batch, ErrInvalid
		}
		entries[entry.Path] = entry
		if entry.Path == "docs" && entry.Type == "tree" {
			hasDocs = true
		}
	}
	if config, ok := entries["veda.yaml"]; ok {
		if config.Type != "blob" || (config.Mode != "100644" && config.Mode != "100755") || config.Size > 16384 {
			return batch, ErrInvalid
		}
		b, e := m.cachedBlob(ctx, c, config, token)
		if e != nil {
			return batch, e
		}
		batch.Metadata, e = parseMetadata(b)
		if e != nil {
			return batch, e
		}
		batch.Kind = "wiki"
	} else if !hasDocs {
		m.store.mu.RLock()
		existing, found := m.store.db.Sources[c.ID]
		m.store.mu.RUnlock()
		if !found || existing.Kind != "program" {
			return batch, ErrNoDocs
		}
	}
	total, count := 0, 0
	paths := make([]string, 0, len(entries))
	for p := range entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		entry := entries[p]
		if entry.Type != "blob" || !importablePath(p, batch.Kind) {
			continue
		}
		ext := strings.ToLower(path.Ext(p))
		markdown := ext == ".md" || ext == ".markdown"
		image := ext == ".png" || ext == ".jpeg" || ext == ".jpg" || ext == ".gif" || ext == ".webp"
		if !markdown && !image {
			continue
		}
		if entry.Mode != "100644" && entry.Mode != "100755" || entry.Size > 4<<20 || markdown && entry.Size > 256000 {
			batch.Withheld++
			batch.Complete = false
			continue
		}
		b, e := m.cachedBlob(ctx, c, entry, token)
		if e != nil {
			return batch, e
		}
		count++
		total += len(b)
		if count > 2000 || total > 16<<20 {
			return batch, &remoteError{Code: "repository_limit"}
		}
		if markdown {
			if !utf8.Valid(b) || secretContent.Match(b) {
				batch.Withheld++
				batch.Complete = false
				continue
			}
			batch.Files[p] = string(b)
		} else {
			if _, e = imageRef(b); e != nil {
				batch.Withheld++
				batch.Complete = false
				continue
			}
			batch.Assets[p] = b
		}
	}
	m.store.mu.RLock()
	src, exists := m.store.db.Sources[c.ID]
	m.store.mu.RUnlock()
	expected := src.ImportGeneration
	batch.ExpectedImport = &expected
	batch.Create = !exists
	if c.Private {
		batch.Visibility = "private"
	} else {
		batch.Visibility = "public"
	}
	return batch, nil
}
func (m *SyncManager) syncSource(ctx context.Context, c GitHubConnection) {
	fail := func(e error) { m.store.connectionStatus(c.ID, "error", "unknown", errorCode(e), nil) }
	secret, _, e := m.store.secret(c.AccountID)
	if e != nil {
		fail(e)
		return
	}
	token, e := m.github.token(ctx, secret, c.Repository, c.Write)
	if e != nil {
		fail(e)
		return
	}
	info, e := m.github.repository(ctx, c.Repository, token)
	if e != nil {
		fail(e)
		return
	}
	if info.ID != c.RepositoryID {
		fail(ErrConflict)
		return
	}
	visibilityChanged := c.Private != *info.Private
	c.Private = *info.Private
	if e = m.store.connectionStatus(c.ID, "syncing", "verified", "", &info); e != nil {
		return
	}
	head, e := m.github.head(ctx, c.Repository, c.Branch, token)
	if e != nil {
		fail(e)
		return
	}
	m.store.mu.RLock()
	source, exists := m.store.db.Sources[c.ID]
	m.store.mu.RUnlock()
	matchingVisibility := exists && (source.Visibility == "private") == c.Private
	if matchingVisibility && !visibilityChanged && head == c.LastPull && commitID.MatchString(c.TreeSHA) {
		if attempted, err := m.deletePendingMove(ctx, c, head, token); err != nil {
			m.store.connectionStatus(c.ID, "error", "verified", errorCode(err), &info)
			return
		} else if attempted {
			return
		}
		if e = m.pushPending(ctx, c, head, c.TreeSHA, token); e != nil {
			m.store.connectionStatus(c.ID, "error", "verified", errorCode(e), &info)
			return
		}
		state, code := m.store.sourceSyncState(c.ID)
		m.store.connectionStatus(c.ID, state, "verified", code, &info)
		return
	}
	tree, e := m.github.tree(ctx, c.Repository, head, token)
	if e != nil {
		fail(e)
		return
	}
	// HEAD equality is insufficient after a visibility transition: always rebuild
	// publication decisions from the verified repository snapshot.
	batch, e := m.remoteBatch(ctx, c, head, token, tree)
	if errors.Is(e, ErrNoDocs) {
		m.store.connectionStatus(c.ID, "skipped", "verified", "", &info)
		return
	}
	if e != nil {
		fail(e)
		return
	}
	visibility := "public"
	if c.Private {
		visibility = "private"
	}
	if _, e = m.store.applyImport(batch, &remoteProof{ConnectionID: c.ID, ConfigRevision: c.ConfigRevision, Visibility: visibility, TreeSHA: tree.SHA}, false); e != nil {
		fail(e)
		return
	}
	if attempted, err := m.deletePendingMove(ctx, c, head, token); err != nil {
		m.store.connectionStatus(c.ID, "error", "verified", errorCode(err), &info)
		return
	} else if attempted {
		return
	}
	if e = m.pushPending(ctx, c, head, tree.SHA, token); e != nil {
		m.store.connectionStatus(c.ID, "error", "verified", errorCode(e), &info)
		return
	}
	state, code := m.store.sourceSyncState(c.ID)
	m.store.connectionStatus(c.ID, state, "verified", code, &info)
}

// Serialize local mutations with the remote ref update. Once prepared, the
// stored commit keeps both articles locked until a complete pull resolves it.
func (m *SyncManager) deletePendingMove(ctx context.Context, c GitHubConnection, head, token string) (bool, error) {
	eligible := false
	for _, move := range m.store.moves() {
		if move.SourceConnectionID == c.ID && (move.State == "waiting_source_delete" || move.State == "deleting_source") {
			eligible = true
			break
		}
	}
	if !eligible {
		return false, nil
	}
	tree, err := m.github.tree(ctx, c.Repository, head, token)
	if err != nil {
		return true, err
	}
	m.store.mu.Lock()
	defer m.store.mu.Unlock()
	current, ok := m.store.db.Connections[c.ID]
	if !ok || current.Paused || !current.Write || current.ConfigRevision != c.ConfigRevision || current.RepositoryID != c.RepositoryID || current.Branch != c.Branch {
		return true, ErrConflict
	}
	for _, move := range m.store.db.Moves {
		if move.SourceConnectionID != c.ID || (move.State != "waiting_source_delete" && move.State != "deleting_source") {
			continue
		}
		// Re-evaluate the complete local invariants immediately before preparing.
		db := m.store.clone()
		reconcileMoves(&db)
		move = db.Moves[move.ID]
		if move.State != "waiting_source_delete" && move.State != "deleting_source" {
			return true, m.store.commit(db)
		}
		original := db.Articles[move.SourceID+"/"+move.SourcePath]
		prepare := func(commit string) error {
			move.State, move.PreparedDeletion = "deleting_source", commit
			db.Moves[move.ID] = move
			return m.store.commit(db)
		}
		_, err := m.github.remove(ctx, c.Repository, c.Branch, head, tree, move.SourcePath, original.Versions[move.SourceRevision-1].Text, token, prepare)
		if err == nil {
			m.Wake()
		}
		return true, err
	}
	return false, nil
}
func (m *SyncManager) pushPending(ctx context.Context, c GitHubConnection, head, treeSHA, token string) error {
	for _, job := range m.store.jobs() {
		if job.SourceID != c.ID || job.State == "complete" || job.State == "superseded" {
			continue
		}
		if !c.Write {
			m.store.setJob(job.ID, "blocked", "write_not_enabled", "", "")
			continue
		}
		if job.Visibility != "public" && !c.Private {
			m.store.setJob(job.ID, "blocked", "publication_confirmation_required", "", "")
			continue
		}
		if due, e := time.Parse(time.RFC3339Nano, job.NextAttempt); e == nil && due.After(time.Now()) {
			continue
		}
		m.store.mu.RLock()
		a, ok := m.store.db.Articles[c.ID+"/"+job.Path]
		current, connected := m.store.db.Connections[c.ID]
		m.store.mu.RUnlock()
		if !ok || !connected || current.Paused || current.ConfigRevision != c.ConfigRevision {
			return ErrConflict
		}
		if job.Revision > len(a.Versions) || a.Versions[job.Revision-1].SHA != job.SHA {
			return ErrInvalid
		}
		pending := false
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				pending = true
				break
			}
		}
		if pending {
			m.store.setJob(job.ID, "conflict", "source_conflict", "", "")
			continue
		}
		if job.State == "conflict" {
			continue
		}
		prepared := func(commit string) error {
			m.store.mu.RLock()
			latest, ok := m.store.db.Connections[c.ID]
			m.store.mu.RUnlock()
			if !ok || latest.Paused || latest.ConfigRevision != c.ConfigRevision {
				return ErrConflict
			}
			return m.store.setJob(job.ID, "prepared", "", commit, "")
		}
		if job.Visibility != "public" {
			info, e := m.github.repository(ctx, c.Repository, token)
			if e != nil {
				return e
			}
			if !*info.Private {
				m.store.setJob(job.ID, "blocked", "publication_confirmation_required", "", "")
				return &remoteError{Code: "publication_confirmation_required"}
			}
		}
		commit, e := m.github.push(ctx, c.Repository, c.Branch, head, treeSHA, job.Path, a.Versions[job.Revision-1].Text, token, prepared)
		if e != nil {
			m.store.setJob(job.ID, "retry", errorCode(e), "", "")
			return e
		}
		// Do not optimistically acknowledge the remote update. The next pull must
		// observe the actual blob, which also recovers a lost PATCH response.
		m.store.setJob(job.ID, "prepared", "", "", commit)
		m.Wake()
		return nil // one commit per source per pass; refresh before the next write
	}
	return nil
}
