package mcp

import (
	"strings"
	"testing"
)

// TestInvalidArgumentsRefusedWithoutRequest covers every schema keyword the
// tools use: required, additionalProperties, type, minLength, maxLength,
// minimum, maximum, const, enum and pattern.
func TestInvalidArgumentsRefusedWithoutRequest(t *testing.T) {
	hex := func(n int) string { return strings.Repeat("a", n) }
	const (
		object = "arguments must be a JSON object"
		secret = "SECRET-VALUE"
	)
	cases := []struct{ tool, args, reason string }{
		{"veda_sources", `{"a":1}`, "unknown property 'a'"},
		{"veda_sources", `[]`, object},
		{"veda_sources", `"x"`, object},
		{"veda_search", `{}`, "missing required property 'query'"},
		{"veda_search", `{"query":""}`, "property 'query' is too short"},
		{"veda_search", `{"query":1}`, "property 'query' has the wrong type"},
		{"veda_search", `{"query":null}`, "property 'query' has the wrong type"},
		{"veda_find", `{"query":"q","limit":0}`, "property 'limit' out of range"},
		{"veda_find", `{"query":"q","limit":101}`, "property 'limit' out of range"},
		{"veda_find", `{"query":"q","limit":"5"}`, "property 'limit' has the wrong type"},
		{"veda_find", `{"query":"q","limit":1.5}`, "property 'limit' has the wrong type"},
		{"veda_find", `{"query":"q","limit":true}`, "property 'limit' has the wrong type"},
		{"veda_find", `{"query":"q","offset":-1}`, "property 'offset' out of range"},
		{"veda_find", `{"query":"q","offset":1e400}`, "property 'offset' has the wrong type"},
		{"veda_find", `{"query":"q","extra":"` + secret + `"}`, "unknown property 'extra'"},
		{"veda_read", `{"source_id":"a"}`, "missing required property 'path'"},
		{"veda_read_excerpt", `{"source_id":"a","path":"b","expected_sha256":"` + hex(63) + `"}`, "property 'expected_sha256' is too short"},
		{"veda_read_excerpt", `{"source_id":"a","path":"b","expected_sha256":"` + hex(65) + `"}`, "property 'expected_sha256' is too long"},
		{"veda_read_excerpt", `{"source_id":"a","path":"b","limit":16001}`, "property 'limit' out of range"},
		{"veda_read_display_excerpt", `{"source_id":"a","path":"b","expected_original_sha256":1}`, "property 'expected_original_sha256' has the wrong type"},
		{"veda_prepare_update", `{"source_id":"a","path":"b","expected_revision":0,"text":"x"}`, "property 'expected_revision' out of range"},
		{"veda_prepare_update", `{"source_id":"a","path":"b","expected_revision":1,"text":5}`, "property 'text' has the wrong type"},
		{"veda_prepare_new_article", `{"source_id":"a","path":"b","text":"` + strings.Repeat("я", 1<<20+1) + `"}`, "property 'text' is too long"},
		{"veda_prepare_new_article", `{"source_id":"a","path":"b","text":"` + strings.Repeat("я", 600000) + `"}`, ""}, // within maxLength, over the byte bound: handler refusal
		{"veda_create_source", `{"id":"a","title":"t","kind":"` + secret + `","visibility":"private"}`, "property 'kind' is not an allowed value"},
		{"veda_create_source", `{"id":"a","title":"t","kind":"wiki","visibility":null}`, "property 'visibility' is not an allowed value"},
		{"veda_write_article", `{"source_id":"a","path":"b","text":"x","expected_revision":-1}`, "property 'expected_revision' out of range"},
		{"veda_cancel_move", `{"id":"m","confirm_cancel":false}`, "property 'confirm_cancel' must be true"},
		{"veda_move_article", `{"source_id":"s","source_path":"p","expected_revision":1,"destination_id":"d","destination_path":"q","confirm_move":false}`, "property 'confirm_move' must be true"},
		{"veda_translate_article", `{"source_id":"a","path":"b","expected_revision":1,"original_sha256":"o","text":"t","expected_translation_fingerprint":"XYZ"}`,
			"property 'expected_translation_fingerprint' does not match the required pattern"},
		{"veda_translate_article", `{"source_id":"a","path":"b","expected_revision":1,"original_sha256":"o","text":"t","expected_translation_fingerprint":"` +
			strings.Repeat("A", 64) + `"}`, "property 'expected_translation_fingerprint' does not match the required pattern"},
		{"veda_reconcile_sources", `{"source_id":"a","destination_id":"b","limit":51}`, "property 'limit' out of range"},
		{"veda_resolve_conflict", `{"source_id":"a","path":"b","index":0,"expected_revision":1,"resolution":"both"}`, "property 'resolution' is not an allowed value"},
		{"veda_github_connect", `{"id":"g","repository":"o/r","adopt_existing":"yes"}`, "property 'adopt_existing' has the wrong type"},
		{"veda_publish_job", `{"job_id":"j","expected_revision":1}`, "missing required property 'confirm_publication'"},
		{"veda_moves", `{"limit":0}`, "property 'limit' out of range"},
	}
	fake, base := startREST(t, nil)
	session := connect(t, base, false)
	for _, tc := range cases {
		got := refusal(t, call(t, session, tc.tool, tc.args))
		want := "Input validation error: " + tc.reason
		if tc.reason == "" {
			want = genericRefusal
		}
		if got != want {
			t.Fatalf("%s %.80s: %q, want reason %q", tc.tool, tc.args, got, tc.reason)
		}
		if strings.Contains(got, secret) {
			t.Fatalf("%s: refusal leaks an argument value: %q", tc.tool, got)
		}
	}
	if fake.count() != 0 {
		t.Fatalf("invalid arguments sent %d requests", fake.count())
	}
}

func TestUnknownToolKeepsGenericRefusal(t *testing.T) {
	_, base := startREST(t, nil)
	session := connect(t, base, true)
	for _, name := range []string{"nope", "veda_write_article"} {
		if got := refusal(t, call(t, session, name, `{"x":1}`)); got != genericRefusal {
			t.Fatalf("%s: %q", name, got)
		}
	}
}

func TestLongOrOddPropertyNameIsMasked(t *testing.T) {
	_, base := startREST(t, nil)
	session := connect(t, base, true)
	got := refusal(t, call(t, session, "veda_sources", `{"`+strings.Repeat("k", 65)+`":1}`))
	if got != "Input validation error: unknown property '?'" {
		t.Fatal(got)
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
