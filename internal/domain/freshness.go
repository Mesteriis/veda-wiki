package domain

import (
	"sort"
	"strings"
	"time"
)

// VerifiedLayout is the date format of the front matter "verified:" field.
const VerifiedLayout = "2006-01-02"

// Verified returns the date the front matter "verified:" field records, the
// day an author last checked the article against live state. ok is false
// when the field is absent; present reports an unparsable value.
func Verified(text string) (day time.Time, ok, present bool) {
	front, _ := SplitFrontMatter(text)
	value, line := FrontMatterValue(front, "verified")
	if line < 0 {
		return time.Time{}, false, false
	}
	day, err := time.Parse(VerifiedLayout, value)
	return day, err == nil, true
}

// StaleArticle is an article not verified within the asked number of days.
type StaleArticle struct {
	SourceID string  `json:"source_id"`
	Path     string  `json:"path"`
	Title    string  `json:"title"`
	Verified *string `json:"verified"` // nil when the article has no valid verified date
	Updated  string  `json:"updated_at"`
}

// StaleSince lists the pages of snap, optionally of one source, whose
// verified date is before cutoff, oldest first, then the pages without a
// valid verified date, least recently updated first.
func StaleSince(snap Snapshot, sourceID string, cutoff time.Time) []StaleArticle {
	var dated, undated []StaleArticle
	for _, p := range snap.Pages {
		if sourceID != "" && p.Environment != sourceID {
			continue
		}
		item := StaleArticle{SourceID: p.Environment, Path: strings.TrimPrefix(p.Path, p.Environment+"/"), Title: p.Title, Updated: p.Updated}
		day, ok, _ := Verified(p.SearchText)
		switch {
		case !ok:
			undated = append(undated, item)
		case day.Before(cutoff):
			value := day.Format(VerifiedLayout)
			item.Verified = &value
			dated = append(dated, item)
		}
	}
	key := func(a StaleArticle) string { return a.SourceID + "/" + a.Path }
	sort.SliceStable(dated, func(i, j int) bool {
		if *dated[i].Verified != *dated[j].Verified {
			return *dated[i].Verified < *dated[j].Verified
		}
		return key(dated[i]) < key(dated[j])
	})
	sort.SliceStable(undated, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339Nano, undated[i].Updated)
		tj, _ := time.Parse(time.RFC3339Nano, undated[j].Updated)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return key(undated[i]) < key(undated[j])
	})
	return append(dated, undated...)
}
