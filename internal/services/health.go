package services

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/proxy"
)

// HealthChecker is responsible for reporting the health of the gateway.
//
// Health checks are typically exposed through endpoints such as:
//
//	GET /health/live   -> "Is this process alive?"
//	GET /health/ready  -> "Can this gateway serve traffic right now?"
//
// Why this service exists:
//
// A production gateway depends on several components:
//
//	Client
//	   │
//	   ▼
//	API Gateway
//	   │
//	   ├── Database
//	   ├── Upstream Service A
//	   ├── Upstream Service B
//	   └── Upstream Service C
//
// Even if the gateway process itself is running, one or more dependencies may
// have failed. Instead of letting clients discover those failures through
// requests, health endpoints allow infrastructure (Kubernetes, load balancers,
// monitoring systems, etc.) to check the gateway's condition first.
//
// This service centralizes all health-related logic in one place instead of
// scattering it across handlers.
//
// Responsibilities:
//
//   - Report liveness (process is running)
//   - Report readiness (gateway is ready to accept traffic)
//   - Probe configured upstream services
//   - Check important dependencies like the database
//   - Return structured health information
type HealthChecker struct {

	// db is the gateway's database connection.
	//
	// Why this is needed:
	//
	// Many gateway features depend on the database:
	//
	//   • authentication
	//   • tenants
	//   • API keys
	//   • usage logging
	//
	// During readiness checks we usually perform a lightweight operation
	// (for example PingContext) to verify the database is reachable.
	//
	// If the database cannot be reached, the gateway should normally report
	// itself as "not ready", even if the HTTP server is still running.
	db *sql.DB

	// registry contains every configured upstream service.
	//
	// Example:
	//
	//   payments  -> http://payments.internal
	//   users     -> http://users.internal
	//   products  -> http://products.internal
	//
	// Why this exists:
	//
	// A readiness check is much more useful if it verifies that important
	// upstream services are also reachable.
	//
	// The HealthChecker iterates over this registry and probes each service.
	registry proxy.Registry

	// startedAt records when this HealthChecker (and effectively the gateway)
	// started.
	//
	// Why this exists:
	//
	// It allows us to calculate uptime later.
	//
	// Example:
	//
	//   startedAt = 10:00
	//   now       = 10:15
	//
	//   uptime = 15 minutes
	startedAt time.Time

	// clock returns the current time.
	//
	// Why this exists:
	//
	// Calling time.Now() directly everywhere makes tests difficult because the
	// current time constantly changes.
	//
	// By storing a function here we can replace it during tests:
	//
	//   checker.clock = func() time.Time {
	//       return fixedTime
	//   }
	//
	// Production uses:
	//
	//   time.Now
	clock func() time.Time

	// httpClientFactory creates HTTP clients used to probe upstream services.
	//
	// Why this exists:
	//
	// Each health probe needs an HTTP client with a timeout.
	//
	// Instead of creating clients manually everywhere, we centralize the logic.
	//
	// Example:
	//
	//   client := checker.httpClientFactory(2 * time.Second)
	//
	// which returns something like:
	//
	//   &http.Client{
	//       Timeout: 2 * time.Second,
	//   }
	//
	// Tests can replace this factory with a fake client that returns predefined
	// responses without making real network requests.
	httpClientFactory func(timeout time.Duration) *http.Client
}

// NewHealthChecker constructs a HealthChecker.
//
// This is the only place where the service is initialized.
//
// Example:
//
//	checker := NewHealthChecker(db, registry)
//
// After construction the checker is ready to:
//
//   - verify database connectivity
//   - probe upstream services
//   - calculate uptime
//   - answer health endpoint requests
//
// Why constructors are preferred:
//
// Instead of allowing callers to manually populate every field:
//
//	&HealthChecker{
//	    db: ...,
//	    registry: ...,
//	    ...
//	}
//
// we guarantee that every required dependency is initialized correctly.
func NewHealthChecker(db *sql.DB, registry proxy.Registry) *HealthChecker {
	return &HealthChecker{

		// Store the shared database connection.
		//
		// The health checker will later use this to verify the database is alive.
		db: db,

		// Store the configured upstream registry.
		//
		// Later the checker will iterate through these services and probe each one.
		registry: registry,

		// Record the gateway startup time.
		//
		// This value never changes after creation.
		startedAt: time.Now().UTC(),

		// Production clock implementation.
		//
		// Tests can replace this with a deterministic clock.
		clock: time.Now,

		// Factory used whenever an HTTP client is needed for upstream probes.
		//
		// Why a factory instead of one shared client?
		//
		// Different probes may require different timeout values, and tests can
		// easily replace this function with a fake implementation.
		httpClientFactory: func(timeout time.Duration) *http.Client {

			// Prevent callers from accidentally creating clients without a timeout.
			//
			// Without a timeout, a broken upstream could cause the health check
			// to block indefinitely.
			if timeout <= 0 {
				timeout = 2 * time.Second
			}

			// Create and return a new HTTP client configured with the timeout.
			return &http.Client{
				Timeout: timeout,
			}
		},
	}
}

