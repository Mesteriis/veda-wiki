package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func refusalFor(t *testing.T, status int, body string) Refused {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	c, err := New(Options{URL: server.URL, KeyFile: writeKey(t, 0o600, testKey)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(t.Context(), "GET", "/api/v1/sources", nil)
	refused, ok := err.(Refused)
	if !ok {
		t.Fatalf("want Refused, got %v", err)
	}
	return refused
}

func TestRefusalMessage(t *testing.T) {
	got := refusalFor(t, 409, `{"error":"revision conflict"}`+"\n").Message()
	want := "VEDA request refused: revision or state conflict; reread before retrying (HTTP 409, revision conflict)."
	if got != want {
		t.Fatalf("got %q", got)
	}
	got = refusalFor(t, 403, "Read-only access\n").Message()
	if got != "VEDA request refused: access denied for this client (HTTP 403)." || strings.Contains(got, "Read-only") {
		t.Fatalf("got %q", got)
	}
	got = refusalFor(t, 502, `{"error":"/private/path canary"}`).Message()
	if got != "VEDA request refused: request failed (HTTP 502)." || strings.Contains(got, "canary") {
		t.Fatalf("got %q", got)
	}
}

func TestRefusalReasonTable(t *testing.T) {
	for status, reason := range refusalReasons {
		got := Refused{Status: status}.Message()
		if !strings.Contains(got, reason) {
			t.Errorf("%d: %q", status, got)
		}
	}
	if len(refusalReasons) != 8 {
		t.Fatalf("reason table changed: %d entries", len(refusalReasons))
	}
}

func TestRefusalCodePattern(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":"not found"}`:                       "not found",
		`{"error":"Upper"}`:                           "",
		`{"error":"with-dash"}`:                       "",
		`{"error":"a\nb"}`:                            "",
		`{"error":"` + strings.Repeat("a", 65) + `"}`: "",
		`{"error":"` + strings.Repeat("a", 64) + `"}`: strings.Repeat("a", 64),
		`{"error":5}`:                                 "",
		`[1]`:                                         "",
		`not json`:                                    "",
	} {
		if got := newRefused(400, []byte(body)).Code; got != want {
			t.Errorf("%q: got %q want %q", body, got, want)
		}
	}
}

func TestRefusalKnowledgeCodes(t *testing.T) {
	for _, tc := range []struct {
		status    int
		body, msg string
	}{
		{503, `{"error":"knowledge_disabled"}`, "VEDA request refused: the knowledge graph module is disabled on this VEDA server (HTTP 503, knowledge_disabled)."},
		{503, `{"error":"knowledge_unavailable"}`, "VEDA request refused: the knowledge index is temporarily unavailable; retry later (HTTP 503, knowledge_unavailable)."},
		{409, `{"error":"reindex_busy"}`, "VEDA request refused: a knowledge reindex is already running; retry later (HTTP 409, reindex_busy)."},
		{504, `{"error":"timeout"}`, "VEDA request refused: VEDA did not answer in time; retry with smaller limits (HTTP 504, timeout)."},
		{404, `{"error":"not_found"}`, "VEDA request refused: not found or not visible to this client (HTTP 404, not_found)."},
		{400, `{"error":"invalid_request"}`, "VEDA request refused: invalid input (HTTP 400, invalid_request)."},
	} {
		if got := refusalFor(t, tc.status, tc.body).Message(); got != tc.msg {
			t.Errorf("%s: %q", tc.body, got)
		}
	}
}
