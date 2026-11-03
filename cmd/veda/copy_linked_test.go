package main

import (
	"github.com/Mesteriis/veda-wiki/internal/render"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"strings"
	"testing"
)

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
	in := transfer.CopyRequest{SourceID: "from", SourcePath: "chapter/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "other/b.md", IncludeLinked: true}
	plan, err := s.planTransfer(in)
	if err != nil || len(plan.Articles) != 2 {
		t.Fatal("root link was not planned", err)
	}
	if _, err = s.copyArticle(in); err != nil {
		t.Fatal("root dependencies not copied", err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if reopened.DB().Sources["to"].Assets["img/icon.png"] != ref || reopened.DB().Articles["to/guide.md"].Versions[0].Text != "# Guide" || reopened.DB().Articles["to/other/b.md"].Versions[0].Text != text {
		t.Fatal("root dependencies changed after copy")
	}
	page, err := reopened.page("to/other/b.md", false)
	if err != nil {
		t.Fatal(err)
	}
	html, _, err := render.Page(page)
	if err != nil || !strings.Contains(string(html), "page=to%2Fguide.md#start") || !strings.Contains(string(html), "/media/to/img%2Ficon.png") {
		t.Fatal("copied dependencies do not render", err)
	}
}
