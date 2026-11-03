package github

import (
	"errors"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// RemoteError reports a failure of the GitHub API or of its response.
type RemoteError struct {
	Code    string
	Status  int
	RetryAt time.Time
}

// Error returns the stable error code.
func (e *RemoteError) Error() string { return e.Code }

// RetryAt returns the rate-limit deadline carried by err, if any.
func RetryAt(err error) time.Time {
	var remote *RemoteError
	if errors.As(err, &remote) {
		return remote.RetryAt
	}
	return time.Time{}
}

// ErrorCode maps err to a stable, non-sensitive failure code.
func ErrorCode(e error) string {
	var remote *RemoteError
	if errors.As(e, &remote) {
		return remote.Code
	}
	if errors.Is(e, domain.ErrConflict) {
		return "source_conflict"
	}
	if errors.Is(e, domain.ErrNoDocs) {
		return "no_documentation"
	}
	return "sync_failed"
}
