package main

import (
	"encoding/json"
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
