package translation

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// Scoped readers validate against a read-only store, so Validate must never
// open an update transaction.
func TestValidateOnlyReadsTheStore(t *testing.T) {
	s := newFixture(t)
	programSource(t, s, "program", "private")
	rev, err := s.put("program", "docs/guide.md", "# Guide\n\nRun `install`.", 0)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := Service{Store: store.Detached(s.Dir(), storetest.Read(t, s.Store))}
	called := false
	readOnly.Status = func(_ domain.Database, sourceID string, _ domain.Source) domain.SourceStatus {
		called = true
		return domain.SourceStatus{SyncState: sourceID}
	}
	got, err := readOnly.Validate(Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Руководство\n\nЗапустите `install`."})
	if err != nil || got["structural_valid"] != true || got["saved"] != false || got["source_status"] != (domain.SourceStatus{SyncState: "program"}) || !called {
		t.Fatal("read-only validation failed", got, err)
	}
	if _, err = readOnly.Submit(Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Руководство\n\nЗапустите `install`."}); err == nil {
		t.Fatal("read-only store accepted a submission")
	}
}
