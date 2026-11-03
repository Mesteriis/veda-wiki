package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"
	"time"
)

func connectFixture(t *testing.T, s *Store, f *githubtest.Fake, write bool) *SyncManager {
	t.Helper()
	m := newSyncManager(s, f.Client())
	account := ""
	if write || f.Private {
		if _, e := s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); e != nil {
			t.Fatal(e)
		}
		account = "operator"
	}
	if _, e := m.connect(context.Background(), connectRequest{ID: "handbook", Repository: "https://github.com/org/handbook", AccountID: account, Write: write}); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	return m
}

func TestBackgroundSyncImportsChangedGitHubHead(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	initialVerification := s.connections()[0].VerifiedAt
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx, 20*time.Millisecond) }()
	defer func() { cancel(); <-done }()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for s.connections()[0].VerifiedAt == initialVerification {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("initial background check did not complete")
		}
	}
	remote := f.Contents()
	remote["guide.md"] = "# Updated upstream"
	f.Advance(remote)
	for {
		page, err := s.page("handbook/guide.md", false)
		if err == nil && page.Text == "# Updated upstream" {
			if _, err := s.page("handbook/guide.md", true); err == nil {
				t.Fatal("private updated article exposed publicly")
			}
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("background sync did not import updated head")
		}
	}
}

