package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

const testToken = "123456789:AAEexampleexampleexampleexample00"

// fakeTelegram records sendMessage calls and answers getMe.
type fakeTelegram struct {
	mu   sync.Mutex
	sent []map[string]any
	fail bool
	path []string
}

func (f *fakeTelegram) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path = append(f.path, r.URL.Path)
	if f.fail || !strings.HasPrefix(r.URL.Path, "/bot"+testToken+"/") {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Unauthorized"})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/getMe") {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]string{"username": "veda_test_bot"}})
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.sent = append(f.sent, body)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
}

func (f *fakeTelegram) messages() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.sent...)
}

func fixture(t *testing.T) (*Service, *fakeTelegram) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fake := &fakeTelegram{}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(server.Close)
	return &Service{Store: store, Bot: NewBotForTest(server.URL, dir, server.Client()), Admins: func() []string { return []string{"owner", "other-admin"} }}, fake
}

func TestStoreIsPrivateAndKeepsRequests(t *testing.T) {
	s, _ := fixture(t)
	info, err := os.Stat(filepath.Join(s.Bot.dir, File))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("store file mode: %v %v", info, err)
	}
	ctx := context.Background()
	now := time.Now()
	r, err := s.Store.AddRequest(ctx, Request{ID: "r1", Sources: []string{"guide"}, Telegram: "@person_1", Comment: "for my agent"}, now)
	if err != nil || r.State != "pending" {
		t.Fatalf("add: %+v %v", r, err)
	}
	if err := s.Store.Decide(ctx, "r1", "approved", "person_1", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Decide(ctx, "r1", "rejected", "", now); err != domain.ErrConflict {
		t.Fatalf("a decided request changed again: %v", err)
	}
	got, err := s.Store.Request(ctx, "r1")
	if err != nil || got.State != "approved" || got.KeyID != "person_1" || got.Sources[0] != "guide" {
		t.Fatalf("stored request: %+v %v", got, err)
	}
	for i := 0; i < MaxPending; i++ {
		if _, err := s.Store.AddRequest(ctx, Request{ID: "p" + string(rune('a'+i%26)) + strings.Repeat("x", i), Sources: []string{"guide"}, Telegram: "@person_1"}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Store.AddRequest(ctx, Request{ID: "overflow", Sources: []string{"guide"}, Telegram: "@person_1"}, now); err != ErrQueueFull {
		t.Fatalf("pending cap: %v", err)
	}
}

func TestBotStoresAValidTokenPrivatelyAndSendsPlainText(t *testing.T) {
	s, fake := fixture(t)
	ctx := context.Background()
	if _, err := s.Bot.SetToken(ctx, "not-a-token"); err == nil {
		t.Fatal("malformed token accepted")
	}
	if _, err := s.Bot.Name(ctx); err != ErrNoBot {
		t.Fatalf("name without token: %v", err)
	}
	name, err := s.Bot.SetToken(ctx, testToken)
	if err != nil || name != "veda_test_bot" {
		t.Fatalf("set token: %q %v", name, err)
	}
	info, err := os.Stat(filepath.Join(s.Bot.dir, TokenFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode: %v %v", info, err)
	}
	if err := s.Store.SetTelegramID(ctx, "owner", 42); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetTelegramID(ctx, "reader", 7); err != nil {
		t.Fatal(err)
	}
	n, err := s.Test(ctx)
	if err != nil || n != 1 {
		t.Fatalf("test message: %d %v", n, err)
	}
	sent := fake.messages()
	if len(sent) != 1 || sent[0]["chat_id"] != float64(42) || sent[0]["parse_mode"] != nil {
		t.Fatalf("sent: %+v", sent)
	}
	fake.mu.Lock()
	fake.fail = true
	fake.mu.Unlock()
	if _, err := s.Test(ctx); err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("failure must not carry the token: %v", err)
	}
}

func TestCheckReportsTransitionsOnce(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	now := time.Now()
	var queued []string
	s.start()
	drain := func() {
		for {
			select {
			case text := <-s.queue:
				queued = append(queued, text)
			default:
				return
			}
		}
	}
	state := Snapshot{
		Sync: []ConnectionState{{ID: "wiki", Repository: "org/wiki", State: "error", LastError: "upstream_rejected"}},
		Keys: []KeyState{{ID: "agent", Expires: now.Add(3 * time.Hour)}, {ID: "later", Expires: now.Add(72 * time.Hour)}},
	}
	s.State = func() Snapshot { return state }
	s.Check(ctx, now)
	s.Check(ctx, now)
	drain()
	if len(queued) != 2 || !strings.Contains(queued[0], "upstream_rejected") || !strings.Contains(queued[1], "agent") {
		t.Fatalf("first pass: %q", queued)
	}
	state.Sync[0].State = "ready"
	queued = nil
	s.Check(ctx, now)
	s.Check(ctx, now)
	drain()
	if len(queued) != 1 || !strings.Contains(queued[0], "восстановлена") {
		t.Fatalf("recovery: %q", queued)
	}
	if err := s.SetEnabled(ctx, EventSync, false); err != nil {
		t.Fatal(err)
	}
	state.Sync[0].State = "error"
	queued = nil
	s.Check(ctx, now)
	drain()
	if len(queued) != 0 {
		t.Fatalf("a switched-off event was sent: %q", queued)
	}
}

func TestFormLimits(t *testing.T) {
	var l Limiter
	now := time.Now()
	for i := 0; i < PerAddressPerHour; i++ {
		if !l.Allow("198.51.100.1", now) {
			t.Fatal("allowed attempt refused")
		}
	}
	if l.Allow("198.51.100.1", now) {
		t.Fatal("per-address limit not applied")
	}
	if !l.Allow("198.51.100.1", now.Add(61*time.Minute)) {
		t.Fatal("per-address limit did not expire")
	}
	for i := 0; l.Allow("203.0.113."+string(rune('0'+i%10))+string(rune('a'+i)), now); i++ {
		if i > TotalPerDay {
			t.Fatal("daily limit not applied")
		}
	}
	for _, nick := range []string{"@ab", "person", "@bad-name", "@" + strings.Repeat("a", 33)} {
		if ValidNick(nick) {
			t.Fatalf("invalid nick accepted: %q", nick)
		}
	}
	if !ValidNick("@person_1") {
		t.Fatal("valid nick refused")
	}
	if _, ok := CleanComment("bell\x07"); ok {
		t.Fatal("control character accepted")
	}
	for _, hidden := range []string{"right\u202eto left", "zero\u200bwidth", "c1\u0085control"} {
		if _, ok := CleanComment(hidden); ok {
			t.Fatalf("invisible character accepted: %q", hidden)
		}
	}
	if c, ok := CleanComment("  line one\nline two  "); !ok || c != "line one\nline two" {
		t.Fatalf("comment: %q %v", c, ok)
	}
}

func TestRequestMessageQuotesTheVisitorsComment(t *testing.T) {
	s, _ := fixture(t)
	s.start()
	s.RequestCreated(context.Background(), Request{Telegram: "@person_1", Comment: "hi\nРешение: https://evil.example"}, []string{"Guide"})
	text := <-s.queue
	if !strings.Contains(text, "│ hi\n│ Решение: https://evil.example") {
		t.Fatalf("comment not quoted: %q", text)
	}
	s.FormLimited(context.Background(), time.Now())
	s.FormLimited(context.Background(), time.Now())
	if len(s.queue) != 1 {
		t.Fatalf("form limit notices: %d", len(s.queue))
	}
}
