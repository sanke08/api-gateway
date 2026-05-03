package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sanke08/api_gateway/internal/services"
)

// AuthHandler exposes the login endpoint.
//
// Why this handler is thin:
// The HTTP layer should only decode JSON, call the service, and encode the response.
// All authentication rules belong in the service layer.
type AuthHandler struct {
	svc services.AuthService
}

// NewAuthHandler creates a login handler.
func NewAuthHandler(svc services.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// ServeHTTP handles POST /login.
//
// Why a dedicated handler exists:
// Login is a public entry point and must be isolated from the rest of the API.
func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}

	var req loginHTTPRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body is invalid")
		return
	}

	result, err := h.svc.Login(r.Context(), services.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, loginHTTPResponseFromResult(result))
}

// loginHTTPRequest is the JSON body accepted by the login endpoint.
type loginHTTPRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginHTTPResponse is the public response returned after a successful login.
//
// Why the response includes memberships:
// The user may belong to multiple tenants. The client can use this list to let
// the person choose which business they want to work under next.
type loginHTTPResponse struct {
	User                  safeUserResponse         `json:"user"`
	Memberships           []safeMembershipResponse `json:"memberships"`
	AccessToken           string                   `json:"access_token"`
	AccessTokenExpiresAt  time.Time                `json:"access_token_expires_at"`
	RefreshToken          string                   `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time                `json:"refresh_token_expires_at"`
	LoggedInAt            time.Time                `json:"logged_in_at"`
}

// // safeUserResponse removes sensitive fields such as the password hash.
// type safeUserResponse struct {
// 	ID        string    `json:"id"`
// 	Email     string    `json:"email"`
// 	CreatedAt time.Time `json:"created_at"`
// 	UpdatedAt time.Time `json:"updated_at"`
// }

// safeMembershipResponse is the public shape of a tenant membership.
type safeMembershipResponse struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// loginHTTPResponseFromResult converts the service result into a safe HTTP response.
func loginHTTPResponseFromResult(result services.LoginResult) loginHTTPResponse {
	memberships := make([]safeMembershipResponse, 0, len(result.Memberships))
	for _, m := range result.Memberships {
		memberships = append(memberships, safeMembershipResponse{
			ID:        m.ID,
			UserID:    m.UserId,
			TenantID:  m.TenantId,
			Role:      string(m.Role),
			Status:    string(m.Status),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		})
	}

	return loginHTTPResponse{
		User: safeUserResponse{
			ID:        result.User.Id,
			Email:     result.User.Email,
			CreatedAt: result.User.CreatedAt,
			UpdatedAt: result.User.UpdatedAt,
		},
		Memberships:           memberships,
		AccessToken:           result.AccessToken,
		AccessTokenExpiresAt:  result.AccessTokenExpiresAt,
		RefreshToken:          result.RefreshToken,
		RefreshTokenExpiresAt: result.RefreshTokenExpiresAt,
		LoggedInAt:            result.LoggedInAt,
	}
}

// writeError maps authentication errors to HTTP responses.
func (h *AuthHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrValidation):
		writeJSONError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, services.ErrUnauthorized):
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
	case errors.Is(err, services.ErrConflict):
		writeJSONError(w, http.StatusConflict, "conflict_error", err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "unexpected server error")
	}
}
