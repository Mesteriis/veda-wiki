package main

import (
	"bytes"
	"context"
	"fmt"
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
			f := newFakeGitHub(t, map[string]string{name: string(remote)})
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/git/blobs/") {
					requests.Add(1)
				}
				f.serve(w, r)
			}))
			defer gateway.Close()
			m := newSyncManager(s, &GitHubClient{base: gateway.URL, http: gateway.Client()})
			entry := gitTreeEntry{Path: name, SHA: gitBlobDigest(remote, 40), Size: int64(len(remote))}
			result, err := m.cachedBlob(context.Background(), GitHubConnection{ID: "handbook", Repository: "org/handbook", RepositoryID: 41, Private: !tc.public}, entry, fixtureAccess)
			if err != nil || !bytes.Equal(result, remote) || requests.Load() != tc.wantRequests {
				t.Fatal("incorrect verified blob reuse", err, requests.Load())
			}
			// Both locally reused and fetched objects must be reusable after attachment.
			result, err = m.cachedBlob(context.Background(), GitHubConnection{ID: "handbook", Repository: "org/handbook", RepositoryID: 41, Private: !tc.public}, entry, fixtureAccess)
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
	f := newFakeGitHub(t, remote)
	f.modify(func() { f.private = true })
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/blobs/") {
			requests.Add(1)
		}
		f.serve(w, r)
	}))
	defer gateway.Close()
	client := &GitHubClient{base: gateway.URL, http: gateway.Client()}
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", GitHubSecret{Kind: "token", Token: fixtureAccess}); err != nil {
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
