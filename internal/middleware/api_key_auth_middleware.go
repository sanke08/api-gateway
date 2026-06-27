package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sanke08/api_gateway/internal/observability"
	requestTypes "github.com/sanke08/api_gateway/internal/pkg/types"
	"github.com/sanke08/api_gateway/internal/services"
)

// APIKeyAuthMiddleware authenticates machine requests using X-API-Key.
//
// What this middleware does:
// - reads the raw API key from the X-API-Key header
// - sends it to the API key authentication service
// - stores the resolved API key and tenant in request context
//
// Why this middleware exists:
// Machine authentication is a cross-cutting concern. It should run before
// handlers so downstream code can trust the resolved tenant identity.
type APIKeyAuthMiddleware struct {
	next          http.Handler
	authenticator services.APIKeyAuthenticator
}

// NewAPIKeyAuthMiddleware creates the middleware wrapper.
//
// Why this constructor exists:
// It makes the request pipeline explicit and easy to test.
func NewAPIKeyAuthMiddleware(next http.Handler, authenticator services.APIKeyAuthenticator) http.Handler {
	return &APIKeyAuthMiddleware{
		next:          next,
		authenticator: authenticator,
	}
}

// ServeHTTP runs API key authentication before passing the request onward.
func (m *APIKeyAuthMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		m.next.ServeHTTP(w, r)
		return
	}

	rawAPIKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if rawAPIKey == "" {
		writeAPIKeyMiddlewareError(w, http.StatusUnauthorized, "unauthorized", "X-API-Key header is required")
		return
	}

	result, err := m.authenticator.Authenticate(r.Context(), services.APIKeyAuthenticationRequest{
		RawAPIKey: rawAPIKey,
	})
	if err != nil {
		m.writeError(w, err)
		return
	}
	if trace, ok := observability.TraceFromContext(r.Context()); ok && trace != nil {
		trace.TenantID = result.Tenant.Id
		trace.APIKeyID = result.APIKey.ID
	}

	ctx := r.Context()
	ctx = requestTypes.WithResolvedTenant(ctx, result.Tenant)
	ctx = requestTypes.WithResolvedAPIKey(ctx, result.APIKey)

	m.next.ServeHTTP(w, r.WithContext(ctx))
}

// writeError maps service-layer errors into stable HTTP responses.
func (m *APIKeyAuthMiddleware) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrValidation):
		writeAPIKeyMiddlewareError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, services.ErrUnauthorized):
		writeAPIKeyMiddlewareError(w, http.StatusUnauthorized, "unauthorized", "invalid api key")
	case errors.Is(err, services.ErrForbidden):
		writeAPIKeyMiddlewareError(w, http.StatusForbidden, "forbidden", "api key access denied")
	case errors.Is(err, services.ErrConflict):
		writeAPIKeyMiddlewareError(w, http.StatusConflict, "conflict_error", err.Error())
	default:
		writeAPIKeyMiddlewareError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
	}
}

// apiKeyMiddlewareErrorResponse is the JSON body returned by the middleware.
//
// Why a separate response type exists:
// Middleware lives outside the handler package, so it should own its own
// compact error response shape.
type apiKeyMiddlewareErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// writeAPIKeyMiddlewareError writes a JSON error response.
//
// Why this exists:
// It keeps middleware error output consistent and avoids leaking internals.
func writeAPIKeyMiddlewareError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(apiKeyMiddlewareErrorResponse{
		Error:   code,
		Message: message,
	})
}
