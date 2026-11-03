package main

import (
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"testing"
)

func TestHiddenPartialSourceRequiresPublicationApprovalForTransfer(t *testing.T) {
	for _, op := range []string{"copy", "copy-linked", "move", "move-linked"} {
		t.Run(op, func(t *testing.T) {
			s := testStore(t)
			linked := op == "copy-linked" || op == "move-linked"
			text := "# Root"
			if linked {
				text += "\n\n[Other](b.md)"
			}
			if op == "copy" || op == "copy-linked" {
				batch := testBatch()
				batch.Visibility = "public"
				batch.Withheld = 1
				batch.Files = map[string]string{"a.md": text, "b.md": "# Other"}
				if _, err := s.importBatch(batch); err != nil {
					t.Fatal(err)
				}
			} else {
				f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": text, "b.md": "# Other"})
				connectFixture(t, s, f, true)
				s.mu.Lock()
				src := s.db.Sources["handbook"]
				src.ImportPartial = true
				s.db.Sources["handbook"] = src
				s.mu.Unlock()
			}
			source(t, s, "target", "public")
			if _, err := s.page("handbook/a.md", true); err != ErrNotFound {
				t.Fatal("partial source unexpectedly visible", err)
			}
			copyReq := copyRequest{SourceID: "handbook", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "a.md", IncludeLinked: linked}
			moveReq := moveRequest{SourceID: "handbook", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "a.md", IncludeLinked: linked, ConfirmMove: true}
			run := func(approved bool) error {
				if op == "copy" || op == "copy-linked" {
					copyReq.ConfirmPublication = approved
					_, err := s.copyArticle(copyReq)
					return err
				}
				moveReq.ConfirmPublication = approved
				_, err := s.moveArticle(moveReq)
				return err
			}
			before, err := json.Marshal(s.db)
			if err != nil {
				t.Fatal(err)
			}
			if err := run(false); err != ErrInvalid {
				t.Fatal("hidden source published without approval", err)
			}
			after, err := json.Marshal(s.db)
			if err != nil || string(before) != string(after) {
				t.Fatal("refused transfer changed state", err)
			}
			if err := run(true); err != nil {
				t.Fatal("approved transfer failed", err)
			}
			if page, err := s.page("target/a.md", true); err != nil || page.Text != text {
				t.Fatal("approved result not public", err)
			}
		})
	}
}
