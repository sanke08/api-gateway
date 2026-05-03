package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	requestTypes "github.com/sanke08/api_gateway/internal/pkg/types"
	"github.com/sanke08/api_gateway/internal/services"
)

// TenantResolutionMiddleware is the HTTP middleware that turns an incoming
// authenticated request into a fully resolved tenant-scoped request.
//
// What this middleware does:
// - reads the Bearer token from the Authorization header
// - reads the tenant ID from the X-Tenant-ID header
// - asks the resolver service to validate the token, tenant, and membership
// - stores the resolved user, tenant, and membership in the request context
//
// Why this middleware exists:
// The rest of the request pipeline should not repeat this logic.
// Once this middleware succeeds, the request is known to belong to a real user
// acting inside a real tenant.
type TenantResolutionMiddleware struct {
	next     http.Handler
	resolver services.TenantResolver
}

// NewTenantResolutionMiddleware creates the tenant resolution middleware.
//
// Why this constructor exists:
// It makes the request pipeline explicit and easy to test.
func NewTenantResolutionMiddleware(next http.Handler, resolver services.TenantResolver) http.Handler {
	return &TenantResolutionMiddleware{
		next:     next,
		resolver: resolver,
	}
}

// ServeHTTP runs the resolution flow before handing the request to the next handler.
func (m *TenantResolutionMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		m.next.ServeHTTP(w, r)
		return
	}

	accessToken, err := bearerTokenFromHeader(r.Header.Get("Authorization"))
	if err != nil {
		writeMiddlewareError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
		return
	}

	tenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	if tenantID == "" {
		writeMiddlewareError(w, http.StatusBadRequest, "validation_error", "X-Tenant-ID header is required")
		return
	}

	result, err := m.resolver.Resolve(r.Context(), services.TenantResolutionRequest{
		AccessToken: accessToken,
		TenantID:    tenantID,
	})

	if err != nil {
		m.writeError(w, err)
		return
	}

	ctx := r.Context()
	ctx = requestTypes.WithAuthenticatedUser(ctx, result.User)
	ctx = requestTypes.WithResolvedTenant(ctx, result.Tenant)
	ctx = requestTypes.WithResolvedMembership(ctx, result.Membership)

	m.next.ServeHTTP(w, r.WithContext(ctx))
}

// bearerTokenFromHeader extracts the token part from a standard Authorization header.
//
// Example header:
// Authorization: Bearer <token>
//
// Why this helper exists:
// Token parsing should be strict and predictable. Accepting malformed headers
// creates avoidable ambiguity and security bugs.
func bearerTokenFromHeader(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", errors.New("authorization header is missing")
	}

	parts := strings.Fields(header)
	if len(parts) != 2 {
		return "", errors.New("invalid authorization header")
	}

	if !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("authorization scheme must be Bearer")
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errors.New("bearer token is empty")
	}

	return token, nil
}

// writeError maps service errors to HTTP status codes for the middleware path.
func (m *TenantResolutionMiddleware) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrValidation):
		writeMiddlewareError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, services.ErrUnauthorized):
		writeMiddlewareError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired access token")
	case errors.Is(err, services.ErrForbidden):
		writeMiddlewareError(w, http.StatusForbidden, "forbidden", "tenant access denied")
	case errors.Is(err, services.ErrConflict):
		writeMiddlewareError(w, http.StatusConflict, "conflict_error", err.Error())
	default:
		writeMiddlewareError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
	}
}

// middlewareErrorResponse is the JSON body returned by the middleware on failure.
type middlewareErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeMiddlewareError writes a stable JSON error response from middleware.
//
// Why this is separate from handler helpers:
// middleware lives in a different package and should not depend on handler internals.
func writeMiddlewareError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(middlewareErrorResponse{
		Error:   code,
		Message: message,
	})
}
