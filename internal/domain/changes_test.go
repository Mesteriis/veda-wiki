package domain

import (
	"testing"
	"time"
)

func TestChangesSinceListsNewerPagesNewestFirst(t *testing.T) {
	snap := Snapshot{Pages: []Page{
		{Path: "a/old.md", Environment: "a", Title: "Old", Updated: "2026-11-01T10:00:00Z", SHA: "1"},
		{Path: "a/new.md", Environment: "a", Title: "New", Updated: "2026-11-07T10:00:00.5Z", SHA: "2"},
		{Path: "a/mid.md", Environment: "a", Title: "Mid", Updated: "2026-11-05T10:00:00Z", SHA: "3"},
		{Path: "b/new.md", Environment: "b", Title: "Other", Updated: "2026-11-07T10:00:00.5Z", SHA: "4"},
		{Path: "a/broken.md", Environment: "a", Updated: "not a time"},
	}}
	since := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) // equal is not newer
	got := ChangesSince(snap, "", since)
	if len(got) != 2 || got[0].SourceID != "a" || got[0].Path != "new.md" || got[1].SourceID != "b" {
		t.Fatalf("%+v", got)
	}
	if one := ChangesSince(snap, "a", since.Add(-time.Hour)); len(one) != 2 || one[1].Path != "mid.md" || one[0].Title != "New" {
		t.Fatalf("%+v", one)
	}
}
