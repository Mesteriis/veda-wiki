package syncer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Connections of different GitHub logins reach GitHub at the same time: the
// repository request of each waits until syncWorkers of them have arrived.
func TestOnceSynchronizesConnectionsOfDifferentAccountsConcurrently(t *testing.T) {
	s := testStore(t)
	names := []string{"org/first", "org/second", "org/third"}
	fakes := map[string]*githubtest.Fake{}
	for i, name := range names {
		f := githubtest.New(t, map[string]string{"docs/" + strings.TrimPrefix(name, "org/") + ".md": "# Page"})
		f.Modify(func() { f.Name = name; f.RepoID = int64(50 + i) })
		fakes[name] = f
	}
	var barrier atomic.Bool
	var arrived, waiting, maxWaiting, logins atomic.Int32
	all := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			// Each saved account is a different GitHub login.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"login":"user%d"}`, logins.Add(1))
			return
		}
		for name, f := range fakes {
			if r.URL.Path == "/repos/"+name && barrier.Load() {
				// Count this request as waiting before it may release the
				// others; otherwise an earlier request can leave before the
				// last one is counted and the overlap looks smaller.
				n := waiting.Add(1)
				for old := maxWaiting.Load(); n > old && !maxWaiting.CompareAndSwap(old, n); old = maxWaiting.Load() {
				}
				if arrived.Add(1) == syncWorkers {
					close(all)
				}
				select {
				case <-all:
				case <-time.After(3 * time.Second):
				}
				waiting.Add(-1)
			}
			if strings.HasPrefix(r.URL.Path, "/repos/"+name+"/") || r.URL.Path == "/repos/"+name {
				f.Serve(w, r)
				return
			}
		}
		fakes[names[0]].Serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, github.NewForTest(gateway.URL, gateway.Client()))
	for i, name := range names {
		account := "account" + string(rune('a'+i))
		if _, err := s.saveAccount(context.Background(), m.github, account, github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Connect(context.Background(), ConnectRequest{ID: strings.TrimPrefix(name, "org/"), Repository: name, AccountID: account}); err != nil {
			t.Fatal(err)
		}
	}
	barrier.Store(true)
	m.Once(context.Background())
	if got := maxWaiting.Load(); got != syncWorkers {
		t.Fatalf("%d connections overlapped, want %d", got, syncWorkers)
	}
	for _, c := range s.connections() {
		if c.LastError != "" {
			t.Fatalf("connection %s failed: %s", c.ID, c.LastError)
		}
	}
}

// hookedManager returns a manager whose connection step is replaced by step,
// over connections with the given IDs and accounts. An empty account is
// anonymous; each account's login is its ID.
func hookedManager(t *testing.T, accounts map[string]string, step func(context.Context, domain.GitHubConnection)) (*fixtureStore, *Manager) {
	t.Helper()
	return hookedManagerWithLogins(t, accounts, nil, step)
}

// hookedManagerWithLogins is hookedManager with the GitHub login of some
// accounts given by logins.
func hookedManagerWithLogins(t *testing.T, accounts, logins map[string]string, step func(context.Context, domain.GitHubConnection)) (*fixtureStore, *Manager) {
	t.Helper()
	s := testStore(t)
	s.mutate(func(db *domain.Database) {
		next := int64(1)
		for id, account := range accounts {
			if account != "" {
				login := account
				if l, ok := logins[account]; ok {
					login = l
				}
				// The step is replaced, so no credential file is read.
				db.Accounts[account] = domain.GitHubAccount{ID: account, Kind: "token", Login: login, SecretRef: strings.Repeat("0", 32)}
			}
			db.Connections[id] = domain.GitHubConnection{ID: id, Repository: "org/" + id, Branch: "main", AccountID: account, RepositoryID: next, ConfigRevision: 1}
			next++
		}
	})
	if db := s.db(); store.Validate(&db) != nil {
		t.Fatal("invalid fixture", store.Validate(&db))
	}
	m := s.manager()
	m.syncConnection = step
	return s, m
}

