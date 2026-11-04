package syncer

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// PublishJob approves publishing the expected revision of a queued write,
// and of its bundle, to a public repository. It requires confirmation.
func (m *Manager) PublishJob(id string, expected int, confirm bool) error {
	if !confirm {
		return domain.ErrInvalid
	}
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		job, ok := db.Outbox[id]
		if !ok {
			return domain.ErrNotFound
		}
		a, ok := db.Articles[job.SourceID+"/"+job.Path]
		src := db.Sources[job.SourceID]
		if !ok || a.Deleted || expected != domain.LatestRevision(a) || job.Revision != expected || job.State == "complete" || job.State == "superseded" || !domain.SourcePublic(*db, src, time.Now()) {
			return domain.ErrConflict
		}
		current, _ := domain.RevisionByNumber(a, expected)
		if domain.CredentialLike(current.Text) {
			return domain.ErrInvalid
		}
		for name, sha := range job.Bundle {
			linked, ok := db.Articles[job.SourceID+"/"+name]
			if !ok || linked.Deleted || !domain.LastHasSHA(linked, sha) || domain.CredentialLike(domain.LastText(linked)) {
				return domain.ErrInvalid
			}
		}
		job.Visibility = "public"
		job.State = "pending"
		job.LastError = ""
		job.NextAttempt = ""
		published := current
		published.Number = 0
		published.Created = time.Now().UTC().Format(time.RFC3339Nano)
		a.Published = &published
		a.PublishedLocal = true
		db.Articles[job.SourceID+"/"+job.Path] = a
		for name := range job.Bundle {
			key := job.SourceID + "/" + name
			linked := db.Articles[key]
			published, _ := domain.LastRevision(linked)
			published.Number = 0
			published.Created = time.Now().UTC().Format(time.RFC3339Nano)
			linked.Published, linked.PublishedLocal = &published, true
			db.Articles[key] = linked
		}
		db.Outbox[id] = job
		conn := db.Connections[src.ConnectionID]
		conn.NextAttempt = ""
		if conn.State != "syncing" {
			conn.State = "pending"
		}
		db.Connections[conn.ID] = conn
		return nil
	})
}

// A prepared ref may have reached GitHub even after a lost response. Only a
// never-prepared group can be cancelled without risking an uncertain update.
func (m *Manager) CancelBundle(id string, confirm bool) error {
	if !confirm {
		return domain.ErrInvalid
	}
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		job, ok := db.Outbox[id]
		if !ok {
			return domain.ErrNotFound
		}
		if len(job.Bundle) == 0 || job.PreparedCommit != "" || job.State == "complete" || job.State == "superseded" {
			return domain.ErrConflict
		}
		job.State = "superseded"
		job.LastError, job.NextAttempt = "", ""
		db.Outbox[id] = job
		domain.ReconcileMoves(db)
		return nil
	})
}
