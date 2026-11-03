package github

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

var namePart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

func RepositoryName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://") {
		u, e := url.Parse(value)
		if e != nil || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", domain.ErrInvalid
		}
		value = strings.TrimPrefix(u.Path, "/")
	}
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, ".git")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !namePart.MatchString(parts[0]) || !namePart.MatchString(parts[1]) || strings.HasSuffix(parts[0], ".") || strings.HasSuffix(parts[1], ".") {
		return "", domain.ErrInvalid
	}
	return strings.Join(parts, "/"), nil
}
func ValidBranch(branch string) bool {
	return len(branch) > 0 && len(branch) < 200 && !strings.HasPrefix(branch, "-") && !strings.HasPrefix(branch, "/") && !strings.HasSuffix(branch, "/") && !strings.HasSuffix(branch, ".") && !strings.HasSuffix(branch, ".lock") && !strings.Contains(branch, "..") && !strings.Contains(branch, "//") && !strings.ContainsAny(branch, " ~^:?*[\\\r\n\x00") && !strings.Contains(branch, "@{")
}

// ValidName reports whether s is a GitHub account or repository name part.
func ValidName(s string) bool { return namePart.MatchString(s) }
