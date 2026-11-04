package domain

import (
	"path"
	"strings"
)

// Title returns the text of the first "# " heading in text, or the base
// name of path p when there is none.
func Title(text, p string) string {
	for {
		line, remaining, more := strings.Cut(text, "\n")
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
		if !more {
			return path.Base(p)
		}
		text = remaining
	}
}