func TestStaleSyncCannotReverifyAfterPauseResume(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	m := connectFixture(t, s, f, false)
	old := s.connections()[0]
	if err := s.pauseConnection(old.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.pauseConnection(old.ID, false); err != nil {
		t.Fatal(err)
	}
	current := s.connections()[0]
	if current.Verification != "unknown" || current.ConfigRevision == old.ConfigRevision {
		t.Fatal("invalid pause fixture")
	}
	m.syncSource(context.Background(), old)
	after := s.connections()[0]
	if after.Verification != "unknown" || after.State != "pending" {
		t.Fatal("stale success changed current status")
	}
	if _, err := s.page("handbook/guide.md", true); err != ErrNotFound {
		t.Fatal("stale result republished source", err)
	}
	f.Modify(func() { f.Fail = true })
	m.syncSource(context.Background(), old)
	after = s.connections()[0]
	if after.Verification != "unknown" || after.State != "pending" || after.LastError != current.LastError {
		t.Fatal("stale failure changed current status")
	}
	f.Modify(func() { f.Fail = false })
	m.Once(context.Background())
	if _, err := s.page("handbook/guide.md", true); err != nil {
		t.Fatal("fresh verification did not restore visibility", err)
	}
}

func TestStaleSyncCannotChangeOutboxAfterReconfiguration(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	m := connectFixture(t, s, f, false)
	if _, err := s.put("handbook", "new.md", "# New", 0); err != nil {
		t.Fatal(err)
	}
	old := s.connections()[0]
	if err := s.pauseConnection(old.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.pauseConnection(old.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.pushPending(context.Background(), old, "", "", ""); err != ErrConflict {
		t.Fatal("stale cycle was accepted", err)
	}
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].State != "pending" || jobs[0].LastError != "" {
		t.Fatal("stale cycle changed outbox")
	}
	current := s.connections()[0]
	if err := m.pushPending(context.Background(), current, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if s.jobs()[0].State != "blocked" {
		t.Fatal("current cycle did not update outbox")
	}
}

func TestStaleOutboxObservationCannotUndoPublicationApproval(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	connectFixture(t, s, f, false)
	if _, err := s.put("handbook", "new.md", "# New", 0); err != nil {
		t.Fatal(err)
	}
	c := s.connections()[0]
	s.mu.Lock()
	key := "handbook/new.md@1"
	j := s.db.Outbox[key]
	j.Visibility = "private"
	s.db.Outbox[key] = j
	s.mu.Unlock()
	old := s.jobs()[0]
	s.mu.Lock()
	j = s.db.Outbox[key]
	j.Visibility = "public"
	s.db.Outbox[key] = j
	s.mu.Unlock()
	if err := s.setJobFor(c, old, "blocked", "publication_confirmation_required", ""); err != ErrConflict {
		t.Fatal("changed visibility was overwritten", err)
	}
	if s.jobs()[0].State != "pending" {
		t.Fatal("changed job was blocked")
	}
	old = s.jobs()[0]
	s.mu.Lock()
	j = s.db.Outbox[key]
	j.State = "conflict"
	s.db.Outbox[key] = j
	s.mu.Unlock()
	if err := s.setJobFor(c, old, "retry", "upstream_unavailable", ""); err != ErrConflict {
		t.Fatal("conflict state was overwritten", err)
	}
}
func TestGitHubPublicPullAndTrackedProgramScope(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Documentation", "README.md": "not imported", "src/main.go": "package original"})
	connectFixture(t, s, f, false)
	if p, e := s.page("handbook/docs/guide.md", true); e != nil || p.Text != "# Documentation" {
		t.Fatal(p, e)
	}
	if len(s.snapshot(true).Pages) != 1 {
		t.Fatal("program scope")
	}
	if s.db.Sources["handbook"].ConnectionID != "handbook" {
		t.Fatal("connection provenance missing")
	}
	if _, e := s.importBatch(testBatch()); !errorsIsConflictOrInvalid(e) {
		t.Fatal("manual import replaced managed source", e)
	}
}
func errorsIsConflictOrInvalid(e error) bool { return e == ErrInvalid || e == ErrConflict }
func TestGitHubWriteThroughAndRestartDurability(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Original", "src/main.go": "package original"})
	_ = connectFixture(t, s, f, true)
	if _, e := s.put("handbook", "docs/guide.md", "# Saved locally", 1); e != nil {
		t.Fatal(e)
	}
	if len(s.jobs()) != 1 {
		t.Fatal("write and outbox not atomic")
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	m := newSyncManager(reopened, f.Client())
	m.Once(context.Background())
	if f.Contents()["docs/guide.md"] != "# Saved locally" || f.Contents()["src/main.go"] != "package original" || f.Force {
		t.Fatal("unsafe remote write")
	}
	job := reopened.jobs()[0]
	if job.State != "prepared" || job.PreparedCommit == "" || job.ObservedCommit != "" {
		t.Fatal("write response must not be reported as a subsequent observation", job)
	}
	reopened, e = openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	if reopened.jobs()[0].ObservedCommit != "" || reopened.db.Outbox[job.ID].ObservedCommit != "" {
		t.Fatal("unconfirmed observation after restart", e)
	}
	m = newSyncManager(reopened, f.Client())
	m.Once(context.Background())
	if reopened.jobs()[0].State != "complete" || reopened.jobs()[0].ObservedCommit == "" {
		t.Fatal(reopened.jobs())
	}
}
func TestGitHubLostResponseRecoversWithoutDuplicatePush(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Durable", 1)
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("prepared write not durable")
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	reopened.retryConnection("handbook")
	newSyncManager(reopened, f.Client()).Once(context.Background())
	if f.WriteCount() != 1 || reopened.jobs()[0].State != "complete" {
		t.Fatal("ambiguous push duplicated or lost", reopened.jobs())
	}
}

func TestLegacyUnconfirmedObservedCommitIsNotReported(t *testing.T) {
	s := testStore(t)
	s.mu.Lock()
	s.db.Outbox = map[string]SyncJob{}
	s.db.Outbox["fixture"] = SyncJob{ID: "fixture", State: "prepared", ObservedCommit: strings.Repeat("a", 40)}
	s.mu.Unlock()
	if got := s.jobs(); len(got) != 1 || got[0].ObservedCommit != "" {
		t.Fatal("legacy unconfirmed commit was exposed as an observation")
	}
}

func TestLaterEditRetainsUncertainWriteUntilObserved(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) { testLaterEditRetainsUncertainWrite(t, backend == "sqlite") })
	}
}

func testLaterEditRetainsUncertainWrite(t *testing.T, sqlite bool) {
	t.Helper()
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	if sqlite {
		if err := migrateSQLite(s.dir); err != nil {
			t.Fatal(err)
		}
		var err error
		s, err = openStore(s.dir)
		if err != nil {
			t.Fatal(err)
		}
		m = newSyncManager(s, f.Client())
	}
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].State != "retry" || jobs[0].PreparedCommit == "" || f.WriteCount() != 1 {
		t.Fatal("fixture did not create an uncertain write", jobs)
	}
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	jobs := s.jobs()
	if len(jobs) != 2 || jobs[0].State != "retry" || jobs[0].PreparedCommit == "" || jobs[1].State != "pending" {
		t.Fatal("later edit discarded uncertain write", jobs)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(reopened, f.Client())
	m.Once(context.Background())
	m.Once(context.Background())
	jobs = reopened.jobs()
	if len(jobs) != 2 || jobs[0].State != "complete" || jobs[1].State != "complete" || f.WriteCount() != 2 {
		t.Fatal("writes were not independently confirmed", jobs, f.WriteCount())
	}
	if got := f.Contents()["README.md"]; got != "# Second edit" {
		t.Fatal("latest edit not written")
	}
}

func TestLaterEditCannotOvertakeDeferredPreparedWrite(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original", "other.md": "# Other"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].PreparedCommit == "" || jobs[0].State != "retry" {
		t.Fatal("fixture write not deferred", jobs)
	}
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "other.md", "# Other edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = false })
	// Force a long retry delay so only an incorrect later-job dispatch can write.
	s.mu.Lock()
	first := s.db.Outbox["handbook/README.md@2"]
	first.NextAttempt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	s.db.Outbox[first.ID] = first
	s.mu.Unlock()
	if err := m.pushPending(context.Background(), s.connections()[0], f.Head, f.Commits[f.Head].Tree, githubtest.Access); err != nil {
		t.Fatal(err)
	}
	if f.WriteCount() != 1 || f.Contents()["README.md"] != "# Original" || f.Contents()["other.md"] != "# Other edit" {
		t.Fatal("later revision overtook prepared write or unrelated article stalled")
	}
}

