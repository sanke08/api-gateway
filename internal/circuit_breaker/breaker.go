package circuitbreaker

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
)

// ErrOpenCircuit means the breaker is currently open and traffic should not be
// sent to the upstream.
//
// Why this exists:
// The proxy layer needs a stable error to detect "do not call upstream right now".
var ErrOpenCircuit = errors.New("circuit breaker is open")

// OpenError is returned when the circuit breaker is OPEN.
//
// Why a custom error exists:
//
// A normal error only says:
//
//	"circuit breaker is open"
//
// But sometimes the gateway needs more information:
//
// - how long until retry is allowed
// - which upstream is failing
//
// Example:
//
// Orders API is down.
//
// Circuit state:
//
//	OPEN
//
// Retry allowed after:
//
//	15 seconds
//
// Error:
//
//	OpenError{
//	    RetryAfter: 15 * time.Second,
//	    Target: "orders-api",
//	}
//
// Gateway can then return:
//
//	HTTP 503 Service Unavailable
//	Retry-After: 15
//
// instead of a generic failure.
type OpenError struct {

	// RetryAfter tells callers how long until another attempt
	// should be made.
	//
	// Example:
	//
	// Circuit opened at:
	//
	//	10:00:00
	//
	// Open duration:
	//
	//	30 seconds
	//
	// Current time:
	//
	//	10:00:10
	//
	// RetryAfter:
	//
	//	20 seconds
	RetryAfter time.Duration

	// Target is an optional human-readable upstream name.
	//
	// Example:
	//
	//	orders-api
	//	payment-service
	//	auth-service
	//
	// Useful for logs and debugging.
	Target string
}

// Error implements the built-in error interface.
//
// Why this method exists:
//
// In Go, any type becomes an error when it implements:
//
//	func Error() string
//
// Since OpenError has extra information (RetryAfter, Target),
// it cannot just be a plain errors.New(...).
//
// We still want:
//
//	return &OpenError{...}
//
// to behave like a normal error.
//
// Therefore we implement Error().
//
// --------------------------------------------------------------------
//
// Example:
//
//	err := &OpenError{
//	    RetryAfter: 15 * time.Second,
//	    Target: "orders-api",
//	}
//
//	fmt.Println(err)
//
// Go automatically calls:
//
//	err.Error()
//
// Output:
//
//	"circuit breaker is open: orders-api"
//
// --------------------------------------------------------------------
//
// Why nil is checked:
//
// Example:
//
//	var err *OpenError
//
// err is nil.
//
// Calling:
//
//	err.Error()
//
// would panic if we tried:
//
//	err.Target
//
// Therefore:
//
//	if e == nil {
//	    return ErrOpenCircuit.Error()
//	}
//
// safely returns:
//
//	"circuit breaker is open"
//
// --------------------------------------------------------------------
//
// Why Target is checked:
//
// Example:
//
//	&OpenError{
//	    RetryAfter: 10*time.Second,
//	    Target: "",
//	}
//
// We do not want:
//
//	"circuit breaker is open: "
//
// So if Target is empty we simply return:
//
//	"circuit breaker is open"
//
// --------------------------------------------------------------------
//
// Examples:
//
//	OpenError{
//		    Target: "orders-api",
//	}
//
// returns:
//
//	"circuit breaker is open: orders-api"
//
//	OpenError{
//		    Target: "",
//	}
//
// returns:
//
//	"circuit breaker is open"
func (e *OpenError) Error() string {
	if e == nil {
		return ErrOpenCircuit.Error()
	}

	target := strings.TrimSpace(e.Target)
	if target == "" {
		return ErrOpenCircuit.Error()
	}

	return fmt.Sprintf("%s: %s", ErrOpenCircuit.Error(), target)
}

