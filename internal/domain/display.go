package domain

import (
	"path"
	"strings"
)

// SplitFrontMatter separates a leading YAML front matter block, as Jekyll,
// MkDocs and GitHub Pages write it between "---" lines, from the Markdown
// body. Text without a closed block is returned whole as the body.
func SplitFrontMatter(text string) (front, body string) {
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		if rest, ok = strings.CutPrefix(text, "---\r\n"); !ok {
			return "", text
		}
	}
	for offset := 0; offset < len(rest); {
		line, _, more := strings.Cut(rest[offset:], "\n")
		if end := strings.TrimRight(line, "\r"); end == "---" || end == "..." {
			next := offset + len(line)
			if more {
				next++
			}
			return rest[:offset], rest[next:]
		}
		if !more {
			break
		}
		offset += len(line) + 1
	}
	return "", text
}

// frontMatterTitle returns the top-level plain or quoted "title:" value.
func frontMatterTitle(front string) string {
	for _, line := range strings.Split(front, "\n") {
		value, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "title:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		return value
	}
	return ""
}

// Title returns the front matter title, else the text of the first "# "
// heading in text, else the base name of path p.
func Title(text, p string) string {
	front, text := SplitFrontMatter(text)
	if title := frontMatterTitle(front); title != "" {
		return title
	}
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
