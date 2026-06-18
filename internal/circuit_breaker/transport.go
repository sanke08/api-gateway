package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Transport wraps another HTTP RoundTripper and blocks traffic when the circuit is open.
//
// Why this exists:
// The proxy uses the transport boundary to decide whether a request may reach
// the upstream.
//
// Why this is the correct layer:
// The breaker should be as close as possible to the upstream request, not in
// handlers and not in routing logic.
// Transport sits between the ReverseProxy and the actual upstream.
//
// Normal flow without circuit breaker:
//
//	Client
//	   |
//	   v
//	Gateway
//	   |
//	   v
//	http.Transport
//	   |
//	   v
//	Upstream
//
// Flow with circuit breaker:
//
//	Client
//	   |
//	   v
//	Gateway
//	   |
//	   v
//	CircuitBreakerTransport
//	   |
//	   v
//	http.Transport
//	   |
//	   v
//	Upstream
//
// Why this exists:
//
// Before every upstream call, we want one place that asks:
//
//	"Is this upstream healthy enough to receive traffic?"
//
// If the answer is:
//
//	NO
//
// we stop immediately and never touch the upstream.
//
// This protects:
//
//   - the upstream
//   - the gateway
//   - the network
//
// from repeated failure storms.
type Transport struct {
	next    http.RoundTripper
	breaker *Breaker
	target  string
}

// NewTransport creates a circuit-breaking transport.
//
// Why this constructor exists:
// It validates the transport setup once and keeps the runtime path simple.
// NewTransport wires a breaker and an HTTP transport together.
//
// Example:
//
//	baseTransport := http.DefaultTransport
//
//	breaker, _ := circuitbreaker.New(policy)
//
//	transport, _ := circuitbreaker.NewTransport(
//	    baseTransport,
//	    breaker,
//	    "orders-api",
//	)
//
// Why validation happens here:
//
// Bad:
//
//	transport starts
//	request arrives
//	nil pointer crash
//
// Better:
//
//	server startup fails immediately
//
// This follows the fail-fast principle.
//
// What each field means:
//
// next
//
//	The real transport that actually sends requests.
//
// breaker
//
//	The state machine that decides whether traffic is allowed.
//
// target
//
//	Human-readable name used in logs and errors.
func NewTransport(next http.RoundTripper, breaker *Breaker, target string) (*Transport, error) {
	if next == nil {
		return nil, errors.New("next transport is required")
	}
	if breaker == nil {
		return nil, errors.New("breaker is required")
	}

	return &Transport{
		next:    next,
		breaker: breaker,
		target:  target,
	}, nil
}

// RoundTrip sends the request upstream only if the breaker allows it.
//
// How it behaves:
// - if the breaker is open, return an OpenError immediately
// - if the request succeeds, report success to the breaker
// - if the request fails, report failure to the breaker
// - if the response status is a configured failure status, count it as failure
// RoundTrip is the heart of the circuit breaker transport.
//
// Every upstream request passes through this function.
//
// Complete flow:
//
//	Request
//	   |
//	   v
//	breaker.Allow()
//	   |
//	   +---- NO ---> return OpenError
//	   |
//	   +---- YES
//	            |
//	            v
//	     next.RoundTrip()
//	            |
//	            v
//	      success/failure
//	            |
//	            v
//	     report result
//
// Why this matters:
//
// The breaker learns from every request.
//
// Successful requests:
//
//	ReportSuccess()
//
// Failed requests:
//
//	ReportFailure()
//
// This feedback loop is what allows the breaker to open and close automatically.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request is required")
	}
	if t == nil || t.next == nil || t.breaker == nil {
		return nil, errors.New("circuit breaker transport is not configured")
	}

	// Step 1:
	//
	// Ask the breaker whether traffic is currently allowed.
	//
	// Example:
	//
	// StateClosed
	//
	// Result:
	//
	//	allowed = true
	//
	// Request proceeds.
	//
	// ----------------------------------
	//
	// Example:
	//
	// StateOpen
	//
	// openDuration = 10s
	//
	// breaker opened 3s ago
	//
	// Result:
	//
	//	allowed = false
	//	retryAfter = 7s
	//
	// Request never reaches upstream.
	now := time.Now().UTC()
	allowed, retryAfter := t.breaker.Allow(now)
	// If the breaker is OPEN we fail immediately.
	//
	// Example:
	//
	// Upstream is completely down.
	//
	// Circuit opened 5 seconds ago.
	//
	// Client sends:
	//
	//	GET /orders
	//
	// Instead of:
	//
	//	Gateway -> Upstream -> Timeout
	//
	// we do:
	//
	//	Gateway -> OpenError
	//
	// instantly.
	//
	// This saves:
	//
	//   - latency
	//   - CPU
	//   - sockets
	//   - retries
	//
	// RetryAfter tells the caller:
	//
	// "Try again later."
	if !allowed {
		return nil, &OpenError{
			RetryAfter: retryAfter,
			Target:     t.target,
		}
	}

	resp, err := t.next.RoundTrip(req)
	if err != nil {
		if isFailureError(err) {
			t.breaker.ReportFailure(time.Now().UTC())
		}
		return nil, err
	}

	if resp == nil {
		t.breaker.ReportFailure(time.Now().UTC())
		return nil, errors.New("upstream returned a nil response")
	}

	if t.breaker.failureStatusAllowed(resp.StatusCode) {
		t.breaker.ReportFailure(time.Now().UTC())
	} else {
		t.breaker.ReportSuccess(time.Now().UTC())
	}

	return resp, nil
}

// isFailureError decides whether an error should count
// toward opening the circuit.
//
// Not all errors are equal.
//
// Example:
//
// User closes browser.
//
// Request context is canceled.
//
// That does NOT mean:
//
// upstream is unhealthy.
//
// Therefore:
//
// do NOT punish the breaker.
//
// --------------------------------
//
// Example:
//
// connection refused
//
// That DOES mean:
//
// upstream probably has a problem.
//
// Therefore:
//
// count as failure.
func isFailureError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return true
}

// RetryAfterDuration extracts retry information from an OpenError.
//
// Example:
//
//	err := &OpenError{
//	    RetryAfter: 10 * time.Second,
//	}
//
//	duration, ok := RetryAfterDuration(err)
//
// Result:
//
//	duration = 10s
//	ok = true
//
// Useful for:
//
//	Retry-After: 10
//
// HTTP headers.
func RetryAfterDuration(err error) (time.Duration, bool) {
	var openErr *OpenError
	if !errors.As(err, &openErr) {
		return 0, false
	}

	return openErr.RetryAfter, true
}

// IsOpenError reports whether the error came from an open circuit.
//
// Why this exists:
// The proxy error handler needs to distinguish an open breaker from other
// upstream transport failures.
// IsOpenError checks whether an error came from an OPEN circuit.
//
// Example:
//
//	err := transport.RoundTrip(req)
//
//	if circuitbreaker.IsOpenError(err) {
//	    // return 503
//	}
//
// Why this exists:
//
// The proxy layer may want different behavior for:
//
//	open circuit
//
// versus
//
//	real network failure
func IsOpenError(err error) bool {
	return errors.Is(err, ErrOpenCircuit)
}

// String returns the target name for logs and diagnostics.
//
// Why this exists:
// It is useful for error messages and observability.
// String provides a readable transport description.
//
// Example:
//
//	fmt.Println(transport)
//
// Output:
//
//	circuitbreaker transport for orders-api
//
// Useful for:
//
//   - logs
//   - debugging
//   - metrics
//
// without exposing internal memory addresses.
func (t *Transport) String() string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("circuitbreaker transport for %s", t.target)
}
