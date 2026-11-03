package syncer

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
)

// syncWrites sends a pending move deletion or else the next pending write of
// a verified connection, then records the connection status.
func (m *Manager) syncWrites(ctx context.Context, c domain.GitHubConnection, head, treeSHA, token string, info github.RepositoryInfo) {
	attempted, moveErr := m.deletePendingMove(ctx, c, head, token)
	if attempted {
		if moveErr != nil {
			m.connectionStatusFor(c, "error", "verified", github.ErrorCode(moveErr), &info, github.RetryAt(moveErr))
		}
		return
	}
	if err := m.pushPending(ctx, c, head, treeSHA, token); err != nil {
		m.connectionStatusFor(c, "error", "verified", github.ErrorCode(err), &info, github.RetryAt(err))
		return
	}
	if moveErr != nil {
		m.connectionStatusFor(c, "error", "verified", github.ErrorCode(moveErr), &info, github.RetryAt(moveErr))
		return
	}
	state, code := m.sourceSyncState(c.ID)
	m.connectionStatusFor(c, state, "verified", code, &info, time.Time{})
}

// pushPending sends at most one queued write of the source per pass. The
// prepared commit is stored before the remote ref update, and the write is
// acknowledged only by a later pull.
func (m *Manager) pushPending(ctx context.Context, c domain.GitHubConnection, head, treeSHA, token string) error {
	jobs := m.Jobs()
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
			if err := m.setJobFor(c, job, "blocked", "write_not_enabled", ""); err != nil {
				return err
			}
			continue
		}
		if due, e := time.Parse(time.RFC3339Nano, job.NextAttempt); e == nil && due.After(time.Now()) {
			continue
		}
		var a domain.Article
		var current domain.GitHubConnection
		var ok, connected bool
		m.read(func(db *domain.Database) {
			a, ok = db.Articles[c.ID+"/"+job.Path]
			current, connected = db.Connections[c.ID]
		})
		if !ok || !connected || current.Paused || current.ConfigRevision != c.ConfigRevision {
			return domain.ErrConflict
		}
		if job.Revision > len(a.Versions) || a.Versions[job.Revision-1].SHA != job.SHA {
			return domain.ErrInvalid
		}
		if domain.CredentialPattern.MatchString(a.Versions[job.Revision-1].Text) {
			if err := m.setJobFor(c, job, "blocked", "credential_like_content", ""); err != nil {
				return err
			}
			continue
		}
		if job.Visibility != "public" && !c.Private {
			if err := m.setJobFor(c, job, "blocked", "publication_confirmation_required", ""); err != nil {
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
			if err := m.setJobFor(c, job, "conflict", "source_conflict", ""); err != nil {
				return err
			}
			continue
		}
		prepared := func(commit string) error {
			return m.prepareJob(job.ID, commit, c)
		}
		if job.Visibility != "public" {
			info, e := m.github.Repository(ctx, c.Repository, token)
			if e != nil {
				return e
			}
			if !*info.Private {
				if err := m.setJobFor(c, job, "blocked", "publication_confirmation_required", ""); err != nil {
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
				if err := m.setJobFor(c, job, "blocked", "credential_like_content", ""); err != nil {
					return err
				}
				continue
			}
			if e == nil {
				_, e = m.github.PushFiles(ctx, c.Repository, c.Branch, head, treeSHA, files, token, prepared)
			}
		}
		if e != nil {
			if err := m.setJobFor(c, job, "retry", github.ErrorCode(e), ""); err != nil {
				return err
			}
			return e
		}
		// Do not optimistically acknowledge the remote update. The next pull must
		// observe the actual blob, which also recovers a lost update response.
		if err := m.setJobFor(c, job, "prepared", "", ""); err != nil {
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
		if (ext == ".md" || ext == ".markdown") && domain.CredentialPattern.Match(content) {
			return true
		}
	}
	return false
}

// bundleContents checks the revisions that must be sent together. Never
// substitute a newer local revision.
func bundleContents(db domain.Database, job domain.SyncJob) (map[string]string, error) {
	result := make(map[string]string, len(job.Bundle))
	for name, sha := range job.Bundle {
		a, ok := db.Articles[job.SourceID+"/"+name]
		if !ok || a.Deleted || len(a.Versions) == 0 || a.Versions[len(a.Versions)-1].SHA != sha {
			return nil, domain.ErrConflict
		}
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				return nil, domain.ErrConflict
			}
		}
		if job.Visibility == "public" && (a.Published == nil || a.Published.SHA != sha) {
			return nil, domain.ErrConflict
		}
		result[name] = a.Versions[len(a.Versions)-1].Text
	}
	return result, nil
}

