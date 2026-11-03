package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Explicit opt-in acceptance on public project documentation only.
func TestRealLocalMLXProjectDocumentationSections(t *testing.T) {
	endpoint, model := os.Getenv("VEDA_TEST_MLX_URL"), os.Getenv("VEDA_TEST_MLX_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("local MLX acceptance not configured")
	}
	original, err := os.ReadFile("../docs/import.md")
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := translationChunks(string(original), 4000)
	if err != nil {
		t.Fatal("public documentation cannot be safely split", err)
	}
	worker, err := newTranslationWorkerProtocol(testStore(t), endpoint, model, "mlx")
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
