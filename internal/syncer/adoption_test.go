package syncer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestAdoptPrivateSourcePreservesLocalData(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Files = map[string]string{"same.md": "# Same", "different.md": "# Local", "local-only.md": "# Local only"}
	b.Assets = map[string][]byte{"image.png": smallPNG(t)}
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "different.md", "# Local edit", 1); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "same.md": "# Same", "different.md": "# Remote", "remote-only.md": "# Remote only", "image.png": string(b.Assets["image.png"])})
	f.Modify(func() { f.Private = true })
	m := newSyncManager(s, f.Client())
	if _, err := s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", Write: true, AdoptExisting: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.db().Sources["handbook"].ConnectionID != "handbook" || s.db().Sources["handbook"].Visibility != "private" {
		t.Fatal("source not attached privately")
	}
	same := s.db().Articles["handbook/same.md"]
	different := s.db().Articles["handbook/different.md"]
	local := s.db().Articles["handbook/local-only.md"]
	remote := s.db().Articles["handbook/remote-only.md"]
	if len(same.Versions) != 1 || same.UpstreamHash == "" || len(different.Versions) != 2 || different.Versions[1].Text != "# Local edit" || len(different.Conflicts) != 1 || different.Conflicts[0].Incoming != "# Remote" || local.Deleted || local.UpstreamHash != "" || len(remote.Versions) != 1 {
		t.Fatal("adoption did not preserve article state")
	}
	if len(s.jobs()) != 0 || f.WriteCount() != 0 {
		t.Fatal("adoption unexpectedly queued or wrote content")
	}
	m.Once(context.Background())
	if s.db().Articles["handbook/local-only.md"].Deleted || f.WriteCount() != 0 {
		t.Fatal("sync deleted or uploaded local-only content")
	}
}

func TestAdoptRejectsConcurrentLocalEdit(t *testing.T) {
	s := testStore(t)
	if _, err := s.importBatch(testBatch()); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	var once sync.Once
	var editErr error
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/blobs/") {
			once.Do(func() { _, editErr = s.put("handbook", "README.md", "# Concurrent edit", 1) })
		}
		f.Serve(w, r)
	}))
	defer proxy.Close()
	client := github.NewForTest(proxy.URL, proxy.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", AdoptExisting: true})
	if editErr != nil || !errors.Is(err, domain.ErrConflict) || len(s.connections()) != 0 {
		t.Fatalf("concurrent edit was not rejected: %v", err)
	}
	a := s.db().Articles["handbook/README.md"]
	if a.Versions[len(a.Versions)-1].Text != "# Concurrent edit" {
		t.Fatal("concurrent edit lost")
	}
}

func TestAdoptRejectsPublicOrMissingAsset(t *testing.T) {
	for _, public := range []bool{true, false} {
		t.Run(map[bool]string{true: "public", false: "missing_asset"}[public], func(t *testing.T) {
			s := testStore(t)
			b := testBatch()
			b.Assets = map[string][]byte{"image.png": smallPNG(t)}
			if _, err := s.importBatch(b); err != nil {
				t.Fatal(err)
			}
			f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Initial"})
			f.Modify(func() { f.Private = !public })
			m := newSyncManager(s, f.Client())
			if _, err := s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
				t.Fatal(err)
			}
			before, err := s.adoptionSnapshot("handbook")
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", AdoptExisting: true})
			if !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("expected conflict: %v", err)
			}
			after, err := s.adoptionSnapshot("handbook")
			if err != nil || before != after || len(s.connections()) != 0 {
				t.Fatal("failed adoption modified source")
			}
		})
	}
}

func TestAdoptSQLiteSurvivesReopen(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Files["local.md"] = "# Local"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Changed"})
	f.Modify(func() { f.Private = true })
	m := newSyncManager(s, f.Client())
	if _, err = s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", AdoptExisting: true}); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if reopened.db().Sources["handbook"].ConnectionID != "handbook" || len(reopened.db().Articles["handbook/README.md"].Conflicts) != 1 || reopened.db().Articles["handbook/local.md"].Deleted {
		t.Fatal("adoption did not survive reopen")
	}
}