// Refuse to overwrite pre-existing files while composing the article and its
// images. The subsequent exact-head update makes this check race-safe.
func (m *Manager) pendingBundle(ctx context.Context, c domain.GitHubConnection, head, treeSHA, token string, job domain.SyncJob, article domain.Article) (map[string][]byte, error) {
	var source domain.Source
	var ok bool
	m.read(func(db *domain.Database) { source, ok = db.Sources[c.ID] })
	if !ok || source.ImportPartial {
		return nil, domain.ErrConflict
	}
	tree, err := m.github.Tree(ctx, c.Repository, head, token)
	if err != nil {
		return nil, err
	}
	if tree.SHA != treeSHA {
		return nil, domain.ErrConflict
	}
	linked, err := m.transfer.LinkContents(c.ID, job.Links)
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
			return nil, domain.ErrInvalid
		}
		entries[entry.Path] = entry
		if entry.Type != "blob" || !domain.ImportablePath(entry.Path, source.Kind) {
			continue
		}
		ext := strings.ToLower(path.Ext(entry.Path))
		markdown := ext == ".md" || ext == ".markdown"
		image := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif"
		if !markdown && !image {
			continue
		}
		if (entry.Mode != "100644" && entry.Mode != "100755") || entry.Size < 0 || entry.Size > 4<<20 || (markdown && entry.Size > domain.MaxArticleBytes) {
			return nil, domain.ErrConflict
		}
		count++
		total += entry.Size
	}
	if job.Revision < 1 || job.Revision > len(article.Versions) {
		return nil, domain.ErrInvalid
	}
	files := map[string][]byte{job.Path: []byte(article.Versions[job.Revision-1].Text)}
	var bundle map[string]string
	var bundleErr error
	m.read(func(db *domain.Database) { bundle, bundleErr = bundleContents(*db, job) })
	if bundleErr != nil {
		return nil, bundleErr
	}
	for name, value := range bundle {
		if name == job.Path {
			return nil, domain.ErrConflict
		}
		files[name] = []byte(value)
	}
	for name, ref := range job.Assets {
		if _, exists := files[name]; exists {
			return nil, domain.ErrConflict
		}
		data, e := m.readAsset(ref)
		if e != nil {
			return nil, e
		}
		files[name] = data
	}
	// Do not introduce a new README over an index added remotely since the
	// transfer was planned. The checked tree is bound to the CAS update.
	shadowed := false
	m.read(func(db *domain.Database) {
		for name := range files {
			a, exists := db.Articles[c.ID+"/"+name]
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
	})
	if shadowed {
		return nil, domain.ErrConflict
	}
	for name, data := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if e, exists := entries[parent]; exists && e.Type != "tree" {
				return nil, domain.ErrConflict
			}
		}
		if entry, exists := entries[name]; exists {
			if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") || !domain.CommitIDPattern.MatchString(entry.SHA) {
				return nil, domain.ErrConflict
			}
			if github.BlobDigest(data, len(entry.SHA)) == entry.SHA {
				delete(files, name)
				continue
			}
			if name != job.Path || article.UpstreamHash == "" {
				return nil, domain.ErrConflict
			}
			matchesBase := false
			for _, rev := range article.Versions {
				if rev.SHA == article.UpstreamHash && github.BlobDigest([]byte(rev.Text), len(entry.SHA)) == entry.SHA {
					matchesBase = true
					break
				}
			}
			if !matchesBase {
				return nil, domain.ErrConflict
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
		return nil, domain.ErrConflict
	}
	return files, nil
}
