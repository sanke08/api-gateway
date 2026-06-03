package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	requesttypes "github.com/sanke08/api_gateway/internal/pkg/types"
)

// Handler is the final gateway component before traffic
// leaves the gateway and goes to a backend service.
//
// Full request flow:
//
//			 Client
//			   v
//		    Router
//			   v
//	 	Tenant Middleware
//			   v
//		Tenant Middleware
//			   v
//	Authentication Middleware
//			   v
//	Membership Middleware
//			   v
//		   Proxy Handler
//			   v
//
// # Upstream Service
//
// Example:
//
// Client:
//
//	GET /orders/123
//
// Context already contains:
//
//	Tenant: acme
//
// Handler:
//
//	proxy := proxies["acme"]
//
// Request forwarded to:
//
//	https://api.acme.internal/orders/123
//
// Important:
//
// This handler does NOT:
//
// - authenticate
// - authorize
// - resolve tenant
//
// Those steps already happened earlier.
//
// This handler only:
//
// 1. finds the correct upstream
// 2. forwards the request
// 3. returns the response
type Handler struct {
	proxies map[string]*httputil.ReverseProxy
	logger  *slog.Logger
}

// Registry provides all upstream targets.
//
// Why this interface exists:
//
// The proxy should not care where upstream
// definitions come from.
//
// Today:
//
// # Memory
//
// Tomorrow:
//
// PostgreSQL
// MySQL
// Redis
// Service Discovery
// Consul
// Kubernetes
//
// The proxy only needs:
//
// registry.All()
//
// Example:
//
// [
//
//	{
//	   TenantID: "acme",
//	   BaseURL: "https://api.acme.internal"
//	},
//	{
//	   TenantID: "amazon",
//	   BaseURL: "https://api.amazon.internal"
//	}
//
// ]
//
// This keeps proxy code independent from storage.
type Registry interface {
	All() []models.UpstreamTarget
}

// NewHandler builds ALL reverse proxies once during startup.
//
// Why this is important:
//
// Creating reverse proxies involves:
//
// - parsing URLs
// - creating transports
// - creating callbacks
// - configuring middleware
//
// Doing this per request would be wasteful.
//
// Instead:
//
// Startup:
//
//	acme     -> ReverseProxy
//	amazon   -> ReverseProxy
//	flipkart -> ReverseProxy
//
// Stored in memory:
//
// map[string]*httputil.ReverseProxy
//
// Request time:
//
// proxy := proxies["acme"]
//
// Only a single map lookup.
//
// O(1)
//
// Extremely fast.
func NewHandler(reg Registry, logger *slog.Logger) (*Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}

	targets := reg.All()
	// Creates:
	//
	// {
	//    "acme":     proxy,
	//    "amazon":   proxy,
	//    "flipkart": proxy,
	// }
	//
	// During requests:
	//
	// proxy := proxies[tenantID]
	//
	// No URL parsing.
	// No transport creation.
	// No expensive work.
	proxies := make(map[string]*httputil.ReverseProxy, len(targets))

	for _, target := range targets {
		if strings.TrimSpace(target.TenantID) == "" {
			return nil, errors.New("proxy.new_handler: tenant_id is required")
		}
		if _, exists := proxies[target.TenantID]; exists {
			return nil, errors.New("proxy.new_handler: duplicate tenant proxy")
		}

		proxy, err := buildProxy(target, logger)
		if err != nil {
			return nil, err
		}

		proxies[target.TenantID] = proxy
	}

	return &Handler{
		proxies: proxies,
		logger:  logger,
	}, nil
}

// ServeHTTP forwards the request to the configured upstream for the resolved tenant.
//
// Why the tenant must already be in context:
// Phase 8 already resolved the tenant identity. This handler does not guess.
// It only forwards traffic after the tenant has been proven and attached to context.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenant, ok := requesttypes.ResolvedTenantFromContext(r.Context())
	if !ok {
		writeProxyError(w, http.StatusInternalServerError, "proxy_error", "tenant context is missing")
		return
	}

	proxy, exists := h.proxies[tenant.Id]
	if !exists {
		writeProxyError(w, http.StatusBadGateway, "upstream_not_configured", "no upstream target is configured for this tenant")
		return
	}

	proxy.ServeHTTP(w, r)
}

