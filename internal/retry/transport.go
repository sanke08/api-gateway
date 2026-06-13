package retry

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

// The complete real production example:

// Client
//    |
//    | GET /orders
//    |
//    v

// Gateway
//    |
//    v

// RetryTransport

// Attempt #1
//    |
//    v

// orders-service

// 503 Service Unavailable
//    ^
//    |
// retry

// Attempt #2
//    |
//    v

// orders-service

// timeout
//    ^
//    |
// retry

// Attempt #3
//    |
//    v

// orders-service

// 200 OK

// response returned

// Without this transport:

// Client
//    |
//    v
// Gateway
//    |
//    v
// orders-service

// 503

// FAIL

// With this transport:

// Client
//    |
//    v
// Gateway
//    |
//    v
// Retry Transport

// 503
// timeout
// 200

// SUCCESS

// Client
//    |
//    v
// Gateway
//    |
//    v
// Reverse Proxy
//    |
//    v
// Retry Transport   <-- this file
//    |
//    v
// HTTP Transport
//    |
//    v
// Upstream Service

//-----------------------------------------------------------------------

// What problem does this solve?

// Without retry:
// Gateway
//    |
//    v
// Orders API

// Request #1
//      |
//      X timeout

// Client gets error immediately

//-----------------------------------------------------------------------

// Transport is a retry-enabled HTTP transport.
//
// Think:
//
// Normal Transport:
//
//	Request
//	   |
//	   v
//	Upstream
//
// If upstream fails:
//
//	X
//
// request fails immediately.
//
// ------------------------------------------------
//
// Retry Transport:
//
//	Request
//	   |
//	   v
//	Attempt #1
//	   |
//	   X timeout
//
//	wait
//
//	Attempt #2
//	   |
//	   X timeout
//
//	wait
//
//	Attempt #3
//	   |
//	   ✓ success
//
// response returned
//
// ------------------------------------------------
//
// Why this exists:
//
// Short network hiccups happen frequently:
//
// - temporary DNS issues
// - TCP resets
// - upstream restart
// - load balancer failover
//
// Retrying a few times often recovers automatically.
type Transport struct {

	// next is the real transport that actually performs HTTP requests.
	//
	// Example:
	//
	// RetryTransport
	//      |
	//      v
	// next.RoundTrip(...)
	//
	//
	// Usually this is:
	//
	//	http.Transport
	//
	// The retry transport does not send requests itself.
	// It delegates actual network work to next.
	next   http.RoundTripper
	set    settings
	random *rng
}

// NewTransport creates a retrying transport around another transport.
//
// Why this constructor exists:
// It validates retry behavior once at startup instead of re-evaluating policy
// structure on every request.
func NewTransport(next http.RoundTripper, policy Policy) (*Transport, error) {
	if next == nil {
		return nil, fmt.Errorf("next transport is required")
	}

	set, err := normalizePolicy(policy)
	if err != nil {
		return nil, err
	}

	return &Transport{
		next:   next,
		set:    set,
		random: newRng(),
	}, nil
}