// Liveness reports whether this gateway process is alive.
//
// What "alive" means:
//
// A liveness check answers only one question:
//
//	"Is this process still running?"
//
// It does NOT check:
//
//	❌ Database
//	❌ Upstream services
//	❌ Redis
//	❌ External APIs
//
// Those belong to readiness checks.
//
// Why this exists:
//
// Container platforms like Kubernetes periodically call a liveness endpoint.
//
// If the process hangs, deadlocks, or crashes:
//
//	Kubernetes
//	      │
//	      ▼
//	GET /health/live
//	      │
//	      ├── healthy   → do nothing
//	      └── failed    → restart container
//
// Because liveness is called very frequently, it should be extremely cheap.
// It should never perform network calls or expensive work.
func (h *HealthChecker) Liveness(ctx context.Context) models.LivenessStatus {

	now := h.now()

	// Build the liveness response.
	// Example response:
	// {
	//     "status":"healthy",
	//     "started_at":"10:00",
	//     "checked_at":"10:05",
	//     "uptime_seconds":300
	// }
	return models.LivenessStatus{

		// Since this function only checks whether the process is alive, reaching this point already means the gateway is healthy.
		Status: "healthy",

		// When the gateway originally started.
		StartedAt: h.startedAt,

		// When this health check was performed.
		CheckedAt: now,

		// Calculate how long the gateway has been running.
		//
		// Example:
		//
		// startedAt = 10:00
		// now       = 10:05
		//
		// uptime = 300 seconds
		UptimeSeconds: int64(now.Sub(h.startedAt).Seconds()),
	}
}

// Readiness reports whether the gateway is ready to accept client traffic.
//
// Unlike liveness, readiness answers:
//
//	"Can this gateway safely serve requests?"
//
// Example:
//
//	Client
//	   │
//	   ▼
//	Load Balancer
//	   │
//	   ▼
//	GET /health/ready
//	   │
//	   ├── ready      → send traffic
//	   └── not_ready  → remove from load balancer
//
// What this method checks:
//
//	✓ Database connectivity
//	✓ Upstream health (for visibility)
//
// Important design decision:
//
// Upstream failures DO NOT automatically make the gateway "not ready".
//
// Why?
//
// Suppose:
//
//	User Service     ❌ down
//	Payment Service  ✅ healthy
//
// The gateway can still:
//
//	✓ authenticate users
//	✓ serve cached responses
//	✓ route requests
//	✓ let circuit breakers reject only broken upstreams
//
// Therefore only the database determines overall readiness,
// while upstream results are reported for monitoring purposes.
func (h *HealthChecker) Readiness(ctx context.Context) models.ReadinessStatus {

	// Record when this readiness check started.
	now := h.now()

	// Check whether the database is reachable.
	//
	// This returns information like:
	//
	// Healthy
	// Latency
	// Error message
	dbStatus := h.probeDatabase(ctx)

	// Probe every configured upstream service.
	//
	// Example result:
	//
	// users     -> healthy
	// payments  -> timeout
	// products  -> healthy
	upstreams := h.probeUpstreams(ctx)

	// Build the readiness response object.
	readiness := models.ReadinessStatus{
		// Timestamp of this readiness check.
		CheckedAt: now,
		// Database health information.
		Database: dbStatus,
		// Health information for every configured upstream.
		Upstreams: upstreams,
	}

	// Decide the overall readiness state.
	// The database is considered the gateway's primary control-plane dependency.
	// Database healthy?
	// YES -> Gateway is ready.
	// NO  -> Gateway is not ready.
	if dbStatus.Healthy {
		// Infrastructure may safely continue routing requests here.
		readiness.Status = "ready"

	} else {
		// Infrastructure should temporarily stop routing requests.
		readiness.Status = "not_ready"
	}

	// Return the complete readiness report.
	return readiness
}