func TestConflictResolutionDoesNotDiscardPreparedWrite(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) { testConflictResolutionDoesNotDiscardPreparedWrite(t, backend == "sqlite") })
	}
}

func testConflictResolutionDoesNotDiscardPreparedWrite(t *testing.T, sqlite bool) {
	t.Helper()
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	if sqlite {
		if err := migrateSQLite(s.dir); err != nil {
			t.Fatal(err)
		}
		var err error
		s, err = openStore(s.dir)
		if err != nil {
			t.Fatal(err)
		}
		m = newSyncManager(s, f.Client())
	}
	if _, err := s.put("handbook", "README.md", "# Local", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Different upstream"})
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	conflicts, jobs := s.conflicts(), s.jobs()
	if len(conflicts) != 1 || len(jobs) != 1 || jobs[0].PreparedCommit == "" {
		t.Fatal("fixture lacks conflict and prepared write", conflicts, jobs)
	}
	for _, choice := range []string{"local", "upstream"} {
		if err := s.resolveConflict("handbook", "README.md", conflicts[0].Index, 2, choice); !errors.Is(err, ErrConflict) {
			t.Fatal("uncertain write was discarded", choice, err)
		}
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.conflicts()) != 1 || reopened.jobs()[0].PreparedCommit == "" || reopened.jobs()[0].State == "superseded" {
		t.Fatal("uncertain write or conflict lost after restart")
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Local"})
	if err := reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(reopened, f.Client()).Once(context.Background())
	if len(reopened.conflicts()) != 0 || reopened.jobs()[0].State != "complete" {
		t.Fatal("confirmed prepared content did not reconcile", reopened.jobs())
	}
}

func TestExternalMatchForLaterEditDoesNotDiscardOlderUncertainWrite(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = false })
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Second edit"})
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if len(jobs) != 2 || jobs[0].PreparedCommit == "" || jobs[0].State == "superseded" || jobs[0].State == "complete" || jobs[1].State == "complete" {
		t.Fatal("later external content hid uncertain write", jobs)
	}
	if len(s.conflicts()) != 1 || f.WriteCount() != 0 || f.Contents()["README.md"] != "# Second edit" {
		t.Fatal("uncertain write did not preserve external content")
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.conflicts()) != 1 || reopened.jobs()[0].PreparedCommit == "" {
		t.Fatal("uncertainty lost after restart")
	}
}

