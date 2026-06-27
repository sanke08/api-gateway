package models

import "time"

// UpstreamTarget defines where requests should be forwarded.
//
// Think of this as a routing configuration record.
//
// The gateway itself usually does not contain business logic.
// Its job is:
//
//  1. receive request
//  2. identify tenant
//  3. find tenant's upstream target
//  4. optionally rewrite path
//  5. forward request
//  6. return response
//
// Example:
//
// Tenant:
//
//	acme-inc
//
// Stored upstream:
//
//	{
//	    TenantID: "acme-inc",
//	    BaseURL: "https://api.acme.internal",
//	}
//
// Request:
//
//	GET /orders
//
// Gateway forwards:
//
//	GET https://api.acme.internal/orders
//
// In a production system these records may come from:
//
// - database
// - config service
// - service discovery
// - admin dashboard
type UpstreamTarget struct {
	// TenantID identifies which tenant owns this upstream.
	//
	// Example:
	//
	//	acme-inc
	//	flipkart
	//	amazon
	//
	// Why this exists:
	//
	// After authentication or tenant resolution,
	// the gateway needs a way to find the correct backend.
	//
	// Example:
	//
	// Incoming request:
	//
	//	Host: acme.example.com
	//
	// Tenant resolver:
	//
	//	tenantID = "acme-inc"
	//
	// Lookup:
	//
	//	upstream := store.Get("acme-inc")
	//
	// Result:
	//
	//	https://api.acme.internal
	TenantID string

	// Name is a human-readable label.
	//
	// Examples:
	//
	//	"orders-api"
	//	"billing-api"
	//	"inventory-service"
	//
	// Why this exists:
	//
	// Machines use TenantID.
	//
	// Humans use Name.
	//
	// Useful for:
	//
	// - dashboards
	// - admin panels
	// - logs
	// - debugging
	//
	// Example log:
	//
	// tenant=acme-inc
	// upstream=orders-api
	Name string

	// BaseURL is the destination server.
	//
	// Example:
	//
	//	https://api.acme.internal
	//
	// This is where requests are ultimately forwarded.
	//
	// Example:
	//
	// Incoming:
	//
	//	GET /orders
	//
	// Gateway builds:
	//
	//	https://api.acme.internal/orders
	//
	// and sends request there.
	//
	// Without BaseURL:
	//
	// the gateway would have nowhere to proxy traffic.
	BaseURL string

	// StripPrefix removes part of the incoming URL path
	// before forwarding.
	//
	// Example:
	//
	// Incoming:
	//
	//	/gateway/orders
	//
	// StripPrefix:
	//
	//	/gateway
	//
	// Result:
	//
	//	/orders
	//
	// Why this exists:
	//
	// Public URLs often differ from internal service URLs.
	StripPrefix string

	// AddPrefix adds a path segment before forwarding.
	//
	// Example:
	//
	// Incoming:
	//
	//	/orders
	//
	// AddPrefix:
	//
	//	/v1
	//
	// Result:
	//
	//	/v1/orders
	//
	// Why this exists:
	//
	// Internal services often expose versioned APIs
	// while public URLs stay clean.
	AddPrefix string

	// PreserveHost controls which Host header is sent
	// to the upstream server.
	//
	// Example incoming:
	//
	// Host: api.customer.com
	//
	// Two options:
	//
	// PreserveHost = true
	//
	// Send:
	//
	// Host: api.customer.com
	//
	// PreserveHost = false
	//
	// Send:
	//
	// Host: api.acme.internal
	//
	// Why this matters:
	//
	// Some applications use Host for:
	//
	// - routing
	// - SSL certificates
	// - virtual hosting
	// - tenant resolution
	PreserveHost bool

	// Timeout is the maximum time the gateway should wait
	// for the upstream to return headers.
	//
	// Example:
	//
	// Incoming request arrives at gateway.
	// Gateway forwards to upstream.
	// Upstream may take time to process and send headers back.
	//
	// Timeout = 5s
	//
	// If upstream does not send headers within 5 seconds:
	//
	// 1. Gateway stops waiting.
	// 2. Gateway returns error to client (504 Gateway Timeout).
	// 3. Upstream request is cancelled.
	//
	// Why this exists:
	//
	// Upstream services may:
	//
	// - be slow
	// - be overloaded
	// - crash
	// - fail to respond
	//
	// Without timeout:
	//
	// - Gateway would hang indefinitely.
	// - Client would wait forever.
	// - Resources would be exhausted.
	Timeout time.Duration

	// Retry controls how the gateway retries temporary upstream failures.
	Retry RetryPolicy

	// RequestTransform describes request-side modifications.
	RequestTransform RequestTransform

	// ResponseTransform describes response-side modifications.
	ResponseTransform ResponseTransform

	// Breaker controls circuit breaker behavior.
	Breaker CircuitBreakerPolicy

	// CreatedAt records when this upstream was created.
	//
	// Example:
	//
	// 2026-05-30 10:00:00 UTC
	//
	// Useful for:
	//
	// - auditing
	// - debugging
	// - compliance
	// - operational history
	CreatedAt time.Time

	// UpdatedAt records the last configuration change.
	//
	// Example:
	//
	// Original:
	//
	//	BaseURL = old-api.internal
	//
	// Later:
	//
	//	BaseURL = new-api.internal
	//
	// UpdatedAt records when the change occurred.
	//
	// Useful for:
	//
	// - audit logs
	// - troubleshooting
	// - rollback analysis
	UpdatedAt time.Time

	// HealthPath is the path used by readiness probes to check upstream health.
	//
	// Example:
	//   "/health"
	//
	// Why this exists:
	// Different backends may expose different health endpoints.
	HealthPath string
}
