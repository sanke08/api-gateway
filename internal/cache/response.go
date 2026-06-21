package cache

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"
)

// CachedResponse stores the exact HTTP response that was returned to the client.
//
// Why this exists:
// The cache must be able to replay the response later without calling the upstream
// again. That means we need the status code, headers, and body.
//
// Why the body is stored as bytes:
// HTTP responses are byte streams. The cache layer should store what was actually
// returned, not a partially interpreted version of it.
// CachedResponse represents a complete HTTP response stored in cache.
//
// Why we store the entire response:
//
// Normally:
//
// Client
//
//	|
//	v
//
// Gateway
//
//	|
//	v
//
// # Upstream Service
//
// Every request requires another upstream call.
//
// With caching:
//
// Client
//
//	|
//	v
//
// Gateway
//
//	|
//	+---- Cache Hit ----> Return CachedResponse
//	|
//	+---- Cache Miss ---> Call Upstream
//
// To replay a response correctly, we must store everything that was originally
// returned to the client:
//
// Example:
//
// Original upstream response:
//
// HTTP/1.1 200 OK
// Content-Type: application/json
// Cache-Control: public
//
//	{
//	  "id": 123,
//	  "name": "sanket"
//	}
//
// To reproduce that response later we need:
//
// - status code (200)
// - headers (Content-Type, Cache-Control)
// - body (JSON bytes)
//
// If we store only the body, we lose important metadata such as content type,
// cookies, cache directives, compression headers, etc.
type CachedResponse struct {
	// StatusCode is the HTTP status that was sent to the client.
	StatusCode int `json:"status_code"`

	// Header stores the response headers as they were sent.
	Header http.Header `json:"header"`

	// Body contains the raw response payload bytes.
	//
	// Example:
	//
	// JSON:
	//
	// {"id":123}
	//
	// HTML:
	//
	// <html>Hello</html>
	//
	// Image:
	//
	// PNG bytes
	//
	// Why []byte:
	//
	// HTTP responses are transmitted as raw bytes.
	//
	// The cache should store exactly what the client originally received rather than
	// trying to interpret or transform the content.
	Body []byte `json:"body"`

	// StoredAt is the time the response was cached.
	StoredAt time.Time `json:"stored_at"`
}

// Marshal converts one cached response into bytes.
//
// Why this exists:
// The cache layer stores raw bytes, so the response must be serialized first.
// Marshal converts a CachedResponse into JSON bytes.
//
// Example:
//
// CachedResponse
//
//	|
//	v
//
// # JSON
//
//	{
//	  "status_code":200,
//	  "header":{...},
//	  "body":"..."
//	}
//
// Why this exists:
//
// Most cache backends store raw bytes.
//
// Before writing into:
//
// Redis
// Memory Cache
// Remote Cache Service
//
// we must serialize the response into a byte representation.
func Marshal(resp CachedResponse) ([]byte, error) {
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	return json.Marshal(resp)
}

// Unmarshal converts cached bytes back into a cached response.
//
// Why this exists:
// A cached entry must be restored into a usable HTTP response shape.
// Unmarshal restores a CachedResponse from cached bytes.
//
// Example:
//
// Cache:
//
//	{
//	  "status_code":200,
//	  "header":{...},
//	  "body":"..."
//	}
//
//	|
//	v
//
// # CachedResponse struct
//
// Why this exists:
//
// Reading from cache returns bytes.
//
// Those bytes must be converted back into a structure that can be used to
// rebuild the HTTP response.
func Unmarshal(data []byte) (CachedResponse, error) {
	var resp CachedResponse
	if len(data) == 0 {
		return resp, fmt.Errorf("cached response is empty")
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return CachedResponse{}, err
	}

	if resp.Header == nil {
		resp.Header = make(http.Header)
	}

	return resp, nil
}