func TestIdenticalLaterEditIsSatisfiedWithoutDuplicatePush(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# Same edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if _, err := s.put("handbook", "README.md", "# Same edit", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if len(jobs) != 2 || jobs[0].State != "complete" || jobs[1].State != "complete" || f.WriteCount() != 1 {
		t.Fatal("identical revision caused duplicate push or lost confirmation", jobs, f.WriteCount())
	}
}

func TestIdenticalLaterEditRequiresItsOwnDependencies(t *testing.T) {
	hash := digest([]byte("# Same edit"))
	db := Database{Outbox: map[string]SyncJob{
		"first": {ID: "first", SourceID: "handbook", Path: "README.md", Revision: 2, SHA: hash, State: "retry", PreparedCommit: strings.Repeat("a", 40)},
		"later": {ID: "later", SourceID: "handbook", Path: "README.md", Revision: 3, SHA: hash, State: "pending", Assets: map[string]BlobRef{"image.png": {SHA: strings.Repeat("b", 64), MIME: "image/png"}}},
	}}
	if !observedWrite(&db, "handbook", "README.md", hash, strings.Repeat("c", 40), nil, nil, true) {
		t.Fatal("prepared write was not observed")
	}
	if db.Outbox["first"].State != "complete" || db.Outbox["later"].State != "pending" {
		t.Fatal("later write confirmed without its image")
	}
}
func TestGitHubPrivateVisibilityAndUnpublishedEdits(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	s.put("handbook", "README.md", "# Private pending canary", 1)
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("private source disclosed")
	}
	f.Modify(func() { f.Private = false })
	s.retryConnection("handbook")
	m.Once(context.Background())
	p, e := s.page("handbook/README.md", true)
	if e != nil || p.Text != "# Upstream" {
		t.Fatal("private pending edit published", p, e)
	}
	owner, _ := s.page("handbook/README.md", false)
	if owner.Text != "# Private pending canary" {
		t.Fatal("local edit lost")
	}
	f.Modify(func() { f.Private = true })
	s.retryConnection("handbook")
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("repository downgrade did not revoke public access")
	}
}
func TestGitHubRevocationOutageAndConflictPreserveLocal(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Local", 1)
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Remote"})
	m.Once(context.Background())
	if len(s.conflicts()) != 1 || f.WriteCount() != 0 {
		t.Fatal("upstream conflict overwritten")
	}
	f.Modify(func() { f.Denied = true })
	s.retryConnection("handbook")
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("revoked source remained public")
	}
	if p, e := s.page("handbook/README.md", false); e != nil || p.Text != "# Local" {
		t.Fatal("local availability lost")
	}
}
func TestGitHubCredentialsRedactedAndAppTokensScoped(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/a.md": "# A"})
	account, e := s.saveAccount(context.Background(), f.Client(), "operator", github.Secret{Kind: "token", Token: githubtest.Access})
	if e != nil || account.SecretRef != "" {
		t.Fatal(account, e)
	}
	state, e := os.ReadFile(path.Join(s.dir, "state.json"))
	if e != nil || strings.Contains(string(state), githubtest.Access) {
		t.Fatal("credential stored in article transaction")
	}
	metadata, _ := json.Marshal(s.accounts())
	if strings.Contains(string(metadata), githubtest.Access) || strings.Contains(string(metadata), "secret_ref") {
		t.Fatal("credential response leaked")
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	secret := github.Secret{Kind: "app", AppID: 7, InstallationID: 9, PrivateKey: string(encoded)}
	if login, e := f.Client().VerifyAccount(context.Background(), secret); e != nil || login != "org" {
		t.Fatal(login, e)
	}
	if _, e = f.Client().Token(context.Background(), &secret, "org/handbook", true); e != nil {
		t.Fatal(e)
	}
	jwt, e := github.AppJWT(secret)
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT")
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if e = rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature); e != nil {
		t.Fatal("JWT signature invalid", e)
	}
}

