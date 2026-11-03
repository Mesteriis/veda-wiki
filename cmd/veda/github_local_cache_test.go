package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPrivateAdoptionReusesOnlyVerifiedLocalBlobs(t *testing.T) {
	for _, tc := range []struct {
		name                                                       string
		asset, corrupt, different, public, sourcePublic, connected bool
		wantRequests                                               int32
	}{
		{name: "matching article"}, {name: "matching image", asset: true},
		{name: "changed upstream", different: true, wantRequests: 1},
		{name: "corrupt local image", asset: true, corrupt: true, wantRequests: 1},
		{name: "public repository", public: true, wantRequests: 1},
		{name: "public local source", sourcePublic: true, wantRequests: 1},
		{name: "connected local source", connected: true, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			b := testBatch()
			if tc.sourcePublic {
				b.Visibility = "public"
			}
			name := "README.md"
			local := []byte("# Initial")
			remote := local
			if tc.asset {
				name = "image.png"
				local = smallPNG(t)
				remote = local
				b.Assets = map[string][]byte{name: local}
			}
			if tc.different {
				remote = []byte("# Different")
			}
			if _, err := s.importBatch(b); err != nil {
				t.Fatal(err)
			}
			if tc.connected {
				src := s.db.Sources["handbook"]
				src.ConnectionID = "handbook"
				s.db.Sources["handbook"] = src
			}
			if tc.corrupt {
				ref := s.db.Sources["handbook"].Assets[name]
				if err := os.WriteFile(filepath.Join(s.dir, "blobs", ref.SHA), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f := githubtest.New(t, map[string]string{name: string(remote)})
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/git/blobs/") {
					requests.Add(1)
				}
				f.Serve(w, r)
			}))
			defer gateway.Close()
			m := newSyncManager(s, github.NewForTest(gateway.URL, gateway.Client()))
			entry := github.TreeEntry{Path: name, SHA: github.BlobDigest(remote, 40), Size: int64(len(remote))}
			result, err := m.cachedBlob(context.Background(), GitHubConnection{ID: "handbook", Repository: "org/handbook", RepositoryID: 41, Private: !tc.public}, entry, githubtest.Access)
			if err != nil || !bytes.Equal(result, remote) || requests.Load() != tc.wantRequests {
				t.Fatal("incorrect verified blob reuse", err, requests.Load())
			}
			// Both locally reused and fetched objects must be reusable after attachment.
			result, err = m.cachedBlob(context.Background(), GitHubConnection{ID: "handbook", Repository: "org/handbook", RepositoryID: 41, Private: !tc.public}, entry, githubtest.Access)
			if err != nil || !bytes.Equal(result, remote) || requests.Load() != tc.wantRequests {
				t.Fatal("cache replay failed", err, requests.Load())
			}
		})
	}
}

func TestAdoptionReusesUnchangedArticlesAcrossRepository(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	batch.Files = map[string]string{}
	remote := map[string]string{"veda.yaml": metadataFixture}
	for i := 0; i < 120; i++ {
		name := fmt.Sprintf("topic/%03d.md", i)
		content := fmt.Sprintf("# Fixture %d", i)
		batch.Files[name] = content
		remote[name] = content
	}
	remote["topic/000.md"] = "# Changed remotely"
	if _, err := s.importBatch(batch); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, remote)
	f.Modify(func() { f.Private = true })
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/blobs/") {
			requests.Add(1)
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.connect(context.Background(), connectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", AdoptExisting: true}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || len(s.conflicts()) != 1 {
		t.Fatal("unchanged local objects were not reused", requests.Load())
	}
	m.Once(context.Background())
	if requests.Load() != 2 {
		t.Fatal("replay unexpectedly fetched cached objects", requests.Load())
	}
}

func TestPrivateRepositoryImportReusesSeparateLocalSource(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	batch.SourceID = "local"
	batch.Files = map[string]string{}
	batch.Assets = map[string][]byte{"image.png": smallPNG(t)}
	remote := map[string]string{"veda.yaml": metadataFixture, "image.png": string(batch.Assets["image.png"])}
	for i := 0; i < 120; i++ {
		name := fmt.Sprintf("topic/%03d.md", i)
		content := fmt.Sprintf("# Fixture %d", i)
		batch.Files[name] = content
		remote[name] = content
	}
	remote["topic/000.md"] = "# Different upstream"
	if _, err := s.importBatch(batch); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, remote)
	f.Modify(func() { f.Private = true })
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/blobs/") {
			requests.Add(1)
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.connect(context.Background(), connectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if requests.Load() != 2 || len(s.db.Sources["handbook"].Assets) != 1 || s.db.Articles["handbook/topic/000.md"].Versions[0].Text != "# Different upstream" {
		t.Fatal("separate source did not reuse exact private objects", requests.Load())
	}
	if s.db.Articles["local/topic/000.md"].Versions[0].Text == s.db.Articles["handbook/topic/000.md"].Versions[0].Text {
		t.Fatal("independent source was overwritten")
	}
}

func TestPrivateCacheDoesNotReuseDifferentPath(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.SourceID = "local"
	b.Files = map[string]string{"other.md": "# Same"}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"guide.md": "# Same"})
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/blobs/") {
			requests.Add(1)
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, github.NewForTest(gateway.URL, gateway.Client()))
	body := []byte("# Same")
	entry := github.TreeEntry{Path: "guide.md", SHA: github.BlobDigest(body, 40), Size: int64(len(body))}
	_, err := m.cachedBlob(context.Background(), GitHubConnection{ID: "handbook", Repository: "org/handbook", RepositoryID: 41, Private: true}, entry, githubtest.Access)
	if err != nil || requests.Load() != 1 {
		t.Fatal("different path reused", err, requests.Load())
	}
}

func TestPrivateLocalCacheSupportsSHA256Objects(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.SourceID = "local"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{})
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/blobs/") {
			requests.Add(1)
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, github.NewForTest(gateway.URL, gateway.Client()))
	body := []byte("# Initial")
	entry := github.TreeEntry{Path: "README.md", SHA: github.BlobDigest(body, 64), Size: int64(len(body))}
	got, err := m.cachedBlob(context.Background(), GitHubConnection{ID: "remote", Repository: "org/handbook", RepositoryID: 41, Private: true}, entry, githubtest.Access)
	if err != nil || !bytes.Equal(got, body) || requests.Load() != 0 {
		t.Fatal("SHA-256 local object not reused", err)
	}
}
