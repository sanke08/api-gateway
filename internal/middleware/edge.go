package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"
)

// EdgePolicy groups every browser-facing HTTP policy into a single configuration.
//
// Why does this struct exist?
//
// An API gateway sits at the "edge" of the system. Every browser request reaches
// the gateway before it reaches authentication, tenant resolution, routing,
// proxying, or any business logic.
//
// Because of that, the gateway must make some decisions immediately:
//
// • Which browser origins are allowed to access this API?
// • Should cookies or credentials be accepted?
// • Which security headers should be attached to every response?
//
// Instead of scattering these settings throughout the middleware, they are grouped
// into one configuration object.
//
// This keeps startup configuration clean and makes the gateway behavior easy to
// understand.
//
// Think of it like this:
//
// Browser
//
//	│
//	│ Request
//	▼
//
// +-----------------------+
// |    API Gateway        |
// |                       |
// |  Edge Policy          |
// |   ├── CORS Rules      |
// |   └── Security Headers|
// +-----------------------+
//
//	      │
//	      ▼
//	Authentication
//	      │
//	      ▼
//	  Proxy Service
//
// The EdgePolicy is the very first browser-related policy applied.
//
// Why combine CORS and Security together?
//
// Both of these concerns belong to the HTTP edge.
//
// They are not business logic.
//
// They are not authentication.
//
// They are not tenant specific.
//
// They simply define:
//
// "How should browsers interact with this gateway?"
//
// Having one EdgePolicy makes configuration much easier.
//
// Instead of:
//
//	config.CORS...
//	config.Security...
//	config.MoreSecurity...
//
// We simply have:
//
//	config.Edge.CORS
//	config.Edge.Security
//
// which is much cleaner.
//
// # Real Example
//
// Imagine your frontend is:
//
//	https://app.company.com
//
// and your API gateway is:
//
//	https://api.company.com
//
// EdgePolicy decides:
//
// ✓ Can app.company.com call the API?
// ✓ Can browsers send cookies?
// ✓ Should X-Frame-Options be DENY?
// ✓ Should browsers enforce HTTPS?
// ✓ Should Content-Security-Policy be added?
//
// All of these are browser edge concerns.
type EdgePolicy struct {

	// CORS defines how browsers are allowed to perform cross-origin requests.
	//

	// What is CORS?

	//
	// CORS stands for:
	//
	//     Cross-Origin Resource Sharing
	//
	// Browsers do NOT allow one website to freely call another website.
	//
	// Example:
	//
	// Frontend:
	//
	//	https://app.example.com
	//
	// API:
	//
	//	https://api.example.com
	//
	// Since these are different origins, the browser first asks:
	//
	// "Is this origin allowed?"
	//
	// The gateway answers that question using this configuration.
	//
	// Without proper CORS configuration, browsers block requests before your
	// application code even runs.
	CORS CORSPolicy

	// Security defines browser security headers that should be attached to
	// every HTTP response.
	//

	// Why are security headers important?

	//
	// Browsers support many security mechanisms that are enabled through HTTP
	// response headers.
	//
	// Examples:
	//
	// • Prevent clickjacking
	// • Disable MIME sniffing
	// • Enforce HTTPS
	// • Restrict iframe embedding
	// • Restrict browser permissions
	//
	// Instead of every handler remembering to add these headers, the middleware
	// automatically adds them using this configuration.
	Security SecurityHeadersPolicy
}

