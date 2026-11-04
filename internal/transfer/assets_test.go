package transfer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestCopyImageRejectsCollisionAndPreservesSource(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	ref := s.asset("from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	if _, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"}); err != nil {
		t.Fatal(err)
	}
	if s.db().Sources["to"].Assets["img/icon.png"] != ref || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("copy lost data")
	}
	s.put("from", "b.md", "![icon](img/icon.png)", 0)
	s.mutate(func(db *domain.Database) {
		dst := db.Sources["to"]
		dst.Assets = map[string]domain.BlobRef{"img/icon.png": {SHA: strings.Repeat("a", 64), MIME: "image/png"}}
		db.Sources["to"] = dst
	})
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "b.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"})
	if err != domain.ErrConflict {
		t.Fatal("collision accepted", err)
	}
	if _, exists := s.db().Articles["to/b.md"]; exists {
		t.Fatal("failed copy committed")
	}
}

func TestCopyRejectsUnresolvedLinksAndRawHTML(t *testing.T) {
	for _, text := range []string{"[guide](other.md)", "<img src=\"local.png\">", "<a href=\"other.md\">link</a>"} {
		s := newFixture(t)
		s.source("from", "private")
		s.source("to", "private")
		s.put("from", "a.md", text, 0)
		if _, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"}); err != domain.ErrInvalid {
			t.Fatal("unresolved dependency accepted", err)
		}
	}
}

func TestImageTransferLimitsDependencies(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	ref := s.asset("from", "img/0.png")
	var content strings.Builder
	s.mutate(func(db *domain.Database) {
		src := db.Sources["from"]
		src.Assets = map[string]domain.BlobRef{}
		for i := 0; i < 2001; i++ {
			name := fmt.Sprintf("img/%d.png", i)
			src.Assets[name] = ref
			fmt.Fprintf(&content, "![image](%s)\n", name)
		}
		db.Sources["from"] = src
	})
	if _, err := s.put("from", "a.md", content.String(), 0); err != nil {
		t.Fatal(err)
	}
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"})
	if err != domain.ErrInvalid {
		t.Fatal("dependency limit not enforced", err)
	}
}
