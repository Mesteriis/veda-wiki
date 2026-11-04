package knowledge

import (
	"reflect"
	"testing"
)

func TestNormalizeByType(t *testing.T) {
	cases := []struct {
		typ, name, key string
		technical      bool
	}{
		{"host", "  DB-1.Example.COM. ", "db-1.example.com", true},
		{"service", "Billing  API", "billingapi", true},
		{"service", "billing-api", "billing-api", true},
		{"repository", "github.com/Mesteriis/Veda.git/", "github.com/mesteriis/veda", true},
		{"service", "Сервис  Оплаты", "сервисоплаты", true},
		{"person", "  Анна   Иванова ", "анна иванова", false},
		{"organization", "ACME\tCorp", "acme corp", false},
		{"technology", "PostgreSQL", "postgresql", false},
		{"project", "VEDA", "veda", false},
		{"document", "Runbook", "runbook", false},
		// Mixed scripts are never folded: the name is kept as written.
		{"service", "Cepвиc", "Cepвиc", false},
		{"person", "Ivan Иванов", "Ivan Иванов", false},
		{"host", "", "", false},
		{"host", "db 1 (old)", "db1(old)", false},
	}
	for _, c := range cases {
		key, technical := Normalize(c.typ, c.name)
		if key != c.key || technical != c.technical {
			t.Errorf("Normalize(%q, %q) = %q, %v; want %q, %v", c.typ, c.name, key, technical, c.key, c.technical)
		}
	}
}

func TestNormalizeKeepsLookAlikeScriptsApart(t *testing.T) {
	cyrillic, _ := Normalize("service", "Сервис")
	mixed, _ := Normalize("service", "Cepвиc")
	latin, _ := Normalize("host", "cep")
	cyr, _ := Normalize("host", "сер")
	if cyrillic == mixed || latin == cyr {
		t.Fatalf("look-alikes merged: %q %q %q %q", cyrillic, mixed, latin, cyr)
	}
}

func TestResolve(t *testing.T) {
	aliases := Aliases{"technology": {"postgres": "postgresql", "pg": "postgresql"}}
	cases := []struct {
		name   string
		a, b   Name
		method ResolutionMethod
	}{
		{"technical key across sources", Name{"a", "host", "DB-1"}, Name{"b", "host", "db-1"}, ResolveTechnicalKey},
		{"alias", Name{"a", "technology", "Postgres"}, Name{"b", "technology", "PostgreSQL"}, ResolveAlias},
		{"alias to alias", Name{"a", "technology", "pg"}, Name{"b", "technology", "postgres"}, ResolveAlias},
		{"person in one source", Name{"a", "person", "Анна Иванова"}, Name{"a", "person", "анна  иванова"}, ResolveSameSourceName},
		{"person across sources", Name{"a", "person", "Анна Иванова"}, Name{"b", "person", "Анна Иванова"}, ResolveCandidate},
		{"organization across sources", Name{"a", "organization", "ACME"}, Name{"b", "organization", "acme"}, ResolveCandidate},
		{"mixed script never matches", Name{"a", "service", "Сервис"}, Name{"a", "service", "Cepвиc"}, ""},
		{"different types", Name{"a", "host", "veda"}, Name{"a", "project", "veda"}, ""},
		{"different names", Name{"a", "person", "Анна"}, Name{"a", "person", "Анна Иванова"}, ""},
		{"unknown type", Name{"a", "city", "Riga"}, Name{"a", "city", "Riga"}, ""},
	}
	for _, c := range cases {
		res, ok := Resolve(c.a, c.b, aliases)
		if ok != (c.method != "") || res.Method != c.method {
			t.Errorf("%s: got %+v %v, want %q", c.name, res, ok, c.method)
		}
		if ok && res.Basis == "" {
			t.Errorf("%s: no basis recorded", c.name)
		}
	}
}

func TestCanonicalKeyScopesNonTechnicalNamesToTheirSource(t *testing.T) {
	a, ra := CanonicalKey(Name{"alpha", "person", "Анна"}, nil)
	b, rb := CanonicalKey(Name{"beta", "person", "Анна"}, nil)
	if a == b || ra.Method != ResolveSameSourceName || rb.Method != ResolveSameSourceName {
		t.Fatalf("person keys %q %q %+v", a, b, ra)
	}
	h1, r1 := CanonicalKey(Name{"alpha", "host", "DB-1"}, nil)
	h2, _ := CanonicalKey(Name{"beta", "host", "db-1"}, nil)
	if h1 != h2 || r1.Method != ResolveTechnicalKey {
		t.Fatalf("host keys %q %q", h1, h2)
	}
	t1, r := CanonicalKey(Name{"alpha", "technology", "Postgres"}, Aliases{"technology": {"postgres": "postgresql"}})
	t2, _ := CanonicalKey(Name{"beta", "technology", "postgresql"}, Aliases{"technology": {"postgres": "postgresql"}})
	if t1 != t2 || r.Method != ResolveAlias {
		t.Fatalf("alias keys %q %q %+v", t1, t2, r)
	}
}

func TestMarkers(t *testing.T) {
	cases := map[string][]string{
		"Сервис не использует Redis.":                 {"negation:не"},
		"Billing no longer depends on Kafka.":         {"negation:no longer"},
		"The API doesn’t use MySQL.":                  {"negation:doesn't"},
		"Планируется перенос на host-2.":              {"plan:планируется"},
		"TODO: migrate to Postgres":                   {"plan:todo"},
		"Например, сервис A зависит от B.":            {"example:например"},
		"E.g. billing runs on db-1.":                  {"example:e.g."},
		"Раньше сервис работал на db-0.":              {"past:раньше"},
		"It used to run on db-0 and will move later.": {"plan:will", "past:used to"},
		"Некоторые нетерпеливые сервисы работают.":    nil,
		"Billing runs on db-1.":                       nil,
	}
	for sentence, want := range cases {
		got := Markers(sentence)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Markers(%q) = %q, want %q", sentence, got, want)
		}
	}
	if MarkersVersion == "" {
		t.Fatal("markers are not versioned")
	}
}
