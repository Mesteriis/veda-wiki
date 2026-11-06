package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnlyDiagramFrameAllowsInlineStyles(t *testing.T) {
	h := New(Config{}, Deps{})
	for path, inline := range map[string]bool{"/": false, "/assets/missing.js": false, diagramFrame: true} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		policy := rec.Header().Get("Content-Security-Policy")
		if got := strings.Contains(policy, "'unsafe-inline'"); got != inline {
			t.Errorf("%s: unsafe-inline = %v in %q", path, got, policy)
		}
		if strings.Contains(policy, "unsafe-eval") || !strings.Contains(policy, "script-src 'self';") {
			t.Errorf("%s: scripts not limited to the interface: %q", path, policy)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, diagramFrame, nil))
	if policy := rec.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "frame-ancestors 'self'") || strings.Contains(policy, "connect-src") {
		t.Errorf("diagram frame policy = %q", policy)
	}
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<script") {
		t.Errorf("diagram frame: %d %q", rec.Code, rec.Body.String())
	}
}
