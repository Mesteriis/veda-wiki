package translation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestLocalOpenAITranslationResponse(t *testing.T) {
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("program", "docs/a.md", "# Setup `install`", 0); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string                           `json:"model"`
			Messages []struct{ Role, Content string } `json:"messages"`
			Template json.RawMessage                  `json:"chat_template_kwargs"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "fixture" || len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Role != "user" || body.Messages[1].Content != "# Setup `install`" || body.Template != nil {
			t.Error("invalid OpenAI-compatible request")
		}
		jsonResponse(w, 200, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": "# Установка `install`"}}}})
	}))
	defer srv.Close()
	worker, err := NewWorker(s.Store, srv.URL, "fixture", "openai")
	if err != nil {
		t.Fatal(err)
	}
	worker.Once(context.Background())
	a, err := s.article("program", "docs/a.md", false)
	if err != nil || a.Translation == nil || a.Translation.Text != "# Установка `install`" {
		t.Fatal("OpenAI-compatible translation not saved", err)
	}
}
