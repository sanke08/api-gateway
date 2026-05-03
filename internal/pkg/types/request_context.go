package types

import (
	"context"

	"github.com/sanke08/api_gateway/internal/models"
)

// contextKey is an unexported type used to prevent collisions inside context.Context.
//
// Why this exists:
// context.Context is shared across the request lifecycle.
// Using a private key type prevents accidental overwrites from unrelated packages.
type contextKey string

const (
	// authenticatedUserContextKey stores the user that authenticated through JWT.
	authenticatedUserContextKey contextKey = "authenticated_user"

	// resolvedTenantContextKey stores the tenant selected for the current request.
	resolvedTenantContextKey contextKey = "resolved_tenant"

	// resolvedMembershipContextKey stores the membership that authorizes the user
	// inside the selected tenant.
	resolvedMembershipContextKey contextKey = "resolved_membership"
)

// WithAuthenticatedUser stores the authenticated user in the request context.
//
// Why this matters:
// Later middleware, handlers, and services can read the same user identity
// without reparsing the token or querying the database again.
func WithAuthenticatedUser(ctx context.Context, user models.User) context.Context {
	return context.WithValue(ctx, authenticatedUserContextKey, user)
}

// AuthenticatedUserFromContext reads the authenticated user from the request context.
func AuthenticatedUserFromContext(ctx context.Context) (models.User, bool) {
	value := ctx.Value(authenticatedUserContextKey)
	if value == nil {
		return models.User{}, false
	}

	user, ok := value.(models.User)
	return user, ok
}

// WithResolvedTenant stores the resolved tenant in the request context.
func WithResolvedTenant(ctx context.Context, tenant models.Tenant) context.Context {
	return context.WithValue(ctx, resolvedTenantContextKey, tenant)
}

// ResolvedTenantFromContext reads the resolved tenant from the request context.
func ResolvedTenantFromContext(ctx context.Context) (models.Tenant, bool) {
	value := ctx.Value(resolvedTenantContextKey)
	if value == nil {
		return models.Tenant{}, false
	}

	tenant, ok := value.(models.Tenant)
	return tenant, ok
}

// WithResolvedMembership stores the resolved membership in the request context.
func WithResolvedMembership(ctx context.Context, membership models.TenantMembership) context.Context {
	return context.WithValue(ctx, resolvedMembershipContextKey, membership)
}

// ResolvedMembershipFromContext reads the resolved membership from the request context.
func ResolvedMembershipFromContext(ctx context.Context) (models.TenantMembership, bool) {
	value := ctx.Value(resolvedMembershipContextKey)
	if value == nil {
		return models.TenantMembership{}, false
	}

	membership, ok := value.(models.TenantMembership)
	return membership, ok
}
