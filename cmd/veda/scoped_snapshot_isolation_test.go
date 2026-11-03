package main

import (
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
	s.Lock()
	src := s.DB().Sources["alpha"]
	src.Metadata = &WikiMetadata{Title: "Original source"}
	src.Assets = map[string]BlobRef{"image.png": {SHA: "original"}}
	s.DB().Sources["alpha"] = src
	a := s.DB().Articles["alpha/guide.md"]
	a.Conflicts = []ImportConflict{{Incoming: "original conflict"}}
	a.Translation = &Translation{Text: "original translation"}
	a.CopiedFrom = &CopyOrigin{Path: "original path"}
	a.ReconcileBase = &ReconcileBase{SourceID: "beta", Path: "secret.md"}
	a.Published = &Revision{Text: "original published"}
	s.DB().Articles["alpha/guide.md"] = a
	s.observations.MarkObserved("alpha", observed)
	s.Unlock()
	view, valid, err := scopedView(s, key, time.Now())
	if err != nil || !valid {
		t.Fatal("snapshot failed", err)
	}
	if _, ok := view.DB().Sources["beta"]; ok {
		t.Fatal("other source included")
	}
	if _, ok := view.DB().Articles["beta/secret.md"]; ok {
		t.Fatal("other article included")
	}
	s.Lock()
	src = s.DB().Sources["alpha"]
	src.Metadata.Title = "changed source"
	src.Assets["image.png"] = BlobRef{SHA: "changed"}
	a = s.DB().Articles["alpha/guide.md"]
	a.Versions[0].Text = "changed revision"
	a.Conflicts[0].Incoming = "changed conflict"
	a.Translation.Text = "changed translation"
	a.CopiedFrom.Path = "changed path"
	a.Published.Text = "changed published"
	s.observations.MarkObserved("alpha", observed.Add(time.Hour))
	s.Unlock()
	vs := view.DB().Sources["alpha"]
	va := view.DB().Articles["alpha/guide.md"]
	if va.ReconcileBase != nil {
		t.Fatal("snapshot included reconciliation baseline from another source")
	}
	if vs.Metadata.Title != "Original source" || vs.Assets["image.png"].SHA != "original" || va.Versions[0].Text != "# Original" || va.Conflicts[0].Incoming != "original conflict" || va.Translation.Text != "original translation" || va.CopiedFrom.Path != "original path" || va.Published.Text != "original published" || !observedAt(view, "alpha").Equal(observed) {
		t.Fatal("snapshot shared mutable state")
	}
	vs.Metadata.Title = "snapshot only"
	vs.Assets["image.png"] = BlobRef{SHA: "snapshot only"}
	va.Versions[0].Text = "snapshot only"
	va.Translation.Text = "snapshot only"
	s.RLock()
	defer s.RUnlock()
	if s.DB().Sources["alpha"].Metadata.Title != "changed source" || s.DB().Sources["alpha"].Assets["image.png"].SHA != "changed" || s.DB().Articles["alpha/guide.md"].Versions[0].Text != "changed revision" || s.DB().Articles["alpha/guide.md"].Translation.Text != "changed translation" {
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
			s.Lock()
			a := s.DB().Articles["alpha/guide.md"]
			if i%2 == 0 {
				a.Versions[0].Text = "first"
			} else {
				a.Versions[0].Text = "second"
			}
			s.Unlock()
		}
	}()
	for i := 0; i < 2000; i++ {
		view, valid, err := scopedView(s, key, time.Now())
		if err != nil || !valid {
			wg.Wait()
			t.Fatal("snapshot failed", err)
		}
		text := view.DB().Articles["alpha/guide.md"].Versions[0].Text
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
		{Source{}, []string{"Metadata", "Assets"}},
		{Article{}, []string{"Translation", "CopiedFrom", "ReconcileBase", "Versions", "Conflicts", "Published"}},
		{WikiMetadata{}, nil}, {BlobRef{}, nil}, {Revision{}, nil},
		{ImportConflict{}, nil}, {Translation{}, nil}, {CopyOrigin{}, nil}, {GitHubConnection{}, nil},
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
