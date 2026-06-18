package models

import "time"

// CircuitBreakerPolicy describes how the gateway should behave when an upstream
// starts failing repeatedly.
//
// Why this struct exists:
// Retry logic handles temporary problems.
// Circuit breaker logic handles repeated or systemic problems.
//
// Why the fields are named this way:
// The names should tell the truth immediately without forcing the reader to
// learn special abbreviations.
//
// Example:
//
//	CircuitBreakerPolicy{
//	    FailureThreshold: 5,
//	    OpenDuration:     10 * time.Second,
//	    ProbeLimit:       1,
//	    SuccessThreshold: 1,
//	    FailureStatusCodes: []int{500, 502, 503, 504},
//	}
//
// Meaning:
// - after 5 consecutive failures, the circuit opens
// - it stays open for 10 seconds
// - after that, 1 test request is allowed in half-open state
// - if that request succeeds, the circuit closes again
// CircuitBreakerPolicy controls how the gateway protects itself
// from unhealthy upstream services.
//
// ------------------------------------------------------------------
//
// Problem:
//
// Imagine:
//
//	Client
//	  |
//	  v
//	Gateway
//	  |
//	  v
//	Orders Service
//
// Orders Service suddenly crashes.
//
// Every request now becomes:
//
//	Client
//	  |
//	  v
//	Gateway
//	  |
//	  v
//	Orders Service
//	      |
//
// //	      X 500
//
// Without a circuit breaker:
//
// Request #1 -> 500
// Request #2 -> 500
// Request #3 -> 500
// Request #1000 -> 500
//
// Gateway keeps sending traffic forever.
//
// This makes recovery harder because the unhealthy service
// continues receiving requests while already struggling.
//
// ------------------------------------------------------------------
//
// Circuit breaker solution:
//
// CLOSED
//
//	|
//	| failures reach threshold
//	v
//
// OPEN
//
//	|
//	| wait OpenDuration
//	v
//
// HALF-OPEN
//
//	|
//	| probe succeeds
//	v
//
// # CLOSED
//
// # OR
//
// HALF-OPEN
//
//	|
//	| probe fails
//	v
//
// # OPEN
//
// ------------------------------------------------------------------
//
// State meanings:
//
// # CLOSED
//
// Normal operation.
//
// Requests:
//
//	Client
//	  |
//	  v
//	Gateway
//	  |
//	  v
//	Upstream
//
// All traffic is allowed.
//
// ------------------------------------------------------------------
//
// # OPEN
//
// Upstream is considered unhealthy.
//
// Requests:
//
//	Client
//	  |
//	  v
//	Gateway
//	  |
//	  X
//
// Gateway rejects immediately.
//
// No request reaches the upstream.
//
// This protects:
//
// - upstream service
// - gateway resources
// - client latency
//
// ------------------------------------------------------------------
//
// # HALF-OPEN
//
// Recovery testing state.
//
// Gateway allows only a few requests.
//
// Example:
//
// ProbeLimit = 1
//
// One request is allowed:
//
//	Client
//	  |
//	  v
//	Gateway
//	  |
//	  v
//	Upstream
//
// Success:
//
// HALF-OPEN -> CLOSED
//
// Failure:
//
// HALF-OPEN -> OPEN
//
// ------------------------------------------------------------------
//
// Example configuration:
//
//	CircuitBreakerPolicy{
//	    FailureThreshold: 5,
//	    OpenDuration:     10 * time.Second,
//	    ProbeLimit:       1,
//	    SuccessThreshold: 1,
//	    FailureStatusCodes: []int{
//	        500,
//	        502,
//	        503,
//	        504,
//	    },
//	}
//
// Meaning:
//
// After:
//
//	5 consecutive failures
//
// Circuit becomes OPEN.
//
// For:
//
//	10 seconds
//
// No traffic is sent upstream.
//
// After 10 seconds:
//
//	1 probe request
//
// is allowed.
//
// If probe succeeds:
//
//	circuit closes
//
// If probe fails:
//
//	circuit opens again
//
// ------------------------------------------------------------------
//
// Why circuit breakers exist:
//
// Retry:
//
//	handles short temporary failures.
//
// Circuit Breaker:
//
//	handles persistent failures.
//
// Together:
//
// Retry       -> recover small hiccups
// Circuit     -> stop hammering broken systems
//
// This combination is common in production gateways,
// service meshes, API gateways, and microservice platforms.
type CircuitBreakerPolicy struct {
	// FailureThreshold is the number of consecutive failures required to open
	// the circuit.
	//
	// Why this matters:
	// It controls how sensitive the breaker is.
	// A lower value makes it more aggressive; a higher value makes it more tolerant.
	// FailureThreshold is the number of consecutive failures
	// required before opening the circuit.
	//
	// Example:
	//
	// FailureThreshold = 5
	//
	// Requests:
	//
	// #1 -> 500
	// #2 -> 500
	// #3 -> 500
	// #4 -> 500
	// #5 -> 500
	//
	// Circuit:
	//
	// CLOSED -> OPEN
	//
	// ------------------------------------------------
	//
	// Why consecutive failures:
	//
	// One random failure should not disable
	// an otherwise healthy service.
	//
	// We only open after repeated failures.
	FailureThreshold int

	// OpenDuration is how long the circuit stays open before allowing a test probe.
	//
	// Why this matters:
	// When an upstream is unhealthy, the gateway should stop sending it traffic
	// for a short period instead of hammering it continuously.
	// OpenDuration is how long the circuit remains OPEN.
	//
	// Example:
	//
	// OpenDuration = 10 seconds
	//
	// Circuit opens:
	//
	// 12:00:00
	//
	// During:
	//
	// 12:00:00 - 12:00:10
	//
	// Every request is rejected immediately.
	//
	// No traffic reaches upstream.
	//
	// After:
	//
	// 12:00:10
	//
	// Circuit enters HALF-OPEN.
	//
	// ------------------------------------------------
	//
	// Think:
	//
	// "How long should we stop bothering the broken service?"
	OpenDuration time.Duration

	// ProbeLimit is the number of requests allowed to test recovery while the
	// circuit is half-open.
	//
	// Why this matters:
	// Half-open traffic should be very small so a failing upstream does not get
	// flooded during recovery.
	// ProbeLimit is how many requests are allowed
	// during HALF-OPEN state.
	//
	// Example:
	//
	// ProbeLimit = 1
	//
	// HALF-OPEN:
	//
	// First request:
	//
	//	Client
	//	  |
	//	  v
	//	Upstream
	//
	// Allowed.
	//
	// Second request:
	//
	//	Client
	//	  |
	//	  X
	//
	// Blocked.
	//
	// ------------------------------------------------
	//
	// Why this exists:
	//
	// If a service is recovering,
	// we do not want to flood it immediately.
	//
	// We test recovery carefully first.
	ProbeLimit int

	// SuccessThreshold is the number of successful probes required to close the
	// circuit from half-open.
	//
	// Why this matters:
	// One success may be enough for simple setups, but this field keeps the design
	// flexible.
	// SuccessThreshold is how many successful probes
	// are required before the circuit closes again.
	//
	// Example:
	//
	// SuccessThreshold = 1
	//
	// HALF-OPEN:
	//
	// Probe #1 -> 200 OK
	//
	// Circuit:
	//
	// HALF-OPEN -> CLOSED
	//
	// ------------------------------------------------
	//
	// Example:
	//
	// SuccessThreshold = 3
	//
	// Probe #1 -> 200
	// Probe #2 -> 200
	// Probe #3 -> 200
	//
	// Circuit:
	//
	// HALF-OPEN -> CLOSED
	//
	// ------------------------------------------------
	//
	// Higher values make recovery validation stricter.
	SuccessThreshold int

	// FailureStatusCodes are HTTP status codes that should count as upstream failures.
	//
	// Example:
	// 500, 502, 503, 504
	//
	// Why this matters:
	// A successful transport-level request may still represent a bad upstream result.
	// FailureStatusCodes are HTTP responses that count
	// as upstream failures.
	//
	// Example:
	//
	// 500 Internal Server Error
	// 502 Bad Gateway
	// 503 Service Unavailable
	// 504 Gateway Timeout
	//
	// These indicate the upstream is unhealthy.
	//
	// ------------------------------------------------
	//
	// Example:
	//
	// Request:
	//
	// GET /orders
	//
	// Response:
	//
	// 503 Service Unavailable
	//
	// Failure counter:
	//
	// +1
	//
	// ------------------------------------------------
	//
	// Important:
	//
	// Network success does NOT always mean
	// business success.
	//
	// Request may successfully reach the upstream,
	// but upstream can still return:
	//
	// 500
	//
	// which should count as a failure.
	FailureStatusCodes []int
}
