// Package auth owns access keys, named users and teams, password hashing and
// browser sessions. It is the only package that changes the AccessKeys, Users
// and Teams collections.
package auth

import "github.com/Mesteriis/veda-wiki/internal/store"

// Service runs credential operations against a store.
type Service struct {
	Store *store.Store
}