// CORSPolicy describes every browser cross-origin rule.
//
// Why does this struct exist?
//
// Browsers enforce the Same-Origin Policy.
//
// That means JavaScript running on:
//
//	https://app.example.com
//
// cannot automatically call:
//
//	https://api.example.com
//
// unless the server explicitly allows it.
//
// The browser first performs a CORS check.
//
// If the gateway says:
//
//	✓ Allowed
//
// the browser continues.
//
// Otherwise:
//
//	Request blocked.
//
// Notice:
//
// Your backend never decides this.
//
// The browser blocks it before your business logic even executes.
//
// That's why CORS belongs at the gateway.
//
// # Real Example
//
// React App
//
//	|
//	|
//	| GET /users
//	|
//	▼
//
// API Gateway
//
//	|
//	| Access-Control-Allow-Origin:
//	| https://app.example.com
//	|
//	▼
//
// Browser:
//
// ✓ Allowed
//
// If the gateway instead returns:
//
// Access-Control-Allow-Origin:
// https://evil.com
//
// Browser:
//
// ❌ Blocked
type CORSPolicy struct {

	// AllowAllOrigins allows every browser origin.
	//
	// Example:
	//
	//	https://abc.com
	//	https://xyz.com
	//	http://localhost:3000
	//
	// Every origin is accepted.
	//
	// This results in:
	//
	//	Access-Control-Allow-Origin: *
	//

	// When should this be used?

	//
	// Mostly for:
	//
	// • Public APIs
	// • Open REST APIs
	// • Documentation APIs
	//

	// When should it NOT be used?

	//
	// If browser credentials are involved:
	//
	// Cookies
	// Authorization Sessions
	// Login
	//
	// Browsers completely reject:
	//
	//	Allow-Origin: *
	//	Allow-Credentials: true
	//
	// Therefore this configuration is intentionally prevented later during
	// normalization.
	AllowAllOrigins bool

	// AllowedOrigins contains the exact browser origins that may access
	// this gateway.
	//
	// Example:
	//
	//	https://app.example.com
	//	https://admin.example.com
	//
	// During request processing:
	//
	// Browser sends:
	//
	//	Origin: https://app.example.com
	//
	// Gateway checks whether that origin exists in this list.
	//
	// If found:
	//
	//	Request allowed.
	//
	// Otherwise:
	//
	//	Request rejected.
	AllowedOrigins []string

	// AllowedMethods defines which HTTP methods browsers may use after
	// a successful preflight.
	//
	// Example:
	//
	//	GET
	//	POST
	//	PUT
	//	PATCH
	//	DELETE
	//
	// If a browser asks:
	//
	// "May I send DELETE?"
	//
	// this list decides the answer.
	AllowedMethods []string

	// AllowedHeaders defines which request headers browsers are allowed
	// to include.
	//
	// Example:
	//
	// Authorization
	// Content-Type
	// X-Request-ID
	// X-Tenant-ID
	//
	// During preflight the browser literally asks:
	//
	// "May I send Authorization?"
	//
	// The middleware compares that request against this list.
	AllowedHeaders []string

	// ExposedHeaders lists response headers that JavaScript is allowed
	// to read.
	//
	// Browsers intentionally hide many response headers from JavaScript.
	//
	// If the gateway returns:
	//
	//	X-Request-ID
	//
	// JavaScript cannot access it unless this header is explicitly exposed.
	//
	// This field controls that behavior.
	ExposedHeaders []string

	// AllowCredentials allows browsers to send cookies or authentication
	// credentials.
	//
	// Examples:
	//
	// Cookies
	// Session IDs
	// Browser Authentication
	//
	// Since credentials are sensitive, wildcard origins are prohibited.
	//
	// Browser specification enforces this automatically.
	AllowCredentials bool

	// MaxAge controls how long browsers cache successful preflight responses.
	//
	// Example:
	//
	// Without caching:
	//
	// Every POST request:
	//
	// OPTIONS
	// POST
	//
	// OPTIONS
	// POST
	//
	// OPTIONS
	// POST
	//
	// With MaxAge:
	//
	// OPTIONS
	// POST
	// POST
	// POST
	// POST
	//
	// This significantly reduces unnecessary network traffic.
	MaxAge time.Duration
}

