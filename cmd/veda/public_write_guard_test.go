package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http"
	"strings"
	"testing"
	"time"
)

const credentialFixture = "password=synthetic-fixture-only"

func TestPublicWriteRejectsCredentialLikeTextWithoutChangingHistory(t *testing.T) {
	s := testStore(t)
	source(t, s, "open", "public")
	source(t, s, "closed", "private")
	if _, err := s.put("open", "guide.md", "# Safe", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("open", "guide.md", credentialFixture, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("public update accepted", err)
	}
	if _, err := s.put("open", "new.md", credentialFixture, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("public creation accepted", err)
	}
	if _, err := s.put("closed", "guide.md", credentialFixture, 0); err != nil {
		t.Fatal("private write rejected", err)
	}
	a, err := s.article("open", "guide.md", false)
	if err != nil || len(a.Versions) != 1 || a.Versions[0].Text != "# Safe" {
		t.Fatal("failed public write changed history", err)
	}
	if _, err = s.article("open", "new.md", false); err != ErrNotFound {
		t.Fatal("failed public creation left article", err)
	}
	body, err := json.Marshal(map[string]any{"source_id": "open", "path": "api.md", "text": credentialFixture, "expected_revision": 0})
	if err != nil {
		t.Fatal(err)
	}
	response := request(Wiki{Store: s, Token: "test-owner"}, http.MethodPost, "/api/v1/articles", string(body), true)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), credentialFixture) {
		t.Fatal("unsafe REST write accepted or echoed", response.Code)
	}
}

func TestLegacyCredentialLikePublicJobIsBlockedAndCorrectable(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "guide.md", "# Safe", 1); err != nil {
		t.Fatal(err)
	}
	// Model a queued revision created before the public write guard existed.
	s.Lock()
	db := s.Clone()
	a := db.Articles["handbook/guide.md"]
	a.Versions = append([]Revision(nil), a.Versions...)
	a.Versions[1].Text, a.Versions[1].SHA = credentialFixture, digest([]byte(credentialFixture))
	db.Articles["handbook/guide.md"] = a
	for id, job := range db.Outbox {
		if job.SourceID == "handbook" && job.Path == "guide.md" && job.Revision == 2 {
			job.SHA = a.Versions[1].SHA
			db.Outbox[id] = job
		}
	}
	err := s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].State != "blocked" || jobs[0].LastError != "credential_like_content" || f.WriteCount() != 0 {
		t.Fatal("unsafe job not blocked", jobs)
	}
	if _, err = s.put("handbook", "guide.md", "# Corrected", 2); err != nil {
		t.Fatal(err)
	}
	jobs = s.jobs()
	for _, job := range jobs {
		if job.Revision == 2 && job.State != "superseded" {
			t.Fatal("corrected revision did not supersede blocked job")
		}
	}
	syncFixtureSource(t, m, "handbook")
	if f.WriteCount() != 1 || f.Contents()["guide.md"] != "# Corrected" {
		t.Fatal("corrected revision did not synchronize")
	}
}

func TestBundleCredentialCheckIgnoresImages(t *testing.T) {
	if !bundleHasCredentialLikeText(map[string][]byte{"a.md": []byte(credentialFixture)}) {
		t.Fatal("unsafe markdown accepted")
	}
	if bundleHasCredentialLikeText(map[string][]byte{"image.png": []byte(credentialFixture), "a.md": []byte("# Safe")}) {
		t.Fatal("binary image treated as markdown")
	}
}

func TestLegacyBundleBlocksCredentialLikeLinkedArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	if _, err := s.put("from", "a.md", "[B](b.md)", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("from", "b.md", "# Safe", 0); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	db := s.Clone()
	a := db.Articles["handbook/b.md"]
	a.Versions = append([]Revision(nil), a.Versions...)
	a.Versions[0].Text, a.Versions[0].SHA = credentialFixture, digest([]byte(credentialFixture))
	published := a.Versions[0]
	published.Number = 0
	a.Published = &published
	db.Articles["handbook/b.md"] = a
	for id, job := range db.Outbox {
		if job.SourceID == "handbook" && job.Path == "a.md" {
			bundle := make(map[string]string, len(job.Bundle))
			for name, sha := range job.Bundle {
				bundle[name] = sha
			}
			bundle["b.md"] = a.Versions[0].SHA
			job.Bundle = bundle
			db.Outbox[id] = job
		}
	}
	err := s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].State != "blocked" || jobs[0].LastError != "credential_like_content" || f.WriteCount() != 0 {
		t.Fatal("unsafe linked bundle was not blocked", jobs)
	}
}

