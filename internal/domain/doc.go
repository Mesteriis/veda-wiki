// Package domain holds the VEDA data model, its errors and validators, and the
// pure state-transition rules over a Database. It performs no locking, file,
// network or system-clock access; callers pass the current time explicitly.
package domain
