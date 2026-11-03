package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"log"
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
	store             *Store
	github            *github.Client
	localImportSource string
	mu                sync.Mutex
	wake              chan struct{}
}

func newSyncManager(s *Store, g *github.Client) *SyncManager {
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
	m.store.RLock()
	invalid := m.store.InvalidLocked()
	m.store.RUnlock()
	if invalid {
		return
	}
	for _, initial := range m.store.connections() {
		if ctx.Err() != nil {
			return
		}
		// A previous source may have put this account on rate-limit cooldown.
		// Reload rather than using the snapshot taken before the loop.
		m.store.RLock()
		c, exists := m.store.DB().Connections[initial.ID]
		m.store.RUnlock()
		if !exists {
			continue
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
	for _, mirror := range m.store.mirrors() {
		if ctx.Err() != nil {
			return
		}
		if !mirror.Enabled {
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		result, err := m.store.reconcileSourcesExpected(jobCtx, reconcileRequest{SourceID: mirror.SourceID, DestinationID: mirror.DestinationID, Limit: 20}, &mirror.Revision)
		cancel()
		if saveErr := m.store.recordMirrorResult(mirror, result, err); saveErr != nil {
			log.Print("source mirror status could not be saved")
		}
		if err == nil && (result.More || result.Created > 0 || result.Updated > 0) {
			m.Wake()
		}
	}
}

type connectRequest struct {
	ID            string `json:"id"`
	Repository    string `json:"repository"`
	AccountID     string `json:"account_id,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Write         bool   `json:"write"`
	AdoptExisting bool   `json:"adopt_existing,omitempty"`
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
	m.store.RLock()
	c, ok := m.store.DB().Connections[in.ID]
	m.store.RUnlock()
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
	if (write || accountID != c.AccountID) && accountID == c.AccountID && c.LastError == "rate_limited" {
		if due, err := time.Parse(time.RFC3339Nano, c.NextAttempt); err == nil && due.After(time.Now()) {
			return c, ErrConflict
		}
	}
	var info *github.RepositoryInfo
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
		token, e := m.github.Token(ctx, secret, c.Repository, write)
		if e != nil {
			return c, e
		}
		repository, e := m.github.Repository(ctx, c.Repository, token)
		if e != nil {
			return c, e
		}
		if repository.ID != c.RepositoryID || write && repository.Archived {
			return c, ErrConflict
		}
		info = &repository
	}
	m.store.Lock()
	defer m.store.Unlock()
	if moveDeletionLocksConnection(*m.store.DB(), c.ID) {
		return c, ErrConflict
	}
	current, ok := m.store.DB().Connections[c.ID]
	if !ok || current.ConfigRevision != *in.Expected {
		return c, ErrConflict
	}
	c = current
	// Reconfiguring the same account must not circumvent GitHub's cooldown.
	keepCooldown := c.AccountID == accountID && c.LastError == "rate_limited"
	c.AccountID = accountID
	c.Write = write
	c.ConfigRevision++
	if c.Paused {
		c.State = "paused"
	} else {
		c.State = "pending"
	}
	if !keepCooldown {
		c.NextAttempt = ""
		c.LastError = ""
	}
	c.LastPull = ""
	c.TreeSHA = ""
	db := m.store.Clone()
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
			if !keepCooldown {
				j.NextAttempt = ""
			}
			if j.State == "blocked" {
				j.State = "pending"
				j.LastError = ""
			}
			db.Outbox[id] = j
		}
	}
	if e := m.store.Commit(db); e != nil {
		return c, e
	}
	m.Wake()
	return c, nil
}

func (m *SyncManager) connect(ctx context.Context, in connectRequest) (GitHubConnection, error) {
	if !identifier.MatchString(in.ID) {
		return GitHubConnection{}, ErrInvalid
	}
	var snapshot string
	if in.AdoptExisting {
		if in.ID == m.localImportSource {
			return GitHubConnection{}, errLocalImportActive
		}
		if in.AccountID == "" {
			return GitHubConnection{}, ErrInvalid
		}
		var err error
		snapshot, err = m.store.adoptionSnapshot(in.ID)
		if err != nil {
			return GitHubConnection{}, err
		}
	}
	repo, e := github.RepositoryName(in.Repository)
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
	token, e := m.github.Token(ctx, secret, repo, in.Write)
	if e != nil {
		return GitHubConnection{}, e
	}
	info, e := m.github.Repository(ctx, repo, token)
	if e != nil {
		return GitHubConnection{}, e
	}
	branch := in.Branch
	if branch == "" {
		branch = info.DefaultBranch
	}
	if !github.ValidBranch(branch) || info.Archived && in.Write {
		return GitHubConnection{}, ErrInvalid
	}
	head, e := m.github.Head(ctx, repo, branch, token)
	if e != nil {
		return GitHubConnection{}, e
	}
	c := GitHubConnection{ID: in.ID, Repository: info.FullName, RepositoryID: info.ID, Branch: branch, AccountID: in.AccountID, Write: in.Write, Private: *info.Private, Verification: "verified", VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano), State: "pending", ConfigRevision: 1}
	if in.AdoptExisting {
		if !c.Private {
			return GitHubConnection{}, ErrConflict
		}
		tree, err := m.github.Tree(ctx, c.Repository, head, token)
		if err != nil {
			return GitHubConnection{}, err
		}
		batch, err := m.remoteBatch(ctx, c, head, token, tree)
		if err != nil {
			return GitHubConnection{}, err
		}
		if batch.Create || !batch.Complete || batch.Withheld != 0 {
			return GitHubConnection{}, ErrConflict
		}
		// Verify the remote identity, visibility and head again immediately
		// before the atomic attachment; a later sync still re-verifies them.
		latest, err := m.github.Repository(ctx, c.Repository, token)
		if err != nil {
			return GitHubConnection{}, err
		}
		if latest.ID != c.RepositoryID || !*latest.Private || latest.Archived && c.Write {
			return GitHubConnection{}, ErrConflict
		}
		currentHead, err := m.github.Head(ctx, c.Repository, c.Branch, token)
		if err != nil {
			return GitHubConnection{}, err
		}
		if currentHead != head {
			return GitHubConnection{}, ErrConflict
		}
		if err := ctx.Err(); err != nil {
			return GitHubConnection{}, err
		}
		c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
		proof := &importer.RemoteProof{ConnectionID: c.ID, ConfigRevision: c.ConfigRevision, Visibility: "private", TreeSHA: tree.SHA, Adoption: &importer.AdoptionProof{Connection: c, Account: account, Snapshot: snapshot, Context: ctx}}
		if _, err = m.store.importer().Apply(batch, proof, false); err != nil {
			return GitHubConnection{}, err
		}
		m.Wake()
		for _, saved := range m.store.connections() {
			if saved.ID == c.ID {
				return saved, nil
			}
		}
		return GitHubConnection{}, ErrConflict
	}
	m.store.Lock()
	defer m.store.Unlock()
	if _, ok := m.store.DB().Connections[c.ID]; ok {
		return c, ErrConflict
	}
	if _, ok := m.store.DB().Sources[c.ID]; ok {
		return c, ErrConflict
	}
	db := m.store.Clone()
	db.Connections[c.ID] = c
	if e = m.store.Commit(db); e != nil {
		return c, e
	}
	m.Wake()
	return c, nil
}
func (m *SyncManager) cachedBlob(ctx context.Context, c GitHubConnection, entry github.TreeEntry, token string) ([]byte, error) {
	if !commitID.MatchString(entry.SHA) {
		return nil, ErrInvalid
	}
	dir := filepath.Join(m.store.Dir(), "git-objects", fmt.Sprint(c.RepositoryID))
	filename := filepath.Join(dir, entry.SHA)
	if info, e := os.Lstat(filename); e == nil && info.Mode().IsRegular() && info.Size() <= 4<<20 {
		b, e := os.ReadFile(filename)
		if e == nil && github.BlobDigest(b, len(entry.SHA)) == entry.SHA {
			return b, nil
		}
	}
	// An authenticated private repository can reuse identical bytes from any
	// disconnected private source. The repository tree supplies the exact Git
	// object and length; source identities and article state stay independent.
	if c.Private {
		m.store.RLock()
		var articles []string
		var images []BlobRef
		for id, src := range m.store.DB().Sources {
			if src.Visibility != "private" || src.ConnectionID != "" {
				continue
			}
			if a, ok := m.store.DB().Articles[id+"/"+entry.Path]; ok && !a.Deleted && len(a.Versions) > 0 {
				articles = append(articles, a.Versions[len(a.Versions)-1].Text)
			}
			if ref, ok := src.Assets[entry.Path]; ok {
				images = append(images, ref)
			}
		}
		m.store.RUnlock()
		var local []byte
		matched := false
		for _, text := range articles {
			if int64(len(text)) != entry.Size {
				continue
			}
			candidate := []byte(text)
			if github.BlobDigest(candidate, len(entry.SHA)) == entry.SHA {
				local = candidate
				matched = true
				break
			}
		}
		if !matched {
			for _, ref := range images {
				image, err := m.store.readAsset(ref)
				if err == nil && int64(len(image)) == entry.Size && github.BlobDigest(image, len(entry.SHA)) == entry.SHA {
					local = image
					matched = true
					break
				}
			}
		}
		if matched {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := atomicPrivateFile(dir, entry.SHA, local); err != nil {
				return nil, err
			}
			return local, nil
		}
	}
	b, e := m.github.Blob(ctx, c.Repository, entry.SHA, token)
	if e != nil {
		return nil, e
	}
	if github.BlobDigest(b, len(entry.SHA)) != entry.SHA {
		return nil, &github.RemoteError{Code: "blob_digest_mismatch"}
	}
	if e = atomicPrivateFile(dir, entry.SHA, b); e != nil {
		return nil, e
	}
	return b, nil
}
func (m *SyncManager) remoteBatch(ctx context.Context, c GitHubConnection, head, token string, tree github.Tree) (importer.Batch, error) {
	batch := importer.Batch{SourceID: c.ID, Kind: "program", Files: map[string]string{}, Assets: map[string][]byte{}, Commit: head, Complete: true}
	entries := map[string]github.TreeEntry{}
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
			return batch, &github.RemoteError{Code: "invalid_veda_metadata"}
		}
		b, e := m.cachedBlob(ctx, c, config, token)
		if e != nil {
			return batch, e
		}
		batch.Metadata, e = importer.ParseMetadata(b)
		if e != nil {
			return batch, &github.RemoteError{Code: "invalid_veda_metadata"}
		}
		batch.Kind = "wiki"
	} else if entry, exists := entries["docs"]; exists && (entry.Type != "tree" || entry.Mode != "040000") {
		// A file, symlink or submodule named docs is not an absent directory.
		// Treating it as one would delete previously imported documentation.
		return batch, &github.RemoteError{Code: "invalid_docs_directory"}
	} else if !hasDocs {
		m.store.RLock()
		existing, found := m.store.DB().Sources[c.ID]
		m.store.RUnlock()
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
		if entry.Mode != "100644" && entry.Mode != "100755" || entry.Size > 4<<20 || markdown && entry.Size > maxArticleBytes {
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
		if count > 2000 || total > importer.MaxBytes {
			return batch, &github.RemoteError{Code: "repository_limit"}
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
	if batch.Metadata != nil && batch.Metadata.Image != "" {
		if _, ok := batch.Assets[batch.Metadata.Image]; !ok {
			return batch, &github.RemoteError{Code: "veda_image_unavailable"}
		}
	}
	m.store.RLock()
	src, exists := m.store.DB().Sources[c.ID]
	m.store.RUnlock()
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
	fail := func(e error) {
		m.store.connectionStatusFor(c, "error", "unknown", github.ErrorCode(e), nil, github.RetryAt(e))
	}
	secret, _, e := m.store.secret(c.AccountID)
	if e != nil {
		fail(e)
		return
	}
	token, e := m.github.Token(ctx, secret, c.Repository, c.Write)
	if e != nil {
		fail(e)
		return
	}
	info, e := m.github.Repository(ctx, c.Repository, token)
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
	if e = m.store.connectionStatusFor(c, "syncing", "verified", "", &info, time.Time{}); e != nil {
		return
	}
	head, e := m.github.Head(ctx, c.Repository, c.Branch, token)
	if e != nil {
		fail(e)
		return
	}
	if head != c.LastPull {
		if e = m.store.observeChangedHead(c, head); e != nil {
			return
		}
	}
	m.store.RLock()
	source, exists := m.store.DB().Sources[c.ID]
	m.store.RUnlock()
	matchingVisibility := exists && (source.Visibility == "private") == c.Private
	repairingAssets := false
	if !exists && c.State == "skipped" && !visibilityChanged && head == c.LastPull && commitID.MatchString(c.TreeSHA) {
		m.store.connectionStatusFor(c, "skipped", "verified", "", &info, time.Time{})
		return
	}
	if matchingVisibility && !visibilityChanged && head == c.LastPull && commitID.MatchString(c.TreeSHA) {
		if err := m.store.observeGitHubHead(c, head, c.TreeSHA); err == nil {
			m.syncWrites(ctx, c, head, c.TreeSHA, token, info)
			return
		} else if !errors.Is(err, importer.ErrAssetUnavailable) {
			return
		}
		repairingAssets = true
	}
	tree, e := m.github.Tree(ctx, c.Repository, head, token)
	if e != nil {
		fail(e)
		return
	}
	// HEAD equality is insufficient after a visibility transition: always rebuild
	// publication decisions from the verified repository snapshot.
	batch, e := m.remoteBatch(ctx, c, head, token, tree)
	if errors.Is(e, ErrNoDocs) {
		if !exists {
			if err := m.store.recordSkippedRepository(c, head, tree.SHA); err != nil && !errors.Is(err, ErrConflict) {
				fail(err)
			}
		} else {
			// A previously imported wiki no longer has its identifying
			// manifest. Its old snapshot cannot be confirmed as current.
			fail(&github.RemoteError{Code: "repository_type_changed"})
		}
		return
	}
	if e != nil {
		fail(e)
		return
	}
	if exists && source.Kind != batch.Kind {
		// Reclassifying a source would invalidate stored article paths and
		// translations. Keep its local history and require explicit recovery.
		fail(&github.RemoteError{Code: "repository_type_changed"})
		return
	}
	visibility := "public"
	if c.Private {
		visibility = "private"
	}
	if _, e = m.store.importer().Apply(batch, &importer.RemoteProof{ConnectionID: c.ID, ConfigRevision: c.ConfigRevision, Visibility: visibility, TreeSHA: tree.SHA}, false); e != nil {
		if errors.Is(e, importer.ErrAssetUnavailable) {
			fail(&github.RemoteError{Code: "asset_unavailable"})
			return
		}
		fail(e)
		return
	}
	if repairingAssets {
		if err := m.store.observeGitHubHead(c, head, tree.SHA); errors.Is(err, importer.ErrAssetUnavailable) {
			fail(&github.RemoteError{Code: "asset_unavailable"})
			return
		} else if err != nil {
			return
		}
		// A move or push must revalidate its dependencies in a later cycle.
		state, code := m.store.sourceSyncState(c.ID)
		m.store.connectionStatusFor(c, state, "verified", code, &info, time.Time{})
		return
	}
	m.syncWrites(ctx, c, head, tree.SHA, token, info)
}

func (m *SyncManager) syncWrites(ctx context.Context, c GitHubConnection, head, treeSHA, token string, info github.RepositoryInfo) {
	attempted, moveErr := m.deletePendingMove(ctx, c, head, token)
	if attempted {
		if moveErr != nil {
			m.store.connectionStatusFor(c, "error", "verified", github.ErrorCode(moveErr), &info, github.RetryAt(moveErr))
		}
		return
	}
	if err := m.pushPending(ctx, c, head, treeSHA, token); err != nil {
		m.store.connectionStatusFor(c, "error", "verified", github.ErrorCode(err), &info, github.RetryAt(err))
		return
	}
	if moveErr != nil {
		m.store.connectionStatusFor(c, "error", "verified", github.ErrorCode(moveErr), &info, github.RetryAt(moveErr))
		return
	}
	state, code := m.store.sourceSyncState(c.ID)
	m.store.connectionStatusFor(c, state, "verified", code, &info, time.Time{})
}

// Serialize local mutations with the remote ref update. Once prepared, the
// stored commit keeps both articles locked until a complete pull resolves it.
func (m *SyncManager) deletePendingMove(ctx context.Context, c GitHubConnection, head, token string) (bool, error) {
	var firstErr error
	for _, move := range m.store.transfer().Moves() {
		if move.SourceConnectionID != c.ID || (move.State != "waiting_source_delete" && move.State != "deleting_source") {
			continue
		}
		attempted, err := m.deleteMove(ctx, c, head, token, move)
		if err == nil && attempted {
			return true, nil
		}
		if err == nil {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		// A request with an uncertain outcome may have advanced this branch.
		// Pull again before attempting another deletion using the same head.
		m.store.RLock()
		current := m.store.DB().Moves[move.ID]
		m.store.RUnlock()
		if current.State == "deleting_source" {
			return true, err
		}
	}
	return false, firstErr
}

func (m *SyncManager) deleteMove(ctx context.Context, c GitHubConnection, head, token string, candidate Move) (bool, error) {
	var destinationRevision int
	if candidate.DestinationConnectionID != "" {
		m.store.RLock()
		destination, ok := m.store.DB().Connections[candidate.DestinationConnectionID]
		target, targetOK := m.store.DB().Articles[candidate.DestinationID+"/"+candidate.DestinationPath]
		m.store.RUnlock()
		if !ok || !targetOK || len(target.Versions) != 1 || destination.Paused || destination.RepositoryID != candidate.DestinationRepositoryID || destination.Branch != candidate.DestinationBranch {
			return true, ErrConflict
		}
		destinationRevision = destination.ConfigRevision
		secret, _, err := m.store.secret(destination.AccountID)
		if err != nil {
			return true, err
		}
		destinationToken, err := m.github.Token(ctx, secret, destination.Repository, false)
		if err != nil {
			return true, err
		}
		info, err := m.github.Repository(ctx, destination.Repository, destinationToken)
		if err != nil {
			return true, err
		}
		if info.ID != destination.RepositoryID || *info.Private != destination.Private {
			return true, ErrConflict
		}
		destinationHead, err := m.github.Head(ctx, destination.Repository, destination.Branch, destinationToken)
		if err != nil {
			return true, err
		}
		destinationTree, err := m.github.Tree(ctx, destination.Repository, destinationHead, destinationToken)
		if err != nil {
			return true, err
		}
		if _, err = github.VerifiedTreeFile(destinationTree, candidate.DestinationPath, target.Versions[0].Text); err != nil {
			return true, err
		}
		for name, ref := range candidate.Assets {
			data, err := m.store.readAsset(ref)
			if err != nil {
				return true, err
			}
			if _, err = github.VerifiedTreeFile(destinationTree, name, string(data)); err != nil {
				return true, err
			}
		}
		linked, err := m.store.transfer().LinkContents(candidate.DestinationID, candidate.Links)
		if err != nil {
			return true, err
		}
		for name, text := range linked {
			if _, err = github.VerifiedTreeFile(destinationTree, name, text); err != nil {
				return true, err
			}
		}
	}
	tree, err := m.github.Tree(ctx, c.Repository, head, token)
	if err != nil {
		return true, err
	}
	m.store.Lock()
	defer m.store.Unlock()
	current, ok := m.store.DB().Connections[c.ID]
	if !ok || current.Paused || !current.Write || current.ConfigRevision != c.ConfigRevision || current.RepositoryID != c.RepositoryID || current.Branch != c.Branch {
		return true, ErrConflict
	}
	if candidate.DestinationConnectionID != "" {
		destination, ok := m.store.DB().Connections[candidate.DestinationConnectionID]
		if !ok || destination.Paused || destination.ConfigRevision != destinationRevision {
			return true, ErrConflict
		}
	}
	for _, move := range m.store.DB().Moves {
		if move.ID != candidate.ID || move.SourceConnectionID != c.ID || (move.State != "waiting_source_delete" && move.State != "deleting_source") {
			continue
		}
		// Re-evaluate the complete local invariants immediately before preparing.
		db := m.store.Clone()
		reconcileMoves(&db)
		move = db.Moves[move.ID]
		if move.State != "waiting_source_delete" && move.State != "deleting_source" {
			return true, m.store.Commit(db)
		}
		if err := m.store.VerifyBlobs(move.Assets); err != nil {
			return true, err
		}
		original := db.Articles[move.SourceID+"/"+move.SourcePath]
		previousState := move.State
		prepared := false
		prepare := func(commit string) error {
			move.State, move.PreparedDeletion = "deleting_source", commit
			db.Moves[move.ID] = move
			if err := m.store.Commit(db); err != nil {
				return err
			}
			prepared = true
			return nil
		}
		_, err := m.github.Remove(ctx, c.Repository, c.Branch, head, tree, move.SourcePath, original.Versions[move.SourceRevision-1].Text, token, prepare)
		var remote *github.RemoteError
		// A definitive rejection of the first ref update cannot have changed the
		// branch. Do not clear an earlier uncertain attempt on a later rejection.
		if prepared && previousState == "waiting_source_delete" && errors.As(err, &remote) && (remote.Status == 409 || remote.Status == 422) {
			move.State, move.PreparedDeletion = "waiting_source_delete", ""
			db.Moves[move.ID] = move
			if commitErr := m.store.Commit(db); commitErr != nil {
				return true, commitErr
			}
		}
		if err == nil {
			m.Wake()
		}
		return true, err
	}
	return false, nil
}
func (m *SyncManager) pushPending(ctx context.Context, c GitHubConnection, head, treeSHA, token string) error {
	jobs := m.store.jobs()
	uncertain := map[string]int{}
	for _, job := range jobs {
		if job.SourceID == c.ID && job.PreparedCommit != "" && job.State != "complete" && job.State != "superseded" {
			if previous, ok := uncertain[job.Path]; !ok || job.Revision < previous {
				uncertain[job.Path] = job.Revision
			}
		}
	}
	for _, job := range jobs {
		if job.SourceID != c.ID || job.State == "complete" || job.State == "superseded" {
			continue
		}
		// A later revision must not overtake an unresolved prepared ref update.
		if revision, ok := uncertain[job.Path]; ok && job.Revision > revision {
			continue
		}
		if job.State == "conflict" {
			continue
		}
		if !c.Write {
			if err := m.store.setJobFor(c, job, "blocked", "write_not_enabled", ""); err != nil {
				return err
			}
			continue
		}
		if due, e := time.Parse(time.RFC3339Nano, job.NextAttempt); e == nil && due.After(time.Now()) {
			continue
		}
		m.store.RLock()
		a, ok := m.store.DB().Articles[c.ID+"/"+job.Path]
		current, connected := m.store.DB().Connections[c.ID]
		m.store.RUnlock()
		if !ok || !connected || current.Paused || current.ConfigRevision != c.ConfigRevision {
			return ErrConflict
		}
		if job.Revision > len(a.Versions) || a.Versions[job.Revision-1].SHA != job.SHA {
			return ErrInvalid
		}
		if secretContent.MatchString(a.Versions[job.Revision-1].Text) {
			if err := m.store.setJobFor(c, job, "blocked", "credential_like_content", ""); err != nil {
				return err
			}
			continue
		}
		if job.Visibility != "public" && !c.Private {
			if err := m.store.setJobFor(c, job, "blocked", "publication_confirmation_required", ""); err != nil {
				return err
			}
			continue
		}
		pending := false
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				pending = true
				break
			}
		}
		if pending {
			if err := m.store.setJobFor(c, job, "conflict", "source_conflict", ""); err != nil {
				return err
			}
			continue
		}
		prepared := func(commit string) error {
			return m.store.prepareJob(job.ID, commit, c)
		}
		if job.Visibility != "public" {
			info, e := m.github.Repository(ctx, c.Repository, token)
			if e != nil {
				return e
			}
			if !*info.Private {
				if err := m.store.setJobFor(c, job, "blocked", "publication_confirmation_required", ""); err != nil {
					return err
				}
				return &github.RemoteError{Code: "publication_confirmation_required"}
			}
		}
		var e error
		directoryIndex := a.CopiedFrom != nil && a.CopiedFrom.DirectoryIndex
		if len(job.Assets) == 0 && len(job.Links) == 0 && len(job.Bundle) == 0 && !directoryIndex {
			_, e = m.github.Push(ctx, c.Repository, c.Branch, head, treeSHA, job.Path, a.Versions[job.Revision-1].Text, token, prepared)
		} else {
			var files map[string][]byte
			files, e = m.pendingBundle(ctx, c, head, treeSHA, token, job, a)
			if e == nil && bundleHasCredentialLikeText(files) {
				if err := m.store.setJobFor(c, job, "blocked", "credential_like_content", ""); err != nil {
					return err
				}
				continue
			}
			if e == nil {
				_, e = m.github.PushFiles(ctx, c.Repository, c.Branch, head, treeSHA, files, token, prepared)
			}
		}
		if e != nil {
			if err := m.store.setJobFor(c, job, "retry", github.ErrorCode(e), ""); err != nil {
				return err
			}
			return e
		}
		// Do not optimistically acknowledge the remote update. The next pull must
		// observe the actual blob, which also recovers a lost update response.
		if err := m.store.setJobFor(c, job, "prepared", "", ""); err != nil {
			return err
		}
		m.Wake()
		return nil // one commit per source per pass; refresh before the next write
	}
	return nil
}

func bundleHasCredentialLikeText(files map[string][]byte) bool {
	for name, content := range files {
		ext := strings.ToLower(path.Ext(name))
		if (ext == ".md" || ext == ".markdown") && secretContent.Match(content) {
			return true
		}
	}
	return false
}

// Refuse to overwrite pre-existing files while composing the article and its
// images. The subsequent exact-head update makes this check race-safe.
func (m *SyncManager) pendingBundle(ctx context.Context, c GitHubConnection, head, treeSHA, token string, job SyncJob, article Article) (map[string][]byte, error) {
	m.store.RLock()
	source, ok := m.store.DB().Sources[c.ID]
	m.store.RUnlock()
	if !ok || source.ImportPartial {
		return nil, ErrConflict
	}
	tree, err := m.github.Tree(ctx, c.Repository, head, token)
	if err != nil {
		return nil, err
	}
	if tree.SHA != treeSHA {
		return nil, ErrConflict
	}
	linked, err := m.store.transfer().LinkContents(c.ID, job.Links)
	if err != nil {
		return nil, err
	}
	for name, text := range linked {
		if _, err = github.VerifiedTreeFile(tree, name, text); err != nil {
			return nil, err
		}
	}
	entries := map[string]github.TreeEntry{}
	count := 0
	total := int64(0)
	for _, entry := range tree.Tree {
		if _, exists := entries[entry.Path]; exists {
			return nil, ErrInvalid
		}
		entries[entry.Path] = entry
		if entry.Type != "blob" || !importablePath(entry.Path, source.Kind) {
			continue
		}
		ext := strings.ToLower(path.Ext(entry.Path))
		markdown := ext == ".md" || ext == ".markdown"
		image := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"
		if !markdown && !image {
			continue
		}
		if (entry.Mode != "100644" && entry.Mode != "100755") || entry.Size < 0 || entry.Size > 4<<20 || (markdown && entry.Size > maxArticleBytes) {
			return nil, ErrConflict
		}
		count++
		total += entry.Size
	}
	if job.Revision < 1 || job.Revision > len(article.Versions) {
		return nil, ErrInvalid
	}
	files := map[string][]byte{job.Path: []byte(article.Versions[job.Revision-1].Text)}
	m.store.RLock()
	bundle, bundleErr := bundleContentsLocked(*m.store.DB(), job)
	m.store.RUnlock()
	if bundleErr != nil {
		return nil, bundleErr
	}
	for name, value := range bundle {
		if name == job.Path {
			return nil, ErrConflict
		}
		files[name] = []byte(value)
	}
	for name, ref := range job.Assets {
		if _, exists := files[name]; exists {
			return nil, ErrConflict
		}
		data, e := m.store.readAsset(ref)
		if e != nil {
			return nil, e
		}
		files[name] = data
	}
	// Do not introduce a new README over an index added remotely since the
	// transfer was planned. The checked tree is bound to the CAS update.
	m.store.RLock()
	shadowed := false
	for name := range files {
		a, exists := m.store.DB().Articles[c.ID+"/"+name]
		if !exists || a.CopiedFrom == nil || !a.CopiedFrom.DirectoryIndex {
			continue
		}
		if _, hasReadme := entries[name]; !hasReadme {
			_, hasIndex := entries[path.Join(path.Dir(name), "index.md")]
			if hasIndex {
				shadowed = true
				break
			}
		}
	}
	m.store.RUnlock()
	if shadowed {
		return nil, ErrConflict
	}
	for name, data := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if e, exists := entries[parent]; exists && e.Type != "tree" {
				return nil, ErrConflict
			}
		}
		if entry, exists := entries[name]; exists {
			if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") || !commitID.MatchString(entry.SHA) {
				return nil, ErrConflict
			}
			if github.BlobDigest(data, len(entry.SHA)) == entry.SHA {
				delete(files, name)
				continue
			}
			if name != job.Path || article.UpstreamHash == "" {
				return nil, ErrConflict
			}
			matchesBase := false
			for _, rev := range article.Versions {
				if rev.SHA == article.UpstreamHash && github.BlobDigest([]byte(rev.Text), len(entry.SHA)) == entry.SHA {
					matchesBase = true
					break
				}
			}
			if !matchesBase {
				return nil, ErrConflict
			}
		} else {
			count++
			total += int64(len(data))
		}
	}
	if count > 2000 || total > importer.MaxBytes {
		return nil, &github.RemoteError{Code: "repository_limit"}
	}
	if len(files) == 0 {
		return nil, ErrConflict
	}
	return files, nil
}
