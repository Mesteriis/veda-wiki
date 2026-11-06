// Command veda-site exports a wiki or program checkout as a static website
// (for GitHub Pages) with VEDA's Markdown rules. It does not embed the VEDA
// reading interface, so `go install` builds it without a frontend step.
package main

import (
	"fmt"
	"os"

	"github.com/Mesteriis/veda-wiki/internal/site"
)

func main() {
	if err := site.Command(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "veda-site:", err)
		os.Exit(1)
	}
}
