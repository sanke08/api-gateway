package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/sanke08/api_gateway/internal/observability"
)

// key type to store request ID in context
type ctxKey string

const requestIDKey ctxKey = "request_id"

// LoggingMiddleware adds structured logging for all HTTP requests.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate or get req ID
		reqId := r.Header.Get("X-Request-ID")
		if reqId == "" {
			reqId = generateRequestID()
		}

		// Store request ID in context

		ctx := context.WithValue(r.Context(), requestIDKey, reqId)
		r = r.WithContext(ctx)

		// Wrap ResponseWriter to capture status code
		lrw := &loggingResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Call next handler
		next.ServeHTTP(lrw, r)

		// Log request
		observability.Info("HTTP Request",
			"request_id", reqId,
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration", time.Since(start).Microseconds(),
		)

	})
}

// generateRequestID returns a simple unique string (could replace with UUID in future)
func generateRequestID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b)

}

// loggingResponseWriter captures the status code for logging
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(statusCode int) {
	lrw.statusCode = statusCode
	lrw.ResponseWriter.WriteHeader(statusCode)
}