// buildProxy creates one dedicated reverse proxy
// for one tenant.
//
// Example:
//
// Tenant:
//
//	acme
//
// Upstream:
//
//	https://api.acme.internal
//
// Generated:
//
//	ReverseProxy
//
// Stored:
//
//	proxies["acme"]
//
// Later:
//
//	 request
//
//			|
//			v
//
// proxies["acme"]
//
//	|
//	v
//
// api.acme.internal
//
// Why one proxy per tenant:
//
// Different tenants may need:
//
// - different upstream URLs
// - different timeouts
// - different path rewrites
// - different transport settings
func buildProxy(target models.UpstreamTarget, logger *slog.Logger) (*httputil.ReverseProxy, error) {
	baseURL, err := url.Parse(target.BaseURL)
	if err != nil {
		return nil, err
	}

	// ReverseProxy lifecycle:
	//
	// Incoming Request
	//        |
	//        v
	// Director()
	//        |
	//        v
	// Rewrite URL
	// Add Gateway Headers
	//        |
	//        v
	// Transport Sends Request
	//        |
	//        v
	// Upstream Response
	//        |
	//        v
	// ModifyResponse()
	//        |
	//        v
	// Client
	//
	// Error:
	//
	// Director
	//    |
	// Transport
	//    |
	// Failure
	//    |
	// ErrorHandler()
	proxy := &httputil.ReverseProxy{
		// Director runs BEFORE the request is sent.
		//
		// Think:
		//
		// Original Request:
		//
		// GET /gateway/orders
		//
		// Director transforms it into:
		//
		// GET https://api.acme.internal/v1/orders
		//
		// Director is the last place where
		// outgoing requests can be modified.
		Director: func(outReq *http.Request) {
			// rewriteRequest transforms the request
			// from public gateway form into backend form.
			//
			// Example:
			//
			// Client:
			//
			// GET /gateway/orders?id=1
			//
			// Configuration:
			//
			// BaseURL      = https://api.acme.internal
			// StripPrefix = /gateway
			// AddPrefix   = /v1
			//
			// Result:
			//
			// GET https://api.acme.internal/v1/orders?id=1
			//
			// Steps:
			//
			// 1. Replace scheme
			// 2. Replace host
			// 3. Rewrite path
			// 4. Preserve query string
			// 5. Configure Host header
			rewriteRequest(outReq, baseURL, target)

			// Apply the configured request transform before the request leaves the gateway.
			//
			// Example:
			// - remove internal client headers
			// - add gateway-controlled headers
			// - rewrite the outgoing path if the route requires it
			applyRequestTransform(outReq, target.RequestTransform)

			// injectGatewayHeaders attaches trusted gateway
			// information to outgoing requests.
			//
			// Why this exists:
			//
			// Clients are NOT trusted.
			//
			// Client:
			//
			// X-Tenant-ID: fake
			//
			// must never be trusted.
			//
			// Instead:
			//
			// Gateway resolves tenant,
			// authenticates user,
			// resolves membership.
			//
			// Then writes authoritative headers.
			//
			// Example:
			//
			// X-Gateway-Tenant-ID: acme
			// X-Gateway-User-ID: user_123
			// X-Gateway-Membership-Role: admin
			//
			// Backend services can trust these values
			// because they were generated by the gateway.
			injectGatewayHeaders(outReq)
		},
		ModifyResponse: func(resp *http.Response) error {

			// Apply response transformation before the response returns to the client.
			//
			// Example:
			// - remove internal upstream headers
			// - add gateway response headers
			applyResponseTransform(resp, target.ResponseTransform)

			// This header helps during debugging and operational tracing.
			// It does not change business logic.
			if target.Name != "" {
				resp.Header.Set("X-Gateway-Upstream", target.Name)
			}
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			handleProxyError(logger, rw, req, err, target)
		},
		FlushInterval: 100 * time.Millisecond,
	}

	proxy.Transport = transportForTarget(target)

	return proxy, nil
}