// Unwrap exposes the underlying root error.
//
// Why this exists:
//
// OpenError contains extra information:
//
//	RetryAfter
//	Target
//
// but logically it still represents:
//
//	ErrOpenCircuit
//
// Go provides error wrapping so higher layers can ask:
//
// "is this error fundamentally an open circuit?"
//
// without caring about additional metadata.
//
// --------------------------------------------------------------------
//
// Example:
//
//	err := &OpenError{
//	    RetryAfter: 10*time.Second,
//	    Target: "orders-api",
//	}
//
// This is NOT:
//
//	err == ErrOpenCircuit
//
// because OpenError and ErrOpenCircuit are different values.
//
// Without Unwrap:
//
//	errors.Is(err, ErrOpenCircuit)
//
// would return:
//
//	false
//
// --------------------------------------------------------------------
//
// With Unwrap:
//
// Go does:
//
//	err
//	  |
//
// //	  v
//
//	OpenError
//	  |
//	  v
//	ErrOpenCircuit
//
// and therefore:
//
//	errors.Is(err, ErrOpenCircuit)
//
// returns:
//
//	true
//
// --------------------------------------------------------------------
//
// Real gateway example:
//
//	err := breaker.Allow()
//
//	if errors.Is(err, ErrOpenCircuit) {
//	    return HTTP 503
//	}
//
// The caller does not care whether:
//
//	OpenError
//	CustomOpenError
//	AnotherWrappedError
//
// was returned.
//
// It only cares:
//
// "is the circuit open?"
//
// Unwrap makes that possible.
//
// --------------------------------------------------------------------
//
// Visualization:
//
// OpenError
//
//	|
//	+-- RetryAfter: 30s
//	|
//	+-- Target: orders-api
//	|
//	+-- Unwrap()
//	       |
//	       v
//	 ErrOpenCircuit
//
// errors.Is() walks this chain automatically.
func (e *OpenError) Unwrap() error {
	return ErrOpenCircuit
}

// State represents the current health state of one upstream.
//
// Think of a circuit breaker like an electrical fuse.
//
// Normal:
//
//	traffic flows
//
// Repeated failures:
//
//	fuse trips
//
// Recovery:
//
//	test carefully before allowing full traffic again.
//
// The breaker moves through:
//
//	CLOSED
//	   |
//	   | failures exceed threshold
//	   v
//	OPEN
//	   |
//	   | open timeout expires
//	   v
//	HALF_OPEN
//	   |
//	   | enough successful probes
//	   v
//	CLOSED
//
// or
//
//	HALF_OPEN
//	   |
//	   | probe fails
//	   v
//	OPEN
type State string

const (
	// StateClosed means traffic is flowing normally.
	StateClosed State = "closed"

	// StateOpen means the upstream is considered unhealthy.
	//
	// Traffic behavior:
	//
	//	Client
	//	   |
	//	   v
	//	Circuit Breaker
	//	   |
	//	   X
	//	   |
	//	Blocked
	//
	// No request reaches the upstream.
	//
	// Why:
	//
	// The upstream is already failing.
	//
	// Sending more traffic would:
	//
	// - waste resources
	// - increase latency
	// - make recovery harder
	//
	// Example:
	//
	// FailureThreshold = 5
	//
	// Requests:
	//
	//	fail
	//	fail
	//	fail
	//	fail
	//	fail
	//
	// Circuit opens.
	//
	// Next requests:
	//
	//	rejected immediately
	//
	// No upstream call is made.
	StateOpen State = "open"

	// StateHalfOpen means the breaker is testing whether the
	// upstream has recovered.
	//
	// Traffic behavior:
	//
	// Most traffic is still blocked.
	//
	// Only a very small number of requests are allowed through.
	//
	// Example:
	//
	// ProbeLimit = 1
	//
	// Circuit was OPEN.
	//
	// Timeout expires.
	//
	// First request:
	//
	//	allowed through
	//
	// Second request:
	//
	//	blocked
	//
	// until the first probe finishes.
	//
	// Why:
	//
	// We do not want thousands of requests hitting a service
	// that may still be broken.
	StateHalfOpen State = "half_open"
)

