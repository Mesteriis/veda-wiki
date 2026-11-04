package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

const credentialFixture = "password=synthetic-fixture-only"

func TestLegacyCredentialLikePublicJobIsBlockedAndCorrectable(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "guide.md", "# Safe", 1); err != nil {
		t.Fatal(err)
	}
	// Model a queued revision created before the public write guard existed.
	s.mutate(func(db *domain.Database) {
		a := db.Articles["handbook/guide.md"]
		a.Versions = append([]domain.Revision(nil), a.Versions...)
		a.Versions[1].Text, a.Versions[1].SHA = credentialFixture, domain.Digest([]byte(credentialFixture))
		db.Articles["handbook/guide.md"] = a
		for id, job := range db.Outbox {
			if job.SourceID == "handbook" && job.Path == "guide.md" && job.Revision == 2 {
				job.SHA = a.Versions[1].SHA
				db.Outbox[id] = job
			}
		}
	})
	m.Once(context.Background())
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].State != "blocked" || jobs[0].LastError != "credential_like_content" || f.WriteCount() != 0 {
		t.Fatal("unsafe job not blocked", jobs)
	}
	if _, err := s.put("handbook", "guide.md", "# Corrected", 2); err != nil {
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
	if _, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	s.mutate(func(db *domain.Database) {
		a := db.Articles["handbook/b.md"]
		a.Versions = append([]domain.Revision(nil), a.Versions...)
		a.Versions[0].Text, a.Versions[0].SHA = credentialFixture, domain.Digest([]byte(credentialFixture))
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
	})
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
	db := s.db()
	db.Outbox = map[string]domain.SyncJob{
		"old":      {ID: "old", SourceID: "handbook", Path: "guide.md", Revision: 1, State: "blocked", LastError: "credential_like_content", Assets: map[string]domain.BlobRef{"img/icon.png": ref}},
		"prepared": {ID: "prepared", SourceID: "handbook", Path: "guide.md", Revision: 1, State: "blocked", LastError: "credential_like_content", PreparedCommit: "uncertain"},
	}
	rev := domain.Revision{Number: 2, Text: "# Safe\n\n![icon](img/icon.png)", SHA: domain.Digest([]byte("# Safe\n\n![icon](img/icon.png)"))}
	domain.EnqueueWrite(&db, domain.Article{SourceID: "handbook", Path: "guide.md"}, rev, time.Now())
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
	s.mutate(func(db *domain.Database) {
		src := db.Sources["from"]
		src.Visibility = "public"
		db.Sources["from"] = src
	})
	before, err := json.Marshal(s.db())
	if err != nil {
		t.Fatal(err)
	}
	copy := transfer.CopyRequest{SourceID: "from", SourcePath: "unsafe.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "unsafe.md", ConfirmPublication: true}
	if _, err = s.copyArticle(copy); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("legacy public sensitive copy accepted", err)
	}
	linked := transfer.CopyRequest{SourceID: "from", SourcePath: "root.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "root.md", IncludeLinked: true, ConfirmPublication: true}
	if _, err = s.copyArticle(linked); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("legacy public sensitive bundle accepted", err)
	}
	move := transfer.MoveRequest{SourceID: "from", SourcePath: "unsafe.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "unsafe.md", ConfirmMove: true, ConfirmPublication: true}
	if _, err = s.moveArticle(move); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("legacy public sensitive move accepted", err)
	}
	after, err := json.Marshal(s.db())
	if err != nil || string(before) != string(after) {
		t.Fatal("failed transfer changed state", err)
	}
}