// / SecurityHeadersPolicy defines the browser security headers that the gateway
// should automatically attach to HTTP responses.
//
// Why does this struct exist?
//
// Modern browsers provide many built-in security features, but almost all of
// them are disabled unless the server explicitly enables them through HTTP
// response headers.
//
// Instead of every handler remembering to set:
//
//	X-Frame-Options
//	Strict-Transport-Security
//	Content-Security-Policy
//	Referrer-Policy
//
// the gateway sets them once for every response.
//
// Since every request passes through the gateway, this becomes the perfect
// place to enforce consistent security across every backend service.
//
// Think of the flow like this:
//
// Browser
//
//	│
//	│ GET /users
//	▼
//
// +-------------------------+
// |      API Gateway        |
// |                         |
// | Adds Security Headers   |
// +-------------------------+
//
//	│
//	▼
//
// # Backend Service
//
// The backend only returns business data.
//
// The gateway is responsible for browser protection.
//
// Why is this better?
//
// Imagine your system has:
//
// User Service
// Order Service
// Payment Service
// Notification Service
//
// Without gateway-level security:
//
// Every service must remember to add the exact same headers.
//
// That's repetitive and error-prone.
//
// With a gateway:
//
// Every response automatically gets the same protection.
type SecurityHeadersPolicy struct {

	// EnableHSTS enables the Strict-Transport-Security (HSTS) header.
	//
	// -------------------------------------------------------------------------
	// What is HSTS?
	// -------------------------------------------------------------------------
	//
	// HSTS tells browsers:
	//
	// "Once you've successfully visited this website using HTTPS,
	// never use HTTP again."
	//
	// Example:
	//
	// First visit:
	//
	//	https://api.example.com
	//
	// Browser receives:
	//
	//	Strict-Transport-Security:
	//	max-age=31536000
	//
	// From that moment on,
	// even if the user types:
	//
	//	http://api.example.com
	//
	// the browser automatically upgrades it to:
	//
	//	https://api.example.com
	//
	// without contacting the server first.
	//
	// This prevents downgrade attacks where attackers try to force HTTP.
	//
	// -------------------------------------------------------------------------
	// Important
	// -------------------------------------------------------------------------
	//
	// HSTS should ONLY be sent over HTTPS.
	//
	// Sending it over plain HTTP has no meaning because attackers could simply
	// remove the header.
	EnableHSTS bool

	// HSTSMaxAge defines how long browsers should remember the HSTS rule.
	//
	// Unit:
	//
	// Seconds
	//
	// Example:
	//
	//	31536000
	//
	// means:
	//
	//	365 days
	//
	// During that time the browser refuses to use HTTP for this site.
	HSTSMaxAge int

	// IncludeSubdomains tells browsers to apply HSTS to every subdomain.
	//
	// Example:
	//
	//	api.example.com
	//	auth.example.com
	//	admin.example.com
	//
	// Instead of protecting only:
	//
	//	api.example.com
	//
	// browsers also protect:
	//
	//	*.example.com
	//
	// This closes security gaps where subdomains accidentally remain on HTTP.
	IncludeSubdomains bool

	// Preload enables the "preload" directive.
	//
	// Some browsers maintain a built-in list of websites that should always use
	// HTTPS, even before the user's first visit.
	//
	// Adding the "preload" directive signals that the domain intends to join
	// that list.
	//
	// Note:
	//
	// Simply enabling this flag does NOT automatically preload your domain.
	// The domain must also be submitted to browser vendors.
	Preload bool

	// FrameOptions controls the X-Frame-Options header.
	//
	// -------------------------------------------------------------------------
	// Why is this important?
	// -------------------------------------------------------------------------
	//
	// Without protection,
	// another website can embed your application inside an invisible iframe.
	//
	// That enables attacks such as clickjacking.
	//
	// Example:
	//
	// Evil Website
	//
	// +--------------------------------------+
	// | "Click here to win $1000!"           |
	// |                                      |
	// | (Actually clicking your API portal)  |
	// +--------------------------------------+
	//
	// The user thinks they clicked one thing,
	// but actually clicked another.
	//
	// Common values:
	//
	//	DENY
	//	SAMEORIGIN
	//
	// Most API gateways should simply use:
	//
	//	DENY
	FrameOptions string

	// ReferrerPolicy controls the Referrer-Policy response header.
	//
	// -------------------------------------------------------------------------
	// What is a Referrer?
	// -------------------------------------------------------------------------
	//
	// Browsers often send the previous page URL when navigating.
	//
	// Example:
	//
	//	User visits:
	//
	//	https://shop.example.com/account/orders
	//
	// Then clicks:
	//
	//	https://another-site.com
	//
	// Browser may send:
	//
	//	Referer:
	//	https://shop.example.com/account/orders
	//
	// Sometimes that information should NOT be leaked.
	//
	// Referrer-Policy controls how much information browsers share.
	//
	// Common values:
	//
	//	no-referrer
	//	same-origin
	//	strict-origin-when-cross-origin
	ReferrerPolicy string

	// ContentTypeNosniff enables the X-Content-Type-Options header.
	//
	// Browser behavior:
	//
	// If a server accidentally labels JavaScript as plain text,
	// browsers sometimes "guess" the real type.
	//
	// That guessing is called MIME sniffing.
	//
	// Attackers can abuse it.
	//
	// Setting:
	//
	//	X-Content-Type-Options: nosniff
	//
	// tells browsers:
	//
	// "Do NOT guess.
	// Trust only the declared Content-Type."
	ContentTypeNosniff bool

	// ContentSecurityPolicy defines the Content-Security-Policy header.
	//
	// -------------------------------------------------------------------------
	// What does CSP do?
	// -------------------------------------------------------------------------
	//
	// CSP tells browsers exactly what content they are allowed to load.
	//
	// Example:
	//
	//	default-src 'self'
	//
	// means:
	//
	// Only resources from this website may execute.
	//
	// This greatly reduces Cross-Site Scripting (XSS) attacks.
	//
	// Since this gateway mostly serves APIs rather than HTML pages,
	// this field is optional.
	ContentSecurityPolicy string

	// PermissionsPolicy controls browser permissions available to pages.
	//
	// Examples:
	//
	//	camera=()
	//	microphone=()
	//	geolocation=()
	//
	// This prevents websites from requesting capabilities they should never use.
	PermissionsPolicy string

	// CrossOriginOpenerPolicy controls process isolation between windows.
	//
	// It helps protect against certain cross-origin attacks and improves browser
	// isolation.
	//
	// Common value:
	//
	//	same-origin
	CrossOriginOpenerPolicy string

	// CrossOriginResourcePolicy controls which external websites are allowed
	// to load resources served by this gateway.
	//
	// Common values:
	//
	//	same-origin
	//	same-site
	//	cross-origin
	//
	// This provides another layer of browser-side resource protection.
	CrossOriginResourcePolicy string
}