func TestPrivateGitHubWriteKeepsCredentialLikeTextLocal(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "guide.md", credentialFixture, 1); err != nil {
		t.Fatal("private write rejected", err)
	}
	m.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["guide.md"] != "# Initial" {
		t.Fatal("private credential-like text reached repository")
	}
	if article, err := s.article("handbook", "guide.md", false); err != nil || article.Versions[1].Text != credentialFixture {
		t.Fatal("local private revision was lost", err)
	}
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].State != "blocked" || jobs[0].LastError != "credential_like_content" {
		t.Fatal("private job not blocked", jobs)
	}
}

func TestPrivateCredentialLikeWriteStaysPrivateAfterRepositoryPublication(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "guide.md", credentialFixture, 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Private = false })
	m.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["guide.md"] != "# Initial" {
		t.Fatal("private credential-like text reached public repository")
	}
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].State != "blocked" || jobs[0].LastError != "credential_like_content" {
		t.Fatal("unsafe job not blocked before publication review", jobs)
	}
	public, err := s.page("handbook/guide.md", true)
	if err != nil || public.Text != "# Initial" {
		t.Fatal("private revision leaked to public reader", err)
	}
}

func TestCorrectedBlockedJobRetainsReferencedDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "handbook", "private")
	ref := fixtureAsset(t, s, "handbook", "img/icon.png")
	db := s.Clone()
	db.Outbox = map[string]SyncJob{
		"old":      {ID: "old", SourceID: "handbook", Path: "guide.md", Revision: 1, State: "blocked", LastError: "credential_like_content", Assets: map[string]BlobRef{"img/icon.png": ref}},
		"prepared": {ID: "prepared", SourceID: "handbook", Path: "guide.md", Revision: 1, State: "blocked", LastError: "credential_like_content", PreparedCommit: "uncertain"},
	}
	rev := Revision{Number: 2, Text: "# Safe\n\n![icon](img/icon.png)", SHA: digest([]byte("# Safe\n\n![icon](img/icon.png)"))}
	enqueueWrite(&db, Article{SourceID: "handbook", Path: "guide.md"}, rev, time.Now())
	if db.Outbox["old"].State != "superseded" || db.Outbox["prepared"].State != "blocked" {
		t.Fatal("incorrect blocked-job supersession")
	}
	if db.Outbox["handbook/guide.md@2"].Assets["img/icon.png"] != ref {
		t.Fatal("still-referenced asset lost")
	}
}

func TestLegacyPublicCredentialTextCannotPropagateByCopyOrMove(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "public")
	if _, err := s.put("from", "unsafe.md", credentialFixture, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("from", "root.md", "[unsafe](unsafe.md)", 0); err != nil {
		t.Fatal(err)
	}
	// Model content made public by an earlier version before the write guard.
	s.Lock()
	db := s.Clone()
	src := db.Sources["from"]
	src.Visibility = "public"
	db.Sources["from"] = src
	err := s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(*s.DB())
	if err != nil {
		t.Fatal(err)
	}
	copy := copyRequest{SourceID: "from", SourcePath: "unsafe.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "unsafe.md", ConfirmPublication: true}
	if _, err = s.copyArticle(copy); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy public sensitive copy accepted", err)
	}
	linked := copyRequest{SourceID: "from", SourcePath: "root.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "root.md", IncludeLinked: true, ConfirmPublication: true}
	if _, err = s.copyArticle(linked); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy public sensitive bundle accepted", err)
	}
	move := moveRequest{SourceID: "from", SourcePath: "unsafe.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "unsafe.md", ConfirmMove: true, ConfirmPublication: true}
	if _, err = s.moveArticle(move); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy public sensitive move accepted", err)
	}
	after, err := json.Marshal(*s.DB())
	if err != nil || string(before) != string(after) {
		t.Fatal("failed transfer changed state", err)
	}
}