// CloneHeader makes a deep copy of an HTTP header map.
//
// Why this exists:
// Headers are mutable maps. We must copy them so the cached value stays stable.
// CloneHeader creates a deep copy of an HTTP header map.
//
// Why deep copy matters:
//
// Header is a map:
//
// map[string][]string
//
// Maps and slices are reference types.
//
// Example:
//
// original := header
//
// Both variables now point to the same underlying data.
//
// If somebody modifies:
//
// original["Content-Type"]
//
// the cached copy changes too.
//
// Deep copying creates completely independent storage so cached responses
// cannot be modified accidentally after being stored.
func CloneHeader(h http.Header) http.Header {
	if h == nil {
		return make(http.Header)
	}

	out := make(http.Header, len(h))
	for key, values := range h {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}

	return out
}

// Record creates a cached response from a live HTTP response shape.
//
// Why this exists:
// The middleware needs a quick way to capture the response before storing it.
// Record captures a live HTTP response and converts it into a cacheable object.
//
// Example:
//
// Upstream returns:
//
// HTTP/1.1 200 OK
// Content-Type: application/json
//
// {"name":"sanket"}
//
// Middleware records:
//
// statusCode = 200
// header     = response headers
// body       = response bytes
//
// and creates:
//
//	CachedResponse{
//	    StatusCode: 200,
//	    Header: ...,
//	    Body: ...,
//	    StoredAt: now,
//	}
//
// This object can then be serialized and stored in cache.
func Record(statusCode int, header http.Header, body []byte) CachedResponse {
	return CachedResponse{
		StatusCode: statusCode,
		Header:     CloneHeader(header),
		// Why copy the body:
		//
		// body is a slice.
		//
		// If we store:
		//
		// Body: body
		//
		// both variables point to the same underlying memory.
		//
		// Later modifications:
		//
		// body[0] = 'X'
		//
		// could accidentally corrupt the cached response.
		//
		// append([]byte(nil), body...)
		//
		// creates a completely independent copy of the bytes.
		Body:     append([]byte(nil), body...),
		StoredAt: time.Now().UTC(),
	}
}

// ToBytes serializes one cached response.
// ToBytes is a convenience wrapper around Marshal.
//
// Instead of:
//
// data, err := Marshal(resp)
//
// callers can simply write:
//
// data, err := resp.ToBytes()
//
// Both produce the same serialized cache payload.
func (r CachedResponse) ToBytes() ([]byte, error) {
	return Marshal(r)
}

// FromBytes restores one cached response from raw bytes.
// FromBytes is a convenience wrapper around Unmarshal.
//
// Instead of:
//
// resp, err := Unmarshal(data)
//
// callers can write:
//
// resp, err := FromBytes(data)
//
// The result is the same restored CachedResponse structure.
func FromBytes(data []byte) (CachedResponse, error) {
	return Unmarshal(data)
}

// Apply writes the cached response to an HTTP response writer.
//
// Why this exists:
// A cache hit should behave exactly like a normal upstream response.
// Apply writes the cached response back to the client.
//
// Think of this as:
//
// Instead of calling the upstream service again,
// we already have the response stored in cache.
//
//	CachedResponse {
//	    StatusCode: 200
//	    Header: {
//	        Content-Type: application/json
//	    }
//	    Body: {"name":"Sanket"}
//	}
//
// Apply() recreates the original HTTP response.
//
// It writes:
//
// HTTP/1.1 200 OK
// Content-Type: application/json
//
// {"name":"Sanket"}
//
// Why copy headers first:
// Headers must be sent before the response body.
//
// Why write status code next:
// The client must receive the original HTTP status.
//
// Why write body last:
// The body is the actual response payload.
//
// Result:
// Cache hit behaves exactly like an upstream response.
// Client cannot tell whether data came from cache or backend.
func (r CachedResponse) Apply(w http.ResponseWriter) {
	if r.Header == nil {
		r.Header = make(http.Header)
	}

	// Copy all cached headers into the new response.
	//
	// Example:
	//
	// Cached response contains:
	//
	// Header = {
	//     "Content-Type": {"application/json"},
	//     "Cache-Control": {"public", "max-age=60"},
	//     "X-Request-ID": {"abc123"},
	// }
	//
	// Outer loop:
	//
	// key   = "Content-Type"
	// values = {"application/json"}
	//
	// key   = "Cache-Control"
	// values = {"public", "max-age=60"}
	//
	// key   = "X-Request-ID"
	// values = {"abc123"}
	//
	// Inner loop:
	//
	// For "Cache-Control":
	//
	// value = "public"
	// w.Header().Add("Cache-Control", "public")
	//
	// value = "max-age=60"
	// w.Header().Add("Cache-Control", "max-age=60")
	//
	// Result sent to client:
	//
	// Content-Type: application/json
	// Cache-Control: public
	// Cache-Control: max-age=60
	// X-Request-ID: abc123
	//
	// Why Add() instead of Set():
	//
	// Set() replaces existing values.
	//
	// Example:
	//
	// Header:
	// Cache-Control: public
	// Cache-Control: max-age=60
	//
	// If we use Set():
	//
	// Cache-Control: max-age=60
	//
	// First value gets lost.
	//
	// Add() preserves ALL header values exactly as they were
	// when the response was originally cached.
	//
	// This is important because HTTP headers can legally
	// contain multiple values.
	for key, values := range r.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(r.StatusCode)
	_, _ = w.Write(r.Body)
}

