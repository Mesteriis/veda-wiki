package knowledge

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Name is one extracted name of an entity type in a source.
type Name struct {
	SourceID string
	Type     string
	Text     string
}

// Aliases are explicit, owner-configured rules: entity type, then the
// normalized key of an alias, then the canonical name it stands for.
type Aliases map[string]map[string]string

// Normalize returns the key of name under the rules of its entity type
// (NormalizeVersion "normalize-v3"). Keys are in Unicode NFC, so composed
// and decomposed spellings share a key; NFC never maps one script to
// another. White space is trimmed and collapsed first. A name whose letters mix scripts (for example Latin and Cyrillic) is
// never folded: its key is the collapsed name as written and it is not
// technical, so look-alikes stay distinct. Otherwise:
//   - host, repository, service: a leading type word ("host", "server",
//     "service", "repo", "repository", "хост", "сервер", "сервис",
//     "репозиторий", any case) followed by white space is dropped first, so
//     "host db-01" and "db-01" share a key; the script check then applies
//     to what remains. Then lower case without any white space; a host
//     drops trailing dots, a repository trailing slashes and ".git". The key
//     is technical, and merges across sources, when it holds only letters,
//     digits and ". - _ / : @".
//   - every other type: lower case with single spaces, never technical; it
//     merges only inside one source.
func Normalize(entityType, name string) (key string, technical bool) {
	collapsed := norm.NFC.String(strings.Join(strings.Fields(name), " "))
	if technicalTypes[entityType] {
		collapsed = stripTypeWord(collapsed)
	}
	if collapsed == "" || mixedScript(collapsed) {
		return collapsed, false
	}
	lower := strings.ToLower(collapsed)
	if technicalTypes[entityType] {
		k := strings.ReplaceAll(lower, " ", "")
		switch entityType {
		case "host":
			k = strings.TrimRight(k, ".")
		case "repository":
			k = strings.TrimSuffix(strings.TrimRight(k, "/"), ".git")
		}
		return k, k != "" && technicalKey(k)
	}
	return lower, false
}

var technicalTypes = map[string]bool{"host": true, "repository": true, "service": true}

// typeWords are the type nouns a model often includes at the start of a
// technical name ("host db-01"); compared after lower casing.
var typeWords = map[string]bool{
	"host": true, "server": true, "service": true, "repo": true, "repository": true,
	"хост": true, "сервер": true, "сервис": true, "репозиторий": true,
}

// stripTypeWord drops one leading type word followed by white space from a
// collapsed name; a bare type word, or a word that only contains one, stays.
func stripTypeWord(collapsed string) string {
	first, rest, found := strings.Cut(collapsed, " ")
	if !found || rest == "" || !typeWords[strings.ToLower(first)] {
		return collapsed
	}
	return rest
}

func technicalKey(k string) bool {
	for _, r := range k {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(".-_/:@", r) {
			return false
		}
	}
	return true
}

// scripts groups letters by writing system; Japanese and Chinese scripts
// count as one, since they are routinely written together.
var scripts = []struct {
	name   string
	tables []*unicode.RangeTable
}{
	{"latin", []*unicode.RangeTable{unicode.Latin}},
	{"cyrillic", []*unicode.RangeTable{unicode.Cyrillic}},
	{"greek", []*unicode.RangeTable{unicode.Greek}},
	{"armenian", []*unicode.RangeTable{unicode.Armenian}},
	{"georgian", []*unicode.RangeTable{unicode.Georgian}},
	{"arabic", []*unicode.RangeTable{unicode.Arabic}},
	{"hebrew", []*unicode.RangeTable{unicode.Hebrew}},
	{"cjk", []*unicode.RangeTable{unicode.Han, unicode.Hiragana, unicode.Katakana}},
	{"hangul", []*unicode.RangeTable{unicode.Hangul}},
}

func mixedScript(s string) bool {
	seen := ""
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		script := "other"
		for _, sc := range scripts {
			if unicode.In(r, sc.tables...) {
				script = sc.name
				break
			}
		}
		if seen != "" && seen != script {
			return true
		}
		seen = script
	}
	return false
}

// CanonicalKey is the key of the canonical entity a name resolves to, and
// how. An explicit alias, or the canonical name of one, gives "type:name"
// (alias); a technical key gives "type:key" (technical_key); any other name
// is scoped to its source as "type@source:key" (same_source_name), so equal
// person or organization names in different sources stay distinct entities.
// An empty name has no key.
func CanonicalKey(n Name, aliases Aliases) (string, Resolution) {
	k, technical := Normalize(n.Type, n.Text)
	if k == "" {
		return "", Resolution{}
	}
	rules := aliases[n.Type]
	if canonical, ok := rules[k]; ok {
		if ck, _ := Normalize(n.Type, canonical); ck != "" {
			return n.Type + ":" + ck, Resolution{Method: ResolveAlias, Basis: k + " -> " + ck}
		}
	}
	for _, canonical := range rules {
		if ck, _ := Normalize(n.Type, canonical); ck == k {
			return n.Type + ":" + ck, Resolution{Method: ResolveAlias, Basis: ck}
		}
	}
	if technical {
		return n.Type + ":" + k, Resolution{Method: ResolveTechnicalKey, Basis: k}
	}
	return n.Type + "@" + n.SourceID + ":" + k, Resolution{Method: ResolveSameSourceName, Basis: n.SourceID}
}

// Resolve decides whether two names denote the same entity and on which
// basis. Names of different or undeclared types never match. Equal canonical
// keys match by alias, technical key or same-source name, in that order of
// precedence. Equal normalized non-technical names in different sources are
// only a candidate: a suggestion, never a merge. ok is false for no match.
func Resolve(a, b Name, aliases Aliases) (res Resolution, ok bool) {
	if a.Type != b.Type || !SchemaV1().HasEntity(a.Type) {
		return Resolution{}, false
	}
	ka, ra := CanonicalKey(a, aliases)
	kb, rb := CanonicalKey(b, aliases)
	if ka == "" || kb == "" {
		return Resolution{}, false
	}
	if ka == kb {
		if rb.Method == ResolveAlias && ra.Method != ResolveAlias {
			return rb, true
		}
		return ra, true
	}
	na, _ := Normalize(a.Type, a.Text)
	nb, _ := Normalize(b.Type, b.Text)
	if ra.Method == ResolveSameSourceName && rb.Method == ResolveSameSourceName && na == nb {
		return Resolution{Method: ResolveCandidate, Basis: na}, true
	}
	return Resolution{}, false
}
