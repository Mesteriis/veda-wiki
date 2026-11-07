package domain

import (
	"testing"
	"time"
)

func TestVerifiedReadsTheFrontMatterDate(t *testing.T) {
	for text, want := range map[string]struct{ ok, present bool }{
		"---\nverified: 2026-11-07\n---\n# A":   {true, true},
		"---\nverified: \"2026-11-07\"\n---\n#": {true, true},
		"---\nverified: last week\n---\n# A":    {false, true},
		"---\ntitle: A\n---\n# A":               {false, false},
		"# A\nverified: 2026-11-07":             {false, false},
	} {
		day, ok, present := Verified(text)
		if ok != want.ok || present != want.present || (ok && day.Format(VerifiedLayout) != "2026-11-07") {
			t.Fatalf("%q: %v %v %v", text, day, ok, present)
		}
	}
}

func TestStaleSinceListsOldVerifiedThenUnverified(t *testing.T) {
	page := func(path, verified, updated string) Page {
		text := "# T"
		if verified != "" {
			text = "---\nverified: " + verified + "\n---\n# T"
		}
		return Page{Path: "s/" + path, Environment: "s", Title: path, SearchText: text, Updated: updated}
	}
	snap := Snapshot{Pages: []Page{
		page("fresh.md", "2026-11-01", "2026-11-01T00:00:00Z"),
		page("old.md", "2026-01-10", "2026-11-06T00:00:00Z"),
		page("older.md", "2025-12-01", "2026-11-06T00:00:00Z"),
		page("none-new.md", "", "2026-11-06T00:00:00Z"),
		page("none-old.md", "", "2026-02-01T00:00:00Z"),
		page("bad.md", "soon", "2026-03-01T00:00:00Z"),
		{Path: "x/old.md", Environment: "x", SearchText: "---\nverified: 2020-01-01\n---\n"},
	}}
	got := StaleSince(snap, "s", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	var order []string
	for _, a := range got {
		order = append(order, a.Path)
	}
	want := []string{"older.md", "old.md", "none-old.md", "bad.md", "none-new.md"}
	if len(order) != len(want) {
		t.Fatalf("%v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("%v", order)
		}
	}
	if *got[0].Verified != "2025-12-01" || got[2].Verified != nil {
		t.Fatalf("%+v", got)
	}
}
