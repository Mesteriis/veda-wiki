package httpapi

import (
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestScopedSnapshotCopiesMutableStateAndOmitsOtherSources(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	if _, err := s.put("alpha", "guide.md", "# Original", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("beta", "secret.md", "# Other", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("reader", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.mutate(t, func(db *domain.Database) {
		src := db.Sources["alpha"]
		src.Metadata = &domain.WikiMetadata{Title: "Original source"}
		src.Assets = map[string]domain.BlobRef{"image.png": {SHA: "original"}}
		db.Sources["alpha"] = src
		a := db.Articles["alpha/guide.md"]
		a.Conflicts = []domain.ImportConflict{{Incoming: "original conflict"}}
		a.CopiedFrom = &domain.CopyOrigin{Path: "original path"}
		a.ReconcileBase = &domain.ReconcileBase{SourceID: "beta", Path: "secret.md"}
		a.Published = &domain.Revision{Text: "original published", Assets: map[string]domain.BlobRef{"image.png": {SHA: "original"}}}
		a.Versions[0].Assets = map[string]domain.BlobRef{"image.png": {SHA: "original"}}
		db.Articles["alpha/guide.md"] = a
	})
	s.observations.MarkObserved("alpha", observed)
	view, valid, err := scopedView(s, key, time.Now())
	if err != nil || !valid {
		t.Fatal("snapshot failed", err)
	}
	snapshot := view.state(t)
	if _, ok := snapshot.Sources["beta"]; ok {
		t.Fatal("other source included")
	}
	if _, ok := snapshot.Articles["beta/secret.md"]; ok {
		t.Fatal("other article included")
	}
	// Change the live records in place: the snapshot must not share them.
	storetest.Corrupt(s.Store, func(db *domain.Database) {
		src := db.Sources["alpha"]
		src.Metadata.Title = "changed source"
		src.Assets["image.png"] = domain.BlobRef{SHA: "changed"}
		a := db.Articles["alpha/guide.md"]
		a.Versions[0].Text = "changed revision"
		a.Conflicts[0].Incoming = "changed conflict"
		a.CopiedFrom.Path = "changed path"
		a.Published.Text = "changed published"
		a.Versions[0].Assets["image.png"] = domain.BlobRef{SHA: "changed"}
		a.Published.Assets["image.png"] = domain.BlobRef{SHA: "changed"}
	})
	s.observations.MarkObserved("alpha", observed.Add(time.Hour))
	snapshot = view.state(t)
	vs := snapshot.Sources["alpha"]
	va := snapshot.Articles["alpha/guide.md"]
	if va.ReconcileBase != nil {
		t.Fatal("snapshot included reconciliation baseline from another source")
	}
	if va.Versions[0].Assets["image.png"].SHA != "original" || va.Published.Assets["image.png"].SHA != "original" {
		t.Fatal("snapshot shared revision images")
	}
	if vs.Metadata.Title != "Original source" || vs.Assets["image.png"].SHA != "original" || va.Versions[0].Text != "# Original" || va.Conflicts[0].Incoming != "original conflict" || va.CopiedFrom.Path != "original path" || va.Published.Text != "original published" || !observedAt(view, "alpha").Equal(observed) {
		t.Fatal("snapshot shared mutable state")
	}
	vs.Metadata.Title = "snapshot only"
	vs.Assets["image.png"] = domain.BlobRef{SHA: "snapshot only"}
	va.Versions[0].Text = "snapshot only"
	live := s.state(t)
	if live.Sources["alpha"].Metadata.Title != "changed source" || live.Sources["alpha"].Assets["image.png"].SHA != "changed" || live.Articles["alpha/guide.md"].Versions[0].Text != "changed revision" {
		t.Fatal("snapshot mutation escaped")
	}
}

func TestScopedSnapshotDuringConcurrentMutation(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	if _, err := s.put("alpha", "guide.md", "first", 0); err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("reader", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			storetest.Corrupt(s.Store, func(db *domain.Database) {
				a := db.Articles["alpha/guide.md"]
				if i%2 == 0 {
					a.Versions[0].Text = "first"
				} else {
					a.Versions[0].Text = "second"
				}
			})
		}
	}()
	for i := 0; i < 2000; i++ {
		view, valid, err := scopedView(s, key, time.Now())
		if err != nil || !valid {
			wg.Wait()
			t.Fatal("snapshot failed", err)
		}
		text := view.state(t).Articles["alpha/guide.md"].Versions[0].Text
		if text != "first" && text != "second" {
			wg.Wait()
			t.Fatal("invalid snapshot")
		}
	}
	wg.Wait()
}

// Any future reference-bearing field must be added to the snapshot copy and
// exercised above; otherwise a shallow snapshot can race with writers.
func TestScopedSnapshotMutableFieldInventory(t *testing.T) {
	cases := []struct {
		value   any
		mutable []string
	}{
		{domain.Source{}, []string{"Metadata", "Assets"}},
		{domain.Article{}, []string{"CopiedFrom", "ReconcileBase", "Versions", "Conflicts", "Published"}},
		{domain.WikiMetadata{}, nil}, {domain.BlobRef{}, nil}, {domain.Revision{}, []string{"Assets"}},
		{domain.ImportConflict{}, nil}, {domain.CopyOrigin{}, nil}, {domain.GitHubConnection{}, nil},
	}
	for _, tc := range cases {
		typ := reflect.TypeOf(tc.value)
		var fields []string
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			switch field.Type.Kind() {
			case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Chan, reflect.Func:
				fields = append(fields, field.Name)
			case reflect.Struct, reflect.Array:
				t.Fatalf("review nested field %s.%s for snapshot isolation", typ.Name(), field.Name)
			}
		}
		if !slices.Equal(fields, tc.mutable) {
			t.Fatalf("review mutable fields of %s: %v", typ.Name(), fields)
		}
	}
}
