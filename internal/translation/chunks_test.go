package translation

import (
	"reflect"
	"strings"
	"testing"
)

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

func TestTranslationChunksAllowHTMLInFinalChunk(t *testing.T) {
	original := strings.Repeat("Hello ", 3000) + "\n\n" + strings.Repeat("World ", 3000) + "\n\n<div>Keep HTML text</div>\n"
	chunks, err := translationChunks(original, 32000)
	if err != nil || len(chunks) < 2 || strings.Join(chunks, "") != original {
		t.Fatal("safe final HTML chunk rejected", err)
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
