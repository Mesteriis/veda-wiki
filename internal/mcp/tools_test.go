package mcp

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

type goldenTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type golden struct {
	Server struct {
		Name, Version, Instructions string
	} `json:"server"`
	Owner  []goldenTool `json:"owner"`
	Reader []goldenTool `json:"reader"`
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	raw, err := os.ReadFile("testdata/tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

// normalized sorts tools by name and round-trips them through JSON so key
// order and Go types do not matter.
func normalized(t *testing.T, tools []goldenTool) any {
	t.Helper()
	sorted := append([]goldenTool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	raw, err := json.Marshal(sorted)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestToolsMatchPythonAdapter(t *testing.T) {
	g := loadGolden(t)
	for _, mode := range []struct {
		name     string
		readOnly bool
		want     []goldenTool
	}{{"owner", false, g.Owner}, {"reader", true, g.Reader}} {
		t.Run(mode.name, func(t *testing.T) {
			fake, base := startREST(t, nil)
			session := connect(t, base, mode.readOnly)
			init := session.InitializeResult()
			if init.ServerInfo.Name != g.Server.Name || init.ServerInfo.Version != g.Server.Version ||
				init.Instructions != g.Server.Instructions {
				t.Fatalf("server identity: %+v %q", init.ServerInfo, init.Instructions)
			}
			listed, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var got []goldenTool
			for _, tool := range listed.Tools {
				got = append(got, goldenTool{tool.Name, tool.Description, tool.InputSchema})
			}
			if want, have := normalized(t, mode.want), normalized(t, got); !reflect.DeepEqual(want, have) {
				wantJSON, _ := json.MarshalIndent(want, "", " ")
				haveJSON, _ := json.MarshalIndent(have, "", " ")
				t.Fatalf("tools differ\nwant %s\nhave %s", wantJSON, haveJSON)
			}
			if fake.count() != 0 {
				t.Fatalf("listing tools sent %d requests", fake.count())
			}
		})
	}
}

func TestToolTableOrderMatchesPythonAdapter(t *testing.T) {
	g := loadGolden(t)
	var names, readerNames []string
	for _, spec := range Tools {
		names = append(names, spec.Name)
		if spec.ReadOnly {
			readerNames = append(readerNames, spec.Name)
		}
	}
	var want, wantReader []string
	for _, tool := range g.Owner {
		want = append(want, tool.Name)
	}
	for _, tool := range g.Reader {
		wantReader = append(wantReader, tool.Name)
	}
	sort.Strings(readerNames)
	sort.Strings(wantReader)
	if !reflect.DeepEqual(names, want) || !reflect.DeepEqual(readerNames, wantReader) || len(readerNames) != 14 {
		t.Fatalf("names %v\nreader %v", names, readerNames)
	}
}

// The translation tools were removed; neither the owner nor a reader may list them.
func TestRemovedTranslationToolsAreNotListed(t *testing.T) {
	removed := map[string]bool{"veda_read_display": true, "veda_read_display_excerpt": true, "veda_prepare_translation": true, "veda_translation_needed": true, "veda_translate_article": true}
	for _, readOnly := range []bool{false, true} {
		_, base := startREST(t, nil)
		listed, err := connect(t, base, readOnly).ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range listed.Tools {
			if removed[tool.Name] {
				t.Errorf("reader=%v lists removed tool %s", readOnly, tool.Name)
			}
		}
	}
}
