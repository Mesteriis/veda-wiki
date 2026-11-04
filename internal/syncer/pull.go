package syncer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// cachedBlob returns a verified Git blob from the local object cache, from
// identical local private content, or from GitHub, and caches it.
func (m *Manager) cachedBlob(ctx context.Context, c domain.GitHubConnection, entry github.TreeEntry, token string) ([]byte, error) {
	if !domain.ValidCommitID(entry.SHA) {
		return nil, domain.ErrInvalid
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
		var articles []string
		var images []domain.BlobRef
		m.read(func(db *domain.Database) {
			for id, src := range db.Sources {
				if src.Visibility != "private" || src.ConnectionID != "" {
					continue
				}
				if a, ok := db.Articles[id+"/"+entry.Path]; ok && !a.Deleted && len(a.Versions) > 0 {
					articles = append(articles, a.Versions[len(a.Versions)-1].Text)
				}
				if ref, ok := src.Assets[entry.Path]; ok {
					images = append(images, ref)
				}
			}
		})
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
				image, err := m.readAsset(ref)
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
			if err := store.WritePrivateFile(dir, entry.SHA, local); err != nil {
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
	if e = store.WritePrivateFile(dir, entry.SHA, b); e != nil {
		return nil, e
	}
	return b, nil
}

// readAsset returns a stored image after checking it against ref.
func (m *Manager) readAsset(ref domain.BlobRef) ([]byte, error) {
	content, err := m.store.ReadBlob(ref.SHA)
	if err != nil {
		return nil, err
	}
	if verified, check := domain.ImageRef(content); check != nil || verified != ref {
		return nil, domain.ErrInvalid
	}
	return content, nil
}

// remoteBatch builds the import batch of a repository tree at head.
func (m *Manager) remoteBatch(ctx context.Context, c domain.GitHubConnection, head, token string, tree github.Tree) (importer.Batch, error) {
	batch := importer.Batch{SourceID: c.ID, Kind: "program", Files: map[string]string{}, Assets: map[string][]byte{}, Commit: head, Complete: true}
	entries := map[string]github.TreeEntry{}
	hasDocs := false
	for _, entry := range tree.Tree {
		if _, exists := entries[entry.Path]; exists {
			return batch, domain.ErrInvalid
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
		var existing domain.Source
		var found bool
		m.read(func(db *domain.Database) { existing, found = db.Sources[c.ID] })
		if !found || existing.Kind != "program" {
			return batch, domain.ErrNoDocs
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
		if entry.Type != "blob" || !domain.ImportablePath(p, batch.Kind) {
			continue
		}
		ext := strings.ToLower(path.Ext(p))
		markdown := ext == ".md" || ext == ".markdown"
		image := ext == ".png" || ext == ".jpeg" || ext == ".jpg" || ext == ".gif" || ext == ".webp"
		if !markdown && !image {
			continue
		}
		if entry.Mode != "100644" && entry.Mode != "100755" || entry.Size > 4<<20 || markdown && entry.Size > domain.MaxArticleBytes {
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
			if !utf8.Valid(b) || domain.CredentialLikeBytes(b) {
				batch.Withheld++
				batch.Complete = false
				continue
			}
			batch.Files[p] = string(b)
		} else {
			if _, e = domain.ImageRef(b); e != nil {
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
	var src domain.Source
	var exists bool
	m.read(func(db *domain.Database) { src, exists = db.Sources[c.ID] })
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

// syncSource verifies one connection, imports a changed head and then sends
// pending writes.
func (m *Manager) syncSource(ctx context.Context, c domain.GitHubConnection) {
	fail := func(e error) {
		m.connectionStatusFor(c, "error", "unknown", github.ErrorCode(e), nil, github.RetryAt(e))
	}
	secret, _, e := m.secret(c.AccountID)
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
		fail(domain.ErrConflict)
		return
	}
	visibilityChanged := c.Private != *info.Private
	c.Private = *info.Private
	if e = m.connectionStatusFor(c, "syncing", "verified", "", &info, time.Time{}); e != nil {
		return
	}
	head, e := m.github.Head(ctx, c.Repository, c.Branch, token)
	if e != nil {
		fail(e)
		return
	}
	if head != c.LastPull {
		if e = m.observeChangedHead(c, head); e != nil {
			return
		}
	}
	var source domain.Source
	var exists bool
	m.read(func(db *domain.Database) { source, exists = db.Sources[c.ID] })
	matchingVisibility := exists && (source.Visibility == "private") == c.Private
	repairingAssets := false
	if !exists && c.State == "skipped" && !visibilityChanged && head == c.LastPull && domain.ValidCommitID(c.TreeSHA) {
		m.connectionStatusFor(c, "skipped", "verified", "", &info, time.Time{})
		return
	}
	if matchingVisibility && !visibilityChanged && head == c.LastPull && domain.ValidCommitID(c.TreeSHA) {
		if err := m.observeGitHubHead(c, head, c.TreeSHA); err == nil {
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
	if errors.Is(e, domain.ErrNoDocs) {
		if !exists {
			if err := m.recordSkippedRepository(c, head, tree.SHA); err != nil && !errors.Is(err, domain.ErrConflict) {
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
	if _, e = m.importer.Apply(batch, remoteProof(c, visibility, batch.Commit, tree.SHA), false); e != nil {
		if errors.Is(e, importer.ErrAssetUnavailable) {
			fail(&github.RemoteError{Code: "asset_unavailable"})
			return
		}
		fail(e)
		return
	}
	if repairingAssets {
		if err := m.observeGitHubHead(c, head, tree.SHA); errors.Is(err, importer.ErrAssetUnavailable) {
			fail(&github.RemoteError{Code: "asset_unavailable"})
			return
		} else if err != nil {
			return
		}
		// A move or push must revalidate its dependencies in a later cycle.
		state, code := m.sourceSyncState(c.ID)
		m.connectionStatusFor(c, state, "verified", code, &info, time.Time{})
		return
	}
	m.syncWrites(ctx, c, head, tree.SHA, token, info)
}