// EdgeMiddleware is the gateway edge middleware.
//
// What it does:
// - applies security headers to every response
// - handles CORS on normal requests
// - short-circuits CORS preflight requests
//
// Why this exists:
// Browser-facing edge behavior belongs before auth, tenant resolution,
// proxying, and business logic. Preflight must not be forced through login rules.
type EdgeMiddleware struct {
	policy corsSecurityState
}

// corsSecurityState is the normalized runtime version of EdgePolicy.
//
// Why this exists:
// Public config may contain zero values or messy input.
// The runtime state should be validated and ready to use.
type corsSecurityState struct {
	cors     corsState
	security securityState
}

// corsState is the normalized runtime version of CORSPolicy.
type corsState struct {
	allowAllOrigins  bool
	allowedOrigins   map[string]struct{}
	allowedMethods   []string
	allowedHeaders   []string
	exposedHeaders   []string
	allowCredentials bool
	maxAgeSeconds    int
}

// securityState is the normalized runtime version of SecurityHeadersPolicy.
type securityState struct {
	enableHSTS                bool
	hstsMaxAge                int
	includeSubdomains         bool
	preload                   bool
	frameOptions              string
	referrerPolicy            string
	contentTypeNosniff        bool
	contentSecurityPolicy     string
	permissionsPolicy         string
	crossOriginOpenerPolicy   string
	crossOriginResourcePolicy string
}

