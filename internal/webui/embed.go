// Package webui embeds the built reading interface. `make frontend` copies
// web/dist into dist before the Go build.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var files embed.FS

// Assets returns the built interface rooted at its index.html.
func Assets() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time
	}
	return sub
}

// Index returns the interface's entry page.
func Index() ([]byte, error) { return files.ReadFile("dist/index.html") }
