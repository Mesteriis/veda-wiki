package extractorclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/extractorclient"
)

// The fixtures in testdata are the wire contract v1 shared with the Python
// service (services/knowledge-extractor): the request Go sends for one
// snapshot and a valid response to it. UPDATE_FIXTURES=1 rewrites the
// request fixture from the Go types.
func fixtureRequest(t *testing.T) knowledge.ExtractRequest {
	t.Helper()
	blocks, err := knowledge.Prepare("# Сервис billing 🚀\n\nbilling runs on db1 and uses Redis.\n")
	if err != nil {
		t.Fatal(err)
	}
	reqs, err := knowledge.Requests(knowledge.Snapshot{Key: knowledge.SnapshotKey{SourceID: "ops", Path: "billing.md", SHA: "0f"}, Fingerprint: "kg1-fixture"}, knowledge.SchemaV1(), blocks)
	if err != nil || len(reqs) != 1 {
		t.Fatal(err)
	}
	return reqs[0]
}

func strictDecode(t *testing.T, data []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestWireFixturesMatchTheGoContract(t *testing.T) {
	req := fixtureRequest(t)
	path := filepath.Join("testdata", "extract_request.json")
	if os.Getenv("UPDATE_FIXTURES") == "1" {
		data, _ := json.MarshalIndent(req, "", "  ")
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fromFixture knowledge.ExtractRequest
	strictDecode(t, data, &fromFixture)
	got, _ := json.Marshal(req)
	want, _ := json.Marshal(fromFixture)
	if !bytes.Equal(got, want) {
		t.Fatalf("Go request differs from fixture:\n%s\n%s", got, want)
	}
	var res knowledge.ExtractResult
	resData, err := os.ReadFile(filepath.Join("testdata", "extract_response.json"))
	if err != nil {
		t.Fatal(err)
	}
	strictDecode(t, resData, &res)
	if err = knowledge.ValidateResult(req, res); err != nil {
		t.Fatal(err)
	}
}

func server(t *testing.T, handler http.HandlerFunc) *extractorclient.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := extractorclient.New(srv.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestExtractSendsKeyAndValidates(t *testing.T) {
	req := fixtureRequest(t)
	response, _ := os.ReadFile(filepath.Join("testdata", "extract_response.json"))
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/extract" || r.Method != http.MethodPost || r.Header.Get(extractorclient.KeyHeader) != "test-key" || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var got knowledge.ExtractRequest
		if json.Unmarshal(body, &got) != nil || got.RequestID != req.RequestID {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		_, _ = w.Write(response)
	})
	res, err := c.Extract(context.Background(), req)
	if err != nil || len(res.Blocks) != 2 || res.Blocks[1].Dropped == nil || res.Blocks[1].Dropped.OverLimit != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	// A response whose quote is off by one code point is refused.
	broken := bytes.Replace(response, []byte(`"start": 16`), []byte(`"start": 15`), 1)
	c = server(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(broken) })
	if _, err = c.Extract(context.Background(), req); !errors.Is(err, knowledge.ErrInvalidResponse) {
		t.Fatal("mismatched span accepted", err)
	}
}

func TestStatusCodesMapToKnowledgeErrors(t *testing.T) {
	req := fixtureRequest(t)
	cases := []struct {
		status int
		want   []error
	}{
		{http.StatusUnauthorized, []error{knowledge.ErrUnavailable}},
		{http.StatusServiceUnavailable, []error{knowledge.ErrUnavailable}},
		{http.StatusUnprocessableEntity, []error{knowledge.ErrRejected}},
		{http.StatusRequestEntityTooLarge, []error{knowledge.ErrRejected, knowledge.ErrBlockTooLarge}},
		{http.StatusBadRequest, []error{knowledge.ErrRejected}},
		{http.StatusGatewayTimeout, []error{knowledge.ErrTimeout}},
		{http.StatusInternalServerError, []error{knowledge.ErrExtractorError}},
	}
	for _, tc := range cases {
		c := server(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, `{"error":{"code":"invalid_request","message":"https://secret.example/token"}}`)
		})
		_, err := c.Extract(context.Background(), req)
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "test-key") {
			t.Fatalf("%d: %v", tc.status, err)
		}
		for _, w := range tc.want {
			if !errors.Is(err, w) {
				t.Errorf("%d: %v is not %v", tc.status, err, w)
			}
		}
		if tc.status >= 500 && tc.status != http.StatusServiceUnavailable && (errors.Is(err, knowledge.ErrInvalidResponse) || errors.Is(err, knowledge.ErrUnavailable)) {
			t.Errorf("%d: classified as %v", tc.status, err)
		}
	}
	// A 400 without the service's JSON error (a proxy, a host check) is an
	// outage, not a rejection of the article.
	for _, body := range []string{"", "Invalid host header", `{"detail":"x"}`} {
		c := server(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, body)
		})
		if _, err := c.Extract(context.Background(), req); !errors.Is(err, knowledge.ErrUnavailable) || errors.Is(err, knowledge.ErrRejected) {
			t.Errorf("400 %q: %v", body, err)
		}
	}
	// 503 busy: the service works but its inference slot stayed taken.
	busy := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":"busy","message":"no inference slot became free in time"}}`)
	})
	if _, err := busy.Extract(context.Background(), req); !errors.Is(err, knowledge.ErrBusy) || errors.Is(err, knowledge.ErrUnavailable) {
		t.Errorf("503 busy: %v", err)
	}
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte(" "), extractorclient.MaxResponseBytes+1))
	})
	if _, err := c.Extract(context.Background(), req); !errors.Is(err, knowledge.ErrInvalidResponse) {
		t.Fatal("oversized response accepted", err)
	}
	down, _ := extractorclient.New("http://127.0.0.1:1", "k")
	if _, err := down.Extract(context.Background(), req); !errors.Is(err, knowledge.ErrUnavailable) {
		t.Fatal("refused connection is not unavailable", err)
	}
}

