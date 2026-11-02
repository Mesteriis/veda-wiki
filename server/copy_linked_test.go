package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCopyLinkedLocalCyclesAndAssets(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "[B](b.md) ![icon](img/icon.png)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.md", "b.md"} {
		if s.db.Articles["from/"+p].Deleted || s.db.Articles["to/"+p].CopiedFrom == nil {
			t.Fatal("copy or source missing", p)
		}
	}
	if s.db.Sources["to"].Assets["img/icon.png"] != ref {
		t.Fatal("asset missing")
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Articles["to/b.md"].Versions[0].Text != "[A](a.md)" {
		t.Fatal("batch not persisted", err)
	}
}
func TestCopyLinkedLocalRejectsConflictWithoutPartialWrites(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# expected", 0)
	s.put("to", "b.md", "# different", 0)
	before, _ := json.Marshal(s.db)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true})
	after, _ := json.Marshal(s.db)
	if err != ErrConflict || string(before) != string(after) {
		t.Fatal("conflict changed store", err)
	}
}

func TestTransferDirectoryLinkFailsWithoutChangingEitherSource(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "linked"}[linked], func(t *testing.T) {
			s := testStore(t)
			source(t, s, "from", "private")
			source(t, s, "to", "private")
			if _, err := s.put("from", "guide.md", "# Guide\n\n[Docs](docs/)", 0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.put("from", "docs/README.md", "# Docs", 0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.put("to", "docs/README.md", "# Docs", 0); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(s.db)
			_, err := s.planTransfer(copyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md"})
			if err != ErrInvalid {
				t.Fatal("directory link transfer plan was accepted", err)
			}
			_, err = s.copyArticle(copyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: linked})
			after, _ := json.Marshal(s.db)
			if err != ErrInvalid || string(before) != string(after) {
				t.Fatal("directory link copy did not fail atomically", err)
			}
			_, err = s.moveArticle(moveRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: linked, ConfirmMove: true})
			after, _ = json.Marshal(s.db)
			if err != ErrInvalid || string(before) != string(after) {
				t.Fatal("directory link move did not fail atomically", err)
			}
		})
	}
}
func TestCopyLinkedLocalChecksPublicationForEveryArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "public")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	in := copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true}
	if _, err := s.copyArticle(in); err != ErrInvalid {
		t.Fatal("unapproved publication", err)
	}
	if _, ok := s.db.Articles["to/a.md"]; ok {
		t.Fatal("partial publication")
	}
	in.ConfirmPublication = true
	if _, err := s.copyArticle(in); err != nil {
		t.Fatal(err)
	}
	if s.db.Articles["to/b.md"].Published == nil {
		t.Fatal("linked article not published")
	}
}

func TestCopyLinkedLocalMissingImageLeavesNoPartialBatch(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "![missing](missing.png)", 0)
	before, _ := json.Marshal(s.db)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true})
	after, _ := json.Marshal(s.db)
	if err != ErrInvalid || string(before) != string(after) {
		t.Fatal("missing image changed store", err)
	}
}

func TestCopyLinkedLocalRejectsSensitiveLinkedText(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "public")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "password=fixture-value-only", 0)
	before, _ := json.Marshal(s.db)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true})
	after, _ := json.Marshal(s.db)
	if err != ErrInvalid || string(before) != string(after) {
		t.Fatal("sensitive linked article was published", err)
	}
}

func TestCopyLinkedRootPathsPreserveSourceDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	if _, err := s.put("from", "guide.md", "# Guide", 0); err != nil {
		t.Fatal(err)
	}
	text := "[Guide](/guide.md#start)\n\n![Icon](/img/icon.png)"
	if _, err := s.put("from", "chapter/a.md", text, 0); err != nil {
		t.Fatal(err)
	}
	in := copyRequest{SourceID: "from", SourcePath: "chapter/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "other/b.md", IncludeLinked: true}
	plan, err := s.planTransfer(in)
	if err != nil || len(plan.Articles) != 2 {
		t.Fatal("root link was not planned", err)
	}
	if _, err = s.copyArticle(in); err != nil {
		t.Fatal("root dependencies not copied", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.db.Sources["to"].Assets["img/icon.png"] != ref || reopened.db.Articles["to/guide.md"].Versions[0].Text != "# Guide" || reopened.db.Articles["to/other/b.md"].Versions[0].Text != text {
		t.Fatal("root dependencies changed after copy")
	}
	page, err := reopened.page("to/other/b.md", false)
	if err != nil {
		t.Fatal(err)
	}
	html, _, err := render(page)
	if err != nil || !strings.Contains(string(html), "page=to%2Fguide.md#start") || !strings.Contains(string(html), "/media/to/img%2Ficon.png") {
		t.Fatal("copied dependencies do not render", err)
	}
}

func TestRootDependenciesRemainBoundWhenQueuedArticleChanges(t *testing.T) {
	ref := BlobRef{SHA: strings.Repeat("a", 64), MIME: "image/png"}
	if got := retainedPendingAssets("![Icon](/img/icon.png)", "nested/a.md", map[string]BlobRef{"img/icon.png": ref}); got["img/icon.png"] != ref {
		t.Fatal("root image dependency lost")
	}
	if got := retainedPendingLinks("[Guide](/guide.md)", "nested/a.md", map[string]string{"guide.md": "sha"}); got["guide.md"] != "sha" {
		t.Fatal("root link dependency lost")
	}
}

func TestRootAssetConflictRejectsEntireCopy(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	other, err := imageRef(append(smallPNG(t), 1))
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	db := s.clone()
	target := db.Sources["to"]
	target.Assets = map[string]BlobRef{"img/icon.png": other}
	db.Sources["to"] = target
	err = s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("from", "chapter/a.md", "![Icon](/img/icon.png)", 0); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(s.db)
	_, err = s.copyArticle(copyRequest{SourceID: "from", SourcePath: "chapter/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "other/b.md"})
	after, _ := json.Marshal(s.db)
	if err != ErrConflict || string(before) != string(after) {
		t.Fatal("conflicting root asset changed the store", err)
	}
}