// Policy is the validated runtime configuration.
//
// Why this exists:
//
// Public config may contain:
//
//	FailureThreshold = 0
//	ProbeLimit = 0
//
// which is invalid.
//
// During startup we normalize everything once.
//
// Requests later use this ready-to-use structure.
//
// This avoids:
//
// - repeated validation
// - repeated allocations
// - repeated configuration parsing
type Policy struct {

	// failureThreshold is how many consecutive failures
	// are required before opening the circuit.
	//
	// Example:
	//
	// threshold = 5
	//
	// Results:
	//
	//	fail (1)
	//	fail (2)
	//	fail (3)
	//	fail (4)
	//	fail (5)
	//
	// Circuit opens.
	failureThreshold int

	// openDuration is how long the breaker remains OPEN.
	//
	// Example:
	//
	//	openDuration = 30s
	//
	// Circuit opens:
	//
	//	10:00:00
	//
	// Next probe allowed:
	//
	//	10:00:30
	openDuration time.Duration

	// probeLimit controls how many test requests
	// may run while HALF_OPEN.
	//
	// Example:
	//
	// probeLimit = 2
	//
	// HALF_OPEN:
	//
	// Request 1 -> allowed
	// Request 2 -> allowed
	// Request 3 -> blocked
	//
	// until one finishes.
	probeLimit int

	// successThreshold controls how many successful probes
	// are required before closing the breaker.
	//
	// Example:
	//
	// successThreshold = 3
	//
	// HALF_OPEN:
	//
	// success (1)
	// success (2)
	// success (3)
	//
	// Circuit closes.
	successThreshold int

	// failureStatusCodes are HTTP responses that count
	// as upstream failures.
	//
	// Example:
	//
	// 500 Internal Server Error
	// 502 Bad Gateway
	// 503 Service Unavailable
	// 504 Gateway Timeout
	//
	// Request succeeded at network level,
	// but business-wise the upstream failed.
	failureStatusCodes map[int]struct{}
}

// Breaker manages the complete state machine for one upstream.
//
// Example:
//
// Gateway
//
//	|
//	+---- Orders API Breaker
//	|
//	+---- Billing API Breaker
//	|
//	+---- Auth API Breaker
//
// Each upstream gets its own breaker.
//
// Why:
//
// One unhealthy service should not affect other services.
type Breaker struct {

	// mu protects all breaker state.
	//
	// Thousands of requests may hit the breaker concurrently.
	//
	// Without this lock:
	//
	// goroutine A -> update failure count
	// goroutine B -> update failure count
	//
	// race condition.
	mu sync.Mutex

	// policy contains the validated breaker configuration.
	//
	// Example:
	//
	// threshold = 5
	// openDuration = 30s
	policy Policy

	// current state.
	//
	// CLOSED
	// OPEN
	// HALF_OPEN
	state State

	// consecutiveFailures counts failures while CLOSED.
	//
	// Example:
	//
	// threshold = 5
	//
	// fail -> 1
	// fail -> 2
	// fail -> 3
	//
	// success:
	//
	// reset to 0
	//
	// because failures are no longer consecutive.
	consecutiveFailures int

	// openedAt records when the breaker entered OPEN.
	//
	// Example:
	//
	// openDuration = 30s
	//
	// openedAt:
	//
	//	10:00:00
	//
	// current:
	//
	//	10:00:15
	//
	// still OPEN.
	openedAt time.Time

	// halfOpenProbes counts currently running probes.
	//
	// Example:
	//
	// probeLimit = 2
	//
	// probe 1 running
	// probe 2 running
	//
	// count = 2
	//
	// next probe rejected.
	halfOpenProbes int

	// halfOpenSuccesses counts successful probes.
	//
	// Example:
	//
	// successThreshold = 3
	//
	// probe success -> 1
	// probe success -> 2
	// probe success -> 3
	//
	// circuit closes.
	halfOpenSuccesses int
}

// New creates a breaker from the public policy.
//
// Why this constructor exists:
// It validates the policy once at startup instead of on the hot path.
// New creates a new circuit breaker.
//
// Why this constructor exists:
//
// The public configuration comes from:
//
//	models.CircuitBreakerPolicy
//
// Example:
//
//	CircuitBreakerPolicy{
//	    FailureThreshold: 5,
//	    OpenDuration: 30*time.Second,
//	    ProbeLimit: 1,
//	    SuccessThreshold: 1,
//	}
//
// Before using it, we validate and normalize it once.
//
// Why:
//
// We do NOT want every request doing:
//
//	if threshold <= 0 ...
//	if probeLimit <= 0 ...
//
// Validation belongs at startup.
//
// ------------------------------------------------------------------
//
// Startup flow:
//
//	main.go
//	   |
//	   v
//	CircuitBreakerPolicy
//	   |
//	   v
//	New()
//	   |
//	   v
//	normalize()
//	   |
//	   v
//	Breaker
//
// ------------------------------------------------------------------
//
// Initial state:
//
// Every breaker starts CLOSED.
//
// Meaning:
//
//	client
//	   |
//	   v
//	gateway
//	   |
//	   v
//	upstream
//
// Traffic flows normally.
//
// ------------------------------------------------------------------
//
// Example:
//
//	breaker, _ := New(policy)
//
// Result:
//
//	breaker.state == StateClosed
//
//	breaker.consecutiveFailures == 0
//
// No failures have happened yet.
func New(policy models.CircuitBreakerPolicy) (*Breaker, error) {
	norm, err := normalize(policy)
	if err != nil {
		return nil, err
	}

	return &Breaker{
		policy: norm,
		state:  StateClosed,
	}, nil
}

