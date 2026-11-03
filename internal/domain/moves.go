package domain

import (
	"encoding/json"
)

// Called only inside the same transaction that changes source or destination.
// Connected destinations must be observed in GitHub before source removal.
func ReconcileMoves(db *Database) {
	for id, m := range db.Moves {
		if m.State != "waiting_destination" && m.State != "waiting_source_delete" && m.State != "deleting_source" {
			continue
		}
		source, exists := db.Articles[m.SourceID+"/"+m.SourcePath]
		src, srcOK := db.Sources[m.SourceID]
		if !exists || !srcOK || source.Deleted || TranslationFingerprint(source.Translation) != m.TranslationFingerprint || len(source.Versions) != m.SourceRevision || source.Versions[m.SourceRevision-1].SHA != m.SHA || src.ConnectionID != m.SourceConnectionID || (m.SourceConnectionID == "" && src.ImportGeneration != "") {
			m.State = "source_changed"
			db.Moves[id] = m
			continue
		}
		if m.SourceConnectionID != "" {
			conn, ok := db.Connections[m.SourceConnectionID]
			if !ok || conn.RepositoryID != m.SourceRepositoryID || conn.Branch != m.SourceBranch || source.UpstreamHash != m.SHA {
				m.State = "source_changed"
				db.Moves[id] = m
				continue
			}
			for _, conflict := range source.Conflicts {
				if conflict.Resolution == "" {
					m.State = "source_changed"
					db.Moves[id] = m
					break
				}
			}
			if m.State == "source_changed" {
				continue
			}
		}
		target, exists := db.Articles[m.DestinationID+"/"+m.DestinationPath]
		dst, dstOK := db.Sources[m.DestinationID]
		if !exists || !dstOK || target.Deleted || len(target.Versions) != 1 || target.Versions[0].SHA != m.SHA || TranslationFingerprint(target.Translation) != TranslationFingerprint(MovedTranslation(source.Translation)) || (m.DestinationConnectionID == "" && !AssetsPresent(dst, m.Assets)) || !LocalLinksPresent(*db, m.DestinationID, m.Links) {
			m.State = "destination_changed"
			db.Moves[id] = m
			continue
		}
		conflicting := false
		for _, c := range target.Conflicts {
			if c.Resolution == "" {
				conflicting = true
				break
			}
		}
		if conflicting {
			m.State = "destination_changed"
			db.Moves[id] = m
			continue
		}
		if dst.ConnectionID != m.DestinationConnectionID {
			m.State = "destination_changed"
			db.Moves[id] = m
			continue
		}
		if m.DestinationConnectionID != "" {
			conn, ok := db.Connections[m.DestinationConnectionID]
			if !ok || conn.RepositoryID != m.DestinationRepositoryID || conn.Branch != m.DestinationBranch {
				m.State = "destination_changed"
				db.Moves[id] = m
				continue
			}
			jobID := m.DestinationID + "/" + m.DestinationPath + "@1"
			job, ok := db.Outbox[jobID]
			if !ok || job.State == "superseded" || job.SHA != m.SHA {
				m.State = "destination_changed"
				db.Moves[id] = m
				continue
			}
			if job.State != "complete" {
				continue
			}
			if !AssetsPresent(dst, m.Assets) || !LinksPresent(*db, m.DestinationID, m.Links) {
				m.State = "destination_changed"
				db.Moves[id] = m
				continue
			}
		}
		if m.DestinationConnectionID == "" && !LinksPresent(*db, m.DestinationID, m.Links) {
			m.State = "destination_changed"
			db.Moves[id] = m
			continue
		}
		if m.SourceConnectionID != "" {
			if m.State != "deleting_source" {
				m.State = "waiting_source_delete"
			}
			db.Moves[id] = m
			continue
		}
		source.Deleted = true
		db.Articles[m.SourceID+"/"+m.SourcePath] = source
		m.State = "complete"
		db.Moves[id] = m
	}
}

// A prepared deletion may have reached GitHub even when the response was lost.
// Keep both local copies stable until a complete remote pull resolves it.
func MoveDeletionLocks(db Database, sourceID, p string) bool {
	return BundleLocks(db, sourceID, p) || MoveOperationLocks(db, sourceID, p)
}

// MoveOperationLocks reports whether a move that is deleting its source
// locks article p of sourceID as an endpoint or a link dependency.
func MoveOperationLocks(db Database, sourceID, p string) bool {
	for _, m := range db.Moves {
		if m.State == "deleting_source" {
			if (m.SourceID == sourceID && m.SourcePath == p) || (m.DestinationID == sourceID && m.DestinationPath == p) {
				return true
			}
			if m.DestinationID == sourceID {
				if _, dependency := m.Links[p]; dependency {
					return true
				}
			}
		}
	}
	return false
}

// MoveDeletionLocksConnection reports whether a move that is deleting its
// source involves connection id.
func MoveDeletionLocksConnection(db Database, id string) bool {
	for _, m := range db.Moves {
		if m.State == "deleting_source" && (m.SourceConnectionID == id || m.DestinationConnectionID == id) {
			return true
		}
	}
	return false
}

// TranslationFingerprint returns a digest of tr, or "" for no translation.
func TranslationFingerprint(tr *Translation) string {
	if tr == nil {
		return ""
	}
	b, _ := json.Marshal(tr)
	return Digest(b)
}

// MovedTranslation copies tr for a moved or copied article; the copy needs
// public approval again.
func MovedTranslation(tr *Translation) *Translation {
	if tr == nil {
		return nil
	}
	copy := *tr
	copy.PublicApproved = false
	return &copy
}

// AssetsPresent reports whether source holds every expected image with the
// expected reference.
func AssetsPresent(source Source, expected map[string]BlobRef) bool {
	for name, ref := range expected {
		if actual, ok := source.Assets[name]; !ok || actual != ref {
			return false
		}
	}
	return true
}

// LinksPresent reports whether every expected article of sourceID exists,
// is live and has the expected current revision digest.
func LinksPresent(db Database, sourceID string, expected map[string]string) bool {
	src, ok := db.Sources[sourceID]
	if !ok {
		return false
	}
	for name, sha := range expected {
		a, exists := db.Articles[sourceID+"/"+name]
		if !exists || a.Deleted || len(a.Versions) == 0 || a.Versions[len(a.Versions)-1].SHA != sha {
			return false
		}
		if src.ConnectionID != "" && a.UpstreamHash != sha {
			return false
		}
		if src.Visibility == "public" && (a.Published == nil || a.Published.SHA != sha) {
			return false
		}
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				return false
			}
		}
	}
	return true
}

// Pending remote bundles have not acquired upstream hashes yet. Their local
// revisions must still match until a complete remote pull confirms them.
func LocalLinksPresent(db Database, sourceID string, expected map[string]string) bool {
	if _, ok := db.Sources[sourceID]; !ok {
		return false
	}
	for name, sha := range expected {
		a, ok := db.Articles[sourceID+"/"+name]
		if !ok || a.Deleted || len(a.Versions) == 0 || a.Versions[len(a.Versions)-1].SHA != sha {
			return false
		}
		for _, conflict := range a.Conflicts {
			if conflict.Resolution == "" {
				return false
			}
		}
	}
	return true
}