// IsValid checks whether this cached response contains enough information to safely replay to a client.
//
// Example valid response:
//
//	CachedResponse{
//	    StatusCode: 200,
//	    Header: http.Header{},
//	}
//
// Result:
// true
//
// Example invalid response:
//
//	CachedResponse{
//	    StatusCode: 0,
//	}
//
// Result:
// false
//
// Why StatusCode > 0:
// HTTP responses must have a valid status.
//
// Why Header != nil:
// Avoid nil-map issues when replaying headers.
//
// Purpose:
// Prevent corrupt cache entries from being used.
func (r CachedResponse) IsValid() bool {
	return r.StatusCode > 0 && r.Header != nil
}

// BuildKey creates a cache key from multiple stable parts.
//
// Why this exists:
// A cache key must vary by tenant and by request shape, otherwise one tenant
// could receive another tenant's data.
// BuildKey creates one cache key from multiple parts.
//
// Example:
//
// BuildKey(
//
//	"tenant-1",
//	"GET",
//	"/products",
//
// )
//
// Result:
//
// tenant-1|GET|/products
//
// Why use multiple parts:
// One string alone is usually not unique enough.
//
// Example:
//
// tenant-1 GET /products
// tenant-2 GET /products
//
// must produce different cache keys.
//
// Why remove empty parts:
//
// BuildKey(
//
//	"tenant-1",
//	"",
//	"/products",
//
// )
//
// becomes:
//
// tenant-1|/products
//
// Purpose:
// Create stable cache identifiers.
func BuildKey(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cleaned = append(cleaned, part)
	}

	return strings.Join(cleaned, "|")
}

// StableRequestPart creates a stable request fragment for a cache key.
//
// Example:
//
//	GET /products?page=1
//
// becomes something like:
//
//	GET|/products|page=1
//
// Why this exists:
// The response cache should treat the method, path, and query as part of the
// response identity.
// StableRequestPart creates the request-specific part of a cache key.
//
// Example request:
//
// GET /products?page=1
//
// Produces:
//
// GET|/products|page=1
//
// Another request:
//
// GET /products?page=2
//
// Produces:
//
// GET|/products|page=2
//
// Different query parameters produce different cache entries.
//
// Why include method:
//
// GET /products
//
// and
//
// POST /products
//
// are completely different operations.
//
// Why include path:
//
// GET /products
//
// and
//
// GET /users
//
// should not share cache.
//
// Why include query:
//
// page=1
//
// and
//
// page=2
//
// usually return different data.
//
// Purpose:
// Create a unique identity for a request.
func StableRequestPart(method, pathValue, rawQuery string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	pathValue = cleanPath(pathValue)
	rawQuery = strings.TrimSpace(rawQuery)

	if rawQuery == "" {
		return BuildKey(method, pathValue)
	}

	return BuildKey(method, pathValue, rawQuery)
}

