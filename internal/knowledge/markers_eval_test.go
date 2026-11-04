package knowledge

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// evalSentence is one hand-annotated line of the extractor evaluation set
// (services/knowledge-extractor/eval/sentences.jsonl). Relations are
// [type, head, tail, modality]; modality is current, negation, plan,
// example or past.
type evalSentence struct {
	ID        string     `json:"id"`
	Text      string     `json:"text"`
	Relations [][]string `json:"relations"`
}

// TestMarkersOnEvalSet scores the claim status rule on the annotated set:
// a claim is "uncertain" when Markers finds anything in its sentence. Each
// sentence is one claim context there, so every relation of a sentence gets
// the same status. The counts are a recorded baseline (see
// docs/knowledge-graph.md, Evaluation); a change to the marker lists must
// bump MarkersVersion and update them here.
func TestMarkersOnEvalSet(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "services", "knowledge-extractor", "eval", "sentences.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var sentences, relations, uncertainOK, missed, falseUncertain, certainOK, categoryOK int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var s evalSentence
		if err := json.Unmarshal(scanner.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		sentences++
		markers := Markers(s.Text)
		for _, r := range s.Relations {
			if len(r) != 4 {
				t.Fatalf("%s: relation %v is not [type, head, tail, modality]", s.ID, r)
			}
			relations++
			modality := r[3]
			switch {
			case modality != "current" && len(markers) > 0:
				uncertainOK++
				if hasCategory(markers, modality) {
					categoryOK++
				} else {
					t.Logf("%s: %s %s→%s is %s, markers %v", s.ID, r[0], r[1], r[2], modality, markers)
				}
			case modality != "current":
				missed++
				t.Logf("%s: %s %s→%s is %s, no marker", s.ID, r[0], r[1], r[2], modality)
			case len(markers) > 0:
				falseUncertain++
				t.Logf("%s: %s %s→%s is current, markers %v", s.ID, r[0], r[1], r[2], markers)
			default:
				certainOK++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d sentences, %d relations: uncertain correctly %d (right category %d), missed %d, current marked uncertain %d, current kept %d",
		sentences, relations, uncertainOK, categoryOK, missed, falseUncertain, certainOK)
	got := [6]int{relations, uncertainOK, categoryOK, missed, falseUncertain, certainOK}
	want := [6]int{43, 13, 13, 0, 3, 27}
	if got != want {
		t.Fatalf("marker baseline changed: got %v, want %v (relations, uncertain ok, category ok, missed, false uncertain, current ok)", got, want)
	}
}

func hasCategory(markers []string, category string) bool {
	for _, m := range markers {
		if strings.HasPrefix(m, category+":") {
			return true
		}
	}
	return false
}