// Allow decides whether a request is allowed to reach the upstream.
//
// Think of this as the gatekeeper.
//
// Every request must ask:
//
//	"Can I go to the upstream?"
//
// before making the network call.
//
// ------------------------------------------------------------------
//
// Request flow:
//
//	Client
//	   |
//	   v
//	Breaker.Allow()
//	   |
//	   +---- YES -> call upstream
//	   |
//	   +---- NO  -> return immediately
//
// ------------------------------------------------------------------
//
// CLOSED state:
//
// State:
//
//	CLOSED
//
// Meaning:
//
// Upstream is healthy.
//
// Result:
//
//	return true, 0
//
// Request proceeds normally.
//
// ------------------------------------------------------------------
//
// OPEN state:
//
// State:
//
//	OPEN
//
// Meaning:
//
// Upstream recently failed too many times.
//
// Example:
//
// FailureThreshold = 5
//
// Failures:
//
//	fail
//	fail
//	fail
//	fail
//	fail
//
// Circuit opens.
//
// ------------------------------------------------------------------
//
// While OPEN:
//
// Every request gets blocked.
//
// Example:
//
// openedAt      = 10:00:00
// openDuration  = 30s
//
// Request arrives:
//
//	10:00:10
//
// Elapsed:
//
//	10 seconds
//
// Remaining:
//
//	20 seconds
//
// Result:
//
//	false, 20s
//
// ------------------------------------------------------------------
//
// After timeout expires:
//
// openedAt      = 10:00:00
// openDuration  = 30s
//
// Request arrives:
//
//	10:00:31
//
// Open period finished.
//
// Circuit transitions:
//
//	OPEN
//	  |
//	  v
//	HALF_OPEN
//
// Reset probe counters:
//
//	halfOpenProbes = 0
//	halfOpenSuccesses = 0
//
// Then allow HALF_OPEN logic to decide.
//
// ------------------------------------------------------------------
//
// HALF_OPEN state:
//
// Upstream might be healthy again.
//
// We do not trust it yet.
//
// Only a few probe requests are allowed.
//
// Example:
//
// ProbeLimit = 1
//
// First request:
//
//	allowed
//
// Second request:
//
//	rejected
//
// until the probe completes.
//
// ------------------------------------------------------------------
//
// Visual:
//
// CLOSED
//
//	|
//	| failures
//	v
//
// OPEN
//
//	|
//	| timeout expires
//	v
//
// HALF_OPEN
//
//	|
//	| successful probes
//	v
//
// CLOSED
func (b *Breaker) Allow(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true, 0

	case StateOpen:
		if now.Sub(b.openedAt) < b.policy.openDuration {
			retryAfter := max(b.policy.openDuration-now.Sub(b.openedAt), 0)
			return false, retryAfter
		}

		// Open duration has elapsed, so we move to half-open.
		b.state = StateHalfOpen
		b.halfOpenProbes = 0
		b.halfOpenSuccesses = 0
		return b.allowHalfOpen(now)

	case StateHalfOpen:
		return b.allowHalfOpen(now)

	default:
		// Unknown state should be treated as unavailable.
		return false, b.policy.openDuration
	}
}

