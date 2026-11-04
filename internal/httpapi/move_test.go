package httpapi

import (
	"bytes"
	"context"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func connectedMoveFixtures(t *testing.T) (*Store, *syncer.Manager, *githubtest.Fake, *githubtest.Fake) {
	t.Helper()
	s := testStore(t)
	one := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	two := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination"})
	one.Modify(func() { one.Private = true })
	two.Modify(func() { two.Name = "org/second"; two.RepoID = 42; two.Private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second := strings.HasPrefix(r.URL.Path, "/repos/org/second")
		if r.URL.Path == "/graphql" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "bad request", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			second = bytes.Contains(body, []byte(`"name":"second"`)) || bytes.Contains(body, []byte(`fixture-node-org/second`))
		}
		if second {
			two.Serve(w, r)
		} else {
			one.Serve(w, r)
		}
	}))
	t.Cleanup(gateway.Close)
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, repo string }{{"one", "org/handbook"}, {"two", "org/second"}} {
		if _, err := m.Connect(context.Background(), syncer.ConnectRequest{ID: item.id, Repository: item.repo, AccountID: "operator", Write: true}); err != nil {
			t.Fatal(err)
		}
	}
	m.Once(context.Background())
	return s, m, one, two
}

func syncFixtureSource(t *testing.T, m *syncer.Manager, id string) {
	t.Helper()
	for _, conn := range m.Connections() {
		if conn.ID == id {
			m.SyncSource(context.Background(), conn)
			return
		}
	}
	t.Fatalf("missing connection %s", id)
}

func TestMoveRejectsImportedSourcesAndRelativeDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	source(t, s, "target", "private")
	for _, item := range []struct{ path, text string }{{"image.md", "# Doc\n![image](assets/a.png)"}, {"link.md", "# Doc\n[guide](../guide.md)"}} {
		if _, e := s.put("local", item.path, item.text, 0); e != nil {
			t.Fatal(e)
		}
		req := transfer.MoveRequest{SourceID: "local", SourcePath: item.path, ExpectedRevision: 1, DestinationID: "target", DestinationPath: item.path, ConfirmMove: true}
		if _, e := s.moveArticle(req); e != domain.ErrInvalid {
			t.Fatal("relative dependency accepted", e)
		}
	}
	batch := testBatch()
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	req := transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "x.md", ConfirmMove: true}
	if _, e := s.moveArticle(req); e != domain.ErrInvalid {
		t.Fatal("imported source accepted", e)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	if r := request(w, "GET", "/api/v1/moves", "", false); r.Code != 404 {
		t.Fatal("move metadata exposed", r.Code)
	}
}