// RoundTrip sends the request upstream and retries temporary failures when safe.
//
// Retry rules:
// - if the method is not allowed, do one attempt only
// - if the body cannot be replayed, do one attempt only
// - if the response status is not retryable, return it immediately
// - if the network error is not retryable, return it immediately
//
// Why this matters:
// Retries are useful only when they are controlled and safe.
// RoundTrip is the heart of the retry transport.
//
// Example:
//
// Policy:
//
//	Attempts = 3
//
// Request:
//
//	GET /orders
//
// Flow:
//
// Attempt #1
//
//	timeout
//
// wait
//
// Attempt #2
//
//	502
//
// wait
//
// Attempt #3
//
//	200 OK
//
// return response
//
// ------------------------------------------------
//
// If all attempts fail:
//
// Attempt #1 -> timeout
// Attempt #2 -> timeout
// Attempt #3 -> timeout
//
// return error
//
// ------------------------------------------------
//
// Why this exists:
//
// It hides temporary upstream failures from clients.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("request is required")
	}
	if t == nil || t.next == nil {
		return nil, errors.New("transport is not configured")
	}

	allowedToRetry := t.canRetry(req)

	for attempt := 1; attempt <= t.set.attempts; attempt++ {
		attemptReq, err := cloneRequest(req)
		if err != nil {
			return nil, err
		}

		resp, err := t.next.RoundTrip(attemptReq)
		if err == nil {
			if !allowedToRetry {
				return resp, nil
			}

			if !defaultStatusAllowed(resp.StatusCode, t.set.statusCodes) || attempt == t.set.attempts {
				return resp, nil
			}

			// The response is retryable, so we must close it before trying again.
			// This avoids leaking bodies and connections.
			drainAndClose(resp.Body)

			delay := nextBackoff(t.set.delay, t.set.maxDelay, t.set.jitter, attempt, t.random)
			if err := waitWithContext(req.Context(), delay); err != nil {
				return nil, err
			}

			continue
		}

		if !allowedToRetry || attempt == t.set.attempts || !isRetryableError(err) {
			return nil, err
		}

		delay := nextBackoff(t.set.delay, t.set.maxDelay, t.set.jitter, attempt, t.random)
		if err := waitWithContext(req.Context(), delay); err != nil {
			return nil, err
		}
	}

	return nil, errors.New("retry transport reached an unexpected state")
}

// canRetry checks whether the current request is safe enough to retry.
//
// Why this exists:
// We should not retry non-idempotent requests blindly.
//
// Conservative default:
// - methods must be in the allowed method set
// - request body must be replayable
// canRetry determines whether retrying is safe.
//
// Not every request should be retried.
//
// Example:
//
// GET /users
//
// Safe:
//
// Reading data multiple times changes nothing.
//
// ------------------------------------------------
//
// POST /payment
//
// Dangerous:
//
// First request may have already charged
// the customer's credit card.
//
// Retrying could charge twice.
//
// ------------------------------------------------
//
// Therefore:
//
// Only approved methods are retried.
//
// Body must also be replayable.
func (t *Transport) canRetry(req *http.Request) bool {
	if req == nil {
		return false
	}

	if !defaultMethodAllowed(req.Method, t.set.methods) {
		return false
	}

	return bodyReplayable(req)
}

// bodyReplayable checks whether the request body can be sent again.
//
// Why this matters:
// If the request body cannot be rebuilt, retries could send an empty or partial body.
// bodyReplayable checks whether the request body
// can be recreated for another attempt.
//
// Example:
//
// POST /upload
//
// Body:
//
//	file.pdf
//
// First attempt reads:
//
//	file.pdf -> EOF
//
// ------------------------------------------------
//
// Retry needs:
//
//	file.pdf again
//
// If request cannot recreate body:
//
// retry is impossible.
//
// ------------------------------------------------
//
// GetBody solves this:
//
// Request
//
//	|
//	+--> GetBody()
//	         |
//	         v
//	   fresh reader
//
// Every retry gets a brand-new reader.
func bodyReplayable(req *http.Request) bool {
	if req == nil {
		return false
	}

	if req.Body == nil || req.Body == http.NoBody {
		return true
	}

	// If GetBody exists, the request can be recreated safely for each retry.
	return req.GetBody != nil
}

