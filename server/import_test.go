package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

const metadataFixture = "schema_version: 1\nkind: wiki\ntitle: Handbook\ndescription: Team documentation\n"

func gitFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		writeFixture(t, dir, name, text)
	}
	gitTest(t, dir, "init", "-q")
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-qm", "Fixture")
	return dir
}
func gitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	argv := append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null", "-c", "init.templateDir=", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)
	if out, e := exec.Command("git", argv...).CombinedOutput(); e != nil {
		t.Fatalf("Git fixture: %v %s", e, out)
	}
}
func writeFixture(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
func testBatch() ImportBatch {
	empty := ""
	return ImportBatch{SourceID: "handbook", Kind: "wiki", Metadata: &WikiMetadata{SchemaVersion: 1, Kind: "wiki", Title: "Handbook"}, Files: map[string]string{"README.md": "# Initial"}, Commit: strings.Repeat("a", 40), Complete: true, Create: true, Visibility: "private", ExpectedImport: &empty}
}
func applyAgain(s *Store, b ImportBatch) ImportBatch {
	b.Create = false
	g := s.db.Sources[b.SourceID].ImportGeneration
	b.ExpectedImport = &g
	return b
}
func smallPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if e := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}

func TestWikiMetadataStrictSchema(t *testing.T) {
	m, e := parseMetadata([]byte(metadataFixture + "url: https://example.org\n"))
	if e != nil || m.Title != "Handbook" {
		t.Fatal(m, e)
	}
	for _, body := range []string{metadataFixture + "visibility: public\n", metadataFixture + "---\ntitle: second\n", metadataFixture + "image: ../outside.png\n", metadataFixture + "title: Duplicate\n", "schema_version: 1\nkind: wiki\ntitle: &name Heading\ndescription: *name\n", metadataFixture + "url: javascript:alert(1)\n", strings.Replace(metadataFixture, "schema_version: 1", "schema_version: 2", 1)} {
		if _, e := parseMetadata([]byte(body)); e == nil {
			t.Fatalf("accepted invalid YAML: %s", body)
		}
	}
}
func TestCheckoutTypesMetadataAndExclusions(t *testing.T) {
	wiki := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "AGENTS.md": "# Routing", "topics/guide.md": "# Guide", ".hidden/secret.md": "exclude", "node_modules/pkg/README.md": "exclude", "app.go": "exclude"})
	batch, e := prepareCheckout(wiki, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if batch.Kind != "wiki" || batch.Dirty || len(batch.Files) != 2 || batch.Metadata.Title != "Handbook" {
		t.Fatalf("bad wiki observation: %+v", batch)
	}
	program := gitFixture(t, map[string]string{"README.md": "# Program", "docs/AGENTS.md": "# Context", "docs/guide.md": "# Installation", "src/main.go": "package main"})
	batch, e = prepareCheckout(program, "program")
	if e != nil || batch.Kind != "program" || len(batch.Files) != 2 {
		t.Fatal(batch, e)
	}
	if _, ok := batch.Files["README.md"]; ok {
		t.Fatal("program root README imported")
	}
	missing := gitFixture(t, map[string]string{"README.md": "# No documentation", "do/file.md": "not docs"})
	if _, e := prepareCheckout(missing, "program"); !errors.Is(e, ErrNoDocs) {
		t.Fatal(e)
	}
}
func TestCheckoutSymlinksSecretsAndSpecialFiles(t *testing.T) {
	repo := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Safe"})
	outside := filepath.Join(t.TempDir(), "outside.md")
	if e := os.WriteFile(outside, []byte("private outside fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(repo, "linked.md")); e != nil {
		t.Fatal(e)
	}
	writeFixture(t, repo, "credentials.md", "password=fixture-for-screening")
	gitTest(t, repo, "add", "credentials.md", "linked.md")
	batch, e := prepareCheckout(repo, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if batch.Complete || batch.Withheld != 2 || !batch.Dirty || len(batch.Files) != 1 {
		t.Fatal("unsafe paths not withheld", batch.Withheld)
	}
	writeFixture(t, repo, "pipe.md", "tracked before special-file replacement")
	gitTest(t, repo, "add", "pipe.md")
	if e := os.Remove(filepath.Join(repo, "pipe.md")); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(filepath.Join(repo, "pipe.md"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := prepareCheckout(repo, "handbook"); e == nil {
		t.Fatal("special file accepted")
	}
}

func TestCheckoutDoesNotPublishIgnoredOrUntrackedMarkdown(t *testing.T) {
	repo := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Tracked", ".gitignore": "*.local.md\nprivate-notes/\n"})
	writeFixture(t, repo, "secrets.local.md", "ignored private canary")
	writeFixture(t, repo, "untracked.md", "untracked private canary")
	writeFixture(t, repo, "private-notes/notes.md", "ignored directory canary")
	batch, e := prepareCheckout(repo, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if len(batch.Files) != 1 || batch.Files["README.md"] != "# Tracked" {
		t.Fatal("non-indexed Markdown entered import")
	}
	gitTest(t, repo, "add", "untracked.md")
	batch, e = prepareCheckout(repo, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if len(batch.Files) != 2 || !batch.Dirty {
		t.Fatal("explicitly staged documentation not observed")
	}
}
func TestImportIsAtomicIdempotentAndPersists(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	result, e := s.importBatch(b)
	if e != nil || result.Created != 1 {
		t.Fatal(result, e)
	}
	b = applyAgain(s, b)
	if r, e := s.importBatch(b); e != nil || r.Unchanged != 1 {
		t.Fatal(r, e)
	}
	b.Files = map[string]string{"README.md": "# Changed", "../invalid.md": "must reject"}
	if _, e := s.importBatch(b); e == nil {
		t.Fatal("invalid batch accepted")
	}
	p, e := s.page("handbook/README.md", false)
	if e != nil || p.Text != "# Initial" {
		t.Fatal("partial batch committed", e)
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	p, e = reopened.page("handbook/README.md", false)
	if e != nil || p.Text != "# Initial" {
		t.Fatal(e)
	}
}

func TestConcurrentImportsRequireCurrentGeneration(t *testing.T) {
	s := testStore(t)
	initial := testBatch()
	if _, e := s.importBatch(initial); e != nil {
		t.Fatal(e)
	}
	b := applyAgain(s, initial)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			batch := b
			batch.Files = map[string]string{"README.md": "# Change " + strings.Repeat("x", i+1)}
			_, e := s.importBatch(batch)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatal("concurrent import overwrote a newer generation", successes)
	}
}
func TestImportConflictsPreserveBothVersionsAndResolve(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("handbook", "README.md", "# Local", 1); e != nil {
		t.Fatal(e)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote"}
	b.Commit = strings.Repeat("b", 40)
	result, e := s.importBatch(b)
	if e != nil || result.Conflicts != 1 {
		t.Fatal(result, e)
	}
	p, _ := s.page("handbook/README.md", false)
	if p.Text != "# Local" {
		t.Fatal("local edit overwritten")
	}
	conflicts := s.conflicts()
	if len(conflicts) != 1 || conflicts[0].Conflict.Incoming != "# Remote" {
		t.Fatal("remote edit lost")
	}
	b = applyAgain(s, b)
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if len(s.conflicts()) != 1 {
		t.Fatal("retry duplicated conflict")
	}
	if e := s.resolveConflict("handbook", "README.md", 0, 1, "upstream"); !errors.Is(e, ErrConflict) {
		t.Fatal("stale conflict resolution accepted", e)
	}
	if e := s.resolveConflict("handbook", "README.md", 0, 2, "upstream"); e != nil {
		t.Fatal(e)
	}
	p, _ = s.page("handbook/README.md", false)
	if p.Text != "# Remote" || len(s.conflicts()) != 0 {
		t.Fatal("resolution failed")
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	a, e := reopened.article("handbook", "README.md", false)
	if e != nil || len(a.Versions) != 3 || a.Versions[1].Text != "# Local" {
		t.Fatal("history lost")
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Next remote"}
	if r, e := s.importBatch(b); e != nil || r.Updated != 1 {
		t.Fatal(r, e)
	}
}

func TestImportConflictSupersessionAndUpstreamRevert(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	s.importBatch(b)
	s.put("handbook", "README.md", "# Local", 1)
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote one"}
	s.importBatch(b)
	b = applyAgain(s, b)
	b.Commit = strings.Repeat("b", 40)
	s.importBatch(b)
	if len(s.db.Articles["handbook/README.md"].Conflicts) != 1 {
		t.Fatal("unchanged incoming duplicated")
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote two"}
	s.importBatch(b)
	if len(s.conflicts()) != 1 || len(s.db.Articles["handbook/README.md"].Conflicts) != 2 {
		t.Fatal("prior incoming history lost or still pending")
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Initial"}
	s.importBatch(b)
	if len(s.conflicts()) != 0 {
		t.Fatal("upstream revert retained obsolete conflict")
	}
	p, _ := s.page("handbook/README.md", false)
	if p.Text != "# Local" {
		t.Fatal("local edit overwritten on revert")
	}
}
func TestImportDeletionsPartialCoverageAndLocalConflict(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Files["other.md"] = "# Other"
	s.importBatch(b)
	b = applyAgain(s, b)
	b.Files = map[string]string{}
	b.Complete = false
	b.Withheld = 1
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if len(s.snapshot(false).Pages) != 2 {
		t.Fatal("partial coverage deleted articles")
	}
	s.put("handbook", "README.md", "# Local edit", 1)
	b = applyAgain(s, b)
	b.Complete = true
	b.Withheld = 0
	r, e := s.importBatch(b)
	if e != nil || r.Deleted != 1 || r.Conflicts != 1 {
		t.Fatal(r, e)
	}
	if len(s.snapshot(false).Pages) != 1 {
		t.Fatal("tombstone visibility")
	}
	if e := s.resolveConflict("handbook", "README.md", 0, 2, "local"); e != nil {
		t.Fatal(e)
	}
	b = applyAgain(s, b)
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if len(s.snapshot(false).Pages) != 1 {
		t.Fatal("locally retained article deleted")
	}
}
func TestImportMediaPrivacyIntegrityAndMarkdownLinks(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.Metadata.Image = "assets/cover.png"
	b.Assets = map[string][]byte{"assets/cover.png": smallPNG(t)}
	b.Files["README.md"] = "# Page\n\n![Cover](assets/cover.png)"
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", false); r.Code != 404 {
		t.Fatal("private image disclosed")
	}
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", true); r.Code != 200 || r.Header().Get("Content-Type") != "image/png" {
		t.Fatal(r.Code)
	}
	p, _ := s.page("handbook/README.md", false)
	body, _, e := render(p)
	if e != nil || !strings.Contains(string(body), "/media/handbook/assets%2Fcover.png") {
		t.Fatal(body, e)
	}
	ref := s.db.Sources["handbook"].Assets["assets/cover.png"]
	os.WriteFile(filepath.Join(s.dir, "blobs", ref.SHA), []byte("corrupt"), 0600)
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", true); r.Code != 503 {
		t.Fatal("corrupt asset served", r.Code)
	}
	w.Public = true
	if r := request(w, "GET", "/media/handbook/assets/cover.png", "", true); r.Code != 404 {
		t.Fatal("public listener image bypass")
	}
}
func TestImportAndConflictRESTBoundaries(t *testing.T) {
	s := testStore(t)
	w := Wiki{Store: s, Token: "test-owner"}
	b := testBatch()
	payload, _ := json.Marshal(b)
	if r := request(w, "POST", "/api/v1/import", string(payload), false); r.Code != 403 {
		t.Fatal(r.Code)
	}
	if r := request(w, "POST", "/api/v1/import", string(payload), true); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	for _, route := range []string{"/api/v1/conflicts", "/api/v1/import"} {
		if r := request(w, "GET", route, "", false); r.Code != 404 {
			t.Fatal(route, r.Code)
		}
	}
	s.put("handbook", "README.md", "# Local", 1)
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote private canary"}
	payload, _ = json.Marshal(b)
	request(w, "POST", "/api/v1/import", string(payload), true)
	if r := request(w, "GET", "/api/v1/conflicts?source_id=handbook&path=README.md&index=0", "", true); r.Code != 200 || !strings.Contains(r.Body.String(), "Remote private canary") {
		t.Fatal(r.Code)
	}
	if r := request(w, "POST", "/api/v1/conflicts/resolve", `{"source_id":"handbook","path":"README.md","index":0,"expected_revision":2,"resolution":"upstream"}`, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
}
