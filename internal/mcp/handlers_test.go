package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

const proposalBase = `{"revision":3,"sha256":"base","unresolved_conflict":false,` +
	`"conflict_resolution_blocked_by_write":false,"write_blocked_by_move":false,"write_blocked_by_bundle":false,` +
	`"source_status":{"sync_state":"local"}}`

func TestUnknownArgumentRefusedWithoutRequest(t *testing.T) {
	fake, base := startREST(t, nil)
	session := connect(t, base, false)
	got := refusal(t, call(t, session, "veda_read", `{"source_id":"a","path":"b","extra":1}`))
	if got != genericRefusal || fake.count() != 0 {
		t.Fatalf("%q requests=%d", got, fake.count())
	}
}

func TestSearchUsesBoundedEndpoint(t *testing.T) {
	response := `{"results":[{"source_id":"a","path":"b","title":"Ö <x>"}],"total":1}`
	fake, base := startREST(t, map[string]string{"GET /api/v1/search": response})
	session := connect(t, base, true)
	result := call(t, session, "veda_search", `{"query":"vps lxc"}`)
	got := success(t, result)
	if requests := fake.all(); len(requests) != 1 || requests[0].URI != "/api/v1/search?limit=20&q=vps+lxc" {
		t.Fatalf("requests %+v", requests)
	}
	if got["total"] != float64(1) {
		t.Fatalf("result %v", got)
	}
	want := `{"results": [{"source_id": "a", "path": "b", "title": "Ö <x>"}], "total": 1}`
	if body := text(t, result); body != want {
		t.Fatalf("text %s", body)
	}
}

func TestPrepareUpdateRejectsStaleRevision(t *testing.T) {
	fake, base := startREST(t, map[string]string{"GET /api/v1/articles/proposal-base": proposalBase})
	session := connect(t, base, true)
	got := refusal(t, call(t, session, "veda_prepare_update",
		`{"source_id":"a","path":"b.md","expected_revision":2,"text":"# New"}`))
	requests := fake.all()
	if got != genericRefusal || len(requests) != 1 || requests[0].Method != "GET" ||
		requests[0].URI != "/api/v1/articles/proposal-base?path=b.md&source_id=a" {
		t.Fatalf("%q %+v", got, requests)
	}
}

func TestPrepareUpdateBuildsUnsavedProposal(t *testing.T) {
	_, base := startREST(t, map[string]string{"GET /api/v1/articles/proposal-base": proposalBase})
	session := connect(t, base, true)
	result := call(t, session, "veda_prepare_update", `{"source_id":"a","path":"b.md","expected_revision":3,"text":"# Ново"}`)
	sum := sha256.Sum256([]byte("# Ново"))
	want := `{"source_id": "a", "path": "b.md", "expected_revision": 3, "base_sha256": "base", ` +
		`"proposed_sha256": "` + hex.EncodeToString(sum[:]) + `", "text": "# Ново", "saved": false, ` +
		`"unresolved_conflict": false, "conflict_resolution_blocked_by_write": false, "write_blocked_by_move": false, ` +
		`"write_blocked_by_bundle": false, "source_status": {"sync_state": "local"}, ` +
		`"note": "Proposal only; review and an authorized write are still required. Conflict status may change before writing."}`
	success(t, result)
	if body := text(t, result); body != want {
		t.Fatalf("text\n%s\nwant\n%s", body, want)
	}
}

func TestPrepareUpdateNotes(t *testing.T) {
	for _, tc := range []struct{ flags, note string }{
		{`"write_blocked_by_move":true,"write_blocked_by_bundle":true`, "a move blocks writes"},
		{`"write_blocked_by_bundle":1`, "a linked bundle blocks writes"},
		{`"conflict_resolution_blocked_by_write":true,"unresolved_conflict":true`, "blocks conflict resolution"},
		{`"conflict_resolution_blocked_by_write":true`, "Conflict status may change"},
		{`"unresolved_conflict":"yes"`, "resolve the import conflict"},
	} {
		response := `{"revision":1,"sha256":"s","source_status":{},"unresolved_conflict":false,` +
			`"conflict_resolution_blocked_by_write":false,"write_blocked_by_move":false,"write_blocked_by_bundle":false,` +
			tc.flags + `}`
		_, base := startREST(t, map[string]string{"GET /api/v1/articles/proposal-base": response})
		got := success(t, call(t, connect(t, base, true), "veda_prepare_update",
			`{"source_id":"a","path":"b","expected_revision":1,"text":""}`))
		if note, _ := got["note"].(string); !strings.Contains(note, tc.note) {
			t.Fatalf("%s: note %q", tc.flags, note)
		}
	}
}