// CacheableMethod reports whether a method is usually safe to cache.
//
// Why this exists:
// By default, response caching should be conservative.
// CacheableMethod checks whether a request method is safe to cache.
//
// Example:
//
// GET /products
//
// Result:
// true
//
// Example:
//
// POST /products
//
// Result:
// false
//
// Why GET only:
//
// GET should not modify data.
//
// POST usually creates data.
// PUT updates data.
// DELETE removes data.
//
// Caching those can become dangerous.
//
// Purpose:
// Default safe caching behavior.
func CacheableMethod(method string) bool {
	return strings.EqualFold(strings.TrimSpace(method), http.MethodGet)
}

// DefaultTTL gives a safe starter cache lifetime.
//
// Why this exists:
// Some callers want a simple default if the route does not specify a TTL.
// DefaultTTL returns the default cache lifetime.
//
// Example:
//
// cache.Set(key, value, DefaultTTL())
//
// Result:
//
// Cache entry expires after 30 seconds.
//
// Why have a default:
//
// Not every route wants to specify a TTL.
//
// Instead of:
//
// cache.Set(..., 30*time.Second)
//
// everywhere, we centralize it.
//
// Purpose:
// Simple starter cache policy.
func DefaultTTL() time.Duration {
	return 30 * time.Second
}

// cleanPath normalizes a path for cache key construction.
//
// Why this exists:
// It keeps cache keys stable and avoids duplicate keys caused by trailing slashes.
// cleanPath normalizes URL paths before building cache keys.
//
// Example:
//
// "/products/"
//
// becomes
//
// "/products"
//
// Example:
//
// "products"
//
// becomes
//
// "/products"
//
// Example:
//
// ""
//
// becomes
//
// "/"
//
// Why this matters:
//
// Without cleaning:
//
// /products
//
// and
//
// /products/
//
// would create two cache entries
// even though they usually mean the same resource.
//
// Purpose:
// Prevent duplicate cache keys.
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}

	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "" {
		return "/"
	}

	return cleaned
}

// IsCacheableResponse decides whether the response is safe to store.
//
// What it checks:
// - status code is cacheable
// - body is within the configured limit
// - response does not explicitly forbid caching
// - response is not setting cookies
//
// Why this exists:
// Not every successful response should be cached.
// IsCacheableResponse decides whether a response should be stored.
//
// Example:
//
// 200 OK
// Body size = 1KB
// No Cache-Control restrictions
// No cookies
//
// Result:
// true
//
// Example:
//
// 500 Internal Server Error
//
// Result:
// false
//
// Why:
// We usually do not want to cache failures.
//
// Example:
//
// 200 OK
// Body size = 50MB
//
// Result:
// false
//
// Why:
// Very large responses can waste memory.
//
// Example:
//
// Cache-Control: no-store
//
// Result:
// false
//
// Why:
// Upstream explicitly forbids caching.
//
// Example:
//
// Set-Cookie: session=abc
//
// Result:
// false
//
// Why:
// Cookies often contain user-specific data.
// Caching them can leak information between users.
//
// Purpose:
// Only store responses that are safe and useful.
func IsCacheableResponse(resp CachedResponse, maxBodyBytes int) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}
	if maxBodyBytes > 0 && len(resp.Body) > maxBodyBytes {
		return false
	}
	if hasNoStore(resp.Header) {
		return false
	}
	if resp.Header.Get("Set-Cookie") != "" {
		return false
	}
	return true
}

// hasNoStore checks whether the response explicitly forbids caching.
// hasNoStore checks whether the upstream explicitly says
// the response should not be cached.
//
// Example:
//
// Cache-Control: no-store
//
// Result:
// true
//
// Meaning:
// Never save this response anywhere.
//
// Example:
//
// Cache-Control: private
//
// Result:
// true
//
// Meaning:
// Response belongs to a specific user.
// Shared cache should not store it.
//
// Example:
//
// Cache-Control: public, max-age=60
//
// Result:
// false
//
// Meaning:
// Response may be cached.
//
// Purpose:
// Respect upstream cache rules.
func hasNoStore(h http.Header) bool {
	cc := strings.ToLower(h.Get("Cache-Control"))
	if strings.Contains(cc, "no-store") {
		return true
	}
	if strings.Contains(cc, "private") {
		return true
	}
	return false
}
