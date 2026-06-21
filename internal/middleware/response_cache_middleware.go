package middleware

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/cache"
	requesttypes "github.com/sanke08/api_gateway/internal/pkg/types"
)

// Request
//    |
//    v
// Check Cache
//    |
//    |-- HIT --> Return Cached Response
//    |
//    |-- MISS -->
//                 Call Upstream
//                        |
//                        v
//                 Capture Response
//                        |
//                        v
//                 Store In Cache
//                        |
//                        v
//                 Return Response

// ResponseCachePolicy describes how response caching should behave for one route.
//
// Why this exists:
// Response caching must stay explicit. A route should opt in with a clear policy
// instead of receiving hidden caching behavior.
//
// What the fields mean:
//
//   - TTL
//     How long the cached entry stays valid.
//
//   - MaxBodyBytes
//     Maximum body size that may be cached.
//
//   - VaryHeaders
//     Extra request headers that should be part of the cache key.
//
//   - CacheableStatuses
//     Which response status codes may be cached.
//
//   - KeyPrefix
//     Optional namespace prefix for cache keys.
//
// Example:
//
//	ResponseCachePolicy{
//	    TTL:             30 * time.Second,
//	    MaxBodyBytes:    1 << 20,
//	    VaryHeaders:     []string{"Accept"},
//	    CacheableStatuses: []int{200},
//	    KeyPrefix:       "route-cache",
//	}
//
// ResponseCachePolicy defines the caching rules for a route.
//
// Example:
//
// GET /products
//
// Cache for:
//
//	30 seconds
//
// Max response size:
//
//	1 MB
//
// Cache only:
//
//	200 OK responses
//
// Include:
//
//	Accept header
//
// in cache key.
//
// Result:
//
// GET /products
// Accept: application/json
//
// and
//
// GET /products
// Accept: text/html
//
// become different cache entries.
//
// Why this exists:
//
// Different routes need different caching behavior.
//
// Example:
//
// /health
//
//	cache 5 seconds
//
// /products
//
//	cache 1 minute
//
// /user/profile
//
//	no cache
type ResponseCachePolicy struct {
	TTL               time.Duration
	MaxBodyBytes      int
	VaryHeaders       []string
	CacheableStatuses []int
	KeyPrefix         string
}

// ResponseCacheMiddleware is an HTTP middleware that caches safe GET responses.
//
// Why this exists:
// The middleware decides whether a request can be served from cache and whether
// the upstream response should be stored after the request finishes.
// ResponseCacheMiddleware is the actual caching engine.
//
// Responsibilities:
//
// 1. Build cache key
// 2. Check cache
// 3. Return cached response if found
// 4. Capture new response if not found
// 5. Store response for future requests
//
// Think:
//
// Client
//
//	|
//	v
//
// Middleware
//
//	|
//	+--> Cache
//	|
//	+--> Upstream
type ResponseCacheMiddleware struct {
	store  cache.Store
	policy policyState
}

// policyState is the normalized internal form of the cache policy.
//
// Why this exists:
// The public policy may contain zero values or messy input.
// The internal policy should be ready for fast request-time checks.
// policyState is the optimized runtime version.
//
// User configuration:
//
// TTL = 30s
// CacheableStatuses = [200]
//
// becomes:
//
// ttl = 30s
// cacheableStatuses = map[200]struct{}
//
// Why:
//
// Map lookup:
//
// O(1)
//
// instead of scanning slices on every request.
type policyState struct {
	ttl               time.Duration
	maxBodyBytes      int
	varyHeaders       []string
	cacheableMethods  map[string]struct{}
	cacheableStatuses map[int]struct{}
	keyPrefix         string
}

// NewResponseCacheMiddleware creates a cache middleware for HTTP routes.
//
// Why this constructor exists:
// It validates the cache policy once at startup instead of on every request.
//
// Why the store is injected:
// The middleware should not care whether the store is remote, local, or hybrid.
func NewResponseCacheMiddleware(store cache.Store, policy ResponseCachePolicy) (func(http.Handler) http.Handler, error) {
	if store == nil {
		return nil, fmt.Errorf("cache store is required")
	}

	norm, err := normalizePolicy(policy)
	if err != nil {
		return nil, err
	}

	mw := &ResponseCacheMiddleware{
		store:  store,
		policy: norm,
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !mw.shouldCacheMethod(r.Method) {
				// If the request method is not cacheable, just pass it through.
				next.ServeHTTP(w, r)
				return
			}

			key, ok := mw.buildKey(r)
			if !ok {
				// Missing tenant context or other required identity data.
				// We skip caching rather than breaking the request.
				next.ServeHTTP(w, r)
				return
			}

			if data, ok := mw.store.Get(r.Context(), key); ok {
				cached, err := cache.FromBytes(data)
				if err == nil && cached.IsValid() {
					cached.Apply(w)
					return
				}
			}

			// Cache miss: capture the live response.
			recorder := newCaptureWriter(w, mw.policy.maxBodyBytes)
			next.ServeHTTP(recorder, r)

			cached := cache.Record(
				recorder.statusCode,
				recorder.header,
				recorder.body.Bytes(),
			)

			if !mw.shouldCacheStatus(recorder.statusCode) {
				return
			}
			if recorder.tooLarge {
				return
			}
			if !cache.IsCacheableResponse(cached, mw.policy.maxBodyBytes) {
				return
			}

			data, err := cached.ToBytes()
			if err != nil {
				return
			}

			mw.store.Set(r.Context(), key, data, mw.policy.ttl)
		})
	}, nil
}

