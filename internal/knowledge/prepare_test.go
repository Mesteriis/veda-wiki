package knowledge

import (
	"fmt"
	"strings"
	"testing"
)

// checkMap verifies the source map contract: spans are ordered, inside the
// block and the source, and every mapped range is verbatim source text.
func checkMap(t *testing.T, markdown string, blocks []Block) {
	t.Helper()
	src := []rune(markdown)
	for _, b := range blocks {
		text := []rune(b.Text)
		prev := Span{}
		for i, s := range b.Map {
			if s.Start < prev.End || s.End <= s.Start || s.End > len(text) || s.SrcEnd > len(src) || s.SrcStart < prev.SrcEnd {
				t.Fatalf("block %s span %d out of order or range: %+v after %+v", b.ID, i, s, prev)
			}
			if s.End-s.Start != s.SrcEnd-s.SrcStart || string(text[s.Start:s.End]) != string(src[s.SrcStart:s.SrcEnd]) {
				t.Fatalf("block %s span %d is not verbatim: %q vs %q", b.ID, i, string(text[s.Start:s.End]), string(src[s.SrcStart:s.SrcEnd]))
			}
			prev = s
		}
	}
}

func prepare(t *testing.T, markdown string) []Block {
	t.Helper()
	blocks, err := Prepare(markdown)
	if err != nil {
		t.Fatal(err)
	}
	checkMap(t, markdown, blocks)
	return blocks
}

func texts(blocks []Block) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = b.Kind + ":" + b.Text
	}
	return out
}

func runeIndex(s, sub string, from int) int {
	r := []rune(s)
	for i := from; i+len([]rune(sub)) <= len(r); i++ {
		if string(r[i:i+len([]rune(sub))]) == sub {
			return i
		}
	}
	return -1
}

// quote finds the nth occurrence of sub in the block and checks that it maps
// back to the same text in the source.
func quote(t *testing.T, markdown string, b Block, sub string, nth int) (int, int) {
	t.Helper()
	start := -1
	for i := 0; i <= nth; i++ {
		start = runeIndex(b.Text, sub, start+1)
		if start < 0 {
			t.Fatalf("%q occurrence %d not in %q", sub, i, b.Text)
		}
	}
	end := start + len([]rune(sub))
	srcStart, srcEnd, exact := SourceRange(b, start, end)
	if !exact || string([]rune(markdown)[srcStart:srcEnd]) != sub {
		t.Fatalf("%q maps to [%d,%d) exact=%v", sub, srcStart, srcEnd, exact)
	}
	return srcStart, srcEnd
}

func TestPrepareCyrillicEmojiAndCombiningCharacters(t *testing.T) {
	md := "# Сервис 🚀 оплаты\n\nКафе́ «Ёлка» 👩‍💻 использует **PostgreSQL** и Redis.\n"
	blocks := prepare(t, md)
	want := []string{"heading:Сервис 🚀 оплаты", "paragraph:Кафе́ «Ёлка» 👩‍💻 использует PostgreSQL и Redis."}
	if got := texts(blocks); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	srcStart, srcEnd := quote(t, md, blocks[1], "Redis", 0)
	if srcStart != runeIndex(md, "Redis", 0) || srcEnd != srcStart+5 {
		t.Fatalf("Redis at [%d,%d)", srcStart, srcEnd)
	}
	quote(t, md, blocks[0], "оплаты", 0)
	quote(t, md, blocks[1], "Кафе́", 0)
	quote(t, md, blocks[1], "👩‍💻", 0)
	// The emphasis markers are not text: the quote spans a source gap.
	start := runeIndex(blocks[1].Text, "использует PostgreSQL", 0)
	srcStart, srcEnd, exact := SourceRange(blocks[1], start, start+len([]rune("использует PostgreSQL")))
	if exact || string([]rune(md)[srcStart:srcEnd]) != "использует **PostgreSQL" {
		t.Fatalf("cross-markup quote: [%d,%d) exact=%v", srcStart, srcEnd, exact)
	}
}

func TestPrepareRepeatedNamesMapToTheirOwnOccurrence(t *testing.T) {
	md := "API вызывает API, а *API* зовёт API.\n"
	b := prepare(t, md)[0]
	seen := map[int]bool{}
	for i := 0; i < 4; i++ {
		srcStart, _ := quote(t, md, b, "API", i)
		if seen[srcStart] {
			t.Fatalf("occurrence %d reuses source offset %d", i, srcStart)
		}
		seen[srcStart] = true
	}
}

func TestPrepareTableCellsAndListItemsAreSeparateBlocks(t *testing.T) {
	md := "- первый `код` пункт\n- второй\n  - вложенный\n\n| Сервис | Хост |\n|---|---|\n| billing | db-1 |\n|  | x |\n"
	blocks := prepare(t, md)
	want := []string{
		"list_item:первый  пункт", "list_item:второй", "list_item:вложенный",
		"table_cell:Сервис", "table_cell:Хост", "table_cell:billing", "table_cell:db-1", "table_cell:x",
	}
	if got := texts(blocks); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	quote(t, md, blocks[6], "db-1", 0)
	quote(t, md, blocks[0], "пункт", 0)
}

