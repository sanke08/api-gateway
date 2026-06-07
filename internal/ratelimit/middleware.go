package ratelimit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// NewMiddleware returns a standard HTTP middleware that enforces one rate limit rule.
//
// What it does:
// - builds a key from the request
// - asks the limiter whether the request is allowed
// - returns 429 if the request exceeds the limit
//
// Why this exists:
// The limiter itself only stores token-bucket state.
// The middleware connects that limiter to HTTP traffic.
func NewMiddleware(
	limiter *Limiter,
	rule Rule,
	keyFunc KeyFunc,
) (func(http.Handler) http.Handler, error) {
	if limiter == nil {
		return nil, fmt.Errorf("rate limiter is required")
	}

	normalized, err := normalizeRule(rule)
	if err != nil {
		return nil, err
	}

	if keyFunc == nil {
		keyFunc = KeyRoute
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, err := keyFunc(r)
			if err != nil {
				writeRateLimitError(
					w,
					http.StatusInternalServerError,
					"rate_limit_context_missing",
					"rate limit key could not be built",
					0,
				)
				return
			}

			allowed, retryAfter, err := limiter.allow(key, normalized)
			if err != nil {
				if errors.Is(err, ErrInvalidKey) || errors.Is(err, ErrInvalidRule) {
					writeRateLimitError(
						w,
						http.StatusBadRequest,
						"invalid_rate_limit",
						"rate limit configuration is invalid",
						0,
					)
					return
				}

				writeRateLimitError(
					w,
					http.StatusInternalServerError,
					"rate_limit_error",
					"rate limit check failed",
					0,
				)
				return
			}

			if !allowed {
				retrySeconds := retryAfterSeconds(retryAfter)
				writeRateLimitError(
					w,
					http.StatusTooManyRequests,
					"rate_limited",
					"too many requests",
					retrySeconds,
				)
				return
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}

// writeRateLimitError writes the rate-limit rejection response.
//
// Why this exists:
// The client should get a predictable 429 response with Retry-After.
func writeRateLimitError(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	retryAfter int64,
) {
	w.Header().Set("Content-Type", "application/json")
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	}
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(rateLimitErrorResponse{
		Error:   code,
		Message: message,
	})
}

// rateLimitErrorResponse is the public JSON shape for rate-limit failures.
type rateLimitErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// retryAfterSeconds converts a duration to whole seconds for Retry-After.
//
// Why this exists:
// HTTP Retry-After is commonly expressed in seconds.
func retryAfterSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}

	secs := int64(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}
	if secs < 1 {
		secs = 1
	}

	return secs
}