func TestGitHubCompareAndSwapNeverForcesUpdates(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Local", 1)
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	if f.WriteCount() != 0 || s.jobs()[0].State != "retry" || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("CAS refusal lost work", s.jobs())
	}
	p, _ := s.page("handbook/README.md", false)
	if p.Text != "# Local" {
		t.Fatal("local write lost")
	}
	f.Modify(func() { f.Reject = false })
	s.retryConnection("handbook")
	m.Once(context.Background())
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}
func TestGitHubMultipleRepositoriesStayIndependent(t *testing.T) {
	s := testStore(t)
	one := githubtest.New(t, map[string]string{"docs/guide.md": "# Public one"})
	two := githubtest.New(t, map[string]string{"docs/guide.md": "# Private two"})
	two.Modify(func() { two.Name = "org/second"; two.RepoID = 42; two.Private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			two.Serve(w, r)
		} else {
			one.Serve(w, r)
		}
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access})
	if _, e := m.connect(context.Background(), connectRequest{ID: "one", Repository: "org/handbook"}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.connect(context.Background(), connectRequest{ID: "two", Repository: "org/second"}); e == nil {
		t.Fatal("connector guessed a saved credential")
	}
	if _, e := m.connect(context.Background(), connectRequest{ID: "two", Repository: "org/second", AccountID: "operator"}); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	if len(s.snapshot(false).Pages) != 2 || len(s.snapshot(true).Pages) != 1 {
		t.Fatal("merged visibility failure")
	}
	p, _ := s.page("one/docs/guide.md", true)
	if p.Text != "# Public one" {
		t.Fatal(p)
	}
	if _, e := s.page("two/docs/guide.md", true); e != ErrNotFound {
		t.Fatal("private repository disclosed")
	}
}
func TestGitHubPauseAndExpiredProofFailClosed(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Public"})
	m := connectFixture(t, s, f, false)
	if e := s.pauseConnection("handbook", true); e != nil {
		t.Fatal(e)
	}
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("paused connection public")
	}
	m.Once(context.Background())
	s.pauseConnection("handbook", false)
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 1 {
		t.Fatal("resumed source not verified")
	}
	s.mu.Lock()
	c := s.db.Connections["handbook"]
	c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	s.db.Connections[c.ID] = c
	s.mu.Unlock()
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("expired verification published")
	}
	if len(s.snapshot(false).Pages) != 1 {
		t.Fatal("owner lost offline data")
	}
}
func TestGitHubManagementAPIProtectsCredentials(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Public"})
	m := newSyncManager(s, f.Client())
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	body := `{"id":"operator","kind":"token","token":"` + githubtest.Access + `"}`
	if r := request(w, "POST", "/api/v1/github/accounts", body, true); r.Code != 403 {
		t.Fatal("LAN HTTP credentials accepted")
	}
	r := httptest.NewRequest("POST", "/api/v1/github/accounts", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer test-owner")
	response := httptest.NewRecorder()
	w.ServeHTTP(response, r)
	if response.Code != 201 || strings.Contains(response.Body.String(), githubtest.Access) || strings.Contains(response.Body.String(), "secret_ref") {
		t.Fatal(response.Code, response.Body)
	}
	for _, route := range []string{"/api/v1/github/accounts", "/api/v1/github/connections", "/api/v1/github/outbox"} {
		if r := request(w, "GET", route, "", false); r.Code != 404 {
			t.Fatal(route, r.Code)
		}
	}
	if r := request(w, "POST", "/api/v1/github/connections", `{"id":"handbook","repository":"org/handbook","account_id":"operator","write":true}`, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body)
	}
	m.Once(context.Background())
	if r := request(w, "GET", "/api/v1/github/outbox?limit=0", "", true); r.Code != 400 {
		t.Fatal(r.Code)
	}
	w.Public = true
	if r := request(w, "GET", "/api/v1/github/accounts", "", true); r.Code != 404 {
		t.Fatal("public listener accepted owner key")
	}
}