func TestPrepareNewArticle(t *testing.T) {
	fake, base := startREST(t, map[string]string{"GET /api/v1/articles/new-proposal-base": `{"source_status":{"x":1}}`})
	got := success(t, call(t, connect(t, base, true), "veda_prepare_new_article", `{"source_id":"a","path":"n.md","text":"# N"}`))
	if got["expected_revision"] != float64(0) || got["saved"] != false || fake.all()[0].URI != "/api/v1/articles/new-proposal-base?path=n.md&source_id=a" {
		t.Fatalf("%v %+v", got, fake.all())
	}
}

func TestPrepareTranslationRequiresProgramDocs(t *testing.T) {
	sources := `[{"id":"w","kind":"wiki"},{"id":"p","kind":"program"}]`
	for _, args := range []string{
		`{"source_id":"w","path":"docs/a.md","expected_revision":1,"text":"Т"}`,
		`{"source_id":"p","path":"a.md","expected_revision":1,"text":"Т"}`,
		`{"source_id":"missing","path":"docs/a.md","expected_revision":1,"text":"Т"}`,
	} {
		fake, base := startREST(t, map[string]string{"GET /api/v1/sources": sources})
		got := refusal(t, call(t, connect(t, base, true), "veda_prepare_translation", args))
		if requests := fake.all(); got != genericRefusal || len(requests) != 1 || requests[0].URI != "/api/v1/sources" {
			t.Fatalf("%s: %q %+v", args, got, requests)
		}
	}
}

func TestPrepareTranslationValidatesCurrentRevision(t *testing.T) {
	fake, base := startREST(t, map[string]string{
		"GET /api/v1/sources":                         `[{"id":"p","kind":"program"}]`,
		"GET /api/v1/articles":                        `{"source_id":"p","path":"docs/a.md","versions":[{"revision":1},{"revision":2,"sha256":"orig"}]}`,
		"POST /api/v1/articles/translation-validate": `{"translation_fingerprint":"fp","source_status":{"s":1},"valid":true}`,
	})
	got := success(t, call(t, connect(t, base, true), "veda_prepare_translation",
		`{"source_id":"p","path":"docs/a.md","expected_revision":2,"text":"Т <b>"}`))
	requests := fake.all()
	wantBody := `{"source_id":"p","path":"docs/a.md","expected_revision":2,"original_sha256":"orig","text":"Т <b>"}`
	if len(requests) != 3 || requests[2].Body != wantBody {
		t.Fatalf("%+v", requests)
	}
	if got["expected_translation_fingerprint"] != "fp" || got["original_sha256"] != "orig" || got["saved"] != false {
		t.Fatalf("%v", got)
	}
	empty := refusal(t, call(t, connect(t, base, true), "veda_prepare_translation",
		`{"source_id":"p","path":"docs/a.md","expected_revision":2,"text":""}`))
	if empty != genericRefusal {
		t.Fatal(empty)
	}
}

func TestReadMergesLatestVersion(t *testing.T) {
	article := `{"source_id":"a","path":"b.md","versions":[{"revision":1,"text":"old"},{"revision":2,"text":"new","path":"p"}]}`
	fake, base := startREST(t, map[string]string{"GET /api/v1/articles": article})
	result := call(t, connect(t, base, true), "veda_read", `{"source_id":"a","path":"b.md"}`)
	if body := text(t, result); body != `{"source_id": "a", "path": "p", "revision": 2, "text": "new"}` {
		t.Fatalf("text %s", body)
	}
	if uri := fake.all()[0].URI; uri != "/api/v1/articles?path=b.md&source_id=a" {
		t.Fatal(uri)
	}
	_, empty := startREST(t, map[string]string{"GET /api/v1/articles": `{"source_id":"a","path":"b","versions":[]}`})
	if got := refusal(t, call(t, connect(t, empty, true), "veda_read", `{"source_id":"a","path":"b"}`)); got != genericRefusal {
		t.Fatal(got)
	}
}

func TestReaderModeHidesWriteTools(t *testing.T) {
	fake, base := startREST(t, map[string]string{"POST /api/v1/articles": `{}`})
	session := connect(t, base, true)
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "veda_write_article" {
			t.Fatal("write tool listed for a reader")
		}
	}
	got := refusal(t, call(t, session, "veda_write_article", `{"source_id":"a","path":"b","text":"x","expected_revision":0}`))
	if got != genericRefusal || fake.count() != 0 {
		t.Fatalf("%q requests=%d", got, fake.count())
	}
	if got := refusal(t, call(t, session, "no_such_tool", `{}`)); got != genericRefusal {
		t.Fatal(got)
	}
}

