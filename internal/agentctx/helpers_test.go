package agentctx

import (
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// article commits a one-revision article in a source.
func article(t *testing.T, st *store.Store, sourceID, path, text string) {
	t.Helper()
	storetest.Mutate(t, st, func(db *domain.Database) {
		db.Articles[sourceID+"/"+path] = domain.Article{SourceID: sourceID, Path: path, Versions: []domain.Revision{{Number: 1, Text: text, SHA: domain.Digest([]byte(text))}}}
	})
}

var commit = strings.Repeat("a", 40)

// connected commits a verified GitHub connection for a public source whose
// import is complete.
func connected(t *testing.T, st *store.Store, id string) {
	t.Helper()
	storetest.Mutate(t, st, func(db *domain.Database) {
		db.Connections[id] = domain.GitHubConnection{ID: id, Verification: "verified", VerifiedAt: nowText(), State: "idle", LastPull: commit, TreeSHA: commit}
		db.Sources[id] = domain.Source{ID: id, Title: id, Kind: "wiki", Visibility: "public", ConnectionID: id, ImportCommit: commit}
	})
}