// probeDatabase performs a lightweight database health check.
//
// Why this exists:
//
// Before the gateway declares itself "ready", it should verify that its
// most important dependency—the database—is actually reachable.
//
// What this measures:
//
//	✓ Can we connect?
//	✓ How long did it take?
//	✓ If it failed, why?
//
// Example:
//
// Gateway
//
//	│
//	▼
//
// Ping Database
//
//	│
//	├── success → healthy=true
//	└── failure → healthy=false
func (h *HealthChecker) probeDatabase(ctx context.Context) models.DatabaseStatus {

	// Record when this probe is performed.
	now := h.now()

	// If no database was configured at startup, there is nothing to probe.
	if h.db == nil {

		// Return an unhealthy status explaining the problem.
		return models.DatabaseStatus{
			Healthy: false,

			// No probe happened, so latency is zero.
			LatencyMS: 0,

			// Time of this failed check.
			CheckedAt: now,

			// Human-readable explanation.
			Error: "database is not configured",
		}
	}

	// Create a child context with a short timeout.
	//
	// Why?
	//
	// If the database hangs forever,
	// the readiness endpoint must NOT hang forever.
	//
	// After 2 seconds the context automatically cancels the ping.
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

	// Always release resources associated with the timeout context.
	defer cancel()

	// Record when the ping begins.
	start := h.now()

	// Perform a lightweight connectivity check.
	//
	// PingContext does not execute SQL.
	// It simply verifies the database connection is alive.
	err := h.db.PingContext(probeCtx)

	// Measure how long the ping took.
	latency := h.now().Sub(start)

	// Ping failed.
	if err != nil {

		// Return an unhealthy database report.
		return models.DatabaseStatus{

			Healthy: false,

			// Even failed requests have latency.
			LatencyMS: latency.Milliseconds(),

			CheckedAt: now,

			// Include the database error for operators.
			Error: err.Error(),
		}
	}

	// Database responded successfully.
	return models.DatabaseStatus{

		// Database is reachable.
		Healthy: true,

		// Time taken for the ping.
		LatencyMS: latency.Milliseconds(),

		// Timestamp of the successful check.
		CheckedAt: now,
	}
}

// probeUpstreams checks the health of every configured upstream service.
//
// Why this method exists:
//
// A gateway usually forwards requests to multiple backend services.
//
// Example:
//
//	          Gateway
//	             │
//	  ┌──────────┼──────────┐
//	  ▼          ▼          ▼
//	User API  Payment API  Product API
//
// Operators often want to know:
//
//	✓ Which upstreams are healthy?
//	✓ Which ones are slow?
//	✓ Which ones are failing?
//
// This method gathers that information by probing every configured upstream.
//
// Important design decision:
//
// The returned information is mainly for monitoring and debugging.
//
// Even if one upstream is unhealthy:
//
//	User API      ❌
//	Payment API   ✅
//	Product API   ✅
//
// the gateway itself can still be considered "ready" because:
//
//   - Circuit breakers can isolate failures.
//   - Other upstreams continue serving traffic.
//   - Not every tenant uses every upstream.
//
// Therefore this method reports upstream health but does NOT determine the
// gateway's overall readiness.
func (h *HealthChecker) probeUpstreams(ctx context.Context) []models.UpstreamStatus {

	// Record when this probe cycle started.
	//
	// Every upstream checked during this run will share the same timestamp.
	now := h.now()

	// No upstream registry configured.
	//
	// This is not considered an error. It simply means there are no upstreams
	// to probe.
	if h.registry == nil {
		return []models.UpstreamStatus{}
	}

	// Retrieve every configured upstream target.
	//
	// Example:
	//
	// [
	//     users,
	//     payments,
	//     products
	// ]
	targets := h.registry.All()

	// Allocate the result slice.
	//
	// Capacity equals the number of upstreams to avoid unnecessary allocations
	// while appending.
	out := make([]models.UpstreamStatus, 0, len(targets))

	// Probe each upstream individually.
	for _, target := range targets {

		// Perform the actual network probe.
		status := h.probeOneUpstream(ctx, target)

		// Store when this probe cycle occurred.
		status.CheckedAt = now

		// Add the result to the output slice.
		out = append(out, status)
	}

	// Return the complete health report for all upstreams.
	return out
}

