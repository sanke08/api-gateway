package ratelimit

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	requesttypes "github.com/sanke08/api_gateway/internal/pkg/types"
)

// KeyFunc builds the string used to identify one rate-limit bucket.
//
// Why this exists:
// Different scopes need different keys.
//
// Examples:
//
//	tenant:tenant-123
//	user:user-456
//	api_key:key-789
//	route:GET /api/orders
type KeyFunc func(*http.Request) (string, error)

// KeyTenant builds a key from the resolved tenant.
//
// Example:
//
//	tenant:tenant-123
//
// Why this is useful:
// It limits traffic by business, which is a common multi-tenant gateway rule.
func KeyTenant(r *http.Request) (string, error) {
	tenant, ok := requesttypes.ResolvedTenantFromContext(r.Context())
	if !ok {
		return "", fmt.Errorf("tenant context is missing")
	}

	return scopeKey("tenant", tenant.Id), nil
}

// KeyUser builds a key from the resolved user.
//
// Example:
//
//	user:user-456
//
// Why this is useful:
// It limits traffic by human identity after login.
func KeyUser(r *http.Request) (string, error) {
	user, ok := requesttypes.AuthenticatedUserFromContext(r.Context())
	if !ok {
		return "", fmt.Errorf("user context is missing")
	}

	return scopeKey("user", user.Id), nil
}

// KeyAPIKey builds a key from the resolved API key.
//
// Example:
//
//	api_key:key-789
//
// Why this is useful:
// It limits traffic by machine identity.
func KeyAPIKey(r *http.Request) (string, error) {
	apiKey, ok := requesttypes.ResolvedAPIKeyFromContext(r.Context())
	if !ok {
		return "", fmt.Errorf("api key context is missing")
	}

	return scopeKey("api_key", apiKey.ID), nil
}

// KeyRoute builds a key from the HTTP method and the current request path.
//
// Example:
//
//	route:GET /api/orders
//
// Why this is useful:
// It gives route-scoped throttling without needing a database-backed route name
// in this phase.
//
// Important note:
// This uses the current request path, not a route template.
// If later phases add a route name to context, this can be improved.
func KeyRoute(r *http.Request) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	p := cleanPath(r.URL.Path)

	if method == "" {
		return "", fmt.Errorf("request method is missing")
	}

	return scopeKey("route", method+" "+p), nil
}

// KeyTenantRoute combines tenant identity and route identity.
//
// Example:
//
//	tenant:tenant-123|route:GET /api/orders
//
// Why this is useful:
// It lets you throttle one tenant's hot route without affecting other routes.
func KeyTenantRoute(r *http.Request) (string, error) {
	tenant, err := KeyTenant(r)
	if err != nil {
		return "", err
	}

	route, err := KeyRoute(r)
	if err != nil {
		return "", err
	}

	return joinKeyParts(tenant, route), nil
}

// KeyTenantUser combines tenant identity and user identity.
//
// Example:
//
//	tenant:tenant-123|user:user-456
//
// Why this is useful:
// It lets you apply limits per human inside one tenant.
func KeyTenantUser(r *http.Request) (string, error) {
	tenant, err := KeyTenant(r)
	if err != nil {
		return "", err
	}

	user, err := KeyUser(r)
	if err != nil {
		return "", err
	}

	return joinKeyParts(tenant, user), nil
}

// KeyTenantAPIKey combines tenant identity and API key identity.
//
// Example:
//
//	tenant:tenant-123|api_key:key-789
//
// Why this is useful:
// It lets you limit one machine credential inside one tenant.
func KeyTenantAPIKey(r *http.Request) (string, error) {
	tenant, err := KeyTenant(r)
	if err != nil {
		return "", err
	}

	apiKey, err := KeyAPIKey(r)
	if err != nil {
		return "", err
	}

	return joinKeyParts(tenant, apiKey), nil
}

// scopeKey adds a prefix to a value.
//
// Why this exists:
// Prefixing keeps keys readable and avoids accidental collisions.
func scopeKey(scope, value string) string {
	scope = strings.TrimSpace(scope)
	value = strings.TrimSpace(value)

	return scope + ":" + value
}

// joinKeyParts joins already-prefixed key parts into one final rate-limit key.
//
// Example:
//
//	tenant:tenant-123 + route:GET /api/orders
//	=> tenant:tenant-123|route:GET /api/orders
func joinKeyParts(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}

	return strings.Join(filtered, "|")
}

// cleanPath normalizes a path for route-based rate limiting.
//
// Why this exists:
// It keeps route keys stable and predictable.
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}

	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "" {
		return "/"
	}

	return cleaned
}