func TestPrepareExcludesCodeHTMLFrontmatterAndLinkTargets(t *testing.T) {
	md := "---\ntitle: Секрет\nhost: hidden-host\n---\n# Заголовок\n\n```go\nfmt.Println(\"code-host\")\n```\n\n    indented-host\n\n<div>html-host</div>\n\nСм. [docs](https://link-host/x) и <https://auto-host> <span>a</span> ![img-alt](i.png) end\n"
	blocks := prepare(t, md)
	joined := strings.Join(texts(blocks), "|")
	for _, hidden := range []string{"Секрет", "hidden-host", "code-host", "indented-host", "html-host", "link-host", "auto-host", "img-alt", "span"} {
		if strings.Contains(joined, hidden) {
			t.Fatalf("%q leaked into %q", hidden, joined)
		}
	}
	if !strings.Contains(joined, "heading:Заголовок") || !strings.Contains(joined, "docs") {
		t.Fatalf("visible text missing: %q", joined)
	}
	quote(t, md, blocks[0], "Заголовок", 0)

	toml := "+++\ntitle = \"x\"\n+++\nТекст\n"
	if got := texts(prepare(t, toml)); len(got) != 1 || got[0] != "paragraph:Текст" {
		t.Fatalf("toml frontmatter: %q", got)
	}
	// A thematic break that is not at the start is not frontmatter.
	if got := texts(prepare(t, "Текст\n\n---\n\nдальше\n")); len(got) != 2 {
		t.Fatalf("thematic break: %q", got)
	}
	// An unterminated block is not frontmatter either.
	if got := texts(prepare(t, "---\nоткрыто\n")); len(got) == 0 {
		t.Fatalf("unterminated frontmatter swallowed text")
	}
}

func TestPrepareLineBreaksAreGapsAndCRLF(t *testing.T) {
	md := "первая строка\r\nвторая\r\n"
	b := prepare(t, md)[0]
	if b.Text != "первая строка\nвторая" {
		t.Fatalf("text %q", b.Text)
	}
	quote(t, md, b, "вторая", 0)
	start := runeIndex(b.Text, "строка\nвторая", 0)
	if _, _, exact := SourceRange(b, start, start+len([]rune("строка\nвторая"))); exact {
		t.Fatal("quote across a line break reported exact")
	}
}

func TestSourceRangeRejectsInvalidRanges(t *testing.T) {
	b := prepare(t, "abc def\n")[0]
	for _, r := range [][2]int{{-1, 2}, {2, 2}, {3, 2}, {0, 99}} {
		if s, e, exact := SourceRange(b, r[0], r[1]); s != -1 || e != -1 || exact {
			t.Fatalf("%v gave %d %d %v", r, s, e, exact)
		}
	}
	if s, e, exact := SourceRange(b, 0, 7); s != 0 || e != 7 || !exact {
		t.Fatalf("whole block: %d %d %v", s, e, exact)
	}
}

func TestPrepareBlockIDsAreStableAndDistinct(t *testing.T) {
	md := "# A\n\nтекст\n\nтекст\n"
	first, second := prepare(t, md), prepare(t, md)
	ids := map[string]bool{}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatal("block IDs differ between runs")
		}
		if ids[first[i].ID] {
			t.Fatalf("duplicate ID %s", first[i].ID)
		}
		ids[first[i].ID] = true
	}
	edited := prepare(t, "# B\n\nтекст\n\nтекст\n")
	if edited[0].ID == first[0].ID || edited[1].ID != first[1].ID {
		t.Fatalf("IDs do not follow ordinal and text: %v %v", texts(edited), edited[0].ID)
	}
}

func TestPrepareLongArticleKeepsTheTail(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&sb, "## Раздел %d 🚀\n\nСервис svc-%d работает на host-%d, *см.* `cfg` раздел.\n\n", i, i, i)
	}
	sb.WriteString("Последний абзац с хвостом.\n")
	md := sb.String()
	blocks := prepare(t, md)
	if len(blocks) != 6001 {
		t.Fatalf("got %d blocks", len(blocks))
	}
	last := blocks[len(blocks)-1]
	srcStart, srcEnd := quote(t, md, last, "хвостом", 0)
	if srcEnd != len([]rune(md))-2 || srcStart != srcEnd-7 {
		t.Fatalf("tail at [%d,%d) of %d", srcStart, srcEnd, len([]rune(md)))
	}
	quote(t, md, blocks[5999], "host-2999", 0)
}

func TestPrepareRejectsInvalidUTF8(t *testing.T) {
	if _, err := Prepare("ok \xff"); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if blocks, err := Prepare(""); err != nil || len(blocks) != 0 {
		t.Fatal("empty article", blocks, err)
	}
}
