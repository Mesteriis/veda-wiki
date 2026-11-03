package domain

import (
	"errors"
)

var ErrInvalid = errors.New("invalid input")

var ErrConflict = errors.New("revision conflict")

var ErrNotFound = errors.New("not found")
