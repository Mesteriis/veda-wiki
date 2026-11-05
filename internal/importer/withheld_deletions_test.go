package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestWithheldPathsDoNotFreezeUpstreamDeletions(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	b.Files["guide.md"] = "# Guide"
	b.Files["notes.md"] = "# Notes"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	// Upstream deleted guide.md and made notes.md credential-like.
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Initial"}
	b.Commit = strings.Repeat("b", 40)
	b.Complete, b.Withheld, b.WithheldPaths = false, 1, map[string]bool{"notes.md": true}
	r, err := s.importBatch(b)
	if err != nil || r.Deleted != 1 || r.Conflicts != 0 || r.Withheld != 1 {
		t.Fatal(r, err)
	}
	if _, err := s.page("handbook/guide.md"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("upstream deletion frozen by a withheld file", err)
	}
	notes, err := s.article("handbook/notes.md")
	if err != nil || len(notes.Versions) != 1 || notes.Versions[0].Text != "# Notes" || len(notes.Conflicts) != 0 {
		t.Fatal("withheld article lost its previous revision", err)
	}
	if !s.db().Sources["handbook"].ImportPartial {
		t.Fatal("withheld file did not keep the import partial")
	}
	// A locally changed article deleted upstream still becomes a conflict,
	// and the withheld article is still not a deletion.
	if _, err := s.put("handbook", "README.md", "# Local edit", 1); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{}
	b.Commit = strings.Repeat("c", 40)
	r, err = s.importBatch(b)
	if err != nil || r.Conflicts != 1 || r.Deleted != 0 {
		t.Fatal(r, err)
	}
	readme, err := s.article("handbook/README.md")
	if err != nil || len(readme.Conflicts) != 1 || !readme.Conflicts[0].Deleted || readme.Versions[len(readme.Versions)-1].Text != "# Local edit" {
		t.Fatal("deletion conflict not recorded", err)
	}
	if _, err := s.article("handbook/notes.md"); err != nil {
		t.Fatal("withheld article tombstoned", err)
	}
}

func TestWithheldPathsMustAccountForEveryWithheldFile(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	b.Files["guide.md"] = "# Guide"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Initial"}
	b.Complete = false
	for name, change := range map[string]func(Batch) Batch{
		"more paths than withheld files": func(b Batch) Batch { b.Withheld, b.WithheldPaths = 0, map[string]bool{"x.md": true}; return b },
		"withheld path also imported":    func(b Batch) Batch { b.Withheld, b.WithheldPaths = 1, map[string]bool{"README.md": true}; return b },
		"unsafe withheld path":           func(b Batch) Batch { b.Withheld, b.WithheldPaths = 1, map[string]bool{"../x.md": true}; return b },
	} {
		if _, err := s.importBatch(change(b)); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(name, err)
		}
	}
	// An unnamed withheld file leaves the tree coverage unproven.
	b.Withheld, b.WithheldPaths = 2, map[string]bool{"secret.md": true}
	if r, err := s.importBatch(b); err != nil || r.Deleted != 0 {
		t.Fatal(r, err)
	}
	if len(s.livePages()) != 2 {
		t.Fatal("unproven coverage deleted an article")
	}
}

func TestDecodeReadsWithheldPaths(t *testing.T) {
	b := testBatch()
	b.Complete, b.Withheld, b.WithheldPaths = false, 1, map[string]bool{"secret.md": true}
	body, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Batch
	r := httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(body))
	if err := Decode(httptest.NewRecorder(), r, &decoded); err != nil || decoded.Withheld != 1 || !decoded.WithheldPaths["secret.md"] || !decoded.ObservedTree() {
		t.Fatal("withheld paths lost in transport", err)
	}
}