func TestAdoptRejectsChangedRemoteHead(t *testing.T) {
	s := testStore(t)
	if _, err := s.importBatch(testBatch()); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	var mu sync.Mutex
	heads := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/ref/heads/") {
			mu.Lock()
			heads++
			if heads == 2 {
				f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Later"})
			}
			mu.Unlock()
		}
		f.Serve(w, r)
	}))
	defer proxy.Close()
	client := github.NewForTest(proxy.URL, proxy.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	before, err := s.adoptionSnapshot("handbook")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", AdoptExisting: true})
	after, snapshotErr := s.adoptionSnapshot("handbook")
	if !errors.Is(err, domain.ErrConflict) || snapshotErr != nil || before != after || len(s.connections()) != 0 {
		t.Fatal("changed remote head was not rejected", err)
	}
}

func TestAdoptRejectsIncompleteOrWrongKind(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"incomplete", map[string]string{"veda.yaml": metadataFixture, "README.md": string([]byte{0xff})}},
		{"wrong kind", map[string]string{"docs/guide.md": "# Guide"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			if _, err := s.importBatch(testBatch()); err != nil {
				t.Fatal(err)
			}
			f := githubtest.New(t, tc.files)
			f.Modify(func() { f.Private = true })
			m := newSyncManager(s, f.Client())
			if _, err := s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
				t.Fatal(err)
			}
			before, err := s.adoptionSnapshot("handbook")
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", AdoptExisting: true})
			after, snapshotErr := s.adoptionSnapshot("handbook")
			if !errors.Is(err, domain.ErrConflict) || snapshotErr != nil || before != after || len(s.connections()) != 0 {
				t.Fatal("unsafe import was not rejected", err)
			}
		})
	}
}

func TestAdoptCancellationBeforeCommitPreservesSource(t *testing.T) {
	s := testStore(t)
	if _, err := s.importBatch(testBatch()); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	heads := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/ref/heads/") {
			mu.Lock()
			heads++
			if heads == 2 {
				cancel()
			}
			mu.Unlock()
		}
		f.Serve(w, r)
	}))
	defer proxy.Close()
	client := github.NewForTest(proxy.URL, proxy.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	before, err := s.adoptionSnapshot("handbook")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Connect(ctx, ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", AdoptExisting: true})
	after, snapshotErr := s.adoptionSnapshot("handbook")
	if err == nil || snapshotErr != nil || before != after || len(s.connections()) != 0 {
		t.Fatal("cancelled request changed source")
	}
}

func TestAdoptionCommitChecksCancellation(t *testing.T) {
	s := testStore(t)
	initial := testBatch()
	if _, err := s.importBatch(initial); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Remote"})
	f.Modify(func() { f.Private = true })
	if _, err := s.saveAccount(context.Background(), f.Client(), "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	before, err := s.adoptionSnapshot("handbook")
	if err != nil {
		t.Fatal(err)
	}
	account := s.db().Accounts["operator"]
	b := testBatch()
	b.Create = false
	b.Files["README.md"] = "# Remote"
	gen := s.db().Sources["handbook"].ImportGeneration
	b.ExpectedImport = &gen
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := domain.GitHubConnection{ID: "handbook", AccountID: "operator", Private: true, ConfigRevision: 1}
	proof := remoteProof(conn, "private", b.Commit, "")
	proof.Adoption = adoptionProof(ctx, conn, account, before)
	_, err = s.applyImport(b, proof, false)
	after, snapshotErr := s.adoptionSnapshot("handbook")
	if !errors.Is(err, context.Canceled) || snapshotErr != nil || before != after || len(s.connections()) != 0 {
		t.Fatal("cancelled adoption committed", err)
	}
}
