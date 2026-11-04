package domain

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// EnqueueWrite queues revision rev of a for writing to GitHub. It supersedes
// the article's unprepared pending jobs and keeps the image and link
// dependencies of queued jobs that rev still references.
func EnqueueWrite(db *Database, a Article, rev Revision, now time.Time) {
	if db.Outbox == nil {
		db.Outbox = map[string]SyncJob{}
	}
	retained := map[string]BlobRef{}
	retainedLinks := map[string]string{}
	privateDependencies := false
	for id, job := range db.Outbox {
		if job.SourceID == a.SourceID && job.Path == a.Path && !JobTerminal(job.State) {
			inherited := RetainedPendingAssets(rev.Text, a.Path, job.Assets)
			inheritedLinks := RetainedPendingLinks(rev.Text, a.Path, job.Links)
			if (len(inherited) > 0 || len(inheritedLinks) > 0) && job.Visibility != "public" {
				privateDependencies = true
			}
			for p, ref := range inherited {
				retained[p] = ref
			}
			for p, sha := range inheritedLinks {
				retainedLinks[p] = sha
			}
		}
		if job.SourceID == a.SourceID &&
			job.Path == a.Path &&
			job.PreparedCommit == "" &&
			len(job.Bundle) == 0 &&
			(job.State == "pending" || job.State == "retry" || (job.State == "blocked" && job.LastError == "credential_like_content")) {
			job.State = "superseded"
			db.Outbox[id] = job
		}
	}
	id := writeJobID(a, rev)
	visibility := "private"
	if !privateDependencies && SourcePublic(*db, db.Sources[a.SourceID], now) {
		visibility = "public"
	}
	if (len(retained) > 0 || len(retainedLinks) > 0) && HasRootReference(rev.Text) && db.Version < 12 {
		db.Version = 12
	}
	db.Outbox[id] = SyncJob{
		ID:         id,
		SourceID:   a.SourceID,
		Path:       a.Path,
		Revision:   rev.Number,
		SHA:        rev.SHA,
		State:      "pending",
		Created:    rev.Created,
		Visibility: visibility,
		Assets:     retained,
		Links:      retainedLinks,
	}
}

// writeJobID is the outbox ID of the write of revision rev of a.
func writeJobID(a Article, rev Revision) string {
	return a.SourceID + "/" + a.Path + "@" + strconv.Itoa(rev.Number)
}

// AddWriteDependencies merges images and link hashes into the job that
// EnqueueWrite queued for revision rev of a, so the remote write carries them.
// A name already queued with another reference or hash is a conflict.
func AddWriteDependencies(db *Database, a Article, rev Revision, assets map[string]BlobRef, links map[string]string) error {
	id := writeJobID(a, rev)
	job := db.Outbox[id]
	if job.Assets == nil {
		job.Assets = map[string]BlobRef{}
	}
	for name, ref := range assets {
		if old, ok := job.Assets[name]; ok && old != ref {
			return ErrConflict
		}
		job.Assets[name] = ref
	}
	if job.Links == nil {
		job.Links = map[string]string{}
	}
	for name, sha := range links {
		if old, ok := job.Links[name]; ok && old != sha {
			return ErrConflict
		}
		job.Links[name] = sha
	}
	db.Outbox[id] = job
	return nil
}

// EnqueueBundle queues revision root of a as EnqueueWrite does and sets the
// job's images, link hashes and bundled articles, which the remote write
// commits together with root.
func EnqueueBundle(db *Database, a Article, root Revision, assets map[string]BlobRef, links, bundle map[string]string, now time.Time) {
	EnqueueWrite(db, a, root, now)
	id := writeJobID(a, root)
	job := db.Outbox[id]
	job.Assets, job.Links, job.Bundle = assets, links, bundle
	db.Outbox[id] = job
}

// BundleLocks reports whether an active bundled outbox job of sourceID
// writes or depends on article p.
func BundleLocks(db Database, sourceID, p string) bool {
	for _, job := range db.Outbox {
		if (len(job.Bundle) == 0 && !job.ScopeBundle) || job.SourceID != sourceID || JobTerminal(job.State) {
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
		if job.ScopeBundlePaths[p] {
			return true
		}
	}
	return false
}

// Caller holds the store lock or uses a scoped snapshot.
func ConflictResolutionBlocked(db Database, sourceID, p string) bool {
	for _, job := range db.Outbox {
		if job.SourceID != sourceID || JobTerminal(job.State) {
			continue
		}
		if job.Path == p && (job.PreparedCommit != "" || job.ScopePrepared || len(job.Bundle) != 0 || job.ScopeBundle) {
			return true
		}
		if len(job.Bundle) != 0 {
			if _, ok := job.Bundle[p]; ok {
				return true
			}
			if _, ok := job.Links[p]; ok {
				return true
			}
		}
		if job.ScopeBundle && job.ScopeBundlePaths[p] {
			return true
		}
	}
	return false
}

// Keep only images still referenced by a later revision of a queued article.
func RetainedPendingAssets(value, articlePath string, candidates map[string]BlobRef) map[string]BlobRef {
	if len(candidates) == 0 {
		return nil
	}
	refs := map[string]BlobRef{}
	root := Markdown.Parser().Parse(text.NewReader([]byte(value)))
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		image, ok := node.(*ast.Image)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(image.Destination))
		if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
			return ast.WalkContinue, nil
		}
		name, valid := RepositoryTarget(articlePath, u.Path)
		if ref, found := candidates[name]; valid && found {
			refs[name] = ref
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// RetainedPendingLinks keeps only the link dependencies still referenced by
// a later revision of a queued article.
func RetainedPendingLinks(value, articlePath string, candidates map[string]string) map[string]string {
	if len(candidates) == 0 {
		return nil
	}
	refs := map[string]string{}
	root := Markdown.Parser().Parse(text.NewReader([]byte(value)))
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		link, ok := node.(*ast.Link)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(link.Destination))
		if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
			return ast.WalkContinue, nil
		}
		name, valid := RepositoryTarget(articlePath, u.Path)
		if _, found := candidates[name]; !valid || !found {
			name, valid = DirectoryReadmeTarget(articlePath, u.Path)
		}
		if sha, found := candidates[name]; valid && found {
			refs[name] = sha
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// Earlier binaries do not retain repository-root references in pending work.
// Such work requires a version gate before it is committed to disk.
func HasRootReference(value string) bool {
	root := Markdown.Parser().Parse(text.NewReader([]byte(value)))
	found := false
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var destination []byte
		switch n := node.(type) {
		case *ast.Link:
			destination = n.Destination
		case *ast.Image:
			destination = n.Destination
		default:
			return ast.WalkContinue, nil
		}
		u, err := url.Parse(string(destination))
		if err == nil && u.Scheme == "" && u.Host == "" && strings.HasPrefix(u.Path, "/") {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

// SupersedePendingWrites marks every unfinished queued write of path p
// superseded, after an upstream revision was chosen over them.
func SupersedePendingWrites(db *Database, sourceID, p string) {
	for id, job := range db.Outbox {
		if job.SourceID == sourceID && job.Path == p && job.State != "complete" {
			job.State = "superseded"
			db.Outbox[id] = job
		}
	}
}