func TestGitHubEnableWritesPreservesQueuedLocalArticles(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Original"})
	m := connectFixture(t, s, f, false)
	s.put("handbook", "docs/guide.md", "# Local", 1)
	m.Once(context.Background())
	if s.jobs()[0].State != "blocked" || f.WriteCount() != 0 {
		t.Fatal("read-only connection wrote remotely")
	}
	if _, e := s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); e != nil {
		t.Fatal(e)
	}
	account, write, expected := "operator", true, 1
	c, e := m.configure(context.Background(), configureRequest{ID: "handbook", AccountID: &account, Write: &write, Expected: &expected})
	if e != nil || c.ConfigRevision != 2 {
		t.Fatal(c, e)
	}
	if _, e := m.configure(context.Background(), configureRequest{ID: "handbook", Write: &write, Expected: &expected}); e != ErrConflict {
		t.Fatal("stale configuration accepted", e)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}

func TestGitHubPrivateQueuedWritesRequirePublicationConsent(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Private unsent canary", 1)
	f.Modify(func() { f.Private = false })
	m.Once(context.Background())
	jobs := s.jobs()
	if f.WriteCount() != 0 || len(jobs) != 1 || jobs[0].LastError != "publication_confirmation_required" {
		t.Fatal("private work auto-published", jobs)
	}
	public, e := s.article("handbook", "README.md", true)
	if e != nil || public.Versions[0].Text != "# Upstream" || public.Versions[0].Number != 0 {
		t.Fatal("public history contains private metadata", public, e)
	}
	if e = s.publishJob(jobs[0].ID, 2, false); e != ErrInvalid {
		t.Fatal("implicit publication accepted")
	}
	if e = s.publishJob(jobs[0].ID, 1, true); e != ErrConflict {
		t.Fatal("stale publication accepted")
	}
	if e = s.connectionStatusFor(s.connections()[0], "syncing", "verified", "", nil, time.Time{}); e != nil {
		t.Fatal(e)
	}
	if e = s.publishJob(jobs[0].ID, 2, true); e != nil {
		t.Fatal(e)
	}
	// Approval publishes the reviewed revision exactly as it does between syncs;
	// a sync in progress neither hides it nor resets the connection state.
	if page, e := s.page("handbook/README.md", true); e != nil || page.Text != "# Private unsent canary" || s.connections()[0].State != "syncing" {
		t.Fatal("approved publication not served consistently during sync", page.Text, e)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}

func TestGitHubExpiredPublicProofDoesNotApproveQueuedWrite(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	m := connectFixture(t, s, f, true)
	s.mu.Lock()
	c := s.db.Connections["handbook"]
	c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	s.db.Connections[c.ID] = c
	s.mu.Unlock()
	if _, e := s.put("handbook", "README.md", "# Unverified edit", 1); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if f.WriteCount() != 0 || len(jobs) != 1 || jobs[0].LastError != "publication_confirmation_required" {
		t.Fatal("expired proof allowed publication", jobs)
	}
}

// A sync in progress must not hide verified public content, but its fresh
// visibility check must not extend the previous snapshot's proof either.
func TestPublicSnapshotStaysReadableDuringSync(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	connectFixture(t, s, f, false)
	if _, e := s.page("handbook/README.md", true); e != nil {
		t.Fatal("initial public article unavailable", e)
	}
	c := s.connections()[0]
	public := false
	info := github.RepositoryInfo{ID: c.RepositoryID, Private: &public}
	if e := s.connectionStatusFor(c, "syncing", "verified", "", &info, time.Time{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.page("handbook/README.md", true); e != nil {
		t.Fatal("public article hidden while syncing", e)
	}
	if s.connections()[0].VerifiedAt != c.VerifiedAt {
		t.Fatal("sync start extended the previous snapshot's visibility proof")
	}
	private := true
	info.Private = &private
	if e := s.connectionStatusFor(c, "syncing", "verified", "", &info, time.Time{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.page("handbook/README.md", true); e == nil {
		t.Fatal("repository turned private but stayed public during sync")
	}
	if _, e := s.page("handbook/README.md", false); e != nil {
		t.Fatal("private local copy lost", e)
	}
}
