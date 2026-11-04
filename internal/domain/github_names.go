package domain

import (
	"net/url"
	"regexp"
	"strings"
)

var gitHubNamePart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

// RepositoryName returns the canonical owner/name form of a GitHub repository
// name or https://github.com link, rejecting anything ambiguous.
func RepositoryName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://") {
		u, e := url.Parse(value)
		if e != nil || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", ErrInvalid
		}
		value = strings.TrimPrefix(u.Path, "/")
	}
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, ".git")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !gitHubNamePart.MatchString(parts[0]) || !gitHubNamePart.MatchString(parts[1]) || strings.HasSuffix(parts[0], ".") || strings.HasSuffix(parts[1], ".") {
		return "", ErrInvalid
	}
	return strings.Join(parts, "/"), nil
}

// ValidBranch reports whether branch is a safe Git branch name.
func ValidBranch(branch string) bool {
	return len(branch) > 0 &&
		len(branch) < 200 &&
		!strings.HasPrefix(branch, "-") &&
		!strings.HasPrefix(branch, "/") &&
		!strings.HasSuffix(branch, "/") &&
		!strings.HasSuffix(branch, ".") &&
		!strings.HasSuffix(branch, ".lock") &&
		!strings.Contains(branch, "..") &&
		!strings.Contains(branch, "//") &&
		!strings.ContainsAny(branch, " ~^:?*[\\\r\n\x00") &&
		!strings.Contains(branch, "@{")
}

// ValidGitHubName reports whether s is a GitHub account or repository name part.
func ValidGitHubName(s string) bool { return gitHubNamePart.MatchString(s) }
