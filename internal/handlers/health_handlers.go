package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/sanke08/api_gateway/internal/services"
)

// HealthHandler serves /health.
//
// Why this handler exists:
// Liveness checks should be fast and should not depend on the database or
// upstream services.
type HealthHandler struct {
	checker *services.HealthChecker
}

// NewHealthHandler creates a liveness endpoint handler.
func NewHealthHandler(checker *services.HealthChecker) http.Handler {
	return &HealthHandler{checker: checker}
}

// ServeHTTP returns the liveness status as JSON.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeHealthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}

	if h.checker == nil {
		writeHealthError(w, http.StatusInternalServerError, "health_error", "health checker is not configured")
		return
	}

	status := h.checker.Liveness(r.Context())
	writeJSON(w, http.StatusOK, status)
}

// ReadyHandler serves /ready.
//
// Why this handler exists:
// Readiness should confirm that the gateway can accept traffic.
// It checks the database and includes upstream health details in the response.
type ReadyHandler struct {
	checker *services.HealthChecker
}

// NewReadyHandler creates a readiness endpoint handler.
func NewReadyHandler(checker *services.HealthChecker) http.Handler {
	return &ReadyHandler{checker: checker}
}

// ServeHTTP returns the readiness status as JSON.
//
// Important:
// Upstream failures are reported, but the gateway stays ready if the core
// dependency, the database, is healthy.
func (h *ReadyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeHealthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}

	if h.checker == nil {
		writeHealthError(w, http.StatusInternalServerError, "health_error", "health checker is not configured")
		return
	}

	status := h.checker.Readiness(r.Context())
	if status.Status != "ready" {
		writeJSON(w, http.StatusServiceUnavailable, status)
		return
	}

	writeJSON(w, http.StatusOK, status)
}

// writeHealthError writes a simple JSON error response.
//
// Why this exists:
// Health endpoints should stay machine-readable and easy to debug.
func writeHealthError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}

// ensure the model import is used in case the file is extended later.
// var _ models.LivenessStatus