func TestWriteArticleMethodFollowsRevision(t *testing.T) {
	fake, base := startREST(t, map[string]string{"POST /api/v1/articles": `{"revision":1}`, "PUT /api/v1/articles": `{"revision":2}`})
	session := connect(t, base, false)
	success(t, call(t, session, "veda_write_article", `{"source_id":"a","path":"b","text":"x","expected_revision":0}`))
	success(t, call(t, session, "veda_write_article", `{"source_id":"a","path":"b","text":"y","expected_revision":1}`))
	requests := fake.all()
	if len(requests) != 2 || requests[0].Method != "POST" || requests[1].Method != "PUT" ||
		requests[1].Body != `{"expected_revision":1,"path":"b","source_id":"a","text":"y"}` {
		t.Fatalf("%+v", requests)
	}
}

func TestRefusalUsesStatusAndCode(t *testing.T) {
	_, base := startREST(t, nil)
	got := refusal(t, call(t, connect(t, base, true), "veda_context", `{"source_id":"a","path":"b"}`))
	if got != "VEDA request refused: not found or not visible to this client (HTTP 404, not_found)." {
		t.Fatal(got)
	}
}

func TestWrappedListsAndGitHubStatus(t *testing.T) {
	fake, base := startREST(t, map[string]string{
		"GET /api/v1/sources":            `[{"id":"a"}]`,
		"GET /api/v1/sources/mirrors":    `[]`,
		"GET /api/v1/github/connections": `[{"id":"c"}]`,
		"GET /api/v1/github/outbox":      `{"jobs":[]}`,
	})
	session := connect(t, base, false)
	if body := text(t, call(t, session, "veda_sources", `{}`)); body != `{"sources": [{"id": "a"}]}` {
		t.Fatal(body)
	}
	if body := text(t, call(t, session, "veda_source_mirrors", `{}`)); body != `{"mirrors": []}` {
		t.Fatal(body)
	}
	body := text(t, call(t, session, "veda_github_status", `{"limit":5}`))
	if body != `{"connections": [{"id": "c"}], "outbox": {"jobs": []}}` || fake.all()[3].URI != "/api/v1/github/outbox?limit=5" {
		t.Fatalf("%s %+v", body, fake.all())
	}
}