// ReportSuccess tells the breaker:
//
// "The upstream request succeeded."
//
// Why this exists:
//
// Circuit breakers learn from results.
//
// After every upstream call:
//
// success -> ReportSuccess()
// failure -> ReportFailure()
//
// ------------------------------------------------------------------
//
// CLOSED state behavior:
//
// Example:
//
// consecutiveFailures = 3
//
// Success arrives.
//
// We know failures are no longer consecutive.
//
// Therefore:
//
//	consecutiveFailures = 0
//
// ------------------------------------------------------------------
//
// Why reset?
//
// Example:
//
//	fail
//	fail
//	fail
//	success
//
// These failures are no longer a continuous failure streak.
//
// Future failures start counting again from zero.
//
// ------------------------------------------------------------------
//
// HALF_OPEN behavior:
//
// Example:
//
// Circuit was OPEN.
//
// Timeout expires.
//
// Circuit becomes HALF_OPEN.
//
// Probe request sent.
//
// Request succeeds.
//
// ------------------------------------------------------------------
//
// Internal state:
//
// halfOpenProbes--
//
// because one probe finished.
//
// ------------------------------------------------------------------
//
// Then:
//
// halfOpenSuccesses++
//
// Example:
//
// successThreshold = 3
//
// Probe #1 success:
//
//	successes = 1
//
// Probe #2 success:
//
//	successes = 2
//
// Probe #3 success:
//
//	successes = 3
//
// ------------------------------------------------------------------
//
// Once enough probes succeed:
//
// successes >= successThreshold
//
// and
//
// no probes remain in flight
//
// Circuit closes.
//
// State:
//
//	HALF_OPEN
//	     |
//	     v
//	   CLOSED
//
// Traffic returns to normal.
//
// ------------------------------------------------------------------
//
// Example:
//
// OPEN
//
//	|
//
// timeout
//
//	|
//
// HALF_OPEN
//
//	|
//
// success
// success
// success
//
//	|
//
// CLOSED
func (b *Breaker) ReportSuccess(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.consecutiveFailures = 0

	case StateHalfOpen:
		if b.halfOpenProbes > 0 {
			b.halfOpenProbes--
		}
		b.halfOpenSuccesses++

		// Once enough probes succeed and no probes are in flight, close the circuit.
		if b.halfOpenSuccesses >= b.policy.successThreshold && b.halfOpenProbes == 0 {
			b.state = StateClosed
			b.consecutiveFailures = 0
			b.halfOpenSuccesses = 0
		}
	}
}

// ReportFailure tells the breaker:
//
// "The upstream request failed."
//
// Why this exists:
//
// The breaker must detect unhealthy upstreams.
//
// Every failure increases confidence that the service
// may be down.
//
// ------------------------------------------------------------------
//
// CLOSED state behavior:
//
// Example:
//
// FailureThreshold = 5
//
// Requests:
//
//	fail -> 1
//	fail -> 2
//	fail -> 3
//	fail -> 4
//	fail -> 5
//
// Threshold reached.
//
// Circuit opens.
//
// ------------------------------------------------------------------
//
// State transition:
//
//	CLOSED
//	   |
//	   v
//	OPEN
//
// ------------------------------------------------------------------
//
// Why open?
//
// Without a breaker:
//
// Client requests:
//
//	1000/sec
//
// Broken upstream:
//
//	1000 failures/sec
//
// Gateway keeps hammering the service.
//
// With a breaker:
//
// After threshold:
//
// Requests are blocked locally.
//
// Upstream gets a chance to recover.
//
// ------------------------------------------------------------------
//
// HALF_OPEN behavior:
//
// HALF_OPEN means:
//
// "I think recovery might have happened."
//
// Probe request is sent.
//
// If that probe fails:
//
// Recovery failed.
//
// Circuit immediately reopens.
//
// ------------------------------------------------------------------
//
// Example:
//
// OPEN
//
//	|
//
// timeout
//
//	|
//
// HALF_OPEN
//
//	|
//
// probe request
//
//	|
//
// FAIL
//
//	|
//
// # OPEN
//
// ------------------------------------------------------------------
//
// Internal updates:
//
// halfOpenProbes--
//
// because probe completed.
//
// Then:
//
// open(now)
//
// resets breaker back to OPEN.
//
// open(now) typically:
//
//	state = OPEN
//	openedAt = now
//
// A new waiting period begins.
func (b *Breaker) ReportFailure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.consecutiveFailures++
		if b.consecutiveFailures >= b.policy.failureThreshold {
			b.open(now)
		}

	case StateHalfOpen:
		if b.halfOpenProbes > 0 {
			b.halfOpenProbes--
		}
		b.open(now)
	}
}