// tracker counts running connection steps overall and per group: the
// connection's entry in groups, else its account.
type tracker struct {
	groups    map[string]string
	mu        sync.Mutex
	running   int
	max       int
	byAccount map[string]int
	overlap   bool
	ran       []string
}

func (k *tracker) enter(c domain.GitHubConnection) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.byAccount == nil {
		k.byAccount = map[string]int{}
	}
	k.running++
	k.max = max(k.max, k.running)
	k.byAccount[k.group(c)]++
	if k.byAccount[k.group(c)] > 1 {
		k.overlap = true
	}
	k.ran = append(k.ran, c.ID)
}

func (k *tracker) leave(c domain.GitHubConnection) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.running--
	k.byAccount[k.group(c)]--
}

func (k *tracker) group(c domain.GitHubConnection) string {
	if g, ok := k.groups[c.ID]; ok {
		return g
	}
	return c.AccountID
}

func (k *tracker) didRun(id string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, r := range k.ran {
		if r == id {
			return true
		}
	}
	return false
}

// Connections of one GitHub identity run one after another while another
// identity's connection runs beside them. An identity is the login of an
// account, whichever account record holds it, or anonymous access.
func TestOnceSerializesConnectionsOfSameIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accounts map[string]string
		logins   map[string]string
	}{
		{"same account", map[string]string{"a1": "shared", "a2": "shared", "b1": "other"}, nil},
		{"same login", map[string]string{"a1": "first", "a2": "second", "b1": "other"}, map[string]string{"first": "octo", "second": "Octo"}},
		{"anonymous", map[string]string{"a1": "", "a2": "", "b1": "other"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := tracker{groups: map[string]string{"a1": "a", "a2": "a", "b1": "b"}}
			firstStarted := make(chan struct{})
			otherDone := make(chan struct{})
			secondStarted := make(chan struct{})
			_, m := hookedManagerWithLogins(t, tc.accounts, tc.logins, func(ctx context.Context, c domain.GitHubConnection) {
				k.enter(c)
				defer k.leave(c)
				switch c.ID {
				case "a1":
					close(firstStarted)
					// Hold the identity until the other identity's connection
					// has run beside it, and give a wrongly started a2 the
					// chance to show.
					select {
					case <-otherDone:
					case <-time.After(3 * time.Second):
					}
					select {
					case <-secondStarted:
					case <-time.After(200 * time.Millisecond):
					}
				case "a2":
					close(secondStarted)
				case "b1":
					select {
					case <-firstStarted:
					case <-time.After(3 * time.Second):
					}
					close(otherDone)
				}
			})
			m.Once(context.Background())
			if k.overlap {
				t.Fatal("connections of the same identity overlapped")
			}
			if k.max < 2 {
				t.Fatal("connections of different identities did not overlap")
			}
			if !k.didRun("a2") {
				t.Fatal("second connection of the identity did not run")
			}
		})
	}
}

// A rate limit recorded by one connection is seen by the next connection of
// the same account, which is then skipped.
func TestOnceRereadsCooldownBeforeNextConnectionOfAccount(t *testing.T) {
	var k tracker
	var s *fixtureStore
	s, m := hookedManager(t, map[string]string{"a1": "shared", "a2": "shared", "b1": "other"}, func(ctx context.Context, c domain.GitHubConnection) {
		k.enter(c)
		defer k.leave(c)
		if c.ID == "a1" {
			if err := s.connectionStatusFor(c, "error", "unknown", "rate_limited", nil, time.Now().Add(20*time.Minute)); err != nil {
				t.Error(err)
			}
		}
	})
	m.Once(context.Background())
	if !k.didRun("a1") || !k.didRun("b1") {
		t.Fatal("due connections did not run")
	}
	if k.didRun("a2") {
		t.Fatal("connection ran during its account's cooldown")
	}
}

