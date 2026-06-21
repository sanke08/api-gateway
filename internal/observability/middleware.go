package observability

import (
	"net/http"
	"strconv"
	"time"

	requesttypes "github.com/sanke08/api_gateway/internal/pkg/types"
)

// Middleware returns an HTTP middleware that records request timing and counts.
//
// Why this exists:
// It gives the gateway a single place to observe requests without mixing
// metrics code into handlers, router code, or proxy code.
// Middleware creates the observability middleware.
//
// Think of this middleware as:
//
//	Request
//	   │
//	   ▼
//	Start timer
//	   │
//	Create trace
//	   │
//	Wrap ResponseWriter
//	   │
//	Execute actual handler
//	   │
//	Collect status code + bytes written
//	   │
//	Calculate duration
//	   │
//	Record metrics
//	   │
//	Response
//
// Why this exists:
//
// Without middleware:
//
//	handler A records metrics
//	handler B records metrics
//	handler C records metrics
//
// Every handler duplicates observability code.
//
// With middleware:
//
// Request
//
//	↓
//
// Middleware records everything
//
//	↓
//
// Handler focuses only on business logic
func Middleware(reg *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reg == nil {
				next.ServeHTTP(w, r)
				return
			}

			startedAt := time.Now().UTC()
			trace := NewTrace(r)
			ctx := WithTrace(r.Context(), trace)

			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapped, r.WithContext(ctx))

			endedAt := time.Now().UTC()
			duration := endedAt.Sub(startedAt)

			trace.EndedAt = endedAt
			trace.Duration = duration
			trace.StatusCode = wrapped.statusCode
			trace.BytesWritten = wrapped.bytesWritten

			tenantID := "unknown"
			if tenant, ok := requesttypes.ResolvedTenantFromContext(ctx); ok {
				tenantID = tenant.Id
			}

			labels := Labels{
				"tenant": tenantID,
				"method": r.Method,
				"route":  r.URL.Path,
				"status": strconv.Itoa(wrapped.statusCode),
			}

			reg.RecordRequest(labels, duration, wrapped.bytesWritten)

			if wrapped.statusCode >= 400 {
				reg.RecordError(labels)
			}
		})
	}
}
