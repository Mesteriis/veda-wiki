package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestLocalTranslationWorkerSavesPrivateDraft(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/guide.md", "# Setup\n\nRun `install`.", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" || r.Method != "POST" {
			t.Error("unexpected request")
		}
		var input struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			Stream bool   `json:"stream"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Model != "fixture" || input.Prompt != "# Setup\n\nRun `install`." || input.Stream {
			t.Error("unexpected input")
		}
		jsonResponse(w, 200, map[string]any{"done": true, "done_reason": "stop", "response": "# Установка\n\nЗапустите `install`."})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	private, err := s.page("program/docs/guide.md", false)
	if err != nil || !private.Translated || private.Text != "# Установка\n\nЗапустите `install`." {
		t.Fatal("private draft missing", err)
	}
	public, err := s.page("program/docs/guide.md", true)
	if err != nil || public.Translated || public.Text != "# Setup\n\nRun `install`." {
		t.Fatal("unapproved translation published", err)
	}
	if candidates := s.translationCandidates(); len(candidates) != 1 || candidates[0].State != "publication_review" {
		t.Fatal("missing review", candidates)
	}
}

func TestOldBinaryRejectsTranslationOriginStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V11_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	programSource(t, s, "program", "private")
	rev, err := s.put("program", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.saveTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}, true)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.dir, "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
		t.Fatal("old binary did not reject version 11")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed state")
	}
}

func TestLocalTranslationOriginSurvivesRestartAndApproval(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "program", "public")
	rev, err := s.put("program", "docs/guide.md", "# Setup", 0)
	if err != nil {
		t.Fatal(err)
	}
	in := translationRequest{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Установка"}
	if _, err = s.saveTranslation(in, true); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil || s.db.Version != 11 {
		t.Fatal("origin store failed to reopen", err)
	}
	private, err := s.page("program/docs/guide.md", false)
	if err != nil || private.TranslationOrigin != "local_model" {
		t.Fatal("draft origin lost", err)
	}
	view := request(Wiki{Store: s, Token: "test-owner"}, "GET", "/api/state?page=program%2Fdocs%2Fguide.md", "", true)
	var response struct {
		View struct {
			Page *Page `json:"Page"`
		} `json:"view"`
	}
	if view.Code != 200 || json.Unmarshal(view.Body.Bytes(), &response) != nil || response.View.Page == nil || response.View.Page.TranslationOrigin != "local_model" {
		t.Fatal("reader API lost translation origin")
	}
	public, err := s.page("program/docs/guide.md", true)
	if err != nil || public.Translated || public.TranslationOrigin != "" {
		t.Fatal("draft origin exposed", err)
	}
	in.ConfirmPublication = true
	if _, err = s.submitTranslation(in); err != nil {
		t.Fatal(err)
	}
	public, err = s.page("program/docs/guide.md", true)
	if err != nil || !public.Translated || public.TranslationOrigin != "local_model" {
		t.Fatal("approval lost origin", err)
	}
	in.Text = "# Настройка"
	if _, err = s.submitTranslation(in); err != nil {
		t.Fatal(err)
	}
	public, err = s.page("program/docs/guide.md", true)
	if err != nil || public.TranslationOrigin != "" {
		t.Fatal("edited translation incorrectly attributed", err)
	}
}

func TestLocalTranslationOriginSurvivesMove(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	programSource(t, s, "to", "public")
	rev, err := s.put("from", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.saveTranslation(translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.moveArticle(moveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.page("to/docs/b.md", false)
	if err != nil || private.TranslationOrigin != "local_model" {
		t.Fatal("move lost origin", err)
	}
	public, err := s.page("to/docs/b.md", true)
	if err != nil || public.Translated || public.TranslationOrigin != "" {
		t.Fatal("move published draft", err)
	}
}

func TestLocalTranslationWorkerRejectsStaleOrAlteredCode(t *testing.T) {
	for _, stale := range []bool{false, true} {
		s := testStore(t)
		if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put("program", "docs/a.md", "# Run `install`", 0); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stale {
				if _, err := s.put("program", "docs/a.md", "# Changed `install`", 1); err != nil {
					t.Error(err)
				}
			}
			jsonResponse(w, 200, map[string]any{"done": true, "response": "# Запуск `wrong`"})
		}))
		worker, err := newTranslationWorker(s, srv.URL, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		worker.Once(context.Background())
		srv.Close()
		a, err := s.article("program", "docs/a.md", false)
		if err != nil || a.Translation != nil {
			t.Fatal("invalid translation saved", err)
		}
	}
}

func TestLocalTranslationWorkerRejectsRemoteOrAmbiguousEndpoints(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:11434", "http://192.0.2.1:11434", "https://127.0.0.1:11434", "http://127.0.0.1:11434/path", "http://user@127.0.0.1:11434"} {
		if _, err := newTranslationWorker(testStore(t), endpoint, "model"); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func TestLocalTranslationWorkerDoesNotOverwriteConcurrentOwnerTranslation(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	rev, err := s.put("program", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := s.submitTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод владельца"})
		if err != nil {
			t.Error(err)
		}
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Машинный перевод"})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Перевод владельца" {
		t.Fatal("owner translation overwritten", err)
	}
}

func TestLocalTranslationWorkerRejectsIncompleteAndUnchangedOutput(t *testing.T) {
	for _, fixture := range []struct {
		done           bool
		reason, output string
	}{
		{false, "", "# Перевод"}, {true, "length", "# Перевод"}, {true, "stop", "# Original"}, {true, "stop", "<think>analysis</think>\n# Перевод"},
	} {
		s := testStore(t)
		if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put("program", "docs/a.md", "# Original", 0); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, 200, map[string]any{"done": fixture.done, "done_reason": fixture.reason, "response": fixture.output})
		}))
		worker, err := newTranslationWorker(s, srv.URL, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		worker.Once(context.Background())
		srv.Close()
		a, err := s.article("program", "docs/a.md", false)
		if err != nil || a.Translation != nil {
			t.Fatal("incomplete or unchanged output accepted", fixture, err)
		}
	}
}

func TestOversizedDocumentDoesNotBlockOtherTranslations(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", strings.Repeat("a", 32001), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/b.md", "# Second", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Вторая"})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/b.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Вторая" {
		t.Fatal("shorter article blocked", err)
	}
}

func TestLocalTranslationWorkerProcessesLongDocumentBySection(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	original := "# First\n\n" + strings.Repeat("hello ", 3000) + "\n\n# Second\n\n" + strings.Repeat("world ", 3000) + "\n\n```sh\necho hello\n```\n"
	if _, err := s.put("program", "docs/long.md", original, 0); err != nil {
		t.Fatal(err)
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		requests++
		output := strings.ReplaceAll(strings.ReplaceAll(input.Prompt, "# First", "# Первый"), "# Second", "# Второй")
		output = strings.ReplaceAll(strings.ReplaceAll(output, "hello ", "привет "), "world ", "мир ")
		jsonResponse(w, 200, map[string]any{"done": true, "response": output})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/long.md", false)
	if err != nil || requests != 2 || a.Translation == nil || !strings.Contains(a.Translation.Text, "# Второй") || !strings.Contains(a.Translation.Text, "echo hello") {
		t.Fatal("long document not translated safely", requests, err)
	}
}

func TestTranslationScriptCheckIgnoresCodeAndLinks(t *testing.T) {
	for _, value := range []string{
		"# Setup\n\n```text\nРусский текст\n```\n",
		"# Setup `Русский` and [documentation](https://example.test/русский)",
		"# Setup\n\nThis output remains in English.",
	} {
		if containsRussianProse(value) {
			t.Fatal("non-Russian prose accepted")
		}
	}
	if !containsRussianProse("# Установка\n\nЗапустите `install` и откройте [документацию](guide.md).") {
		t.Fatal("Russian prose refused")
	}
}

func TestLocalTranslationWorkerRejectsNonRussianOutput(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup\n\nRun `install`.", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Installation\n\nRun `install` now."})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation != nil || len(worker.retry) != 1 {
		t.Fatal("non-Russian output was accepted", err)
	}
}

func TestTranslationChunksDoNotSplitInsideFencesOrHTML(t *testing.T) {
	largeFence := "# Heading\n\n```text\n# Fake heading\n" + strings.Repeat("x", 33000) + "\n```\n"
	if _, err := translationChunks(largeFence, 32000); err == nil {
		t.Fatal("oversized code fence split")
	}
	largeHTML := "# Heading\n\n<div>\n" + strings.Repeat("x", 17000) + "\n\n# Fake heading\n" + strings.Repeat("x", 17000) + "\n</div>\n"
	if _, err := translationChunks(largeHTML, 32000); err == nil {
		t.Fatal("HTML-containing document split")
	}
}

