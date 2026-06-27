package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// Trace stores request-scoped timing and identification data.
//
// Why this exists:
// Metrics tell you what happened in aggregate.
// Trace tells you what happened for one specific request.
// Trace stores information about ONE specific request.
//
// Think:
//
// Metrics answer:
//
//	"How many requests failed today?"
//
// Traces answer:
//
//	"What exactly happened to THIS request?"
//
// Example:
//
//	Client
//	  |
//	  v
//	GET /products
//
// Request ID:
//
//	abc123
//
// Trace collects:
//
//	RequestID   = abc123
//	Method      = GET
//	Path        = /products
//	StartedAt   = 10:00:00
//	EndedAt     = 10:00:01
//	Duration    = 1 second
//	StatusCode  = 200
//
// Later:
//
// Logs, metrics, and errors can all reference the same RequestID.
//
// This makes debugging much easier.
type Trace struct {

	// RequestID uniquely identifies one request.
	//
	// Example:
	//
	//	a3f9b12c8e7d4f11
	//
	// Why this matters:
	//
	// Imagine a customer says:
	//
	//	"My request failed."
	//
	// Without RequestID:
	//
	//	You must search thousands of logs.
	//
	// With RequestID:
	//
	//	You search one ID and immediately find:
	//
	//	- gateway logs
	//	- upstream logs
	//	- metrics
	//	- errors
	RequestID string

	// StartedAt records when processing began.
	//
	// Example:
	//
	//	2026-06-21 10:00:00 UTC
	//
	// Used later to calculate request duration.
	StartedAt time.Time

	// EndedAt records when processing finished.
	//
	// Example:
	//
	//	2026-06-21 10:00:01 UTC
	//
	// Together with StartedAt:
	//
	//	EndedAt - StartedAt
	//
	// gives total request time.
	EndedAt time.Time

	// Duration is total request latency.
	//
	// Example:
	//
	//	1.2 seconds
	//
	// This is often one of the most important observability metrics.
	//
	// Questions it helps answer:
	//
	//	Why is this request slow?
	//	Did latency spike?
	Duration time.Duration

	// HTTP method used by the request.
	//
	// Examples:
	//
	//	GET
	//	POST
	//	PUT
	//	DELETE
	Method string

	// Incoming request path.
	//
	// Example:
	//
	//	/products
	//	/orders/123
	//
	// Useful for logs and debugging.
	Path string

	// Resolved tenant identifier.
	//
	// Example:
	//
	//	tenant-123
	//
	// Multi-tenant gateways often need to know:
	//
	//	Which tenant generated this request?
	TenantID string

	// Final HTTP status returned to the client.
	//
	// Examples:
	//
	//	200
	//	404
	//	500
	//
	// Useful for debugging failures.
	StatusCode int

	// Number of bytes sent to the client.
	//
	// Example:
	//
	//	Response:
	//
	//	{"products":[...]}
	//
	// Size:
	//
	//	2048 bytes
	//
	// Can help identify unusually large responses.
	BytesWritten int64

	// UserID is the resolved human user, if available.
	UserID string

	// APIKeyID is the resolved API key, if available.
	APIKeyID string

	// MembershipID is the resolved tenant membership, if available.
	MembershipID string

	// Cached reports whether the request was served from cache.
	Cached bool

	// Retried reports whether the upstream call needed retry behavior.
	Retried bool
}

// traceContextKey is a private context key.
//
// Why this exists:
//
// Context values are shared by many packages.
//
// Bad:
//
//	context.WithValue(ctx, "trace", trace)
//
// Another package might also use:
//
//	context.WithValue(ctx, "trace", somethingElse)
//
// Collision occurs.
//
// Using a private struct type prevents collisions.
//
// Nobody outside this package can accidentally use the same key.
type traceContextKey struct{}

// WithTrace stores a trace pointer in the request context.
//
// Why a pointer is used:
// The middleware can update the trace fields after the request finishes.
func WithTrace(ctx context.Context, trace *Trace) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceFromContext reads a trace pointer from the request context.
func TraceFromContext(ctx context.Context) (*Trace, bool) {
	value := ctx.Value(traceContextKey{})
	if value == nil {
		return nil, false
	}

	trace, ok := value.(*Trace)
	return trace, ok
}

// NewTrace creates a request trace from the incoming HTTP request.
//
// Why this exists:
// The middleware needs a clean starting point for trace data.
//
// Why request ID is extracted from headers first:
// If a caller already sends a request ID, we should preserve it for correlation.
func NewTrace(r *http.Request) *Trace {
	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = newRequestID()
	}

	return &Trace{
		RequestID: requestID,
		StartedAt: time.Now().UTC(),
		Method:    r.Method,
		Path:      r.URL.Path,
	}
}

// newRequestID creates a random request identifier using the standard library.
//
// Why this exists:
// It lets the gateway create a stable request ID even if the client did not send one.
func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
