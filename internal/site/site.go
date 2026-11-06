// Package site exports a wiki or program checkout as a static website with
// the same Markdown rules as VEDA, for GitHub Pages and other static hosts.
// It reads the checkout as an import does: only tracked Markdown and images,
// a program's docs/ only, and never files withheld as credential-like.
package site

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/render"
)

// sourceID names the checkout inside the renderer; it never appears in the
// output.
const sourceID = "site"

var (
	//go:embed assets/site.css
	siteCSS []byte
	//go:embed assets/site.js
	siteJS []byte
	//go:embed assets/page.html
	pageHTML string
	page     = template.Must(template.New("page").Parse(pageHTML))
)

// Options configures one export.
type Options struct {
	Dir    string // checkout to read
	Out    string // output directory; must be absent or empty
	Title  string // site title; defaults to veda.yaml title or the directory name
	Strict bool   // fail after writing when any page has warnings
	Lang   string // interface and alert language: "en" (default) or "ru"
}

// labels are the site's interface strings in one language.
type labels struct {
	Lang, Pages, OnThisPage, Built string
	alerts                         render.AlertTitles
}

var languages = map[string]labels{
	"en": {Lang: "en", Pages: "Pages", OnThisPage: "On this page", Built: "Built with", alerts: render.EnglishAlerts},
	"ru": {Lang: "ru", Pages: "Страницы", OnThisPage: "На этой странице", Built: "Собрано", alerts: render.RussianAlerts},
}

// Report summarises an export.
type Report struct {
	Pages    int
	Assets   int
	Withheld int
	Skipped  []string                    // index.md pages shadowed by README.md
	Warnings map[string][]render.Warning // by Markdown path
}

// Export renders every page of the checkout into opts.Out.
func Export(opts Options) (Report, error) {
	report := Report{Warnings: map[string][]render.Warning{}}
	if opts.Lang == "" {
		opts.Lang = "en"
	}
	text, known := languages[opts.Lang]
	if !known {
		return report, fmt.Errorf("unsupported language %q: use en or ru", opts.Lang)
	}
	if err := emptyDir(opts.Out); err != nil {
		return report, err
	}
	batch, err := importer.PrepareCheckout(opts.Dir, sourceID)
	if err != nil {
		return report, fmt.Errorf("read checkout: %w", err)
	}
	report.Withheld = batch.Withheld
	title := opts.Title
	if title == "" && batch.Metadata != nil {
		title = batch.Metadata.Title
	}
	if title == "" {
		abs, _ := filepath.Abs(opts.Dir)
		title = filepath.Base(abs)
	}
	outputs := map[string]string{} // Markdown path -> HTML path
	for name := range batch.Files {
		if target, ok := htmlPath(name, batch.Files); ok {
			outputs[name] = target
		} else {
			report.Skipped = append(report.Skipped, name)
		}
	}
	sort.Strings(report.Skipped)
	nav := navigation(batch.Files, outputs)
	for name, target := range outputs {
		markdown := batch.Files[name]
		p := domain.Page{Path: sourceID + "/" + name, Text: markdown}
		body, toc, err := render.PageWith(p, links(target, batch.Files), text.alerts)
		if err != nil {
			return report, fmt.Errorf("render %s: %w", name, err)
		}
		if w := render.Warnings(p); len(w) > 0 {
			report.Warnings[name] = w
		}
		pageTitle := domain.Title(markdown, name)
		full := title
		if target != "index.html" {
			full = pageTitle + " · " + title
		}
		var out bytes.Buffer
		err = page.Execute(&out, map[string]any{
			"Title": full, "Site": title, "Root": relative(target, "index.html"),
			"CSS": relative(target, "site.css"), "JS": relative(target, "site.js"),
			"Diagrams": strings.Contains(string(body), `<pre class="mermaid">`),
			"Body":     body, "TOC": toc, "Nav": nav.forPage(target), "Text": text,
		})
		if err != nil {
			return report, err
		}
		if err := writeFile(opts.Out, target, out.Bytes()); err != nil {
			return report, err
		}
		report.Pages++
	}
	for name, data := range batch.Assets {
		if err := writeFile(opts.Out, name, data); err != nil {
			return report, err
		}
		report.Assets++
	}
	if _, ok := outputs["README.md"]; !ok && batch.Kind == "program" {
		// A program's pages live under docs/; the root sends readers there.
		if docs, ok := outputs["docs/README.md"]; ok {
			redirect := fmt.Sprintf(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0; url=%s"><title>%s</title><a href="%s">%s</a>`,
				docs, template.HTMLEscapeString(title), docs, template.HTMLEscapeString(title))
			if err := writeFile(opts.Out, "index.html", []byte(redirect)); err != nil {
				return report, err
			}
		}
	}
	for name, data := range map[string][]byte{".nojekyll": nil, "site.css": siteCSS, "site.js": siteJS} {
		if err := writeFile(opts.Out, name, data); err != nil {
			return report, err
		}
	}
	if opts.Strict && len(report.Warnings) > 0 {
		return report, fmt.Errorf("%d pages have formatting warnings", len(report.Warnings))
	}
	return report, nil
}