// NewEdgeMiddleware creates the browser edge middleware.
//
// Why this constructor exists:
// It validates the policy once at startup and returns a middleware factory.
// That keeps request-time work small and predictable.
func NewEdgeMiddleware(policy EdgePolicy) (func(http.Handler) http.Handler, error) {
	normalized, err := normalizeEdgePolicy(policy)
	if err != nil {
		return nil, err
	}

	mw := &EdgeMiddleware{
		policy: normalized,
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mw.applySecurityHeaders(w, r)

			origin := strings.TrimSpace(r.Header.Get("Origin"))

			// Non-browser callers often do not send Origin.
			// In that case we do not apply CORS rules.
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed, allowedOrigin := mw.isOriginAllowed(origin)
			if !allowed {
				writeEdgeError(w, http.StatusForbidden, "origin_not_allowed", "origin is not allowed")
				return
			}

			// CORS headers must be present on both preflight and actual cross-origin requests.
			mw.applyCORSHeaders(w, allowedOrigin, r, false)

			// Preflight requests never need to reach business logic.
			if isPreflightRequest(r) {
				if !mw.isMethodAllowed(r.Header.Get("Access-Control-Request-Method")) {
					writeEdgeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "requested method is not allowed")
					return
				}

				if !mw.areRequestedHeadersAllowed(r.Header.Get("Access-Control-Request-Headers")) {
					writeEdgeError(w, http.StatusForbidden, "headers_not_allowed", "requested headers are not allowed")
					return
				}

				mw.applyCORSHeaders(w, allowedOrigin, r, true)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}

// applySecurityHeaders writes standard hardening headers.
//
// Why this exists:
// These headers make the gateway safer at the HTTP edge without changing
// application logic.
//
// Why this runs on every response:
// The caller should always receive the hardening headers when applicable.
func (m *EdgeMiddleware) applySecurityHeaders(w http.ResponseWriter, r *http.Request) {
	if m == nil {
		return
	}

	h := w.Header()

	if m.policy.security.contentTypeNosniff {
		h.Set("X-Content-Type-Options", "nosniff")
	}

	if m.policy.security.frameOptions != "" {
		h.Set("X-Frame-Options", m.policy.security.frameOptions)
	} else {
		// Conservative default for an API gateway.
		h.Set("X-Frame-Options", "DENY")
	}

	if m.policy.security.referrerPolicy != "" {
		h.Set("Referrer-Policy", m.policy.security.referrerPolicy)
	} else {
		h.Set("Referrer-Policy", "no-referrer")
	}

	if csp := strings.TrimSpace(m.policy.security.contentSecurityPolicy); csp != "" {
		h.Set("Content-Security-Policy", csp)
	}

	if pp := strings.TrimSpace(m.policy.security.permissionsPolicy); pp != "" {
		h.Set("Permissions-Policy", pp)
	}

	if coop := strings.TrimSpace(m.policy.security.crossOriginOpenerPolicy); coop != "" {
		h.Set("Cross-Origin-Opener-Policy", coop)
	}

	if corp := strings.TrimSpace(m.policy.security.crossOriginResourcePolicy); corp != "" {
		h.Set("Cross-Origin-Resource-Policy", corp)
	}

	// HSTS should only be sent when the request is actually secure.
	// This avoids telling browsers to enforce HTTPS on an insecure origin.
	if m.policy.security.enableHSTS && isSecureRequest(r) {
		header := buildHSTSHeader(m.policy.security)
		if header != "" {
			h.Set("Strict-Transport-Security", header)
		}
	}
}

// applyCORSHeaders writes the CORS response headers.
//
// Why this exists:
// CORS headers must be consistent between preflight and normal cross-origin requests.
func (m *EdgeMiddleware) applyCORSHeaders(
	w http.ResponseWriter,
	allowedOrigin string,
	r *http.Request,
	preflight bool,
) {
	if m == nil {
		return
	}

	h := w.Header()
	h.Add("Vary", "Origin")

	if allowedOrigin != "" {
		h.Set("Access-Control-Allow-Origin", allowedOrigin)
	}

	if m.policy.cors.allowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}

	if len(m.policy.cors.exposedHeaders) > 0 && !preflight {
		h.Set("Access-Control-Expose-Headers", strings.Join(m.policy.cors.exposedHeaders, ", "))
	}

	if preflight {
		h.Add("Vary", "Access-Control-Request-Method")
		h.Add("Vary", "Access-Control-Request-Headers")

		if len(m.policy.cors.allowedMethods) > 0 {
			h.Set("Access-Control-Allow-Methods", strings.Join(m.policy.cors.allowedMethods, ", "))
		}

		if len(m.policy.cors.allowedHeaders) > 0 {
			h.Set("Access-Control-Allow-Headers", strings.Join(m.policy.cors.allowedHeaders, ", "))
		}

		if m.policy.cors.maxAgeSeconds > 0 {
			h.Set("Access-Control-Max-Age", fmt.Sprintf("%d", m.policy.cors.maxAgeSeconds))
		}
	}
}

