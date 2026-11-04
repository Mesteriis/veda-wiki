package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Validate checks a whole database before it is opened or committed. It fills
// absent collections and defaults the publication scope of legacy outbox jobs.
func Validate(db *domain.Database) error {
	if (db.Version < 1 || db.Version > domain.RevisionsFormat) || db.Version == 14 || db.Sources == nil || db.Articles == nil {
		return errors.New("unsupported store")
	}
	if db.Accounts == nil {
		db.Accounts = map[string]domain.GitHubAccount{}
	}
	if db.Connections == nil {
		db.Connections = map[string]domain.GitHubConnection{}
	}
	if db.Outbox == nil {
		db.Outbox = map[string]domain.SyncJob{}
	}
	if db.Moves == nil {
		db.Moves = map[string]domain.Move{}
	}
	if db.SourceMirrors == nil {
		db.SourceMirrors = map[string]domain.SourceMirror{}
	}
	for id, mirror := range db.SourceMirrors {
		if db.Version < 19 || id != mirror.DestinationID || mirror.SourceID == id || !domain.ValidID(mirror.SourceID) || !domain.ValidID(id) || mirror.Revision < 1 {
			return errors.New("invalid source mirror")
		}
		if _, ok := db.Sources[mirror.SourceID]; !ok {
			return errors.New("mirror source unavailable")
		}
		if _, ok := db.Sources[id]; !ok {
			return errors.New("mirror destination unavailable")
		}
		validState := mirror.State == "pending" || mirror.State == "ready" || mirror.State == "blocked" || mirror.State == "syncing" || mirror.State == "paused" || mirror.State == "error"
		validError := mirror.LastError == "" || mirror.LastError == "source_unavailable" || mirror.LastError == "interrupted" || mirror.LastError == "storage_unavailable"
		if !validState || !validError || mirror.Blocked < 0 || (mirror.Enabled && mirror.State == "paused") || (!mirror.Enabled && mirror.State != "paused") {
			return errors.New("invalid mirror status")
		}
		if len(mirror.BlockedPaths) > 50 || len(mirror.BlockedPaths) > mirror.Blocked {
			return errors.New("invalid mirror blocked paths")
		}
		for _, p := range mirror.BlockedPaths {
			if !domain.ArticlePath(p, db.Sources[mirror.SourceID].Kind) {
				return errors.New("invalid mirror blocked path")
			}
		}
		if mirror.LastRun != "" {
			if _, err := time.Parse(time.RFC3339Nano, mirror.LastRun); err != nil {
				return errors.New("invalid mirror observation time")
			}
		}
	}
	if db.Version == 1 && len(db.Moves) > 0 {
		return errors.New("move records require store version 2")
	}
	if db.Version < 4 && len(db.AccessKeys) > 0 {
		return errors.New("access keys require store version 4")
	}
	for id, key := range db.AccessKeys {
		if err := domain.ValidateAccessKey(id, key, db.Sources); err != nil {
			return err
		}
	}
	if err := domain.ValidatePeople(*db); err != nil {
		return err
	}
	for id, a := range db.Accounts {
		if !domain.ValidID(id) || a.ID != id || (a.Kind != "token" && a.Kind != "app") || !domain.ValidGitHubName(a.Login) || len(a.SecretRef) != 32 {
			return errors.New("invalid stored GitHub account")
		}
		if _, e := hex.DecodeString(a.SecretRef); e != nil {
			return errors.New("invalid credential reference")
		}
	}
	for id, c := range db.Connections {
		repo, e := domain.RepositoryName(c.Repository)
		if e != nil || repo != c.Repository || !domain.ValidID(id) || id != c.ID || c.RepositoryID < 1 || !domain.ValidBranch(c.Branch) || c.ConfigRevision < 1 {
			return errors.New("invalid stored GitHub connection")
		}
		if c.AccountID != "" {
			if _, ok := db.Accounts[c.AccountID]; !ok {
				return errors.New("connection account unavailable")
			}
		}
	}
	for id, src := range db.Sources {
		if id != src.ID || !domain.ValidID(id) || (src.Kind != "wiki" && src.Kind != "program") || (src.Visibility != "public" && src.Visibility != "private") {
			return errors.New("invalid stored source")
		}
		if src.Metadata != nil {
			if e := domain.ValidateMetadata(src.Metadata); e != nil {
				return errors.New("invalid stored metadata")
			}
		}
		if e := validateAssets(src.Assets); e != nil {
			return e
		}
	}
	for key, article := range db.Articles {
		src, exists := db.Sources[article.SourceID]
		if !exists || key != article.SourceID+"/"+article.Path || !domain.ArticlePath(article.Path, src.Kind) || len(article.Versions) == 0 {
			return errors.New("invalid stored article")
		}
		if b := article.ReconcileBase; b != nil {
			origin, found := db.Articles[b.SourceID+"/"+b.Path]
			if db.Version < 18 || b.SourceID == article.SourceID || !found || b.Revision < 1 || !revisionHasSHA(origin, b.Revision, b.SHA) {
				return errors.New("invalid reconciliation baseline")
			}
		}
		previous := 0
		for _, revision := range article.Versions {
			sum := sha256.Sum256([]byte(revision.Text))
			if revision.Number < 1 || revision.Number <= previous ||
				len(revision.Text) > domain.MaxArticleBytes ||
				(len(revision.Text) > domain.LegacyArticleBytes && db.Version < 13) ||
				!utf8.ValidString(revision.Text) ||
				revision.SHA != hex.EncodeToString(sum[:]) {
				return errors.New("invalid stored revision")
			}
			previous = revision.Number
			if e := validateAssets(revision.Assets); e != nil {
				return e
			}
		}
		if article.CopiedFrom != nil {
			origin := article.CopiedFrom
			if origin.DirectoryIndex && (db.Version < 17 || path.Base(origin.Path) != "index.md" || path.Base(article.Path) != "README.md") {
				return errors.New("invalid directory copy provenance")
			}
			src, exists := db.Sources[origin.SourceID]
			if !exists || !domain.ArticlePath(origin.Path, src.Kind) || origin.Revision < 1 || len(origin.SHA) != 64 {
				return errors.New("invalid copy provenance")
			}
			if _, e := hex.DecodeString(origin.SHA); e != nil {
				return errors.New("invalid copy provenance")
			}
		}
		if article.Published != nil {
			p := article.Published
			if len(p.Text) > domain.MaxArticleBytes || (len(p.Text) > domain.LegacyArticleBytes && db.Version < 13) || !utf8.ValidString(p.Text) || domain.Digest([]byte(p.Text)) != p.SHA {
				return errors.New("invalid publication snapshot")
			}
			if e := validateAssets(p.Assets); e != nil {
				return e
			}
		}
		for _, c := range article.Conflicts {
			if !domain.ValidCommitID(c.Commit) ||
				(c.Deleted && (c.Incoming != "" || c.IncomingSHA != "")) ||
				(!c.Deleted && (domain.Digest([]byte(c.Incoming)) != c.IncomingSHA || len(c.Incoming) > domain.MaxArticleBytes || (len(c.Incoming) > domain.LegacyArticleBytes && db.Version < 13))) {
				return errors.New("invalid stored conflict")
			}
		}
	}
	for id, m := range db.Moves {
		source, srcOK := db.Articles[m.SourceID+"/"+m.SourcePath]
		target, dstOK := db.Articles[m.DestinationID+"/"+m.DestinationPath]
		if len(id) != 32 ||
			id != m.ID ||
			m.SourceID == m.DestinationID ||
			!srcOK ||
			!dstOK ||
			m.SourceRevision < 1 {
			return errors.New("invalid stored move")
		}
		// A final move is a journal record: pruning may have removed the
		// revisions it names. ProtectedRevisions keeps them for active moves.
		if domain.MoveTerminal(m.State) {
			if !storedOrPruned(source, m.SourceRevision, m.SHA) || !storedOrPruned(target, 1, m.SHA) {
				return errors.New("invalid stored move")
			}
		} else if !revisionHasSHA(source, m.SourceRevision, m.SHA) || len(target.Versions) < 1 || target.Versions[0].SHA != m.SHA {
			return errors.New("invalid stored move")
		}
		if len(m.Assets) > 2000 {
			return errors.New("too many move assets")
		}
		if len(m.Assets) > 0 && db.Version < 6 {
			return errors.New("move assets require store version 6")
		}
		for p, ref := range m.Assets {
			if !domain.SafeRelative(p) || (db.Sources[m.DestinationID].Kind == "program" && !strings.HasPrefix(p, "docs/")) {
				return errors.New("invalid move asset path")
			}
			if _, e := domain.HexKey(ref.SHA); e != nil {
				return errors.New("invalid move asset digest")
			}
			switch ref.MIME {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return errors.New("invalid move asset type")
			}
		}
		if len(m.Links) > 2000 || (len(m.Links) > 0 && db.Version < 8) {
			return errors.New("invalid move links")
		}
		for p, sha := range m.Links {
			if p == m.DestinationPath || !domain.ArticlePath(p, db.Sources[m.DestinationID].Kind) {
				return errors.New("invalid move link path")
			}
			if len(sha) != 64 {
				return errors.New("invalid move link digest")
			}
			if _, e := hex.DecodeString(sha); e != nil {
				return errors.New("invalid move link digest")
			}
		}
		if _, e := hex.DecodeString(id); e != nil {
			return errors.New("invalid stored move")
		}
		if m.SourceConnectionID == "" {
			if m.SourceRepositoryID != 0 || m.SourceBranch != "" {
				return errors.New("invalid move source identity")
			}
		} else if db.Version < 3 || !domain.ValidID(m.SourceConnectionID) || m.SourceRepositoryID < 1 || !domain.ValidBranch(m.SourceBranch) {
			return errors.New("invalid move source identity")
		}
		if m.DestinationConnectionID == "" {
			if m.DestinationRepositoryID != 0 || m.DestinationBranch != "" {
				return errors.New("invalid move destination identity")
			}
		} else if !domain.ValidID(m.DestinationConnectionID) || m.DestinationRepositoryID < 1 || !domain.ValidBranch(m.DestinationBranch) {
			return errors.New("invalid move destination identity")
		}
		switch m.State {
		case "waiting_destination", "complete", "source_changed", "destination_changed", "cancelled":
		case "waiting_source_delete":
			if m.SourceConnectionID == "" || db.Version < 3 {
				return errors.New("invalid move state")
			}
		case "deleting_source":
			if m.SourceConnectionID == "" || db.Version < 3 || !domain.ValidCommitID(m.PreparedDeletion) {
				return errors.New("invalid prepared deletion")
			}
		default:
			return errors.New("invalid move state")
		}
	}
	for id, j := range db.Outbox {
		if len(j.Bundle) > 2000 || (len(j.Bundle) > 0 && db.Version < 9) {
			return errors.New("invalid queued bundle")
		}
		for p, sha := range j.Bundle {
			a, exists := db.Articles[j.SourceID+"/"+p]
			if p == j.Path || !domain.ArticlePath(p, db.Sources[j.SourceID].Kind) || len(sha) != 64 || !exists || len(a.Versions) == 0 {
				return errors.New("invalid queued bundle article")
			}
			// A linked copy creates each bundle article as revision 1, and the
			// bundle SHA is that revision's. An active job protects revision 1
			// (domain.ProtectedRevisions); a final one does not, so it may be pruned.
			if domain.JobTerminal(j.State) {
				if !storedOrPruned(a, 1, sha) {
					return errors.New("invalid queued bundle article")
				}
			} else if !revisionHasSHA(a, 1, sha) {
				return errors.New("invalid queued bundle article")
			}
			if _, e := hex.DecodeString(sha); e != nil {
				return errors.New("invalid queued bundle digest")
			}
		}
		if len(j.Links) > 2000 || (len(j.Links) > 0 && db.Version < 8) {
			return errors.New("invalid queued links")
		}
		for p, sha := range j.Links {
			if p == j.Path || !domain.ArticlePath(p, db.Sources[j.SourceID].Kind) {
				return errors.New("invalid queued link path")
			}
			if len(sha) != 64 {
				return errors.New("invalid queued link digest")
			}
			if _, e := hex.DecodeString(sha); e != nil {
				return errors.New("invalid queued link digest")
			}
		}
		if len(j.Assets) > 2000 || (len(j.Assets) > 0 && db.Version < 7) {
			return errors.New("invalid queued assets")
		}
		for p, ref := range j.Assets {
			if !domain.RemoteAssetPath(p, db.Sources[j.SourceID].Kind) {
				return errors.New("invalid queued asset path")
			}
			if _, e := domain.HexKey(ref.SHA); e != nil {
				return errors.New("invalid queued asset digest")
			}
			switch ref.MIME {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return errors.New("invalid queued asset type")
			}
		}
		if j.Visibility == "" {
			j.Visibility = "private"
			db.Outbox[id] = j
		}
		if j.Visibility != "private" && j.Visibility != "public" {
			return errors.New("invalid queued publication scope")
		}
		a, ok := db.Articles[j.SourceID+"/"+j.Path]
		if !ok || j.ID != id || j.Revision < 1 {
			return errors.New("invalid outbox revision")
		}
		if domain.JobTerminal(j.State) {
			if !storedOrPruned(a, j.Revision, j.SHA) {
				return errors.New("invalid outbox revision")
			}
		} else if !revisionHasSHA(a, j.Revision, j.SHA) {
			return errors.New("invalid outbox revision")
		}
		switch j.State {
		case "pending", "prepared", "retry", "blocked", "conflict", "complete", "superseded":
		default:
			return errors.New("invalid outbox state")
		}
	}
	return nil
}

// revisionHasSHA reports whether the article stores revision n with this SHA.
func revisionHasSHA(a domain.Article, n int, sha string) bool {
	r, ok := domain.RevisionByNumber(a, n)
	return ok && r.SHA == sha
}

// storedOrPruned reports whether revision n of the article has this SHA or,
// being older than the newest revision, may have been pruned.
func storedOrPruned(a domain.Article, n int, sha string) bool {
	if r, ok := domain.RevisionByNumber(a, n); ok {
		return r.SHA == sha
	}
	return n >= 1 && n < domain.LatestRevision(a)
}

// validateAssets checks an image index of a source, revision or publication
// copy: the media handler serves these references with their stored type.
func validateAssets(assets map[string]domain.BlobRef) error {
	for name, ref := range assets {
		if !domain.SafeRelative(name) {
			return errors.New("invalid stored asset")
		}
		if _, e := domain.HexKey(ref.SHA); e != nil {
			return errors.New("invalid stored asset digest")
		}
		switch ref.MIME {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
		default:
			return errors.New("invalid stored asset type")
		}
	}
	return nil
}
