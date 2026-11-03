package mcp

import (
	"strings"
	"testing"
)

// TestInvalidArgumentsRefusedWithoutRequest covers every schema keyword the
// tools use: required, additionalProperties, type, minLength, maxLength,
// minimum, maximum, const, enum and pattern.
func TestInvalidArgumentsRefusedWithoutRequest(t *testing.T) {
	cases := []struct{ tool, args string }{
		{"veda_sources", `{"a":1}`},
		{"veda_sources", `[]`},
		{"veda_sources", `"x"`},
		{"veda_search", `{}`},
		{"veda_search", `{"query":""}`},
		{"veda_search", `{"query":1}`},
		{"veda_search", `{"query":null}`},
		{"veda_find", `{"query":"q","limit":0}`},
		{"veda_find", `{"query":"q","limit":101}`},
		{"veda_find", `{"query":"q","limit":"5"}`},
		{"veda_find", `{"query":"q","limit":1.5}`},
		{"veda_find", `{"query":"q","limit":true}`},
		{"veda_find", `{"query":"q","offset":-1}`},
		{"veda_find", `{"query":"q","offset":1e400}`},
		{"veda_read", `{"source_id":"a"}`},
		{"veda_read_excerpt", `{"source_id":"a","path":"b","expected_sha256":"` + strings.Repeat("a", 63) + `"}`},
		{"veda_read_excerpt", `{"source_id":"a","path":"b","expected_sha256":"` + strings.Repeat("a", 65) + `"}`},
		{"veda_read_excerpt", `{"source_id":"a","path":"b","limit":16001}`},
		{"veda_read_display_excerpt", `{"source_id":"a","path":"b","expected_original_sha256":1}`},
		{"veda_prepare_update", `{"source_id":"a","path":"b","expected_revision":0,"text":"x"}`},
		{"veda_prepare_update", `{"source_id":"a","path":"b","expected_revision":1,"text":5}`},
		{"veda_prepare_new_article", `{"source_id":"a","path":"b","text":"` + strings.Repeat("я", 1<<20+1) + `"}`},
		{"veda_prepare_new_article", `{"source_id":"a","path":"b","text":"` + strings.Repeat("я", 600000) + `"}`},
		{"veda_create_source", `{"id":"a","title":"t","kind":"other","visibility":"private"}`},
		{"veda_create_source", `{"id":"a","title":"t","kind":"wiki","visibility":null}`},
		{"veda_write_article", `{"source_id":"a","path":"b","text":"x","expected_revision":-1}`},
		{"veda_cancel_move", `{"id":"m","confirm_cancel":false}`},
		{"veda_move_article", `{"source_id":"s","source_path":"p","expected_revision":1,"destination_id":"d","destination_path":"q","confirm_move":false}`},
		{"veda_translate_article", `{"source_id":"a","path":"b","expected_revision":1,"original_sha256":"o","text":"t","expected_translation_fingerprint":"XYZ"}`},
		{"veda_translate_article", `{"source_id":"a","path":"b","expected_revision":1,"original_sha256":"o","text":"t","expected_translation_fingerprint":"` +
			strings.Repeat("A", 64) + `"}`},
		{"veda_reconcile_sources", `{"source_id":"a","destination_id":"b","limit":51}`},
		{"veda_resolve_conflict", `{"source_id":"a","path":"b","index":0,"expected_revision":1,"resolution":"both"}`},
		{"veda_github_connect", `{"id":"g","repository":"o/r","adopt_existing":"yes"}`},
		{"veda_publish_job", `{"job_id":"j","expected_revision":1}`},
		{"veda_moves", `{"limit":0}`},
	}
	fake, base := startREST(t, nil)
	session := connect(t, base, false)
	for _, tc := range cases {
		if got := refusal(t, call(t, session, tc.tool, tc.args)); got != genericRefusal {
			t.Fatalf("%s %.80s: %q", tc.tool, tc.args, got)
		}
	}
	if fake.count() != 0 {
		t.Fatalf("invalid arguments sent %d requests", fake.count())
	}
}

func TestStringLengthCountsCodePoints(t *testing.T) {
	fake, base := startREST(t, map[string]string{"GET /api/v1/articles/excerpt": `{}`})
	session := connect(t, base, true)
	success(t, call(t, session, "veda_read_excerpt", `{"source_id":"a","path":"b","expected_sha256":"`+strings.Repeat("й", 64)+`"}`))
	if fake.count() != 1 {
		t.Fatal("valid code-point length refused")
	}
}

func TestMissingArgumentsMeanEmptyObject(t *testing.T) {
	_, base := startREST(t, map[string]string{"GET /api/v1/sources": `[]`})
	session := connect(t, base, true)
	if body := text(t, call(t, session, "veda_sources", `null`)); body != `{"sources": []}` {
		t.Fatal(body)
	}
}

func TestPythonJSON(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{}`, `{}`},
		{`[]`, `[]`},
		{`{"a":[],"b":{},"c":[1,2.5,-3e+21,true,false,null]}`, `{"a": [], "b": {}, "c": [1, 2.5, -3e+21, true, false, null]}`},
		{`"<&> é   \"\\\/"`, `"<&> é ` + " " + ` \"\\/"`},
		{`"\n\r\t\b\f\u0001\u001f\u007f"`, `"\n\r\t\b\f\u0001\u001f` + "\x7f" + `"`},
		{`{"z":1,"a":{"y":2,"b":3}}`, `{"z": 1, "a": {"y": 2, "b": 3}}`},
	} {
		got, err := pythonJSON([]byte(tc.in))
		if err != nil || string(got) != tc.want {
			t.Fatalf("%s: %s %v", tc.in, got, err)
		}
	}
}