// rewriteRequest changes the outgoing request so it points to the upstream.
//
// What it does:
// - sets scheme and host from the upstream base URL
// - rewrites the path
// - preserves the query string
// - optionally preserves the incoming Host header
//
// Example:
//
// BaseURL:      https://api.acme.internal
// StripPrefix:   /gateway
// AddPrefix:     /v1
// Incoming:      /gateway/orders?id=1
// Forwarded:     https://api.acme.internal/v1/orders?id=1
func rewriteRequest(outReq *http.Request, baseURL *url.URL, target models.UpstreamTarget) {
	outReq.URL.Scheme = baseURL.Scheme
	outReq.URL.Host = baseURL.Host

	// If the upstream base URL already contains a path prefix, keep it.
	// Example:
	// baseURL.Path = /internal
	// request.Path = /orders
	// final = /internal/orders
	outReq.URL.Path = joinPath(
		baseURL.Path,
		rewritePath(
			outReq.URL.Path,
			target.StripPrefix,
			target.AddPrefix,
		),
	)

	if !target.PreserveHost {
		outReq.Host = baseURL.Host
	}
}

// rewritePath transforms paths before forwarding.
//
// Example:
//
// Incoming:
//
// /gateway/orders
//
// Config:
//
// StripPrefix = /gateway
// AddPrefix   = /v1
//
// Step 1:
//
// /gateway/orders
//
// remove "/gateway"
//
// Result:
//
// /orders
//
// Step 2:
//
// prepend "/v1"
//
// Result:
//
// /v1/orders
//
// Final forwarded path:
//
// https://api.acme.internal/v1/orders
//
// This allows public routes and
// internal routes to differ.

func rewritePath(requestPath, stripPrefix, addPrefix string) string {
	requestPath = cleanPath(requestPath)
	stripPrefix = cleanPath(stripPrefix)
	addPrefix = cleanPath(addPrefix)

	pathOut := requestPath

	if stripPrefix != "" && stripPrefix != "/" {
		if pathOut == stripPrefix {
			pathOut = "/"
		} else if strings.HasPrefix(pathOut, stripPrefix+"/") {
			pathOut = strings.TrimPrefix(pathOut, stripPrefix)
			if pathOut == "" {
				pathOut = "/"
			}
		}
	}

	if addPrefix != "" && addPrefix != "/" {
		pathOut = joinPath(addPrefix, pathOut)
	}

	return cleanPath(pathOut)
}

// injectGatewayHeaders adds authoritative gateway headers to the upstream request.
//
// Why this is important:
// The upstream should know which tenant, user, membership, and API key
// were resolved by the gateway.
//
// Why headers are set here:
// This is the last trusted place before the request leaves the gateway.
func injectGatewayHeaders(r *http.Request) {
	tenant, ok := requesttypes.ResolvedTenantFromContext(r.Context())
	if ok {
		r.Header.Set("X-Gateway-Tenant-ID", tenant.Id)
		r.Header.Set("X-Gateway-Tenant-Slug", tenant.Slug)
		r.Header.Set("X-Tenant-ID", tenant.Id)
	}

	user, ok := requesttypes.AuthenticatedUserFromContext(r.Context())
	if ok {
		r.Header.Set("X-Gateway-User-ID", user.Id)
		r.Header.Set("X-Gateway-User-Email", user.Email)
	}

	membership, ok := requesttypes.ResolvedMembershipFromContext(r.Context())
	if ok {
		r.Header.Set("X-Gateway-Membership-ID", membership.ID)
		r.Header.Set("X-Gateway-Membership-Role", string(membership.Role))
		r.Header.Set("X-Gateway-Membership-Status", string(membership.Status))
	}

	apiKey, ok := requesttypes.ResolvedAPIKeyFromContext(r.Context())
	if ok {
		r.Header.Set("X-Gateway-API-Key-ID", apiKey.ID)
		r.Header.Set("X-Gateway-API-Key-Active", boolString(apiKey.Active))
	}
}

