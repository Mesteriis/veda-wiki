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
	"github.com/Mesteriis/veda-wiki/internal/github"
)

// Validate checks a whole database before it is opened or committed. It fills
// absent collections and defaults the publication scope of legacy outbox jobs.
func Validate(db *domain.Database) error {
	if (db.Version < 1 || db.Version > 20) || db.Version == 14 || db.Sources == nil || db.Articles == nil {
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
		if db.Version < 19 || id != mirror.DestinationID || mirror.SourceID == id || !domain.IDPattern.MatchString(mirror.SourceID) || !domain.IDPattern.MatchString(id) || mirror.Revision < 1 {
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
		if !domain.IDPattern.MatchString(id) || a.ID != id || (a.Kind != "token" && a.Kind != "app") || !github.ValidName(a.Login) || len(a.SecretRef) != 32 {
			return errors.New("invalid stored GitHub account")
		}
		if _, e := hex.DecodeString(a.SecretRef); e != nil {
			return errors.New("invalid credential reference")
		}
	}
	for id, c := range db.Connections {
		repo, e := github.RepositoryName(c.Repository)
		if e != nil || repo != c.Repository || !domain.IDPattern.MatchString(id) || id != c.ID || c.RepositoryID < 1 || !github.ValidBranch(c.Branch) || c.ConfigRevision < 1 {
			return errors.New("invalid stored GitHub connection")
		}
		if c.AccountID != "" {
			if _, ok := db.Accounts[c.AccountID]; !ok {
				return errors.New("connection account unavailable")
			}
		}
	}
	for id, src := range db.Sources {
		if id != src.ID || !domain.IDPattern.MatchString(id) || (src.Kind != "wiki" && src.Kind != "program") || (src.Visibility != "public" && src.Visibility != "private") {
			return errors.New("invalid stored source")
		}
		if src.Metadata != nil {
			if e := domain.ValidateMetadata(src.Metadata); e != nil {
				return errors.New("invalid stored metadata")
			}
		}
		for name, ref := range src.Assets {
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
	}
	for key, article := range db.Articles {
		src, exists := db.Sources[article.SourceID]
		if !exists || key != article.SourceID+"/"+article.Path || !domain.ArticlePath(article.Path, src.Kind) || len(article.Versions) == 0 {
			return errors.New("invalid stored article")
		}
		if b := article.ReconcileBase; b != nil {
			origin, found := db.Articles[b.SourceID+"/"+b.Path]
			if db.Version < 18 || b.SourceID == article.SourceID || !found || b.Revision < 1 || b.Revision > len(origin.Versions) || origin.Versions[b.Revision-1].SHA != b.SHA {
				return errors.New("invalid reconciliation baseline")
			}
		}
		for i, revision := range article.Versions {
			sum := sha256.Sum256([]byte(revision.Text))
			if revision.Number != i+1 || len(revision.Text) > domain.MaxArticleBytes || (len(revision.Text) > domain.LegacyArticleBytes && db.Version < 13) || !utf8.ValidString(revision.Text) || revision.SHA != hex.EncodeToString(sum[:]) {
				return errors.New("invalid stored revision")
			}
		}
		if article.Translation != nil {
			tr := article.Translation
			if tr.Origin != "" && (tr.Origin != "local_model" || db.Version < 11) {
				return errors.New("invalid translation origin")
			}
			if src.Kind != "program" || tr.Language != "ru" || len(tr.Text) == 0 || len(tr.Text) > domain.MaxArticleBytes || (len(tr.Text) > domain.LegacyArticleBytes && db.Version < 13) || !utf8.ValidString(tr.Text) || domain.Digest([]byte(tr.Text)) != tr.SHA || len(tr.OriginalSHA) != 64 {
				return errors.New("invalid stored translation")
			}
			if _, e := hex.DecodeString(tr.OriginalSHA); e != nil {
				return errors.New("invalid translation provenance")
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
		}
		for _, c := range article.Conflicts {
			if !domain.CommitIDPattern.MatchString(c.Commit) || (c.Deleted && (c.Incoming != "" || c.IncomingSHA != "")) || (!c.Deleted && (domain.Digest([]byte(c.Incoming)) != c.IncomingSHA || len(c.Incoming) > domain.MaxArticleBytes || (len(c.Incoming) > domain.LegacyArticleBytes && db.Version < 13))) {
				return errors.New("invalid stored conflict")
			}
		}
	}
	for id, m := range db.Moves {
		source, srcOK := db.Articles[m.SourceID+"/"+m.SourcePath]
		target, dstOK := db.Articles[m.DestinationID+"/"+m.DestinationPath]
		if len(id) != 32 || id != m.ID || m.SourceID == m.DestinationID || !srcOK || !dstOK || m.SourceRevision < 1 || m.SourceRevision > len(source.Versions) || len(target.Versions) < 1 || source.Versions[m.SourceRevision-1].SHA != m.SHA || target.Versions[0].SHA != m.SHA {
			return errors.New("invalid stored move")
		}
		if m.TranslationFingerprint != "" {
			if db.Version < 5 || len(m.TranslationFingerprint) != 64 {
				return errors.New("invalid move translation fingerprint")
			}
			if _, e := hex.DecodeString(m.TranslationFingerprint); e != nil {
				return errors.New("invalid move translation fingerprint")
			}
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
		} else if db.Version < 3 || !domain.IDPattern.MatchString(m.SourceConnectionID) || m.SourceRepositoryID < 1 || !github.ValidBranch(m.SourceBranch) {
			return errors.New("invalid move source identity")
		}
		if m.DestinationConnectionID == "" {
			if m.DestinationRepositoryID != 0 || m.DestinationBranch != "" {
				return errors.New("invalid move destination identity")
			}
		} else if !domain.IDPattern.MatchString(m.DestinationConnectionID) || m.DestinationRepositoryID < 1 || !github.ValidBranch(m.DestinationBranch) {
			return errors.New("invalid move destination identity")
		}
		switch m.State {
		case "waiting_destination", "complete", "source_changed", "destination_changed", "cancelled":
		case "waiting_source_delete":
			if m.SourceConnectionID == "" || db.Version < 3 {
				return errors.New("invalid move state")
			}
		case "deleting_source":
			if m.SourceConnectionID == "" || db.Version < 3 || !domain.CommitIDPattern.MatchString(m.PreparedDeletion) {
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
			if p == j.Path || !domain.ArticlePath(p, db.Sources[j.SourceID].Kind) || len(sha) != 64 || !exists || len(a.Versions) == 0 || a.Versions[0].SHA != sha {
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
		if !ok || j.ID != id || j.Revision < 1 || j.Revision > len(a.Versions) || a.Versions[j.Revision-1].SHA != j.SHA {
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
