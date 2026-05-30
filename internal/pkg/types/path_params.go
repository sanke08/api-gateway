package types

import (
	"context"
	"maps"
)

// pathParamsContextKey is a private context key type used for storing
// path parameters in context.Context.
//
// Why we use a custom struct type instead of a string:
// Context keys should be unique and collision-safe. Using a private,
// unexported struct type prevents accidental key collisions with other
// packages that may also store values in the same context.
//
// struct{} is used because it occupies zero memory.
// Package A:
// context.WithValue(ctx, "user", user)
// Package B:
// context.WithValue(ctx, "user", admin)
// Now both use same key:
// "user"
// One overwrites another.
type pathParamsContextKey struct{}

// WithPathParams stores a copy of path parameters in the request context.
//
// Why this matters:
// The router extracts parameters such as tenant_id or user_id from the URL path.
// Storing them in context makes them available to handlers without global state.
func WithPathParams(ctx context.Context, params map[string]string) context.Context {
	copied := make(map[string]string, len(params))
	maps.Copy(copied, params)
	return context.WithValue(ctx, pathParamsContextKey{}, copied)
}

// PathParamsFromContext reads all path parameters from the request context.
//
// Why this exists:
// Handlers often need the full parameter set when building a response or when
// multiple parameters are required for authorization or lookup.
func PathParamsFromContext(ctx context.Context) map[string]string {
	value := ctx.Value(pathParamsContextKey{})
	if value == nil {
		return map[string]string{}
	}

	params, ok := value.(map[string]string)
	if !ok || params == nil {
		return map[string]string{}
	}

	copied := make(map[string]string, len(params))
	maps.Copy(copied, params)

	return copied
}

// PathParam reads one named path parameter from the request context.
//
// Why this helper exists:
// Most handlers only need one value such as tenant_id or user_id.
// This keeps the handler code short and readable.
func PathParam(ctx context.Context, name string) (string, bool) {
	params := PathParamsFromContext(ctx)
	value, ok := params[name]
	return value, ok
}
