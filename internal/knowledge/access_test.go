package knowledge_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	treeA   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func rev(n int, text string) domain.Revision {
	return domain.Revision{Number: n, Text: text, SHA: "sha-" + text, Created: "2026-11-01T00:00:00Z"}
}

func put(db *domain.Database, sourceID, p string, versions ...domain.Revision) {
	db.Articles[sourceID+"/"+p] = domain.Article{SourceID: sourceID, Path: p, Versions: versions}
}

func key(sourceID, p, text string) knowledge.SnapshotKey {
	return knowledge.SnapshotKey{SourceID: sourceID, Path: p, SHA: "sha-" + text}
}

// fixture: a public local source, a private local source and a public
// GitHub source whose published copy differs from the current revision.
func fixture() domain.Database {
	db := domain.Database{
		Sources: map[string]domain.Source{
			"pub":  {ID: "pub", Kind: "wiki", Visibility: "public"},
			"priv": {ID: "priv", Kind: "wiki", Visibility: "private"},
			"gh":   {ID: "gh", Kind: "wiki", Visibility: "public", ConnectionID: "c1", ImportCommit: commitA},
		},
		Articles: map[string]domain.Article{},
		Connections: map[string]domain.GitHubConnection{
			"c1": {ID: "c1", Verification: "verified", VerifiedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), LastPull: commitA, TreeSHA: treeA},
		},
	}
	put(&db, "pub", "guide.md", rev(1, "pub-old"), rev(2, "pub-new"))
	put(&db, "priv", "guide.md", rev(1, "priv-guide"))
	put(&db, "gh", "page.md", rev(4, "gh-draft"))
	gh := db.Articles["gh/page.md"]
	published := rev(3, "gh-published")
	gh.Published = &published
	db.Articles["gh/page.md"] = gh
	return db
}

func keys(set knowledge.AllowedSet) []string {
	out := []string{}
	for _, k := range set.Keys() {
		out = append(out, k.SourceID+"/"+k.Path+"@"+strings.TrimPrefix(k.SHA, "sha-"))
	}
	return out
}

func expect(t *testing.T, name string, set knowledge.AllowedSet, want ...string) {
	t.Helper()
	if got := keys(set); !reflect.DeepEqual(got, append([]string{}, want...)) {
		t.Fatalf("%s: got %q, want %q", name, got, want)
	}
}

func TestAllowedByRole(t *testing.T) {
	db := fixture()
	expect(t, "public", knowledge.Allowed(db, true, knowledge.Scope{}, now),
		"gh/page.md@gh-published", "pub/guide.md@pub-new")
	expect(t, "reader", knowledge.Allowed(db, false, knowledge.Scope{}, now),
		"gh/page.md@gh-draft", "priv/guide.md@priv-guide", "pub/guide.md@pub-new")
	expect(t, "owner", knowledge.Allowed(db, false, knowledge.Scope{Owner: true}, now),
		"gh/page.md@gh-draft", "gh/page.md@gh-published", "priv/guide.md@priv-guide", "pub/guide.md@pub-new")
	// A public request never gains owner snapshots, whatever the scope says.
	expect(t, "public owner", knowledge.Allowed(db, true, knowledge.Scope{Owner: true}, now),
		"gh/page.md@gh-published", "pub/guide.md@pub-new")
	if (knowledge.AllowedSet{}).Has(key("pub", "guide.md", "pub-new")) {
		t.Fatal("zero AllowedSet grants access")
	}
}

func TestAllowedSamePathInTwoSourcesIsDistinct(t *testing.T) {
	db := fixture()
	put(&db, "priv", "guide.md", rev(1, "pub-new")) // same path and same text as pub
	public := knowledge.Allowed(db, true, knowledge.Scope{}, now)
	if !public.Has(key("pub", "guide.md", "pub-new")) || public.Has(key("priv", "guide.md", "pub-new")) {
		t.Fatal("same path and SHA in a hidden source was not kept apart")
	}
	if public.Has(key("pub", "guide.md", "pub-old")) {
		t.Fatal("old revision allowed")
	}
}

