package domain

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// blobRefHolders builds, for every persisted field that can hold a BlobRef,
// a database whose only image reference is ref in that field. The keys are
// the field paths blobRefPaths reports.
var blobRefHolders = map[string]func(ref BlobRef) Database{
	"Sources[].Assets[]": func(ref BlobRef) Database {
		return Database{Sources: map[string]Source{"s": {ID: "s", Assets: map[string]BlobRef{"a.png": ref}}}}
	},
	"Articles[].Versions[].Assets[]": func(ref BlobRef) Database {
		return Database{Articles: map[string]Article{"s/a.md": {Versions: []Revision{{Number: 1}, {Number: 2, Assets: map[string]BlobRef{"a.png": ref}}}}}}
	},
	"Articles[].Published.Assets[]": func(ref BlobRef) Database {
		return Database{Articles: map[string]Article{"s/a.md": {Published: &Revision{Number: 1, Assets: map[string]BlobRef{"a.png": ref}}}}}
	},
	"Outbox[].Assets[]": func(ref BlobRef) Database {
		return Database{Outbox: map[string]SyncJob{"j": {Assets: map[string]BlobRef{"a.png": ref}}}}
	},
	"Moves[].Assets[]": func(ref BlobRef) Database {
		return Database{Moves: map[string]Move{"m": {Assets: map[string]BlobRef{"a.png": ref}}}}
	},
}

// blobRefPaths lists every field reachable from t that holds a BlobRef and is
// persisted (fields tagged json:"-" are skipped).
func blobRefPaths(t reflect.Type, prefix string, out *[]string) {
	switch t.Kind() {
	case reflect.Pointer:
		blobRefPaths(t.Elem(), prefix, out)
	case reflect.Map, reflect.Slice, reflect.Array:
		blobRefPaths(t.Elem(), prefix+"[]", out)
	case reflect.Struct:
		if t == reflect.TypeFor[BlobRef]() {
			*out = append(*out, prefix)
			return
		}
		for i := range t.NumField() {
			field := t.Field(i)
			if !field.IsExported() || strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
				continue
			}
			name := field.Name
			if prefix != "" {
				name = prefix + "." + name
			}
			if field.Anonymous {
				name = prefix
			}
			blobRefPaths(field.Type, name, out)
		}
	}
}

// A BlobRef field added to the database without being handled by
// ReferencedBlobs would let blob collection delete images it still needs.
func TestReferencedBlobsCoversEveryPersistedBlobRef(t *testing.T) {
	var found []string
	blobRefPaths(reflect.TypeFor[Database](), "", &found)
	slices.Sort(found)
	var handled []string
	for p := range blobRefHolders {
		handled = append(handled, p)
	}
	slices.Sort(handled)
	if !slices.Equal(found, handled) {
		t.Fatalf("persisted BlobRef fields %v; ReferencedBlobs tests cover %v", found, handled)
	}
}

func TestReferencedBlobsFindsEachCategory(t *testing.T) {
	ref := BlobRef{SHA: strings.Repeat("a", 64), MIME: "image/png"}
	for name, build := range blobRefHolders {
		t.Run(name, func(t *testing.T) {
			got := ReferencedBlobs(build(ref))
			if len(got) != 1 || !got[ref.SHA] {
				t.Fatalf("referenced = %v", got)
			}
		})
	}
}

func TestReferencedBlobsUnionAndEmpty(t *testing.T) {
	if got := ReferencedBlobs(Database{}); len(got) != 0 {
		t.Fatal("empty database references", got)
	}
	a, b := BlobRef{SHA: strings.Repeat("a", 64)}, BlobRef{SHA: strings.Repeat("b", 64)}
	db := Database{
		Sources:  map[string]Source{"s": {Assets: map[string]BlobRef{"x.png": a}}},
		Articles: map[string]Article{"s/a.md": {Published: &Revision{}, Versions: []Revision{{Assets: map[string]BlobRef{"x.png": a, "y.png": b}}}}},
	}
	if got := ReferencedBlobs(db); len(got) != 2 || !got[a.SHA] || !got[b.SHA] {
		t.Fatal("referenced", got)
	}
}
