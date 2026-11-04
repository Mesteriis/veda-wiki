package translation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestLocalMLXTranslationResponse(t *testing.T) {
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
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
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "mlx")
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
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup\n\nRun `install` and read [guide](guide.md).", 0); err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(s.Store, endpoint, model, "mlx")
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
	original, err := os.ReadFile("../../docs/import.md")
	if err != nil {
		t.Fatal(err)
	}
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/import.md", string(original), 0); err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(s.Store, endpoint, model, "mlx")
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
	_, err = s.SaveDraft(Request{SourceID: "program", Path: "docs/import.md", ExpectedRevision: 1, OriginalSHA: rev.Versions[0].SHA, Text: translated})
	if err != nil {
		t.Fatalf("translated documentation rejected: %v", err)
	}
}

// Explicit opt-in acceptance on public project documentation only.
func TestRealLocalMLXProjectDocumentationSections(t *testing.T) {
	endpoint, model := os.Getenv("VEDA_TEST_MLX_URL"), os.Getenv("VEDA_TEST_MLX_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("local MLX acceptance not configured")
	}
	original, err := os.ReadFile("../../docs/import.md")
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := translationChunks(string(original), 4000)
	if err != nil {
		t.Fatal("public documentation cannot be safely split", err)
	}
	worker, err := NewWorker(newFixture(t).Store, endpoint, model, "mlx")
	if err != nil {
		t.Fatal(err)
	}
	var result strings.Builder
	for i, chunk := range chunks {
		translated, err := worker.translate(context.Background(), chunk)
		if err != nil {
			t.Fatalf("section %d rejected: %v", i, err)
		}
		if !reflect.DeepEqual(protectedMarkdown(chunk), protectedMarkdown(translated)) {
			t.Fatalf("section %d changed protected Markdown", i)
		}
		if result.Len() > 0 {
			result.WriteString("\n\n")
		}
		result.WriteString(translated)
	}
	if !reflect.DeepEqual(protectedMarkdown(string(original)), protectedMarkdown(result.String())) {
		t.Fatal("assembled document changed protected Markdown")
	}
	if !containsRussianProse(result.String()) {
		t.Fatal("insufficient Russian prose")
	}
}