// probeOneUpstream checks the health of a single upstream service.
//
// Example:
//
//	Gateway
//	   │
//	   ▼
//	http://users-service/health
//
// This method:
//
//  1. Builds the health URL.
//  2. Sends an HTTP HEAD request.
//  3. Falls back to GET if HEAD isn't supported.
//  4. Measures latency.
//  5. Returns a detailed health report.
//
// Why HEAD first?
//
// HEAD requests return only headers, not the response body.
//
// That means:
//
//	✓ Less bandwidth
//	✓ Faster
//	✓ Lower CPU usage
//
// Some servers do not implement HEAD correctly, so GET is used as a fallback.
func (h *HealthChecker) probeOneUpstream(ctx context.Context, target models.UpstreamTarget) models.UpstreamStatus {

	// Record when this probe started.
	now := h.now()

	// Normalize important string fields.
	//
	// This removes accidental whitespace such as:
	//
	// " tenant-1 " -> "tenant-1"
	target.TenantID = strings.TrimSpace(target.TenantID)
	target.Name = strings.TrimSpace(target.Name)

	// Every upstream should belong to a tenant.
	//
	// Without a tenant ID we cannot correctly identify ownership.
	if target.TenantID == "" {

		return models.UpstreamStatus{
			Name: target.Name,

			Healthy: false,

			CheckedAt: now,

			Error: "tenant id is missing",
		}
	}

	// Construct the URL that will be probed.
	//
	// Example:
	//
	// Base URL:
	// http://users.internal
	//
	// Health Path:
	// /health
	//
	// Final URL:
	// http://users.internal/health
	probeURL, err := buildProbeURL(target)

	// Invalid URL configuration.
	if err != nil {

		return models.UpstreamStatus{

			TenantID: target.TenantID,

			Name: target.Name,

			Healthy: false,

			CheckedAt: now,

			Error: err.Error(),
		}
	}

	// Determine the timeout for this probe.
	//
	// Every upstream may define its own timeout.
	//
	// If none is configured, use a sensible default.
	timeout := target.Timeout

	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	// Create an HTTP client using the configured timeout.
	client := h.httpClientFactory(timeout)

	// Defensive fallback.
	//
	// Even if the factory unexpectedly returns nil, we still create
	// a usable client instead of panicking.
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
		}
	}

	// First attempt: send an HTTP HEAD request.
	//
	// HEAD is preferred because it avoids downloading the response body.
	status, latency, err := doProbe(
		ctx,
		client,
		http.MethodHead,
		probeURL,
	)

	// Probe succeeded and returned a healthy HTTP status.
	if err == nil && isHealthyStatus(status) {

		return models.UpstreamStatus{

			TenantID: target.TenantID,

			Name: target.Name,

			HealthPath: probePath(target),

			Healthy: true,

			StatusCode: status,

			LatencyMS: latency.Milliseconds(),

			CheckedAt: now,
		}
	}

	// Some servers do not support HEAD.
	//
	// Typical responses:
	//
	// 405 Method Not Allowed
	// 501 Not Implemented
	//
	// In that case we retry using GET.
	if status == http.StatusMethodNotAllowed ||
		status == http.StatusNotImplemented {

		status, latency, err = doProbe(
			ctx,
			client,
			http.MethodGet,
			probeURL,
		)

		// GET succeeded.
		if err == nil && isHealthyStatus(status) {

			return models.UpstreamStatus{

				TenantID: target.TenantID,

				Name: target.Name,

				HealthPath: probePath(target),

				Healthy: true,

				StatusCode: status,

				LatencyMS: latency.Milliseconds(),

				CheckedAt: now,
			}
		}
	}

	// The HTTP request itself failed.
	//
	// Examples:
	//
	// • timeout
	// • DNS failure
	// • connection refused
	// • TLS handshake failure
	if err != nil {

		return models.UpstreamStatus{

			TenantID: target.TenantID,

			Name: target.Name,

			HealthPath: probePath(target),

			Healthy: false,

			StatusCode: status,

			LatencyMS: latency.Milliseconds(),

			CheckedAt: now,

			// Include the transport error for debugging.
			Error: err.Error(),
		}
	}

	// The request completed successfully, but the server returned an
	// unhealthy HTTP status.
	//
	// Example:
	//
	// HTTP 500
	// HTTP 503
	// HTTP 404
	return models.UpstreamStatus{

		TenantID: target.TenantID,

		Name: target.Name,

		HealthPath: probePath(target),

		// Determine health based on the returned status code.
		Healthy: isHealthyStatus(status),

		StatusCode: status,

		LatencyMS: latency.Milliseconds(),

		CheckedAt: now,

		// Convert the HTTP status into a readable message.
		Error: statusErrorMessage(status),
	}
}