func TestLocalMLXTranslationResponse(t *testing.T) {
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup `install`", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("unexpected path")
		}
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
			Template map[string]bool                  `json:"chat_template_kwargs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 || body.Messages[1].Content != "# Setup `install`" || body.Template["enable_thinking"] {
			t.Error("invalid request")
		}
		jsonResponse(w, 200, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": "# Установка `install`"}}}})
	}))
	defer srv.Close()
	worker, err := newTranslationWorkerProtocol(s, srv.URL, "fixture", "mlx")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Установка `install`" {
		t.Fatal("MLX translation not saved", err)
	}
}

// Opt-in synthetic acceptance against an explicitly supplied local MLX server.
func TestRealLocalMLXSyntheticTranslation(t *testing.T) {
	endpoint, model := os.Getenv("VEDA_TEST_MLX_URL"), os.Getenv("VEDA_TEST_MLX_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("local MLX acceptance not configured")
	}
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup\n\nRun `install` and read [guide](guide.md).", 0); err != nil {
		t.Fatal(err)
	}
	worker, err := newTranslationWorkerProtocol(s, endpoint, model, "mlx")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || !strings.Contains(a.Translation.Text, "`install`") || !strings.Contains(a.Translation.Text, "(guide.md)") || !strings.ContainsFunc(a.Translation.Text, func(r rune) bool { return unicode.In(r, unicode.Cyrillic) }) {
		t.Fatal("local model did not produce a structurally valid translation", err)
	}
}

func TestRealLocalMLXProjectDocumentation(t *testing.T) {
	endpoint, model := os.Getenv("VEDA_TEST_MLX_URL"), os.Getenv("VEDA_TEST_MLX_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("local MLX acceptance not configured")
	}
	original, err := os.ReadFile("../docs/import.md")
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	if err := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/import.md", string(original), 0); err != nil {
		t.Fatal(err)
	}
	worker, err := newTranslationWorkerProtocol(s, endpoint, model, "mlx")
	if err != nil {
		t.Fatal(err)
	}
	parts, err := translationChunks(string(original), 32000)
	if err != nil {
		t.Fatal(err)
	}
	var outputs []string
	for _, part := range parts {
		translatedPart, err := worker.translate(context.Background(), part)
		if err != nil {
			t.Fatalf("local model response rejected before validation: %v", err)
		}
		outputs = append(outputs, translatedPart)
	}
	translated := strings.Join(outputs, "\n\n")
	before, after := protectedMarkdown(string(original)), protectedMarkdown(translated)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("model changed protected Markdown: original %d elements, translated %d elements", len(before), len(after))
	}
	if !strings.ContainsFunc(translated, func(r rune) bool { return unicode.In(r, unicode.Cyrillic) }) {
		t.Fatal("response contains no Cyrillic text")
	}
	rev, err := s.article("program", "docs/import.md", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.saveTranslation(translationRequest{SourceID: "program", Path: "docs/import.md", ExpectedRevision: 1, OriginalSHA: rev.Versions[0].SHA, Text: translated}, true)
	if err != nil {
		t.Fatalf("translated documentation rejected: %v", err)
	}
}

func TestTranslationChunksSplitTopLevelParagraphsAndKeepProtectedBlocks(t *testing.T) {
	original := strings.Repeat("Hello ", 3000) + "\n\n```sh\necho safe\n```\n\n" + strings.Repeat("Hello ", 3000) + "\n\n[Guide](guide.md)\n"
	chunks, err := translationChunks(original, 32000)
	if err != nil || len(chunks) < 2 || strings.Join(chunks, "") != original {
		t.Fatal("safe blocks not split losslessly", err)
	}
	var protected []string
	for _, chunk := range chunks {
		if len(chunk) > 32000 {
			t.Fatal("chunk exceeds limit")
		}
		protected = append(protected, protectedMarkdown(chunk)...)
	}
	if !reflect.DeepEqual(protectedMarkdown(original), protected) {
		t.Fatal("protected Markdown changed")
	}
}

func TestTranslationChunksRejectCrossChunkReferenceDefinition(t *testing.T) {
	original := "[Guide][ref] " + strings.Repeat("Hello ", 3000) + "\n\n" + strings.Repeat("World ", 3000) + "\n\n[ref]: guide.md\n"
	if _, err := translationChunks(original, 32000); err == nil {
		t.Fatal("reference split accepted")
	}
}

func TestLocalWorkerTranslatesLargeParagraphs(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "program", "private")
	original := strings.Repeat("Hello ", 3000) + "\n\n```sh\necho safe\n```\n\n" + strings.Repeat("Hello ", 3000)
	if _, err := s.put("program", "docs/guide.md", original, 0); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Prompt string `json:"prompt"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid request")
			return
		}
		calls++
		jsonResponse(w, 200, map[string]any{"done": true, "done_reason": "stop", "response": strings.ReplaceAll(input.Prompt, "Hello", "Привет")})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/guide.md", false)
	if err != nil || calls < 2 || a.Translation == nil || a.Translation.Origin != "local_model" || !reflect.DeepEqual(protectedMarkdown(original), protectedMarkdown(a.Translation.Text)) {
		t.Fatal("large paragraph translation failed", err)
	}
}

