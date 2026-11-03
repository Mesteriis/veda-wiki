package syncer

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Opt-in scale rehearsal: all content and credentials are synthetic.
func TestMirrorSyntheticScale(t *testing.T) {
	mode := os.Getenv("VEDA_MIRROR_SCALE")
	if mode != "1" && mode != "realistic" {
		t.Skip("set VEDA_MIRROR_SCALE=1 or realistic to run the scale rehearsal")
	}
	started := time.Now()
	s := testStore(t)
	b := testBatch()
	b.SourceID = "local"
	b.Files = map[string]string{}
	b.Assets = map[string][]byte{}
	b.Complete = false
	b.Withheld = 13
	remote := map[string]string{"veda.yaml": metadataFixture}
	for i := 0; i < 147; i++ {
		name := fmt.Sprintf("images/image-%03d.png", i)
		image := append(smallPNG(t), byte(i), byte(i>>8))
		if mode == "realistic" {
			size := 103540
			if i == 0 {
				size = 2397368
			}
			image = append(image, bytes.Repeat([]byte{byte(i)}, size-len(image))...)
		}
		b.Assets[name] = image
		remote[name] = string(image)
	}
	for i := 0; i < 536; i++ {
		name := fmt.Sprintf("pages/page-%03d.md", i)
		text := fmt.Sprintf("# Synthetic page %d\n\n![image](../images/image-%03d.png)\n", i, i%147)
		if mode == "realistic" {
			size := 12000
			if i < 3 {
				size = 500000
			} else if i < 27 {
				size = 55000
			}
			text += strings.Repeat("x", size-len(text))
		}
		b.Files[name] = text
		if i < 526 {
			remote[name] = text
		}
		if i == 524 || i == 525 {
			remote[name] = fmt.Sprintf("# Independently changed %d", i)
		}
	}
	for i := 0; i < 75; i++ {
		name := fmt.Sprintf("extra/page-%03d.md", i)
		remote[name] = fmt.Sprintf("# Remote only %d", i)
	}
	articleBytes, imageBytes := 0, 0
	for _, text := range b.Files {
		articleBytes += len(text)
	}
	for _, image := range b.Assets {
		imageBytes += len(image)
	}
	t.Logf("synthetic_article_bytes=%d synthetic_image_bytes=%d", articleBytes, imageBytes)
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, remote)
	f.Modify(func() { f.Private = true })
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/blobs/") {
			requests.Add(1)
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "operator", Write: true}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	version := 0
	if _, err = s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &version}); err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(s, client)
	passes := 0
	peak := uint64(0)
	for passes < 40 {
		passes++
		m.Once(context.Background())
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		if mem.HeapAlloc > peak {
			peak = mem.HeapAlloc
		}
		complete := len(s.jobs()) == 10
		for _, job := range s.jobs() {
			complete = complete && job.State == "complete"
		}
		if s.mirrors()[0].State == "blocked" && s.mirrors()[0].Blocked == 2 && complete {
			break
		}
	}
	if s.mirrors()[0].State != "blocked" || s.mirrors()[0].Blocked != 2 || len(s.jobs()) != 10 || f.WriteCount() != 10 {
		t.Fatal("scale reconciliation did not converge", s.mirrors()[0].State, passes)
	}
	s, err = s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	baselines := 0
	for _, a := range s.db().Articles {
		if a.SourceID == "handbook" && a.ReconcileBase != nil {
			baselines++
		}
	}
	if baselines != 534 || len(s.db().Sources["handbook"].Assets) != 147 || len(f.Contents()) != 1+611+147 {
		t.Fatal("scale state mismatch", baselines)
	}
	if requests.Load() != 78 {
		t.Fatal("unexpected remote blob downloads", requests.Load())
	}
	for _, i := range []int{524, 525} {
		name := fmt.Sprintf("pages/page-%03d.md", i)
		if f.Contents()[name] != fmt.Sprintf("# Independently changed %d", i) || s.db().Articles["local/"+name].Deleted {
			t.Fatal("divergent article was changed")
		}
	}
	t.Logf("synthetic_articles=536 matching=524 differing=2 local_only=10 repository_only=75 images=147 passes=%d observed_heap_bytes=%d elapsed=%s", passes, peak, time.Since(started).Round(time.Millisecond))
}
