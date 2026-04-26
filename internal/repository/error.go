package repository

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/lib/pq"
)

var (
	ErrTenantNotFound = errors.New("tenant not found")
	ErrTenantExists   = errors.New("tenant already exists")
	ErrUserNotFound   = errors.New("user not found")
	ErrUserExists     = errors.New("user already exists")
	ErrAPIKeyNotFound = errors.New("api key not found")
	ErrNoRowsAffected = errors.New("no rows affected")
)

// Package repository provides a structured error handling model for the data layer.
//
// Overview:
//
// This package defines RepoError, a custom error type used to standardize how
// database and repository-level errors are represented and propagated.
//
// Instead of returning raw database/driver errors (which are unstable and
// implementation-specific), all errors are mapped into a small set of stable
// categories (Kind), such as:
//
//   - "not_found"   → resource does not exist
//   - "conflict"    → duplicate or constraint violation
//   - "validation"  → invalid input or data
//   - "internal"    → unexpected/system error
//
// Each RepoError contains:
//   - Kind   → high-level category of the error
//   - Op     → operation where the error occurred (e.g. "GetUser")
//   - Entity → Slug entity involved (e.g. "user")
//   - Err    → underlying/original error (wrapped)
//
// Error Behavior:
//
// RepoError integrates with Go's standard error handling features:
//
//   1. Error()
//      - Provides a formatted string representation for logging/debugging.
//      - Automatically used by fmt, log, etc.
//
//   2. Unwrap()
//      - Returns the underlying error.
//      - Used automatically by errors.Is and errors.As to traverse error chains.
//
//   3. Is()
//      - Customizes comparison logic for errors.Is.
//      - Two RepoError values are considered equal if their Kind matches.
//      - Op, Entity, and underlying Err are intentionally ignored.
//
// Usage:
//
// Always use errors.Is to check error categories:
//
//   if errors.Is(err, ErrNotFound) {
//       // handle not found case
//   }
//
// Do NOT use direct comparison (==), as it bypasses Unwrap() and Is():
//
//   if err == ErrNotFound { // ❌ incorrect
//   }
//
// Design Notes:
//
// - This approach decouples the service layer from database-specific errors.
// - It ensures consistent error handling across the application.
// - It allows internal implementation (e.g. PostgreSQL, pq, pgx) to change
//   without affecting higher layers.
//
// Important:
//
// Avoid mixing plain errors (errors.New) with RepoError for the same purpose,
// as they will not be compatible with errors.Is checks.

// --------------------------------------------------------------------------------------------------------------
//

// [MAIN]

// RepoError is the repository-level error type.
//
// Why this exists:
// The service layer should not need to inspect raw PostgreSQL error messages.
// It should receive stable categories such as not_found, conflict, validation,
// or internal.
type RepoError struct {
	Kind   string //Type/category of error (e.g. "not_found", "conflict")
	Op     string //Operation where error happened
	Entity string // Which Slug object (user, order, etc.)
	Err    error  //Original underlying error
}

func (e *RepoError) Error() string {
	// len (length)	Number of elements currently in the slice
	// cap (capacity)	Maximum elements it can hold before resizing
	parts := make([]string, 0, 3) //make(type, length, capacity)

	if e.Op != "" {
		parts = append(parts, e.Op)
	}

	if e.Entity != "" {
		parts = append(parts, e.Entity)
	}

	if e.Kind != "" {
		parts = append(parts, e.Kind)
	}

	// Join → Combines multiple strings into one
	// parts := []string{"GetUser", "user", "not_found"}
	// base := "GetUser user not_found"
	base := strings.Join(parts, " ")

	if e.Err == nil {
		return base
	}

	if base == "" {
		return e.Err.Error()
	}

	return base + " : " + e.Err.Error()
}

// Unwrap returns the underlying error.
//
// It is used automatically by errors.Is and errors.As to traverse
// the error chain. This allows callers to inspect wrapped errors
// (e.g. sql.ErrNoRows) without accessing e.Err directly.
//
// Note: This is NOT called automatically in normal comparisons.
// It only works when using errors.Is / errors.As.

func (e *RepoError) Unwrap() error {
	return e.Err // This allows errors.Is(repoErr, pg.ErrNoRows) to work.
}

// Is enables errors.Is to compare RepoError values by Kind.
//
// It is called automatically by errors.Is. Two RepoError values
// are considered equal if their Kind matches, ignoring Op, Entity,
// and the underlying error.
//
// Note: This logic is only used with errors.Is. Direct comparison
// using == will NOT use this method and may give incorrect results.
func (e *RepoError) Is(target error) bool {
	t, ok := target.(*RepoError)
	if !ok {
		return false
	}
	return e.Kind == t.Kind
}

var (
	ErrNotFound   = &RepoError{Kind: "not_found"}
	ErrConflict   = &RepoError{Kind: "conflict"}
	ErrValidation = &RepoError{Kind: "validation"}
	ErrInternal   = &RepoError{Kind: "internal"}
)

func wrap(base *RepoError, op, entity string, err error) error {
	if err == nil {
		return nil
	}
	return &RepoError{
		Kind:   base.Kind,
		Op:     op,
		Entity: entity,
		Err:    err,
	}
}

func validationError(op, entity, msg string) error {
	return &RepoError{
		Kind:   ErrValidation.Kind,
		Op:     op,
		Entity: entity,
		Err:    errors.New(msg),
	}
}

func classifySQLError(op, entity string, err error, fkAsNotFound bool) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, sql.ErrNoRows) {
		return wrap(ErrNotFound, op, entity, err)
	}

	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch string(pqErr.Code) {
		case "23505":
			return wrap(ErrConflict, op, entity, err)
		case "23502", "22001":
			return wrap(ErrValidation, op, entity, err)
		case "23503":
			if fkAsNotFound {
				return wrap(ErrNotFound, op, entity, err)
			}
			return wrap(ErrConflict, op, entity, err)
		default:
			return wrap(ErrInternal, op, entity, err)
		}
	}
	return wrap(ErrInternal, op, entity, err)
}
