package services

import "errors"

// ErrUnauthorized represents an authentication failure.
//
// Why this exists:
// Login failures should not leak whether the email exists or the password was wrong.
// The public response should stay generic so account enumeration becomes harder.
var ErrUnauthorized = &ServiceError{Kind: "unauthorized"}

// unauthorizedError creates a service-layer authentication error.
//
// Why this helper exists:
// The service layer needs a stable way to report "login failed" without exposing
// the exact reason to the client.
func unauthorizedError(op, msg string) error {
	return &ServiceError{
		Kind: ErrUnauthorized.Kind,
		Op:   op,
		Err:  errors.New(msg),
	}
}
