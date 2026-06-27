package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/observability"
	requesttypes "github.com/sanke08/api_gateway/internal/pkg/types"
	"github.com/sanke08/api_gateway/internal/services"
)

// UsageMiddleware captures one completed HTTP request and converts it into
// a Usage record that can later be written to the database.
//
// Think of this middleware as the "accountant" of the gateway.
//
// Every request eventually passes through here:
//
//	Client
//	  │
//	  ▼
//	Gateway
//	  │
//	  ▼
//	Auth → Rate Limit → Cache → Proxy → Upstream
//	  │
//	  ▼
//	Usage Middleware  ← We are here
//
// By the time execution reaches this middleware again (after next.ServeHTTP()
// returns), the request has completely finished.
//
// At that point we know:
//
//	✓ Which tenant made the request
//	✓ Which user/API key made it
//	✓ Final HTTP status (200,404,500...)
//	✓ Bytes returned
//	✓ Total request duration
//	✓ Whether cache was used
//	✓ Whether retry happened
//
// All of this information becomes one Usage record.
//
// Why this middleware exists:
//
// We deliberately separate business logic from analytics.
//
// Handlers should only focus on serving requests.
//
// They should NOT contain code like:
//
//	log usage
//	save billing info
//	save analytics
//	record bytes
//
// Instead, this middleware automatically performs those tasks after every
// request finishes.
//
// Important:
//
// This middleware NEVER writes directly to the database.
//
// It only builds a Usage struct and hands it to AsyncUsageTracker.
//
//	Request finished
//	      │
//	      ▼
//	build Usage record
//	      │
//	      ▼
//	enqueue()
//	      │
//	      ▼
//	background worker
//	      │
//	      ▼
//	Database
//
// This keeps request latency low because the client never waits for the database.
type UsageMiddleware struct {
	tracker services.UsageTracker
}

// NewUsageMiddleware creates the usage middleware wrapper.
func NewUsageMiddleware(tracker services.UsageTracker) func(http.Handler) http.Handler {
	mw := &UsageMiddleware{tracker: tracker}

	return func(next http.Handler) http.Handler {
		return mw.wrap(next)
	}
}

// wrap creates the real HTTP middleware.
//
// Remember:
//
// Middleware executes TWICE for every request.
//
// Before handler:
//
//	Client
//	   │
//	   ▼
//	wrap()
//	   │
//	   ▼
//	next.ServeHTTP()
//
// Handler executes here.
//
// After handler:
//
//	next.ServeHTTP() returns
//	   │
//	   ▼
//	wrap() continues executing
//
// This second half is exactly where usage recording happens.
//
// Why?
//
// Because only AFTER the handler finishes do we know:
//
//   - final status code
//   - bytes written
//   - request duration
//   - cache result
//   - retry count
//
// If we tried recording before next.ServeHTTP(), none of those values would
// exist yet.
func (m *UsageMiddleware) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.tracker == nil {
			next.ServeHTTP(w, r)
			return
		}

		// We capture the response so we can record final status and bytes written.
		recorder := newUsageCaptureWriter(w)

		start := time.Now().UTC()
		next.ServeHTTP(recorder, r)
		finish := time.Now().UTC()

		trace, _ := observability.TraceFromContext(r.Context())

		tenantID := ""
		userID := ""
		membershipID := ""
		apiKeyID := ""
		requestID := ""
		cached := false
		retried := false

		if trace != nil {
			requestID = trace.RequestID
			tenantID = trace.TenantID
			userID = trace.UserID
			membershipID = trace.MembershipID
			apiKeyID = trace.APIKeyID
			cached = trace.Cached
			retried = trace.Retried
		}

		// If trace did not already carry tenant identity, fall back to resolved context.
		if tenantID == "" {
			if tenant, ok := requesttypes.ResolvedTenantFromContext(r.Context()); ok {
				tenantID = tenant.Id
			}
		}

		// Build optional IDs as pointers only when present.
		var apiKeyPtr *string
		var userPtr *string
		var membershipPtr *string

		if apiKeyID != "" {
			v := apiKeyID
			apiKeyPtr = &v
		}
		if userID != "" {
			v := userID
			userPtr = &v
		}
		if membershipID != "" {
			v := membershipID
			membershipPtr = &v
		}

		// If we still do not have a tenant, there is nothing safe to store.
		if tenantID == "" {
			return
		}

		duration := finish.Sub(start)
		if trace != nil && !trace.StartedAt.IsZero() && trace.EndedAt.IsZero() {
			// If a separate observability middleware did not finish the trace,
			// we still use our local duration measurement.
			duration = finish.Sub(trace.StartedAt)
		}

		record := models.Usage{
			RequestID:    requestID,
			TenantID:     tenantID,
			APIKeyID:     apiKeyPtr,
			UserID:       userPtr,
			MembershipID: membershipPtr,
			Path:         r.URL.Path,
			Method:       r.Method,
			StatusCode:   recorder.statusCode,
			DurationMS:   duration.Milliseconds(),
			Timestamp:    finish,
			BytesIn:      requestBytesIn(r),
			BytesOut:     recorder.bytesWritten,
			Cached:       cached,
			Retried:      retried,
		}

		_ = m.tracker.Enqueue(r.Context(), record)
	})
}

// requestBytesIn estimates the request body size.
//
// Why this exists:
// Billing preparation needs a usable byte count. Content-Length is the best
// cheap signal when it is available.
func requestBytesIn(r *http.Request) int64 {
	if r == nil {
		return 0
	}
	if r.ContentLength > 0 {
		return r.ContentLength
	}
	return 0
}

// usageCaptureWriter captures status code and body bytes.
//
// Why this exists:
// The usage record needs the final response shape, not just the handler's intent.
type usageCaptureWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	wroteHeader  bool
}

// newUsageCaptureWriter creates the wrapper.
func newUsageCaptureWriter(w http.ResponseWriter) *usageCaptureWriter {
	return &usageCaptureWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

// Header returns the underlying headers.
func (w *usageCaptureWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

// WriteHeader stores the status code and forwards it.
func (w *usageCaptureWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write stores the number of bytes written and forwards them.
func (w *usageCaptureWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += int64(n)
	return n, err
}

// Flush forwards flush behavior.
func (w *usageCaptureWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack forwards hijacking if supported.
func (w *usageCaptureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking not supported")
	}
	return hijacker.Hijack()
}

// Push forwards HTTP/2 push if supported.
func (w *usageCaptureWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

var (
	_ http.Flusher  = (*usageCaptureWriter)(nil)
	_ http.Hijacker = (*usageCaptureWriter)(nil)
	_ http.Pusher   = (*usageCaptureWriter)(nil)
)
