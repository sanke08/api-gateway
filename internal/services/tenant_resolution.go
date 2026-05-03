package services

import (
	"context"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/repository"
)

// TenantResolutionRequest is the input for tenant resolution.
//
// What this means:
// A request has already been authenticated as a human user through the access
// token, and the client also says which tenant it wants to act under.
//
// Why this is separate from login:
// Login proves who the user is.
// Tenant resolution proves which business the user is acting for.
//
// Why the access token is included:
// The resolver must verify the token again before trusting the user ID inside it.
type TenantResolutionRequest struct {
	AccessToken string
	TenantID    string
}

// TenantResolutionResult is the successful result of tenant resolution.
//
// What it contains:
// - the authenticated user
// - the resolved tenant
// - the membership that links the user to that tenant
// - the parsed token claims for future use if needed
//
// Why this is useful:
// Later middleware and handlers can read the resolved identity from context
// without repeating database lookups.
type TenantResolutionResult struct {
	User       models.User
	Tenant     models.Tenant
	Membership models.TenantMembership
	Claims     AccessTokenClaims
	ResolvedAt time.Time
}

// TenantResolver defines the tenant-resolution use case.
//
// Why this interface exists:
// It keeps request processing logic out of handlers and out of repositories.
// The service layer owns the business rules for who can act on which tenant.
type TenantResolver interface {
	Resolve(ctx context.Context, req TenantResolutionRequest) (TenantResolutionResult, error)
}

// tenantResolver is the concrete implementation of TenantResolver.
type tenantResolver struct {
	repos        repository.Repositories
	tokenService TokenService
	clock        func() time.Time
}

// NewTenantResolver creates a tenant resolver.
//
// Why tokenService is injected:
// The service needs to validate access tokens, but token parsing should remain
// a dependency rather than hardwired logic.
func NewTenantResolver(repos repository.Repositories, tokenService TokenService) TenantResolver {
	return &tenantResolver{
		repos:        repos,
		tokenService: tokenService,
		clock:        nowUTC,
	}
}

// Resolve verifies the token, loads the user, loads the tenant, checks the membership,
// and returns the resolved identity bundle.
//
// Why the order matters:
// 1. Verify the access token first.
// 2. Load the user from the token subject.
// 3. Load the tenant from the request.
// 4. Check that the user belongs to that tenant.
// 5. Make sure both the tenant and membership are active.
//
// Why this is necessary:
// A user may belong to multiple tenants. The system must never guess which
// tenant the request should use.
func (r *tenantResolver) Resolve(ctx context.Context, req TenantResolutionRequest) (TenantResolutionResult, error) {
	const op = "tenant.resolve"

	normalized, err := normalizeTenantResolutionRequest(req)
	if err != nil {
		return TenantResolutionResult{}, err
	}

	claims, err := r.tokenService.VerifyAccessToken(normalized.AccessToken)
	if err != nil {
		return TenantResolutionResult{}, unauthorizedError(op, "invalid access token")
	}

	if strings.TrimSpace(claims.Subject) == "" {
		return TenantResolutionResult{}, unauthorizedError(op, "invalid access token subject")
	}

	user, err := r.repos.Users.GetById(ctx, claims.Subject)
	if err != nil {
		if isRepositoryNotFound(err) {
			return TenantResolutionResult{}, unauthorizedError(op, "invalid access token")
		}
		return TenantResolutionResult{}, mapRepositoryError(op, err)
	}

	tenant, err := r.repos.Tenants.GetByID(ctx, normalized.TenantID)
	if err != nil {
		if isRepositoryNotFound(err) {
			return TenantResolutionResult{}, forbiddenError(op, "tenant access denied")
		}
		return TenantResolutionResult{}, mapRepositoryError(op, err)
	}

	if tenant.Status != models.TenantStatusActive {
		return TenantResolutionResult{}, forbiddenError(op, "tenant is not active")
	}

	membership, err := r.repos.Memberships.GetByUserAndTenant(ctx, user.Id, tenant.Id)
	if err != nil {
		if isRepositoryNotFound(err) {
			return TenantResolutionResult{}, forbiddenError(op, "tenant access denied")
		}
		return TenantResolutionResult{}, mapRepositoryError(op, err)
	}

	if membership.Status != models.MembershipStatusActive {
		return TenantResolutionResult{}, forbiddenError(op, "membership is not active")
	}

	return TenantResolutionResult{
		User:       user,
		Tenant:     tenant,
		Membership: membership,
		Claims:     claims,
		ResolvedAt: r.clock(),
	}, nil
}

// normalizedTenantResolutionRequest is the cleaned version of the request input.
//
// Why this type exists:
// The resolver should not work with noisy or whitespace-padded values.
// It should only work with normalized data.
type normalizedTenantResolutionRequest struct {
	AccessToken string
	TenantID    string
}

// normalizeTenantResolutionRequest validates and trims the incoming request.
//
// What it does:
// - trims whitespace
// - ensures the access token is present
// - ensures the tenant ID is present
func normalizeTenantResolutionRequest(req TenantResolutionRequest) (normalizedTenantResolutionRequest, error) {
	const op = "tenant.validate_resolution"

	accessToken := strings.TrimSpace(req.AccessToken)
	tenantID := strings.TrimSpace(req.TenantID)

	if accessToken == "" {
		return normalizedTenantResolutionRequest{}, validationError(op, "access_token is required")
	}
	if tenantID == "" {
		return normalizedTenantResolutionRequest{}, validationError(op, "tenant_id is required")
	}

	return normalizedTenantResolutionRequest{
		AccessToken: accessToken,
		TenantID:    tenantID,
	}, nil
}

// isRepositoryNotFound checks whether a repository error means the row was missing.
//
// Why this helper exists:
// The resolver intentionally hides whether the user or tenant exists.
// That prevents information leakage that could help enumeration attacks.
// func isRepositoryNotFound(err error) bool {
// 	var repoErr *repository.RepoError
// 	if !errors.As(err, &repoErr) {
// 		return false
// 	}
// 	return repoErr.Kind == repository.ErrNotFound.Kind
// }
