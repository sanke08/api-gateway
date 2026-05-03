package services

import "errors"

// ErrForbidden represents an authenticated request that is not allowed to
// proceed for business-authorization reasons.
//
// Why this exists:
// There is a difference between:
// - "the token is invalid or missing"  -> unauthorized
// - "the token is valid, but access is denied" -> forbidden
//
// That distinction matters because it lets the system respond correctly
// without mixing authentication failure with authorization failure.
var ErrForbidden = &ServiceError{Kind: "forbidden"}

// forbiddenError creates a service-layer authorization error.
//
// Why this helper exists:
// The service layer should express access denial in a controlled way instead
// of exposing internal repository or token details.
func forbiddenError(op, msg string) error {
	return &ServiceError{
		Kind: ErrForbidden.Kind,
		Op:   op,
		Err:  errors.New(msg),
	}
}