func TestReadyReturnsModelAndPipeline(t *testing.T) {
	fixture, _ := os.ReadFile(filepath.Join("testdata", "extract_response.json"))
	var res struct {
		Model json.RawMessage `json:"model"`
	}
	if err := json.Unmarshal(fixture, &res); err != nil {
		t.Fatal(err)
	}
	ready := `{"status":"ready","model":` + string(res.Model) + `,"load_seconds":1.5}`
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" || r.Header.Get(extractorclient.KeyHeader) != "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, ready)
	})
	info, err := c.Ready(context.Background())
	if err != nil || info.ID != knowledge.ModelID || info.Revision != knowledge.ModelRevision {
		t.Fatal(info, err)
	}
	if _, err = knowledge.ExtractorDigest(info.Pipeline); err != nil {
		t.Fatal(err)
	}
	c = server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ready","model":{"id":"m","revision":"r","device":"cpu"}}`)
	})
	if _, err = c.Ready(context.Background()); !errors.Is(err, knowledge.ErrUnavailable) {
		t.Fatal("ready without pipeline settings accepted")
	}
	c = server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"status":"loading","reason":"x"}`)
	})
	if _, err = c.Ready(context.Background()); !errors.Is(err, knowledge.ErrUnavailable) {
		t.Fatal("loading accepted")
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8090", "http://[::1]:8090/", "http://localhost:8090", "https://extractor.internal", "https://10.0.0.5:8443"} {
		if _, err := extractorclient.ValidateURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://127.0.0.2:8090", "http://extractor:8090", "http://10.0.0.5:8090", "http://127.0.0.1", "http://localhost", "ftp://127.0.0.1:1", "https://u:p@host", "https://host/path", "https://host?x=1", "127.0.0.1:8090"} {
		if _, err := extractorclient.ValidateURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if _, err := extractorclient.New("http://127.0.0.1:1", "a\nb"); err == nil {
		t.Error("key with a newline accepted")
	}
}