// isOriginAllowed checks whether the incoming origin is permitted.
//
// Why this exists:
// Browsers send an Origin header that must be validated against the allow-list.
func (m *EdgeMiddleware) isOriginAllowed(origin string) (bool, string) {
	origin = normalizeOrigin(origin)
	if origin == "" {
		return false, ""
	}

	if m.policy.cors.allowAllOrigins && !m.policy.cors.allowCredentials {
		return true, "*"
	}

	if _, ok := m.policy.cors.allowedOrigins[origin]; ok {
		return true, origin
	}

	return false, ""
}

// isMethodAllowed checks whether the requested preflight method is permitted.
func (m *EdgeMiddleware) isMethodAllowed(method string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return false
	}

	for _, allowed := range m.policy.cors.allowedMethods {
		if strings.EqualFold(allowed, method) {
			return true
		}
	}

	return false
}

// areRequestedHeadersAllowed checks whether every requested header is permitted.
//
// Why this exists:
// A preflight request asks permission to send certain headers.
// The gateway should only approve headers it expects to see.
func (m *EdgeMiddleware) areRequestedHeadersAllowed(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}

	requested := splitAndNormalizeCSV(raw)
	if len(requested) == 0 {
		return true
	}

	allowed := make(map[string]struct{}, len(m.policy.cors.allowedHeaders))
	for _, h := range m.policy.cors.allowedHeaders {
		allowed[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}

	for _, header := range requested {
		if _, ok := allowed[strings.ToLower(header)]; !ok {
			return false
		}
	}

	return true
}

// normalizeEdgePolicy validates and converts the public policy into runtime state.
func normalizeEdgePolicy(policy EdgePolicy) (corsSecurityState, error) {
	corsState, err := normalizeCORS(policy.CORS)
	if err != nil {
		return corsSecurityState{}, err
	}

	secState := normalizeSecurity(policy.Security)

	return corsSecurityState{
		cors:     corsState,
		security: secState,
	}, nil
}

// normalizeCORS validates CORS settings and fills safe defaults.
func normalizeCORS(policy CORSPolicy) (corsState, error) {
	allowedOrigins := make(map[string]struct{})

	for _, origin := range policy.AllowedOrigins {
		origin = normalizeOrigin(origin)
		if origin == "" {
			continue
		}
		allowedOrigins[origin] = struct{}{}
	}

	if policy.AllowAllOrigins && policy.AllowCredentials {
		return corsState{}, fmt.Errorf("allow all origins cannot be used with credentials")
	}

	allowedMethods := normalizeList(policy.AllowedMethods)
	if len(allowedMethods) == 0 {
		allowedMethods = []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		}
	}

	allowedHeaders := normalizeList(policy.AllowedHeaders)
	if len(allowedHeaders) == 0 {
		// Conservative default set for this gateway.
		allowedHeaders = []string{
			"Authorization",
			"Content-Type",
			"X-Request-ID",
			"X-Tenant-ID",
			"X-API-Key",
		}
	}

	exposedHeaders := normalizeList(policy.ExposedHeaders)

	maxAgeSeconds := int(policy.MaxAge.Seconds())
	if maxAgeSeconds < 0 {
		maxAgeSeconds = 0
	}

	return corsState{
		allowAllOrigins:  policy.AllowAllOrigins,
		allowedOrigins:   allowedOrigins,
		allowedMethods:   allowedMethods,
		allowedHeaders:   allowedHeaders,
		exposedHeaders:   exposedHeaders,
		allowCredentials: policy.AllowCredentials,
		maxAgeSeconds:    maxAgeSeconds,
	}, nil
}