// State returns the current circuit breaker state.
//
// Possible states:
//
//	StateClosed
//	StateOpen
//	StateHalfOpen
//
// Why this method exists:
//
// Other parts of the gateway may want to inspect the breaker:
//
// Examples:
//   - metrics
//   - monitoring dashboards
//   - admin endpoints
//   - debugging
//
// Example:
//
//	state := breaker.State()
//
//	if state == StateOpen {
//	    log.Println("upstream currently blocked")
//	}
//
// Why locking is required:
//
// Requests may be changing the breaker state concurrently:
//
//	Request A -> ReportFailure()
//	Request B -> ReportSuccess()
//	Request C -> State()
//
// Without the mutex, State() could read partially updated values.
//
// Flow:
//
//	lock
//	read state
//	unlock
//	return state
//
// This operation is very cheap because it only reads one field.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// open transitions the breaker into OPEN state.
//
// OPEN means:
//
// "Do not send traffic to the upstream anymore."
//
// Why this exists:
//
// Multiple places may need to open the breaker:
//
//  1. Too many consecutive failures while CLOSED
//  2. A probe failure while HALF-OPEN
//
// Instead of duplicating logic, everything calls open().
//
// Example:
//
// Configuration:
//
//	FailureThreshold = 5
//
// Request results:
//
//	Fail
//	Fail
//	Fail
//	Fail
//	Fail
//
// After the 5th failure:
//
//	open(now)
//
// Result:
//
//	state = OPEN
//
// What happens after opening:
//
// Before:
//
//	StateClosed
//
// After:
//
//	StateOpen
//
// Additional cleanup:
//
// consecutiveFailures = 0
//
// Why reset?
//
// Once the circuit opens,
// old failure history is no longer useful.
//
// openedAt = now
//
// Why store this?
//
// Later Allow() calculates:
//
//	now - openedAt
//
// to know:
//
// "Has the open timeout expired?"
//
// Example:
//
//	openedAt = 12:00:00
//	openDuration = 10s
//
// At:
//
//	12:00:08
//
// still open.
//
// At:
//
//	12:00:11
//
// move to HALF-OPEN.
//
// halfOpenProbes = 0
// halfOpenSuccesses = 0
//
// Why reset?
//
// Any old probe data belongs to a previous recovery attempt
// and should not affect the next one.
func (b *Breaker) open(now time.Time) {
	b.state = StateOpen
	b.openedAt = now
	b.consecutiveFailures = 0
	b.halfOpenProbes = 0
	b.halfOpenSuccesses = 0
}

// allowHalfOpen decides whether a recovery probe is allowed.
//
// HALF-OPEN means:
//
// "The upstream might be healthy again.
// Let's test it carefully."
//
// Why this state exists:
//
// Imagine:
//
// # Upstream crashes
//
// # Breaker opens
//
// After 10 seconds:
//
// Upstream might be back.
//
// We do NOT want to immediately send:
//
//	50,000 requests
//
// because:
//
// If upstream is still unhealthy,
// we create another outage.
//
// Instead:
//
// allow a very small number of test requests.
//
// Example:
//
// ProbeLimit = 1
//
// Current state:
//
//	StateHalfOpen
//
// First request:
//
//	allowHalfOpen()
//
// halfOpenProbes = 0
//
// Since:
//
//	0 < 1
//
// allow request.
//
// Then:
//
//	halfOpenProbes++
//
// Result:
//
//	halfOpenProbes = 1
//
// Second request arrives immediately.
//
// Now:
//
//	halfOpenProbes = 1
//
// Since:
//
//	1 >= ProbeLimit
//
// reject request.
//
// Why reject?
//
// One probe is already testing recovery.
//
// No reason to send more traffic until we know the result.
//
// Retry duration:
//
//	250ms
//
// This is not a precise timer.
//
// It simply tells callers:
//
//	"Try again shortly."
//
// If a probe is allowed:
//
// Before:
//
//	halfOpenProbes = 0
//
// After:
//
//	halfOpenProbes = 1
//
// The request proceeds to the upstream.
//
// Later:
//
// ReportSuccess()
// or
// ReportFailure()
//
// will decrement halfOpenProbes.
func (b *Breaker) allowHalfOpen(now time.Time) (bool, time.Duration) {
	if b.halfOpenProbes >= b.policy.probeLimit {
		// We do not have a separate timer for this case.
		// Returning a small retry duration is enough to keep traffic from piling up.
		return false, 250 * time.Millisecond
	}

	b.halfOpenProbes++
	return true, 0
}

