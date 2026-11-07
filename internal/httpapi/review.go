package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/render"
)

// changes lists the visible articles whose displayed revision is newer than
// since, newest first, so an agent can catch up without rereading.
func (h handler) changes(rw http.ResponseWriter, r *http.Request, public bool) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	since, err := time.Parse(time.RFC3339, q.Get("since"))
	sourceID := q.Get("source_id")
	if err != nil || (sourceID != "" && !domain.ValidID(sourceID)) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	limit, offset, ok := pagination(q, 50, 200)
	if !ok {
		apiError(rw, domain.ErrInvalid)
		return
	}
	all := domain.ChangesSince(h.Store.snapshot(public), sourceID, since)
	start := min(offset, len(all))
	end := start + min(limit, len(all)-start)
	jsonResponse(rw, 200, map[string]any{"results": all[start:end], "total": len(all), "offset": offset})
}

// checkSource reports formatting warnings and missing link and image targets
// across one source's visible articles. Readers only: it reads every article.
func (h handler) checkSource(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	sourceID := q.Get("source_id")
	limit, offset, ok := pagination(q, 100, 500)
	if !domain.ValidID(sourceID) || !ok {
		apiError(rw, domain.ErrInvalid)
		return
	}
	pages, assets, found := h.Store.sourcePages(sourceID)
	if !found {
		apiError(rw, domain.ErrNotFound)
		return
	}
	issues, checked, total := render.CheckSource(pages, sourceID, assets)
	start := min(offset, len(issues))
	end := start + min(limit, len(issues)-start)
	if issues == nil {
		issues = []render.Issue{}
	}
	jsonResponse(rw, 200, map[string]any{"source_id": sourceID, "articles": checked, "total": total,
		"truncated": total > len(issues), "offset": offset, "results": issues[start:end]})
}

// stale lists visible articles whose front matter "verified:" date is older
// than days (default 90), then those without one.
func (h handler) stale(rw http.ResponseWriter, r *http.Request, public bool) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	days, sourceID := 90, q.Get("source_id")
	if q.Has("days") {
		n, err := strconv.Atoi(q.Get("days"))
		if err != nil || n < 1 || n > 3650 {
			apiError(rw, domain.ErrInvalid)
			return
		}
		days = n
	}
	limit, offset, ok := pagination(q, 50, 200)
	if !ok || (sourceID != "" && !domain.ValidID(sourceID)) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days)
	all := domain.StaleSince(h.Store.snapshot(public), sourceID, cutoff)
	unverified := 0
	for _, a := range all {
		if a.Verified == nil {
			unverified++
		}
	}
	start := min(offset, len(all))
	end := start + min(limit, len(all)-start)
	jsonResponse(rw, 200, map[string]any{"results": all[start:end], "total": len(all), "unverified": unverified,
		"days": days, "offset": offset})
}