func TestLocalTranslationWorkerRejectsLostListStep(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "program", "private")
	if _, err := s.put("program", "docs/steps.md", "# Setup\n\n1. First\n2. Second\n", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Установка\n\n1. Первый\n"})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(s, srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	article, err := s.article("program", "docs/steps.md", false)
	if err != nil || article.Translation != nil || len(worker.retry) != 1 {
		t.Fatal("incomplete list was saved", err)
	}
}

func TestLocalTranslationBearerIsPrivateRotatableAndNeverRedirected(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("synthetic-one"), 0600); err != nil {
		t.Fatal(err)
	}
	expected := "Bearer synthetic-one"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != expected {
			t.Error("wrong local bearer")
		}
		jsonResponse(w, 200, map[string]any{"done": true, "response": "# Перевод"})
	}))
	defer srv.Close()
	worker, err := newTranslationWorker(testStore(t), srv.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.tokenFile = tokenPath
	if _, err = worker.translate(context.Background(), "# Original"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(tokenPath, []byte("synthetic-two"), 0600); err != nil {
		t.Fatal(err)
	}
	expected = "Bearer synthetic-two"
	if _, err = worker.translate(context.Background(), "# Original"); err != nil || calls != 2 {
		t.Fatal("rotation failed", err)
	}
	if err = os.Chmod(tokenPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || calls != 2 {
		t.Fatal("insecure file accepted")
	}
	if err = os.Chmod(tokenPath, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(tokenPath, link); err != nil {
		t.Fatal(err)
	}
	worker.tokenFile = link
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || calls != 2 {
		t.Fatal("symlink accepted")
	}
	worker.tokenFile = tokenPath
	if err = os.WriteFile(tokenPath, []byte("synthetic\ninvalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || calls != 2 {
		t.Fatal("invalid bearer accepted")
	}
	if err = os.WriteFile(tokenPath, []byte("synthetic-two"), 0600); err != nil {
		t.Fatal(err)
	}
	redirected := false
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true; w.WriteHeader(200) }))
	defer receiver.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, receiver.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	worker, err = newTranslationWorker(testStore(t), redirect.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worker.tokenFile = tokenPath
	if _, err = worker.translate(context.Background(), "# Original"); err == nil || redirected {
		t.Fatal("redirect sent local bearer")
	}
}
