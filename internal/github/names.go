package github

import "github.com/Mesteriis/veda-wiki/internal/domain"

// The name rules live in domain so storage validation does not depend on the
// GitHub client.

func RepositoryName(value string) (string, error) { return domain.RepositoryName(value) }

func ValidBranch(branch string) bool { return domain.ValidBranch(branch) }

// ValidName reports whether s is a GitHub account or repository name part.
func ValidName(s string) bool { return domain.ValidGitHubName(s) }
