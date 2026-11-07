package domain

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutlineListsHeadingsWithSectionSizes(t *testing.T) {
	text := "---\ntitle: Т\n# not a heading\n---\n# Сервер\nвступление\n## Сеть ##\nадреса\n```bash\n# comment, not a heading\n```\n### Порты\n80\n## Диски\n#hashtag\n    # indented code\nend"
	got := Outline(text)
	want := []struct {
		level int
		title string
		line  int
	}{{1, "Сервер", 5}, {2, "Сеть", 7}, {3, "Порты", 12}, {2, "Диски", 14}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	runes := []rune(text)
	for i, w := range want {
		s := got[i]
		if s.Level != w.level || s.Title != w.title || s.Line != w.line {
			t.Fatalf("%d: %+v", i, s)
		}
		section := string(runes[s.Offset : s.Offset+s.Characters])
		if !strings.HasPrefix(section, strings.Repeat("#", w.level)+" "+w.title) {
			t.Fatalf("%d: offset lands on %q", i, section)
		}
	}
	// "Сеть" spans its subsection "Порты" and stops at "Диски".
	if network := string(runes[got[1].Offset : got[1].Offset+got[1].Characters]); !strings.Contains(network, "80") || strings.Contains(network, "Диски") {
		t.Fatalf("%q", network)
	}
	// The top section runs to the end of the text.
	if got[0].Offset+got[0].Characters != utf8.RuneCountInString(text) {
		t.Fatal("last section must reach the end")
	}
}

func TestOutlineWithoutHeadingsOrFrontMatter(t *testing.T) {
	if got := Outline("plain text\n~~~\n# in tilde fence\n~~~"); got != nil {
		t.Fatalf("%+v", got)
	}
	if got := Outline("# A\r\ntext"); !reflect.DeepEqual(got, []Section{{Level: 1, Title: "A", Line: 1, Offset: 0, Characters: 9}}) {
		t.Fatalf("%+v", got)
	}
}
