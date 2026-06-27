package models

import "time"

// LivenessStatus describes whether the gateway process itself is alive.
//
// Why this exists:
// A liveness check should be cheap and should not depend on upstream systems.
// It answers only one question:
//
//	Is the gateway process running?
//
// Why this is not the same as readiness:
// A process can be alive but still not ready to serve traffic.
//
// Example response:
//
//	{
//	  "status": "healthy",
//	  "started_at": "...",
//	  "checked_at": "...",
//	  "uptime_seconds": 1234
//	}
type LivenessStatus struct {
	// Status is the current liveness state.
	// For this phase, it will usually be "healthy".
	Status string `json:"status"`

	// StartedAt is the time the gateway process started.
	StartedAt time.Time `json:"started_at"`

	// CheckedAt is the time the liveness status was computed.
	CheckedAt time.Time `json:"checked_at"`

	// UptimeSeconds is the total runtime of the gateway process.
	UptimeSeconds int64 `json:"uptime_seconds"`
}

// ReadinessStatus describes whether the gateway is ready to accept traffic.
//
// Why this exists:
// Readiness should verify core dependencies such as the database.
// It may also include upstream probe results for visibility.
//
// Important design choice:
// An unhealthy tenant upstream should be visible here, but it should not
// automatically make the whole gateway unready. The circuit breaker already
// handles failing upstreams at request time.
type ReadinessStatus struct {
	// Status is the current readiness state.
	// For this phase it will be "ready" or "not_ready".
	Status string `json:"status"`

	// CheckedAt is when the readiness check was performed.
	CheckedAt time.Time `json:"checked_at"`

	// Database tells us whether the primary database dependency is reachable.
	Database DatabaseStatus `json:"database"`

	// Upstreams contains the latest probe result for each configured upstream.
	Upstreams []UpstreamStatus `json:"upstreams"`
}

// DatabaseStatus describes the database dependency health.
//
// Why this exists:
// The database is a core dependency for configuration, identity, and usage.
type DatabaseStatus struct {
	// Healthy says whether the database responded successfully.
	Healthy bool `json:"healthy"`

	// LatencyMS is how long the ping took in milliseconds.
	LatencyMS int64 `json:"latency_ms"`

	// CheckedAt is when the database probe completed.
	CheckedAt time.Time `json:"checked_at"`

	// Error contains a human-readable failure message if the probe failed.
	Error string `json:"error,omitempty"`
}

// UpstreamStatus describes the probe result for one tenant backend.
//
// Why this exists:
// The gateway needs to know which upstreams are alive without forcing the
// whole system to go unready when one tenant backend is broken.
type UpstreamStatus struct {
	// TenantID identifies the tenant that owns this upstream.
	TenantID string `json:"tenant_id"`

	// Name is the human-readable upstream name.
	Name string `json:"name"`

	// HealthPath is the probe path used for the check.
	HealthPath string `json:"health_path"`

	// Healthy says whether the upstream responded successfully.
	Healthy bool `json:"healthy"`

	// StatusCode is the HTTP status returned by the upstream probe.
	StatusCode int `json:"status_code"`

	// LatencyMS is how long the upstream probe took in milliseconds.
	LatencyMS int64 `json:"latency_ms"`

	// CheckedAt is when the probe completed.
	CheckedAt time.Time `json:"checked_at"`

	// Error contains a human-readable probe error if the check failed.
	Error string `json:"error,omitempty"`
}
