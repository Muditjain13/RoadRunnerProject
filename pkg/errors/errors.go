// Package errors holds sentinel errors shared across services. Wrap with
// fmt.Errorf("doing x: %w", err) at each layer; check with errors.Is at
// the boundary that needs to branch on it (e.g. gRPC handler mapping to a
// status code).
package errors

import "errors"

var (
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrAlreadyExists      = errors.New("already exists")
	ErrPreconditionFailed = errors.New("precondition failed")
	ErrUnavailable        = errors.New("unavailable")
)
