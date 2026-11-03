package domain

import (
	"encoding/json"
	"errors"
)

// Rules applied in the transaction that imports an observed repository pull.

// AdoptionSnapshot digests exactly the local state whose baselines change when
// a private source is adopted by a GitHub connection. Pending operations and
// tombstones need separate reconciliation before the source can be adopted.
func AdoptionSnapshot(db Database, id string) (string, error) {
	src, ok := db.Sources[id]
	if !ok || src.ConnectionID != "" || src.Visibility != "private" {
		return "", ErrConflict
	}
	if _, exists := db.Connections[id]; exists {
		return "", ErrConflict
	}
	articles := map[string]Article{}
	for key, a := range db.Articles {
		if a.SourceID != id {
			continue
		}
		if a.Deleted {
			return "", ErrConflict
		}
		for _, c := range a.Conflicts {
			if c.Resolution == "" {
				return "", ErrConflict
			}
		}
		articles[key] = a
	}
	for _, j := range db.Outbox {
		if j.SourceID == id && j.State != "complete" && j.State != "superseded" {
			return "", ErrConflict
		}
	}
	for _, m := range db.Moves {
		if m.SourceID != id && m.DestinationID != id {
			continue
		}
		switch m.State {
		case "complete", "cancelled", "source_changed", "destination_changed":
		default:
			return "", ErrConflict
		}
	}
	raw, err := json.Marshal(struct {
		Source   Source
		Articles map[string]Article
	}{src, articles})
	if err != nil {
		return "", errors.New("source snapshot unavailable")
	}
	return Digest(raw), nil
}

// AdoptConnection records conn for the source with the same ID. A local
// checkout's old import base is not a GitHub merge base, so it is cleared:
// differing files become conflicts and absent files stay local.
func AdoptConnection(db *Database, conn GitHubConnection) {
	for key, article := range db.Articles {
		if article.SourceID == conn.ID {
			article.UpstreamHash = ""
			db.Articles[key] = article
		}
	}
	db.Connections[conn.ID] = conn
}

// RecordPull stores the commit and tree a connection was last imported from.
func RecordPull(db *Database, connectionID, commit, treeSHA string) {
	conn := db.Connections[connectionID]
	conn.LastPull = commit
	conn.TreeSHA = treeSHA
	db.Connections[conn.ID] = conn
}

// ObservedWrite completes the queued writes of path p that the observed
// content satisfies and reports whether any did.
func ObservedWrite(db *Database, sourceID, p, hash, commit string, assets map[string]BlobRef, files map[string]string, complete bool) bool {
	matched := 0
	firstPrepared := 0
	matches := map[string]bool{}
	for _, job := range db.Outbox {
		if job.SourceID == sourceID && job.Path == p && job.PreparedCommit != "" && job.State != "complete" && job.State != "superseded" && (firstPrepared == 0 || job.Revision < firstPrepared) {
			firstPrepared = job.Revision
		}
	}
	for id, job := range db.Outbox {
		if job.SourceID != sourceID || job.Path != p {
			continue
		}
		linksMatch := true
		for name, sha := range job.Links {
			if text, exists := files[name]; !exists || Digest([]byte(text)) != sha {
				linksMatch = false
				break
			}
		}
		for name, sha := range job.Bundle {
			if text, exists := files[name]; !exists || Digest([]byte(text)) != sha {
				linksMatch = false
				break
			}
		}
		eligibleState := job.State != "complete" && job.State != "superseded" && (job.State != "conflict" || (job.PreparedCommit != "" && job.Revision == firstPrepared))
		eligible := job.SourceID == sourceID && job.Path == p && job.SHA == hash && eligibleState && ((len(job.Assets) == 0 && len(job.Links) == 0 && len(job.Bundle) == 0) || (complete && AssetsPresent(Source{Assets: assets}, job.Assets) && linksMatch))
		matches[id] = eligible
		if eligible && job.Revision > matched && (firstPrepared == 0 || job.Revision == firstPrepared) {
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
		} else if firstPrepared != 0 && job.Revision > matched && job.PreparedCommit == "" && matches[id] {
			// The same observed content also satisfies a later unprepared edit.
			job.State, job.ObservedCommit, job.LastError = "complete", commit, ""
			db.Outbox[id] = job
		}
	}
	return true
}

// PreparedWriteDiffers reports whether an older prepared write of path p
// carries other content. If a later local revision matches an external
// update, that write still cannot be silently superseded or retried over it.
func PreparedWriteDiffers(db Database, sourceID, p, hash string) bool {
	for _, job := range db.Outbox {
		if job.SourceID == sourceID && job.Path == p && job.PreparedCommit != "" && job.State != "complete" && job.State != "superseded" && job.SHA != hash {
			return true
		}
	}
	return false
}

// ConfirmSourceDeletions settles moves whose source deletion was prepared on
// connectionID, given a complete verified pull with files. A prepared ref
// update may have succeeded even if its response was lost; only such a pull
// can establish that the source is absent. assetsAvailable reports whether
// every blob a move depends on is stored and intact.
func ConfirmSourceDeletions(db *Database, connectionID string, files map[string]string, assetsAvailable func(map[string]BlobRef) bool) {
	for id, move := range db.Moves {
		if move.State != "deleting_source" || move.SourceConnectionID != connectionID {
			continue
		}
		if _, present := files[move.SourcePath]; present {
			continue
		}
		original := db.Articles[move.SourceID+"/"+move.SourcePath]
		if !original.Deleted {
			continue
		}
		target, exists := db.Articles[move.DestinationID+"/"+move.DestinationPath]
		destination, dstOK := db.Sources[move.DestinationID]
		valid := exists && dstOK && !target.Deleted && len(target.Versions) == 1 && target.Versions[0].SHA == move.SHA && destination.ConnectionID == move.DestinationConnectionID && TranslationFingerprint(original.Translation) == move.TranslationFingerprint && TranslationFingerprint(target.Translation) == TranslationFingerprint(MovedTranslation(original.Translation)) && AssetsPresent(destination, move.Assets) && LinksPresent(*db, move.DestinationID, move.Links)
		if !assetsAvailable(move.Assets) {
			valid = false
		}
		for _, conflict := range target.Conflicts {
			if conflict.Resolution == "" {
				valid = false
			}
		}
		if move.DestinationConnectionID != "" {
			connection, ok := db.Connections[move.DestinationConnectionID]
			valid = valid && ok && connection.RepositoryID == move.DestinationRepositoryID && connection.Branch == move.DestinationBranch
		}
		if !valid {
			move.State = "destination_changed"
		} else {
			move.State = "complete"
		}
		db.Moves[id] = move
	}
}
