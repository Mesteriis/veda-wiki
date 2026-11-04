package httpapi

import "testing"

// Knowledge answers depend on the caller's access, so no browser or proxy
// may keep them: every read, success or error, is no-store (jsonResponse).
func TestKnowledgeAnswersAreNotCached(t *testing.T) {
	disabled := Wiki{Store: testStore(t), Token: "test-owner", ReaderToken: "test-reader"}
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	for _, w := range []Wiki{disabled, h.wiki(), h.public()} {
		for _, p := range append([]string{"/api/v1/knowledge/entities?limit=0", "/api/v1/knowledge/entity?id=ent-unknown"}, kgReadRoutes...) {
			for _, bearer := range []string{"", "test-reader"} {
				out := kgGet(w, p, bearer)
				if got := out.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("%s (%q, %d): Cache-Control %q", p, bearer, out.Code, got)
				}
			}
		}
	}
	if got := kgGet(h.wiki(), "/api/v1/knowledge/entities", "").Code; got != 200 {
		t.Fatalf("entities: %d", got)
	}
}