// shouldCacheMethod checks whether the request method is allowed to be cached.
func (m *ResponseCacheMiddleware) shouldCacheMethod(method string) bool {
	if m == nil {
		return false
	}

	_, ok := m.policy.cacheableMethods[strings.ToUpper(strings.TrimSpace(method))]
	return ok
}

// shouldCacheStatus checks whether the response status code is cacheable.
func (m *ResponseCacheMiddleware) shouldCacheStatus(statusCode int) bool {
	if m == nil {
		return false
	}

	_, ok := m.policy.cacheableStatuses[statusCode]
	return ok
}

// buildKey creates a tenant-aware cache key.
//
// Why this matters:
// A cache key must not allow one tenant to receive another tenant's data.
func (m *ResponseCacheMiddleware) buildKey(r *http.Request) (string, bool) {
	tenant, ok := requesttypes.ResolvedTenantFromContext(r.Context())
	if !ok {
		return "", false
	}

	parts := make([]string, 0, 8)
	if m.policy.keyPrefix != "" {
		parts = append(parts, m.policy.keyPrefix)
	}

	parts = append(parts, "tenant:"+tenant.Id)
	parts = append(parts, cache.StableRequestPart(r.Method, r.URL.Path, r.URL.RawQuery))

	for _, name := range m.policy.varyHeaders {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		value := strings.TrimSpace(r.Header.Get(name))
		parts = append(parts, strings.ToLower(name)+"="+value)
	}

	return cache.BuildKey(parts...), true
}

// normalizePolicy validates the public policy and converts it into internal state.
func normalizePolicy(policy ResponseCachePolicy) (policyState, error) {
	ttl := policy.TTL
	if ttl <= 0 {
		ttl = cache.DefaultTTL()
	}
	if ttl <= 0 {
		return policyState{}, fmt.Errorf("cache ttl must be greater than zero")
	}

	maxBodyBytes := policy.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 1 * 1024 * 1024 // 1 MiB starter limit
	}

	methods := map[string]struct{}{
		http.MethodGet: {},
	}

	statuses := policy.CacheableStatuses
	if len(statuses) == 0 {
		statuses = []int{http.StatusOK}
	}

	statusSet := make(map[int]struct{}, len(statuses))
	for _, code := range statuses {
		if code < 100 || code > 599 {
			return policyState{}, fmt.Errorf("invalid cacheable status code: %d", code)
		}
		statusSet[code] = struct{}{}
	}

	vary := make([]string, 0, len(policy.VaryHeaders))
	for _, name := range policy.VaryHeaders {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		vary = append(vary, name)
	}

	return policyState{
		ttl:               ttl,
		maxBodyBytes:      maxBodyBytes,
		varyHeaders:       vary,
		cacheableMethods:  methods,
		cacheableStatuses: statusSet,
		keyPrefix:         strings.TrimSpace(policy.KeyPrefix),
	}, nil
}

// captureWriter records the upstream response while still sending it to the client.
//
// Why this exists:
// Response caching needs the response body, headers, and status code.
// We must capture them without breaking normal HTTP behavior.
// captureWriter records the upstream response while still sending it to the client.
//
// Example:
//
// Handler returns:
//
//	Status: 200
//	Header: Content-Type: application/json
//	Body:   {"id":1}
//
// Normally that response would immediately go to the client and disappear.
//
// With captureWriter:
//
// 1. Response still goes to the client.
// 2. Status is stored.
// 3. Headers are stored.
// 4. Body is stored.
//
// Later:
//
//	cache.Record(...)
//
// can save the entire response into cache.
type captureWriter struct {
	// The real ResponseWriter provided by net/http.
	//
	// Eventually all responses must go here so the client receives them.
	underlying http.ResponseWriter

	// Local copy of response headers.
	//
	// Example:
	//
	//	Content-Type: application/json
	//	Cache-Control: max-age=60
	//
	// We store them so they can later be written into cache.
	header http.Header

	// Final HTTP status code.
	//
	// Example:
	//
	//	200
	//	404
	//	500
	//
	// Needed because cache replay must return the exact same status.
	statusCode int

	// Captured response body.
	//
	// Example:
	//
	//	{"id":123}
	//
	// Stored here while simultaneously being sent to the client.
	body bytes.Buffer

	// Maximum response size allowed for caching.
	//
	// Example:
	//
	//	maxBodyBytes = 1MB
	//
	// If response becomes larger than this,
	// we stop capturing it.
	maxBodyBytes int

	// Indicates response exceeded cache size limit.
	//
	// Example:
	//
	//	Response = 10MB
	//	Limit    = 1MB
	//
	// Result:
	//
	//	tooLarge = true
	//
	// Response still reaches the client,
	// but it will not be cached.
	tooLarge bool

	// Tracks whether WriteHeader was already called.
	//
	// HTTP allows WriteHeader only once.
	wroteHeader bool
}