// normalize converts the public configuration into a validated runtime policy.
//
// Why this exists:
//
// Users may provide:
//
//	CircuitBreakerPolicy{}
//
// with lots of zero values.
//
// The breaker should not repeatedly validate configuration
// on every request.
//
// Instead:
//
// Startup:
//
//	User config
//	    |
//	    v
//	normalize()
//	    |
//	    v
//	Validated Policy
//
// Requests:
//
//	Use Policy directly
//
// No repeated validation.
//
// Example:
//
// Input:
//
//	CircuitBreakerPolicy{
//	    FailureThreshold: 0,
//	    OpenDuration: 0,
//	    ProbeLimit: 0,
//	    SuccessThreshold: 0,
//	}
//
// normalize() replaces them with sensible defaults.
//
// Result:
//
//	FailureThreshold = 5
//	OpenDuration     = 10s
//	ProbeLimit       = 1
//	SuccessThreshold = 1
//
// Why fail fast?
//
// Bad configuration should crash startup immediately.
//
// Better:
//
//	server fails during boot
//
// than:
//
//	server starts and behaves incorrectly under traffic.
func normalize(policy models.CircuitBreakerPolicy) (Policy, error) {
	if policy.FailureThreshold <= 0 {
		policy.FailureThreshold = 5
	}
	if policy.FailureThreshold <= 0 {
		return Policy{}, errors.New("failure threshold must be greater than zero")
	}

	if policy.OpenDuration <= 0 {
		policy.OpenDuration = 10 * time.Second
	}
	if policy.OpenDuration <= 0 {
		return Policy{}, errors.New("open duration must be greater than zero")
	}

	if policy.ProbeLimit <= 0 {
		policy.ProbeLimit = 1
	}
	if policy.ProbeLimit <= 0 {
		return Policy{}, errors.New("probe limit must be greater than zero")
	}

	if policy.SuccessThreshold <= 0 {
		policy.SuccessThreshold = 1
	}
	if policy.SuccessThreshold <= 0 {
		return Policy{}, errors.New("success threshold must be greater than zero")
	}

	statusCodes := policy.FailureStatusCodes
	if len(statusCodes) == 0 {
		statusCodes = []int{500, 502, 503, 504}
	}

	statusSet := make(map[int]struct{}, len(statusCodes))
	for _, code := range statusCodes {
		if code < http.StatusContinue || code > http.StatusNetworkAuthenticationRequired {
			return Policy{}, fmt.Errorf("invalid failure status code: %d", code)
		}
		statusSet[code] = struct{}{}
	}

	return Policy{
		failureThreshold:   policy.FailureThreshold,
		openDuration:       policy.OpenDuration,
		probeLimit:         policy.ProbeLimit,
		successThreshold:   policy.SuccessThreshold,
		failureStatusCodes: statusSet,
	}, nil
}

// failureStatusAllowed checks whether an HTTP response
// should be treated as a circuit-breaker failure.
//
// Example:
//
// Config:
//
//	FailureStatusCodes:
//	    500
//	    502
//	    503
//	    504
//
// Response:
//
//	503 Service Unavailable
//
// Check:
//
//	failureStatusAllowed(503)
//
// Result:
//
//	true
//
// Then:
//
//	ReportFailure()
//
// Another example:
//
//	Response = 404
//
// Check:
//
//	failureStatusAllowed(404)
//
// Result:
//
//	false
//
// Why?
//
// The upstream is reachable.
//
// The client simply requested something that does not exist.
//
// Therefore:
//
// Do NOT punish the circuit breaker.
func (b *Breaker) failureStatusAllowed(code int) bool {
	_, ok := b.policy.failureStatusCodes[code]
	return ok
}