// cloneRequest creates a replayable copy of the request.
//
// Why this exists:
// Each retry must use a fresh request body reader.
// Reusing the same body reader would fail after the first attempt.
// cloneRequest creates a completely fresh request
// for every retry attempt.
//
// Why this matters:
//
// HTTP request bodies are streams.
//
// After first attempt:
//
// request.Body
//
// is already consumed.
//
// ------------------------------------------------
//
// Example:
//
// Attempt #1
//
// POST /orders
// Body:
//
// {"id":1}
//
// Stream consumed.
//
// ------------------------------------------------
//
// Attempt #2
//
// Must create:
//
// {"id":1}
//
// again.
//
// Otherwise:
//
// empty body
//
// would be sent.
//
// ------------------------------------------------
//
// cloneRequest guarantees each retry gets
// a fresh body reader.
func cloneRequest(req *http.Request) (*http.Request, error) {
	if req == nil {
		return nil, errors.New("request is required")
	}

	clone := req.Clone(req.Context())

	if req.Body == nil || req.Body == http.NoBody {
		clone.Body = http.NoBody
		return clone, nil
	}

	if req.GetBody == nil {
		return nil, errors.New("request body cannot be replayed")
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}

	clone.Body = body
	return clone, nil
}

// isRetryableError checks whether a transport error is worth retrying.
//
// Why this exists:
// We only want to retry short-lived network problems.
// Determines whether a network failure is temporary.
//
// Retry:
//
// timeout
// connection reset
// temporary network failure
//
// ------------------------------------------------
//
// Do not retry:
//
// context canceled
//
// Example:
//
// Client closed browser tab.
//
// There is no reason to keep retrying.
//
// ------------------------------------------------
//
// Do not retry:
//
// request deadline exceeded
//
// Caller already gave up.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Never retry if the caller already gave up.
	if errors.Is(err, contextDeadlineErr) || errors.Is(err, contextCanceledErr) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		if temp, ok := any(netErr).(interface{ Temporary() bool }); ok && temp.Temporary() {
			return true
		}
	}

	return false
}

// drainAndClose reads and closes the body so the connection can be reused.
//
// Why this matters:
// When we decide to retry, we must fully consume or close the prior response body.
// Reads remaining response bytes
// and closes the response body.
//
// Example:
//
// Attempt #1
//
// Response:
//
// 503 Service Unavailable
//
// We decide to retry.
//
// ------------------------------------------------
//
// Before retrying:
//
// response body MUST be closed.
//
// Otherwise:
//
// - connection leak
// - socket leak
// - pool exhaustion
//
// ------------------------------------------------
//
// drainAndClose allows connection reuse.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}

	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

// These sentinel errors keep the retry transport simple without importing context
// into every helper.
//
// Why this exists:
// We only need to compare against these if the request context is canceled.
var (
	contextCanceledErr = errors.New("context canceled")
	contextDeadlineErr = errors.New("context deadline exceeded")
)

// normalizeContextErr maps context-style errors to the sentinel values above.
//
// Why this exists:
// It keeps the retry package self-contained and easy to reason about.
func normalizeContextErr(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, contextCanceledErr) {
		return contextCanceledErr
	}
	if errors.Is(err, contextDeadlineErr) {
		return contextDeadlineErr
	}

	return err
}

// bufferBody is a helper that can be used later if you decide to allow
// retries for replayable buffered bodies.
//
// Why this exists:
// Sometimes a request body must be buffered to retry safely.
// For now we keep retries conservative and do not buffer by default.
// Future helper for body buffering.
//
// Example:
//
// POST /import
//
// Body:
//
// 5 KB JSON
//
// ------------------------------------------------
//
// Read once:
//
// []byte
//
// Store in memory.
//
// ------------------------------------------------
//
// Retry:
//
// bytes.NewReader(...)
//
// creates a fresh stream every attempt.
//
// ------------------------------------------------
//
// Current implementation does NOT do this.
//
// It relies on:
//
// req.GetBody()
//
// which is safer and simpler.
func bufferBody(body io.ReadCloser) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	defer body.Close()

	return io.ReadAll(body)
}

// bodyFromBytes creates a fresh body reader from bytes.
//
// Why this exists:
// When a request body is buffered, each retry needs a new reader.
func bodyFromBytes(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}

// mutex is only here to keep the file compile-safe if you later extend it with
// request buffering or shared stats.
//
// Why this exists:
// It makes the transport easy to grow without changing the retry contract.
var _ sync.Locker