// newCaptureWriter creates a response writer that captures the response.
//
// Why this exists:
// It lets the middleware observe the full response after the handler finishes.
func newCaptureWriter(w http.ResponseWriter, maxBodyBytes int) *captureWriter {
	return &captureWriter{
		underlying:   w,
		header:       make(http.Header),
		statusCode:   http.StatusOK,
		maxBodyBytes: maxBodyBytes,
	}
}

// Header returns the local header map.
//
// Why this matters:
// The handler writes headers here, and we copy them to the real writer when
// WriteHeader is called.
// Header returns the local header map.
//
// Example:
//
//	capture.Header().Set(
//	    "Content-Type",
//	    "application/json",
//	)
//
// The header is NOT immediately sent.
//
// It is stored locally first.
//
// Later:
//
//	WriteHeader(...)
func (w *captureWriter) Header() http.Header {
	return w.header
}

// WriteHeader sends the headers and status code to the real writer and also
// stores the status locally for cache capture.
// WriteHeader sends status and headers to the real client.
//
// Example:
//
//	capture.WriteHeader(200)
//
// What happens:
//
// 1. Save status locally.
// 2. Copy headers to real writer.
// 3. Send status to client.
//
// This lets cache middleware remember the response while preserving
// normal HTTP behavior.
func (w *captureWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}

	w.wroteHeader = true
	w.statusCode = statusCode

	dst := w.underlying.Header()
	for key, values := range w.header {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}

	w.underlying.WriteHeader(statusCode)
}

// Write writes the response body to the client and captures it for caching.
//
// Why this exists:
// We need to keep normal HTTP behavior while also collecting the response body.
// Write sends body data to the client AND stores a copy.
//
// Example:
//
//	capture.Write(
//	    []byte(`{"user":"sanket"}`),
//	)
//
// What happens:
//
// 1. Body stored in memory.
// 2. Same bytes sent to client.
//
// Client receives response normally.
// Cache middleware gets a copy.
func (w *captureWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	if !w.tooLarge && w.maxBodyBytes > 0 {
		if w.body.Len()+len(p) > w.maxBodyBytes {
			w.tooLarge = true
		}
		if !w.tooLarge {
			_, _ = w.body.Write(p)
		}
	}

	return w.underlying.Write(p)
}

// Flush passes through to the underlying writer when supported.
//
// Why this exists:
// Reverse proxy and streaming handlers may rely on flushing.
// Flush immediately pushes buffered data to the client.
//
// Example:
//
// Server Sent Events
// Streaming APIs
// Reverse Proxy
//
// depend on Flush().
func (w *captureWriter) Flush() {
	if flusher, ok := w.underlying.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack passes through to the underlying writer when supported.
//
// Why this exists:
// Some handlers may upgrade the connection. We should not break that behavior.
// Hijack gives direct access to the TCP connection.
//
// Example:
//
// # WebSocket upgrade
//
// # HTTP  ->   WebSocket
//
// requires Hijack().
func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.underlying.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking not supported")
	}
	return hijacker.Hijack()
}

// Push passes through to the underlying writer when supported.
//
// Why this exists:
// HTTP/2 server push is uncommon, but the wrapper should not block it.
// Push supports HTTP/2 server push.
//
// Example:
//
// Browser requests:
//
//	/index.html
//
// Server may push:
//
//	/style.css
//	/app.js
//
// automatically.
func (w *captureWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.underlying.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

// Unwrap returns the underlying response writer.
//
// Why this exists:
// Some middleware and handlers may want to reach the original writer.
// Unwrap returns the original ResponseWriter.
//
// Example:
//
// capture
//
//	|
//	v
//
// real ResponseWriter
//
// Some middleware may need direct access to the real writer.
func (w *captureWriter) Unwrap() http.ResponseWriter {
	return w.underlying
}

// // _ ensures the wrapper keeps the optional interfaces when available.
// var (
// 	_ http.Flusher  = (*captureWriter)(nil)
// 	_ http.Hijacker = (*captureWriter)(nil)
// 	_ http.Pusher   = (*captureWriter)(nil)
// )