// htmlPath maps a Markdown path to its page: README.md and index.md become
// the directory's index.html, README.md first; other pages keep their name.
func htmlPath(name string, files map[string]string) (string, bool) {
	dir, base := path.Split(name)
	stem := strings.TrimSuffix(strings.TrimSuffix(base, ".md"), ".markdown")
	switch strings.ToLower(base) {
	case "readme.md", "readme.markdown":
		return dir + "index.html", true
	case "index.md", "index.markdown":
		if _, readme := files[dir+"README.md"]; readme {
			return "", false
		}
		return dir + "index.html", true
	}
	return dir + stem + ".html", true
}

// links maps resolved targets to paths relative to the page being written.
func links(from string, files map[string]string) render.Links {
	return render.Links{
		Article: func(_, target, fragment string) string {
			dest := target
			switch {
			case target == "" || strings.HasSuffix(target, "/"):
				dest = strings.TrimSuffix(target, "/")
				if dest != "" {
					dest += "/"
				}
				dest += "index.html"
			case files[target] != "" || strings.HasSuffix(target, ".md") || strings.HasSuffix(target, ".markdown"):
				dest = path.Dir(target) + "/index.html"
				if mapped, ok := htmlPath(target, files); ok {
					dest = mapped
				}
				dest = strings.TrimPrefix(dest, "./")
			default:
				for _, index := range []string{"/README.md", "/index.md"} {
					if _, ok := files[target+index]; ok {
						dest = target + "/index.html"
						break
					}
				}
			}
			link := relative(from, dest)
			if fragment != "" {
				link += "#" + url.PathEscape(fragment)
			}
			return link
		},
		Image: func(_, target, _ string) string { return relative(from, target) },
	}
}

// relative is the URL of target as seen from the page at from; both are
// paths from the site root.
func relative(from, target string) string {
	fromDirs := strings.Split(from, "/")
	fromDirs = fromDirs[:len(fromDirs)-1]
	parts := strings.Split(target, "/")
	common := 0
	for common < len(fromDirs) && common < len(parts)-1 && fromDirs[common] == parts[common] {
		common++
	}
	parts = parts[common:]
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Repeat("../", len(fromDirs)-common) + strings.Join(parts, "/")
}

// emptyDir creates dir or accepts it when empty; it never overwrites files.
func emptyDir(dir string) error {
	if dir == "" {
		return errors.New("output directory is required")
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("output directory %s is not empty", dir)
	}
	return nil
}

// writeFile writes a site-relative path below root; names come from the
// checkout and were validated as safe relative paths by the importer.
func writeFile(root, name string, data []byte) error {
	if !domain.SafeRelative(name) && name != ".nojekyll" {
		return fmt.Errorf("unsafe output path %q", name)
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}