// A rate limit of one account record holds back connections of another
// record with the same login, but not those of a different login.
func TestRateLimitCooldownSharedBySameLogin(t *testing.T) {
	var k tracker
	var s *fixtureStore
	s, m := hookedManagerWithLogins(t, map[string]string{"a1": "first", "a2": "second", "b1": "other"}, map[string]string{"first": "octo", "second": "OCTO"}, func(ctx context.Context, c domain.GitHubConnection) {
		k.enter(c)
		defer k.leave(c)
		if c.ID == "a1" {
			if err := s.connectionStatusFor(c, "error", "unknown", "rate_limited", nil, time.Now().Add(20*time.Minute)); err != nil {
				t.Error(err)
			}
		}
	})
	m.Once(context.Background())
	if k.didRun("a2") || !k.didRun("b1") {
		t.Fatal("cooldown did not follow the login", k.ran)
	}
	db := s.db()
	if db.Connections["a2"].LastError != "rate_limited" || db.Connections["a2"].NextAttempt == "" {
		t.Fatal("same-login connection kept no cooldown")
	}
	if db.Connections["b1"].LastError != "" {
		t.Fatal("cooldown reached a different login")
	}
}

// A connection moved to another identity during a pass is left to the next
// pass, which is scheduled at once.
func TestOnceDefersConnectionMovedToAnotherAccount(t *testing.T) {
	var k tracker
	var s *fixtureStore
	s, m := hookedManager(t, map[string]string{"a1": "shared", "a2": "shared", "b1": "other"}, func(ctx context.Context, c domain.GitHubConnection) {
		k.enter(c)
		defer k.leave(c)
		if c.ID == "a1" {
			s.mutate(func(db *domain.Database) {
				moved := db.Connections["a2"]
				moved.AccountID = "other"
				moved.ConfigRevision++
				db.Connections["a2"] = moved
			})
		}
	})
	m.Once(context.Background())
	if k.didRun("a2") {
		t.Fatal("moved connection ran beside its new identity's worker")
	}
	if !m.Woken() {
		t.Fatal("moved connection was not scheduled for the next pass")
	}
}

// At most syncWorkers accounts run at once.
func TestOnceBoundsConcurrentConnections(t *testing.T) {
	var k tracker
	accounts := map[string]string{}
	for _, id := range []string{"c1", "c2", "c3", "c4", "c5"} {
		accounts[id] = "account-" + id
	}
	full := make(chan struct{})
	var arrived atomic.Int32
	_, m := hookedManager(t, accounts, func(ctx context.Context, c domain.GitHubConnection) {
		k.enter(c)
		defer k.leave(c)
		if arrived.Add(1) == syncWorkers {
			close(full)
		}
		select {
		case <-full:
		case <-time.After(3 * time.Second):
		}
	})
	m.Once(context.Background())
	if k.max != syncWorkers {
		t.Fatalf("%d connections ran at once, want %d", k.max, syncWorkers)
	}
	if len(k.ran) != len(accounts) {
		t.Fatalf("%d connections ran, want %d", len(k.ran), len(accounts))
	}
}

// Cancellation starts no new connection, neither of a running account nor
// of a waiting one, and Once returns only after the running ones finish.
func TestOnceCancellationWaitsForRunningConnections(t *testing.T) {
	var k tracker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var finished atomic.Int32
	started := make(chan struct{}, 8)
	_, m := hookedManager(t, map[string]string{"a1": "a", "a2": "a", "b1": "b", "c1": "c", "d1": "d"}, func(jobCtx context.Context, c domain.GitHubConnection) {
		k.enter(c)
		defer k.leave(c)
		if c.ID != "a1" {
			started <- struct{}{}
		} else {
			// Cancel once all workers are busy.
			for range syncWorkers - 1 {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
				}
			}
			cancel()
		}
		<-jobCtx.Done()
		finished.Add(1)
	})
	m.Once(ctx)
	if got := len(k.ran); got != syncWorkers || int(finished.Load()) != got {
		t.Fatalf("ran %d connections, %d finished before Once returned", got, finished.Load())
	}
	if k.didRun("a2") || k.didRun("d1") {
		t.Fatal("cancellation started another connection")
	}
}
