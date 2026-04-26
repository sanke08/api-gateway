// Package services defines a custom error type used across the service layer.
//
// Overview:
//
// This package introduces a structured error type (Error) to represent
// business-level failures in a consistent and predictable way. Instead of
// returning raw repository/database errors, all errors are mapped into a small
// set of stable categories (Kind), such as:
//
//   - "validation" → invalid input or request
//   - "conflict"   → duplicate or state conflict
//   - "internal"   → unexpected/system failure
//
// This allows higher layers (e.g. HTTP handlers) to map errors cleanly to
// responses (e.g. 400, 409, 500) without knowing implementation details.
//
// How it works:
//
// The Error struct implements Go’s built-in error interface by defining:
//
//   Error() string
//
// This makes it usable anywhere a standard error is expected.
//
// Additionally, it defines two special methods:
//
//   - Is(target error) bool
//   - Unwrap() error
//
// These methods are NOT overrides of any default behavior.
// Go does not support method overriding like OOP languages.
//
// Instead, Go’s standard library functions (errors.Is and errors.As)
// are designed to *look for these methods* and use them if present.
//
// Behavior:
//
// 1. errors.Is(err, target)
//    - First checks direct equality (err == target)
//    - Then calls err.Is(target) if defined
//    - Then calls Unwrap() to traverse wrapped errors recursively
//
// 2. Is()
//    - Provides custom comparison logic
//    - In this package, errors are considered equal if their Kind matches
//    - This allows comparisons like:
//
//        errors.Is(err, ErrValidation)
//
//      even if the error instances are different
//
// 3. Unwrap()
//    - Returns the underlying error
//    - Enables error chaining
//    - Allows errors.Is / errors.As to inspect deeper causes
//
// Why this design:
//
// - Decouples service logic from repository/database errors
// - Provides stable, predictable error categories
// - Enables flexible error comparison using errors.Is
// - Preserves original errors for debugging via Unwrap()
//
// Important:
//
// - Do NOT use == to compare errors; always use errors.Is
// - These methods (Is, Unwrap) are only used by errors.Is / errors.As
// - They are not called automatically in normal execution
//
// Summary:
//
// This is not overriding Go behavior. Instead, it extends Go’s error handling
// by providing custom comparison and chaining logic that the standard library
// will use when needed.

package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sanke08/api_gateway/internal/repository"
)

// Error is the service-layer error type.
//
// Why this exists:
// The HTTP layer should not need to know database internals.
// It should receive a clean, stable error kind such as validation, conflict,
// or internal, and then map that to HTTP status codes.
type ServiceError struct {
	Kind string //Type/category of error
	Op   string //Operation where error happened
	Err  error  //Original underlying error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}

	base := strings.TrimSpace(strings.Join([]string{e.Op, e.Kind}, " "))
	if e.Err == nil {
		return base
	}
	if base == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s : %s", base, e.Err.Error())
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ServiceError) Is(target error) bool {
	t, ok := target.(*ServiceError)
	if !ok {
		return false
	}
	return e.Kind == t.Kind
}

var (
	ErrValidation = &ServiceError{Kind: "validation"}
	ErrConflict   = &ServiceError{Kind: "conflict"}
	ErrInternal   = &ServiceError{Kind: "internal"}
)

func validationError(op, msg string) error {
	return &ServiceError{
		Kind: ErrValidation.Kind,
		Op:   op,
		Err:  errors.New(msg),
	}
}

func conflictError(op, msg string) error {
	return &ServiceError{
		Kind: ErrConflict.Kind,
		Op:   op,
		Err:  errors.New(msg),
	}
}

func internalError(op string, err error) error {
	return &ServiceError{
		Kind: ErrInternal.Kind,
		Op:   op,
		Err:  err,
	}
}

// mapRepositoryError converts repository errors into service errors.
//
// Why this matters:
// Repositories know about SQL and storage semantics.
// Services should know about business semantics.
func mapRepositoryError(op string, err error) error {
	if err == nil {
		return nil
	}

	var repoErr *repository.RepoError
	if errors.As(err, &repoErr) {
		switch repoErr.Kind {
		case ErrValidation.Kind:
			return &ServiceError{Kind: ErrValidation.Kind, Op: op, Err: repoErr}
		case ErrConflict.Kind:
			return &ServiceError{Kind: ErrConflict.Kind, Op: op, Err: repoErr}
		case ErrValidation.Kind:
			return &ServiceError{Kind: ErrValidation.Kind, Op: op, Err: repoErr}
		default:
			return &ServiceError{Kind: ErrInternal.Kind, Op: op, Err: repoErr}
		}
	}

	return &ServiceError{Kind: ErrInternal.Kind, Op: op, Err: err}
}
