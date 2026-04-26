package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/services"
)

// OnboardingHandler exposes the tenant onboarding endpoint.
//
// Why this handler is thin:
// HTTP parsing and response writing belong here.
// Business rules belong in the service layer.
type OnboardingHandler struct {
	svc services.OnboardingService
}

// NewOnboardingHandler creates a new handler instance.
func NewOnboardingHandler(svc services.OnboardingService) *OnboardingHandler {
	return &OnboardingHandler{svc: svc}
}

// ServeHTTP handles POST /onboard.
func (h *OnboardingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}

	var req onboardingHTTPRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body is invalid")
		return
	}

	result, err := h.svc.OnboardTenant(r.Context(), services.OnboardingRequest{
		TenantName:    req.TenantName,
		TenantSlug:    req.TenantSlug,
		AdminEmail:    req.AdminEmail,
		AdminPassword: req.AdminPassword,
		APIKeyLabel:   req.APIKeyLabel,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, onboardingHTTPResponseFromResult(result))
}

type onboardingHTTPRequest struct {
	TenantName    string  `json:"tenant_name"`
	TenantSlug    string  `json:"tenant_slug"`
	AdminEmail    string  `json:"admin_email"`
	AdminPassword string  `json:"admin_password"`
	APIKeyLabel   *string `json:"api_key_label,omitempty"`
}

type onboardingHTTPResponse struct {
	Tenant          models.Tenant           `json:"tenant"`
	User            safeUserResponse        `json:"user"`
	Membership      models.TenantMembership `json:"membership"`
	APIKey          safeAPIKeyResponse      `json:"api_key"`
	APIKeySecretRaw string                  `json:"api_key_secret_raw"`
}

type safeUserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type safeAPIKeyResponse struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Description *string    `json:"description,omitempty"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func onboardingHTTPResponseFromResult(result services.OnboardingResult) onboardingHTTPResponse {
	return onboardingHTTPResponse{
		Tenant: result.Tenant,
		User: safeUserResponse{
			ID:        result.User.Id,
			Email:     result.User.Email,
			CreatedAt: result.User.CreatedAt,
			UpdatedAt: result.User.UpdatedAt,
		},
		Membership: result.Membership,
		APIKey: safeAPIKeyResponse{
			ID:          result.APIKey.ID,
			TenantID:    result.APIKey.TenantID,
			Description: result.APIKey.Description,
			Active:      result.APIKey.Active,
			CreatedAt:   result.APIKey.CreatedAt,
			UpdatedAt:   result.APIKey.UpdatedAt,
		},
		APIKeySecretRaw: result.APIKeySecretRaw,
	}
}

func (h *OnboardingHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrValidation):
		writeJSONError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, services.ErrConflict):
		writeJSONError(w, http.StatusConflict, "conflict_error", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
	}
}
