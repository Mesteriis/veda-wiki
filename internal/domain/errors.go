package domain

import (
	"errors"
)

// ErrInvalid reports input that fails validation.
var ErrInvalid = errors.New("invalid input")

// ErrConflict reports a stale expected revision or a state that forbids the
// change.
var ErrConflict = errors.New("revision conflict")

// ErrNotFound reports a missing or hidden record.
var ErrNotFound = errors.New("not found")

// ErrNoDocs reports a repository with neither veda.yaml nor a docs
// directory; such a source is skipped.
var ErrNoDocs = errors.New("no veda.yaml or docs directory; source skipped")
