package site

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Only files Git tracks are exported, as in an import.
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// A 1x1 PNG.
const png = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82"

func read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExportWikiSite(t *testing.T) {
	src, out := t.TempDir(), filepath.Join(t.TempDir(), "site")
	write(t, src, map[string]string{
		"veda.yaml":               "schema_version: 1\nkind: wiki\ntitle: Handbook\ndescription: Test\n",
		"README.md":               "# Handbook\n\n[Network](network/) and [VLANs](network/vlans.md#trunks).\n\n```mermaid\ngraph LR\n  A --> B\n```",
		"network/README.md":       "# Network\n\n![Rack](images/rack.png)\n\n[Home](../README.md) [Missing](../../escape.md)",
		"network/vlans.md":        "---\ntitle: VLANs\n---\n\n## Trunks\n\n> [!NOTE]\n> Tagged.\n\n<div>bad</div>",
		"network/images/rack.png": png,
	})
	report, err := Export(Options{Dir: src, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pages != 3 || report.Assets != 1 {
		t.Fatalf("report %+v", report)
	}
	home := read(t, out, "index.html")
	for _, want := range []string{"<title>Handbook</title>", `href="network/index.html"`, `href="network/vlans.html#trunks"`, `<pre class="mermaid">`, `src="site.js"`, `href="site.css"`} {
		if !strings.Contains(home, want) {
			t.Fatalf("index.html missing %q:\n%s", want, home)
		}
	}
	network := read(t, out, "network/index.html")
	for _, want := range []string{`src="images/rack.png"`, `href="../index.html"`, `href="../site.css"`, `href="vlans.html"`} {
		if !strings.Contains(network, want) {
			t.Fatalf("network/index.html missing %q:\n%s", want, network)
		}
	}
	if strings.Contains(network, "escape") && strings.Contains(network, `href="../../escape`) {
		t.Fatal("link left the source")
	}
	vlans := read(t, out, "network/vlans.html")
	if !strings.Contains(vlans, "<title>VLANs · Handbook</title>") || !strings.Contains(vlans, "markdown-alert-note") || strings.Contains(vlans, "<div>bad") || strings.Contains(vlans, `src="../site.js"`) {
		t.Fatalf("vlans.html:\n%s", vlans)
	}
	if read(t, out, "network/images/rack.png") != png {
		t.Fatal("image not copied")
	}
	for _, name := range []string{".nojekyll", "site.css", "site.js"} {
		read(t, out, name)
	}
	if got := report.Warnings["network/vlans.md"]; len(got) != 1 || got[0].Code != "raw_html" {
		t.Fatalf("warnings %+v", report.Warnings)
	}
}

func TestStrictFailsOnWarningsAndOutMustBeEmpty(t *testing.T) {
	src := t.TempDir()
	write(t, src, map[string]string{"veda.yaml": "schema_version: 1\nkind: wiki\ntitle: T\ndescription: D\n", "README.md": "# T\n\n```\nx\n```"})
	if _, err := Export(Options{Dir: src, Out: filepath.Join(t.TempDir(), "a"), Strict: true}); err == nil || !strings.Contains(err.Error(), "warning") {
		t.Fatalf("strict err = %v", err)
	}
	busy := t.TempDir()
	write(t, busy, map[string]string{"keep.txt": "x"})
	if _, err := Export(Options{Dir: src, Out: busy}); err == nil {
		t.Fatal("non-empty output accepted")
	}
}

func TestExportProgramDocsAndWithheldFiles(t *testing.T) {
	src, out := t.TempDir(), filepath.Join(t.TempDir(), "site")
	write(t, src, map[string]string{
		"main.go":        "package main",
		"docs/README.md": "# Tool\n\n[Guide](guide.md)",
		"docs/guide.md":  "# Guide",
		"docs/secret.md": "# Secret\n\npassword=synthetic-fixture-only",
	})
	report, err := Export(Options{Dir: src, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pages != 2 || report.Withheld != 1 {
		t.Fatalf("report %+v", report)
	}
	if !strings.Contains(read(t, out, "docs/index.html"), `href="guide.html"`) || !strings.Contains(read(t, out, "index.html"), `docs/index.html`) {
		t.Fatal("program docs not exported with a root redirect")
	}
	if _, err := os.Stat(filepath.Join(out, "docs", "secret.html")); err == nil {
		t.Fatal("withheld file published")
	}
}

func TestRelative(t *testing.T) {
	for _, tc := range []struct{ from, target, want string }{
		{"index.html", "a/b.html", "a/b.html"},
		{"a/b.html", "a/c.html", "c.html"},
		{"a/b/c.html", "a/x.png", "../x.png"},
		{"a/b.html", "index.html", "../index.html"},
		{"a/b.html", "d/e f.html", "../d/e%20f.html"},
		{"a/index.html", "a/index.html", "index.html"},
	} {
		if got := relative(tc.from, tc.target); got != tc.want {
			t.Errorf("relative(%q, %q) = %q, want %q", tc.from, tc.target, got, tc.want)
		}
	}
}

// The wiki template is the documented example; it must export with no
// formatting warnings, so it cannot drift from the renderer.
func TestWikiTemplateExportsCleanly(t *testing.T) {
	src := t.TempDir()
	template := filepath.Join("..", "..", "examples", "wiki-template")
	files := map[string]string{}
	err := filepath.WalkDir(template, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(template, name)
		files[filepath.ToSlash(rel)] = read(t, template, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, src, files)
	out := filepath.Join(t.TempDir(), "site")
	report, err := Export(Options{Dir: src, Out: out, Strict: true})
	if err != nil {
		t.Fatalf("%v: %+v", err, report.Warnings)
	}
	if report.Pages != 5 || report.Withheld != 0 {
		t.Fatalf("report %+v", report)
	}
	formatting := read(t, out, "guides/formatting.html")
	for _, want := range []string{"markdown-alert-caution", "<details open>", `<pre class="mermaid">`, `class="chroma"`, `class="footnotes"`, "<dl>", "<kbd>Ctrl</kbd>", `href="writing.html"`} {
		if !strings.Contains(formatting, want) {
			t.Fatalf("formatting.html missing %q", want)
		}
	}
}

func TestLanguageSwitchesInterfaceAndAlerts(t *testing.T) {
	src := t.TempDir()
	write(t, src, map[string]string{"veda.yaml": "schema_version: 1\nkind: wiki\ntitle: T\ndescription: D\n", "README.md": "# T\n\n## Part\n\n> [!WARNING]\n> Careful."})
	for lang, want := range map[string][]string{"en": {`lang="en"`, ">Warning<", "On this page"}, "ru": {`lang="ru"`, ">Внимание<", "На этой странице"}} {
		out := filepath.Join(t.TempDir(), lang)
		if _, err := Export(Options{Dir: src, Out: out, Lang: lang}); err != nil {
			t.Fatal(err)
		}
		home := read(t, out, "index.html")
		for _, w := range want {
			if !strings.Contains(home, w) {
				t.Fatalf("%s: missing %q", lang, w)
			}
		}
	}
	if _, err := Export(Options{Dir: src, Out: filepath.Join(t.TempDir(), "x"), Lang: "de"}); err == nil {
		t.Fatal("unknown language accepted")
	}
}