// handleProxyError converts upstream failures into stable gateway responses.
//
// Why this is separate:
// ReverseProxy invokes this callback when the upstream cannot be reached,
// times out, or fails during transport.
func handleProxyError(
	logger *slog.Logger,
	rw http.ResponseWriter,
	req *http.Request,
	err error,
	target models.UpstreamTarget,
) {
	if errors.Is(err, context.Canceled) {
		// The client went away. There is nothing useful to write back.
		return
	}

	status := http.StatusBadGateway
	code := "bad_gateway"
	message := "upstream request failed"

	if isTimeoutError(err) {
		status = http.StatusGatewayTimeout
		code = "gateway_timeout"
		message = "upstream request timed out"
	}

	if logger != nil {
		logger.Error(
			"upstream request failed",
			"tenant_id", target.TenantID,
			"upstream", target.Name,
			"path", req.URL.Path,
			"error", err.Error(),
		)
	}

	writeProxyError(rw, status, code, message)
}

// transportForTarget creates the HTTP transport used by one upstream proxy.
//
// Why this exists:
// Different upstreams may eventually need different timeout behavior.
// The transport is the part that actually performs the HTTP request.
func transportForTarget(target models.UpstreamTarget) http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}

	clone := base.Clone()

	if target.Timeout > 0 {
		clone.ResponseHeaderTimeout = target.Timeout
		clone.ExpectContinueTimeout = target.Timeout
	}

	return clone
}

// writeProxyError writes a JSON error response from the proxy layer.
func writeProxyError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(proxyErrorResponse{
		Error:   code,
		Message: message,
	})
}

// proxyErrorResponse is the public JSON error shape for proxy failures.
//
// Why this exists:
// The proxy should return a stable error body instead of leaking transport details.
type proxyErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// boolString converts a bool to a string.
//
// Why this helper exists:
// Gateway headers are text headers, so booleans must be serialized explicitly.
func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// joinPath joins two paths without destroying their meaning.
//
// Why this exists:
// path.Join can be too aggressive for HTTP paths because it cleans segments.
// This helper keeps the result predictable for gateway forwarding.
//
// Example:
// base = /internal
// extra = /v1/orders
// result = /internal/v1/orders
func joinPath(base, extra string) string {
	base = cleanPath(base)
	extra = cleanPath(extra)

	if base == "/" {
		return extra
	}
	if extra == "/" {
		return base
	}

	return cleanPath(strings.TrimRight(base, "/") + "/" + strings.TrimLeft(extra, "/"))
}

// cleanPath normalizes a path.
//
// Why this exists:
// It keeps request and upstream paths in a stable form for joining.
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

// isTimeoutError checks whether an upstream error is a timeout.
//
// Why this matters:
// Timeouts should produce 504 Gateway Timeout instead of a generic 502.
func isTimeoutError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return errors.Is(err, context.DeadlineExceeded)
}

/*
COMPLETE REQUEST FLOW

Client
  |
  v
GET /gateway/orders/123
  |
  v
Router
  |
  v
Tenant Resolution
  |
  v
tenant = "acme"
  |
  v
Proxy Handler
  |
  +--> proxies["acme"]
  |
  v
ReverseProxy
  |
  +--> rewriteRequest()
  |
  +--> injectGatewayHeaders()
  |
  +--> transportForTarget()
  |
  v
https://api.acme.internal/v1/orders/123
  |
  v
Backend Response
  |
  +--> ModifyResponse()
  |
  v
Client

Failure Path

Backend Offline
      |
      v
ErrorHandler()
      |
      v
HTTP 502

Timeout
      |
      v
ErrorHandler()
      |
      v
HTTP 504
*/
