package models

// RequestTransform defines modifications that should happen
// BEFORE a request leaves the gateway and is sent to an upstream.
//
// Think of it as:
//
// Client Request
//
//	|
//	v
//
// Gateway
//
//	|
//	+--> Add Headers
//	|
//	+--> Remove Headers
//	|
//	+--> Rewrite Path
//	|
//	v
//
// # Upstream Service
//
// Example:
//
// Client:
//
// GET /api/orders
//
// Headers:
//
// Authorization: Bearer xxx
// X-Debug: true
//
// Transform:
//
//	RequestTransform{
//	    AddHeaders: {
//	        "X-Tenant-ID": "tenant-123",
//	    },
//	    RemoveHeaders: []string{
//	        "X-Debug",
//	    },
//	    RewritePath: "/v1/orders",
//	}
//
// Result sent upstream:
//
// GET /v1/orders
//
// Headers:
//
// Authorization: Bearer xxx
// X-Tenant-ID: tenant-123
//
// # X-Debug removed
//
// Why this exists:
//
// Different tenants or routes often need
// different request shaping rules without
// changing application code.
type RequestTransform struct {
	// AddHeaders contains headers that will be inserted
	// into the outgoing request before forwarding.
	//
	// Example:
	//
	// Configuration:
	//
	// AddHeaders: {
	//     "X-Tenant-ID": "tenant-123",
	//     "X-Gateway": "api-gateway",
	// }
	//
	// Incoming Request:
	//
	// GET /orders
	//
	// Outgoing Request:
	//
	// GET /orders
	//
	// X-Tenant-ID: tenant-123
	// X-Gateway: api-gateway
	//
	// Why this is useful:
	//
	// Backend services often need information
	// already resolved by the gateway.
	//
	// Examples:
	//
	// X-Tenant-ID
	// X-User-ID
	// X-Membership-Role
	// X-Request-ID
	//
	// This avoids forcing every backend service
	// to repeat tenant resolution logic.
	AddHeaders map[string]string

	// RemoveHeaders contains header names that should
	// be removed before forwarding the request.
	//
	// Example:
	//
	// Client sends:
	//
	// Authorization: Bearer xxx
	// X-Debug: true
	// X-Internal-Test: enabled
	//
	// Configuration:
	//
	//	RemoveHeaders: []string{
	//	    "X-Debug",
	//	    "X-Internal-Test",
	//	}
	//
	// Forwarded Request:
	//
	// Authorization: Bearer xxx
	//
	// Removed:
	//
	// X-Debug
	// X-Internal-Test
	//
	// Why this matters:
	//
	// Clients should not always control every
	// header reaching internal systems.
	//
	// Common use cases:
	//
	// - security
	// - removing internal headers
	// - preventing spoofing
	// - removing debugging values
	RemoveHeaders []string

	// RewritePath replaces the request path
	// before forwarding.
	//
	// Example:
	//
	// Incoming:
	//
	// GET /api/orders
	//
	// Configuration:
	//
	// RewritePath: "/v1/orders"
	//
	// Outgoing:
	//
	// GET /v1/orders
	//
	// Another Example:
	//
	// Public API:
	//
	// /customers
	//
	// Internal Service:
	//
	// /api/v2/customer-service/customers
	//
	// Rewrite:
	//
	// RewritePath:
	// "/api/v2/customer-service/customers"
	//
	// Why this exists:
	//
	// Public API paths and internal service
	// paths are often different.
	RewritePath string
}

// ResponseTransform defines modifications that happen
// AFTER the upstream service responds but BEFORE the
// client receives the response.
//
// Think:
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
// Upstream
//
//	|
//	v
//
// Response
//
//	|
//	+--> Add Headers
//	|
//	+--> Remove Headers
//	|
//	v
//
// # Client
//
// Example:
//
// Upstream Response:
//
// # HTTP 200
//
// Server: nginx
//
// Gateway Transform:
//
// AddHeaders:
//
//	X-Gateway-Upstream: orders-api
//
// RemoveHeaders:
//
//	Server
//
// Final Client Response:
//
// # HTTP 200
//
// X-Gateway-Upstream: orders-api
//
// # Server header removed
//
// Why this exists:
//
// The gateway may want to:
//
// - add metadata
// - hide infrastructure details
// - enforce security policies
// - standardize responses
type ResponseTransform struct {
	// AddHeaders contains headers that should be added
	// to the response before returning it to the client.
	//
	// Example:
	//
	// Upstream Response:
	//
	// HTTP 200
	//
	// Content-Type: application/json
	//
	// Transform:
	//
	// AddHeaders: {
	//     "X-Gateway-Upstream": "orders-api",
	//     "X-Request-ID": "req-123",
	// }
	//
	// Final Response:
	//
	// HTTP 200
	//
	// Content-Type: application/json
	// X-Gateway-Upstream: orders-api
	// X-Request-ID: req-123
	//
	// Useful for:
	//
	// - tracing
	// - debugging
	// - observability
	// - request correlation
	AddHeaders map[string]string

	// RemoveHeaders contains response headers that
	// should be stripped before returning data
	// to the client.
	//
	// Example:
	//
	// Upstream Response:
	//
	// HTTP 200
	//
	// Server: nginx
	// X-Internal-Trace: abc123
	// Content-Type: application/json
	//
	// Transform:
	//
	// RemoveHeaders: []string{
	//     "Server",
	//     "X-Internal-Trace",
	// }
	//
	// Final Response:
	//
	// HTTP 200
	//
	// Content-Type: application/json
	//
	// Why this matters:
	//
	// Internal implementation details should
	// often remain hidden from clients.
	//
	// Common removals:
	//
	// Server
	// X-Powered-By
	// X-Internal-Trace
	// X-Debug
	RemoveHeaders []string
}