func TestAllowedFailsClosedOnReadRules(t *testing.T) {
	cases := map[string]func(db *domain.Database){
		"deleted": func(db *domain.Database) {
			a := db.Articles["pub/guide.md"]
			a.Deleted = true
			db.Articles["pub/guide.md"] = a
		},
		"partial import": func(db *domain.Database) {
			s := db.Sources["pub"]
			s.ImportPartial = true
			db.Sources["pub"] = s
		},
		"expired visibility proof": func(db *domain.Database) {
			c := db.Connections["c1"]
			c.VerifiedAt = now.Add(-11 * time.Minute).Format(time.RFC3339Nano)
			db.Connections["c1"] = c
		},
		"pending head": func(db *domain.Database) {
			c := db.Connections["c1"]
			c.PendingHead = strings.Repeat("c", 40)
			db.Connections["c1"] = c
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			db := fixture()
			mutate(&db)
			public := knowledge.Allowed(db, true, knowledge.Scope{}, now)
			owner := knowledge.Allowed(db, false, knowledge.Scope{Owner: true}, now)
			switch name {
			case "deleted":
				expect(t, name, public, "gh/page.md@gh-published")
				if owner.Has(key("pub", "guide.md", "pub-new")) {
					t.Fatal("owner sees a deleted article")
				}
			case "partial import":
				expect(t, name, public, "gh/page.md@gh-published")
				if !owner.Has(key("pub", "guide.md", "pub-new")) {
					t.Fatal("partial import hid the article from the owner")
				}
			default:
				expect(t, name, public, "pub/guide.md@pub-new")
				if !owner.Has(key("gh", "page.md", "gh-published")) {
					t.Fatal("owner lost the published copy")
				}
			}
		})
	}
}

// A scoped key and a team user read through the request snapshot that
// auth builds; Allowed sees only what that snapshot holds, and an expired
// key yields no snapshot, so the request is public.
func TestAllowedFollowsScopedSnapshots(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	storetest.Source(t, st, "beta", "private")
	storetest.Mutate(t, st, func(db *domain.Database) {
		put(db, "alpha", "guide.md", rev(1, "alpha-guide"))
		put(db, "beta", "guide.md", rev(1, "beta-guide"))
	})
	service := auth.Service{Store: st}
	clock := time.Now() // key expiry is checked against the wall clock
	token, err := service.CreateAccessKey("agent", []string{"alpha"}, clock.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	view, ok, err := service.ScopedView(token, clock)
	if err != nil || !ok {
		t.Fatal("scoped key rejected", err)
	}
	expect(t, "scoped key", knowledge.Allowed(storetest.Read(t, view), false, knowledge.Scope{}, now), "alpha/guide.md@alpha-guide")
	if _, ok, _ := service.ScopedView(token, clock.Add(2*time.Hour)); ok {
		t.Fatal("expired key granted a snapshot")
	}
	expect(t, "expired key reads as public", knowledge.Allowed(storetest.Read(t, st), true, knowledge.Scope{}, now))

	if _, err := service.SaveTeam("ops", []string{"beta"}, nil); err != nil {
		t.Fatal(err)
	}
	teams, password := []string{"ops"}, "correct horse battery staple"
	if _, err := service.SaveUser("ann", &teams, &password, nil, nil); err != nil {
		t.Fatal(err)
	}
	version, ok := service.AuthenticateUser("ann", password)
	if !ok {
		t.Fatal("user rejected")
	}
	view, ok, err = service.ScopedViewUser("ann", version)
	if err != nil || !ok {
		t.Fatal("team user rejected", err)
	}
	expect(t, "team user", knowledge.Allowed(storetest.Read(t, view), false, knowledge.Scope{}, now), "beta/guide.md@beta-guide")
}

func TestActiveSnapshotsCoverEveryOwnerSnapshot(t *testing.T) {
	db := fixture()
	put(&db, "pub", "gone.md", rev(1, "gone"))
	a := db.Articles["pub/gone.md"]
	a.Deleted = true
	db.Articles["pub/gone.md"] = a
	put(&db, "pub", "empty.md")
	active := knowledge.ActiveSnapshots(db)
	got := []string{}
	for _, s := range active {
		got = append(got, s.Key.SourceID+"/"+s.Key.Path+"@"+strings.TrimPrefix(s.Key.SHA, "sha-")+map[bool]string{true: "+current"}[s.Current]+map[bool]string{true: "+published"}[s.Published])
		if s.Text != strings.TrimPrefix(s.Key.SHA, "sha-") {
			t.Fatalf("%v carries text %q", s.Key, s.Text)
		}
	}
	want := []string{"gh/page.md@gh-draft+current", "gh/page.md@gh-published+published", "priv/guide.md@priv-guide+current+published", "pub/guide.md@pub-new+current+published"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	owner := knowledge.Allowed(db, false, knowledge.Scope{Owner: true}, now)
	if owner.Len() != len(active) {
		t.Fatal("owner set and active snapshots differ")
	}
	for _, s := range active {
		if !owner.Has(s.Key) {
			t.Fatalf("owner cannot read active %v", s.Key)
		}
	}
}