// normalizeSecurity fills safe defaults for security headers.
func normalizeSecurity(policy SecurityHeadersPolicy) securityState {
	frame := strings.TrimSpace(policy.FrameOptions)
	if frame == "" {
		frame = "DENY"
	}

	referrer := strings.TrimSpace(policy.ReferrerPolicy)
	if referrer == "" {
		referrer = "no-referrer"
	}

	return securityState{
		enableHSTS:                policy.EnableHSTS,
		hstsMaxAge:                policy.HSTSMaxAge,
		includeSubdomains:         policy.IncludeSubdomains,
		preload:                   policy.Preload,
		frameOptions:              frame,
		referrerPolicy:            referrer,
		contentTypeNosniff:        policy.ContentTypeNosniff || !policy.ContentTypeNosniff,
		contentSecurityPolicy:     strings.TrimSpace(policy.ContentSecurityPolicy),
		permissionsPolicy:         strings.TrimSpace(policy.PermissionsPolicy),
		crossOriginOpenerPolicy:   strings.TrimSpace(policy.CrossOriginOpenerPolicy),
		crossOriginResourcePolicy: strings.TrimSpace(policy.CrossOriginResourcePolicy),
	}
}

// buildHSTSHeader builds the Strict-Transport-Security header value.
//
// Why this exists:
// HSTS should only be emitted when TLS is actually in use.
func buildHSTSHeader(policy securityState) string {
	maxAge := policy.hstsMaxAge
	if maxAge <= 0 {
		maxAge = 31536000 // 1 year starter default
	}

	parts := []string{
		fmt.Sprintf("max-age=%d", maxAge),
	}

	if policy.includeSubdomains {
		parts = append(parts, "includeSubDomains")
	}
	if policy.preload {
		parts = append(parts, "preload")
	}

	return strings.Join(parts, "; ")
}

// isPreflightRequest checks whether the request is a browser CORS preflight.
//
// Why this exists:
// Preflight requests should be answered before auth and business logic.
func isPreflightRequest(r *http.Request) bool {
	if r == nil {
		return false
	}

	return r.Method == http.MethodOptions &&
		strings.TrimSpace(r.Header.Get("Origin")) != "" &&
		strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")) != ""
}

// isSecureRequest checks whether the request arrived over TLS.
//
// Why this exists:
// HSTS should not be emitted unless the connection is secure.
func isSecureRequest(r *http.Request) bool {
	if r == nil {
		return false
	}

	return r.TLS != nil
}

// splitAndNormalizeCSV splits a comma-separated header list and trims each item.
//
// Why this exists:
// Browser preflight headers arrive as a CSV list.
func splitAndNormalizeCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Keep canonical header casing for readability, but compare in lowercase.
		part = textproto.CanonicalMIMEHeaderKey(part)
		out = append(out, part)
	}

	return out
}

// normalizeList trims, deduplicates, and sorts a string list.
func normalizeList(items []string) []string {
	set := make(map[string]struct{})
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		set[item] = struct{}{}
	}

	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}

	sort.Strings(out)
	return out
}

// normalizeOrigin converts an Origin header value into a stable comparison form.
//
// Example:
//
//	HTTPS://App.Example.com
//
// becomes:
//
//	https://app.example.com
//
// Why this exists:
// Origins should be compared consistently rather than by raw string noise.
func normalizeOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}

	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Host)

	return scheme + "://" + host
}

// writeEdgeError writes a small JSON error response.
//
// Why this exists:
// CORS and edge failures should be machine-readable and easy to debug.
func writeEdgeError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}