func TestForwardingRoutes(t *testing.T) {
	routes := map[string]string{}
	for _, route := range []string{
		"POST /api/v1/sources", "POST /api/v1/articles/copy", "POST /api/v1/sources/reconcile",
		"POST /api/v1/sources/mirrors", "POST /api/v1/articles/transfer-plan", "GET /api/v1/moves",
		"POST /api/v1/moves/cancel", "POST /api/v1/moves", "GET /api/v1/articles/translation-needed",
		"POST /api/v1/articles/translation", "GET /api/v1/conflicts", "POST /api/v1/conflicts/resolve",
		"POST /api/v1/github/connections", "POST /api/v1/github/sync", "POST /api/v1/github/publish-job",
		"POST /api/v1/github/cancel-bundle", "GET /api/v1/articles/display", "GET /api/v1/articles/display-excerpt",
		"GET /api/v1/articles/excerpt", "GET /api/v1/context", "GET /api/v1/search",
	} {
		routes[route] = `{"ok":true}`
	}
	fake, base := startREST(t, routes)
	session := connect(t, base, false)
	sha := strings.Repeat("a", 64)
	for _, tc := range []struct{ tool, args, method, uri, body string }{
		{"veda_create_source", `{"id":"s","title":"T","kind":"wiki","visibility":"private"}`, "POST", "/api/v1/sources",
			`{"id":"s","kind":"wiki","title":"T","visibility":"private"}`},
		{"veda_find", `{"query":"q","source_id":"s","limit":2.0,"offset":0}`, "GET", "/api/v1/search?limit=2&offset=0&q=q&source_id=s", ""},
		{"veda_read_display", `{"source_id":"s","path":"p"}`, "GET", "/api/v1/articles/display?path=p&source_id=s", ""},
		{"veda_read_display_excerpt", `{"source_id":"s","path":"p","expected_sha256":"` + sha + `","expected_original_sha256":"` + sha + `"}`,
			"GET", "/api/v1/articles/display-excerpt?expected_original_sha256=" + sha + "&expected_sha256=" + sha + "&path=p&source_id=s", ""},
		{"veda_read_excerpt", `{"source_id":"s","path":"p","offset":3,"limit":16000}`, "GET", "/api/v1/articles/excerpt?limit=16000&offset=3&path=p&source_id=s", ""},
		{"veda_context", `{"source_id":"s","path":"p"}`, "GET", "/api/v1/context?path=p&source_id=s", ""},
		{"veda_copy_article", `{"source_id":"s","source_path":"p","expected_revision":1,"destination_id":"d","destination_path":"q","include_linked":true}`,
			"POST", "/api/v1/articles/copy",
			`{"destination_id":"d","destination_path":"q","expected_revision":1,"include_linked":true,"source_id":"s","source_path":"p"}`},
		{"veda_reconcile_sources", `{"source_id":"s","destination_id":"d","limit":50}`, "POST", "/api/v1/sources/reconcile",
			`{"destination_id":"d","limit":50,"source_id":"s"}`},
		{"veda_configure_source_mirror", `{"source_id":"s","destination_id":"d","enabled":false,"expected_revision":0}`, "POST",
			"/api/v1/sources/mirrors", `{"destination_id":"d","enabled":false,"expected_revision":0,"source_id":"s"}`},
		{"veda_transfer_plan", `{"source_id":"s","source_path":"p","expected_revision":1,"destination_id":"d","destination_path":"q"}`,
			"POST", "/api/v1/articles/transfer-plan",
			`{"destination_id":"d","destination_path":"q","expected_revision":1,"source_id":"s","source_path":"p"}`},
		{"veda_moves", `{}`, "GET", "/api/v1/moves", ""},
		{"veda_cancel_move", `{"id":"m","confirm_cancel":true}`, "POST", "/api/v1/moves/cancel", `{"confirm_cancel":true,"id":"m"}`},
		{"veda_move_article", `{"source_id":"s","source_path":"p","expected_revision":1,"destination_id":"d","destination_path":"q","confirm_move":true}`,
			"POST", "/api/v1/moves",
			`{"confirm_move":true,"destination_id":"d","destination_path":"q","expected_revision":1,"source_id":"s","source_path":"p"}`},
		{"veda_translation_needed", `{"source_id":"s","limit":100,"offset":1}`, "GET", "/api/v1/articles/translation-needed?limit=100&offset=1&source_id=s", ""},
		{"veda_translate_article", `{"source_id":"s","path":"p","expected_revision":1,"original_sha256":"o","text":"Т","expected_translation_fingerprint":""}`,
			"POST", "/api/v1/articles/translation",
			`{"expected_revision":1,"expected_translation_fingerprint":"","original_sha256":"o","path":"p","source_id":"s","text":"Т"}`},
		{"veda_conflicts", `{"source_id":"s","path":"p","index":0}`, "GET", "/api/v1/conflicts?index=0&path=p&source_id=s", ""},
		{"veda_resolve_conflict", `{"source_id":"s","path":"p","index":0,"expected_revision":2,"resolution":"upstream"}`, "POST",
			"/api/v1/conflicts/resolve", `{"expected_revision":2,"index":0,"path":"p","resolution":"upstream","source_id":"s"}`},
		{"veda_github_connect", `{"id":"g","repository":"o/r","adopt_existing":true}`, "POST", "/api/v1/github/connections",
			`{"adopt_existing":true,"id":"g","repository":"o/r"}`},
		{"veda_sync", `{"id":"g"}`, "POST", "/api/v1/github/sync", `{"id":"g"}`},
		{"veda_publish_job", `{"job_id":"j","expected_revision":1,"confirm_publication":true}`, "POST", "/api/v1/github/publish-job",
			`{"confirm_publication":true,"expected_revision":1,"job_id":"j"}`},
		{"veda_cancel_bundle", `{"job_id":"j","confirm_cancel":true}`, "POST", "/api/v1/github/cancel-bundle", `{"confirm_cancel":true,"job_id":"j"}`},
	} {
		before := fake.count()
		success(t, call(t, session, tc.tool, tc.args))
		requests := fake.all()
		if len(requests) != before+1 {
			t.Fatalf("%s: %d requests", tc.tool, len(requests)-before)
		}
		if got := requests[before]; got.Method != tc.method || got.URI != tc.uri || got.Body != tc.body {
			t.Fatalf("%s: %+v", tc.tool, got)
		}
	}
}