// doProbe performs one HTTP health probe.
//
// Why this exists:
// The request details are shared by HEAD and GET probes, so the logic belongs here.
func doProbe(
	ctx context.Context,
	client *http.Client,
	method string,
	rawURL string,
) (int, time.Duration, error) {
	start := time.Now().UTC()

	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return 0, 0, err
	}

	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return 0, latency, err
	}
	defer resp.Body.Close()

	// Drain nothing here; health checks should be tiny.
	return resp.StatusCode, latency, nil
}

// buildProbeURL builds the full probe URL for one upstream.
//
// Example:
//
//	BaseURL:   https://api.acme.internal
//	HealthPath:/health
//	Result:    https://api.acme.internal/health
func buildProbeURL(target models.UpstreamTarget) (string, error) {
	baseURL, err := url.Parse(strings.TrimSpace(target.BaseURL))
	if err != nil {
		return "", err
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return "", fmt.Errorf("invalid upstream base url")
	}

	p := probePath(target)
	if p == "" {
		p = "/health"
	}

	baseURL.Path = joinPath(baseURL.Path, p)
	return baseURL.String(), nil
}

// probePath returns the configured upstream health path or a safe default.
func probePath(target models.UpstreamTarget) string {
	p := strings.TrimSpace(target.HealthPath)
	if p == "" {
		return "/health"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// joinPath joins URL path pieces predictably.
//
// Why this exists:
// We want a clean path join without losing meaning or accidentally collapsing
// path fragments too aggressively.
func joinPath(basePath, extraPath string) string {
	basePath = strings.TrimSpace(basePath)
	extraPath = strings.TrimSpace(extraPath)

	if basePath == "" {
		basePath = "/"
	}
	if extraPath == "" {
		extraPath = "/"
	}

	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if !strings.HasPrefix(extraPath, "/") {
		extraPath = "/" + extraPath
	}

	switch {
	case basePath == "/":
		return extraPath
	case extraPath == "/":
		return basePath
	default:
		return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(extraPath, "/")
	}
}

// isHealthyStatus treats 2xx and 3xx responses as healthy.
//
// Why this exists:
// Some health endpoints redirect or otherwise return non-2xx but still indicate
// that the service is reachable. For a starter probe, 2xx/3xx is acceptable.
func isHealthyStatus(statusCode int) bool {
	return statusCode >= 200 && statusCode < 400
}

// statusErrorMessage converts a bad status into a readable probe message.
func statusErrorMessage(statusCode int) string {
	if isHealthyStatus(statusCode) {
		return ""
	}
	return fmt.Sprintf("upstream returned status %d", statusCode)
}

// now returns the current time.
func (h *HealthChecker) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock().UTC()
}

// IsReady returns whether readiness should be reported as ready.
//
// Why this exists:
// It keeps the HTTP layer simple.
func (h *HealthChecker) IsReady(ctx context.Context) bool {
	return h.probeDatabase(ctx).Healthy
}

// AnyUpstreamUnhealthy reports whether any configured upstream failed its probe.
//
// Why this exists:
// The readiness response can expose degraded upstreams without making the whole
// gateway unready.
func (h *HealthChecker) AnyUpstreamUnhealthy(ctx context.Context) bool {
	for _, upstream := range h.probeUpstreams(ctx) {
		if !upstream.Healthy {
			return true
		}
	}
	return false
}

// ProbeSummary gives a compact view of upstream health.
//
// Why this exists:
// Useful for future admin APIs and dashboards.
type ProbeSummary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
}
