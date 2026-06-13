package retry

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrInvalidPolicy means the retry policy cannot be used safely.
//
// Why this exists:
// Retry settings must fail fast when they are not meaningful.
// A broken retry policy is worse than no retry policy.
var ErrInvalidPolicy = errors.New("retry policy is invalid")

// Policy describes when and how the gateway should retry an upstream request.
//
// Why this struct exists:
// The retry behavior should be explicit and easy to read.
// Instead of scattering retry constants across the codebase, one Policy value
// captures the full intent.
//
// What the fields mean:
//
//   - Attempts
//     Total number of tries including the very first request.
//     If Attempts = 3, the gateway sends the request once and retries at most 2 times.
//     Zero or negative means "use the default of 3".
//
//   - Delay
//     How long to wait before the first retry.
//     This is the base unit for exponential backoff — subsequent retries
//     multiply this value by a power of 2 (see nextBackoff).
//     Zero or negative means "use the default of 50ms".
//
//   - MaxDelay
//     The ceiling on how long a single wait can be.
//     Even if exponential backoff would produce a 10-second delay, MaxDelay
//     caps it so the gateway never waits longer than this value between tries.
//     Zero or negative means "use the default of 1 second".
//
//   - Jitter
//     A maximum amount of random extra time added to each backoff wait.
//     Without jitter, every failing instance retries at the exact same moment
//     which creates a retry storm. With jitter each instance waits a slightly
//     different amount so the upstream is not hit simultaneously.
//     Zero means no jitter. Negative is invalid.
//
//   - Methods
//     HTTP methods the policy is allowed to retry.
//     Only safe (idempotent) methods should be retried. Retrying a POST could
//     create duplicate records on the upstream service.
//     If empty, defaults to: GET, HEAD, OPTIONS, PUT, DELETE.
//     POST and PATCH are intentionally excluded from the default set.
//
//   - StatusCodes
//     Upstream HTTP response status codes that should trigger a retry.
//     For example: 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout.
//     These indicate a temporary upstream problem, not a client error.
//     If empty, defaults to: 502, 503, 504.
//
// Example — aggressive retry for a critical read path:
//
//	p := Policy{
//	    Attempts:    5,                       // first request + 4 retries
//	    Delay:       100 * time.Millisecond,  // wait 100ms before retry 1
//	    MaxDelay:    2 * time.Second,         // never wait more than 2s between tries
//	    Jitter:      50 * time.Millisecond,   // add up to 50ms random extra wait
//	    Methods:     []string{"GET"},
//	    StatusCodes: []int{502, 503, 504},
//	}
//
// Example — zero value uses all safe defaults:
//
//	p := Policy{}
//	// Attempts=3, Delay=50ms, MaxDelay=1s, Jitter=0
//	// Methods: GET HEAD OPTIONS PUT DELETE
//	// StatusCodes: 502 503 504
type Policy struct {
	Attempts    int
	Delay       time.Duration
	MaxDelay    time.Duration
	Jitter      time.Duration
	Methods     []string
	StatusCodes []int
}

// settings is the normalized internal form of Policy.
//
// Why this exists:
// Policy is a public struct the caller fills in, so it may have zero values or
// incomplete data. settings is the private, validated, ready-to-use version.
//
// The key difference:
// Policy uses slices ([]string, []int) because that is convenient to write.
// settings converts them to maps (map[string]struct{}, map[int]struct{}) so
// method and status code lookups are O(1) instead of O(n).
//
// Example — after normalizePolicy(Policy{}) the settings hold:
//
//	s.attempts    = 3
//	s.delay       = 50ms
//	s.maxDelay    = 1s
//	s.jitter      = 0
//	s.methods     = {"GET":{}, "HEAD":{}, "OPTIONS":{}, "PUT":{}, "DELETE":{}}
//	s.statusCodes = {502:{}, 503:{}, 504:{}}
type settings struct {
	attempts    int
	delay       time.Duration
	maxDelay    time.Duration
	jitter      time.Duration
	methods     map[string]struct{}
	statusCodes map[int]struct{}
}

// normalizePolicy validates the caller's Policy and fills in safe defaults.
//
// Why this exists:
// The caller should not have to specify every field. normalizePolicy applies
// defaults for any zero value and rejects settings that are clearly wrong
// (e.g. negative jitter, blank method name, status code outside 100–599).
//
// Defaults applied:
//   - Attempts <= 0  → 3
//   - Delay <= 0     → 50ms
//   - MaxDelay <= 0  → 1s
//   - MaxDelay < Delay → raised to equal Delay (so the cap is never below base)
//   - Methods empty  → GET HEAD OPTIONS PUT DELETE
//   - StatusCodes empty → 502 503 504
//
// Example — partial policy, missing fields filled in:
//
//	s, err := normalizePolicy(Policy{Attempts: 2})
//	// s.attempts    = 2
//	// s.delay       = 50ms  (default)
//	// s.maxDelay    = 1s    (default)
//	// s.methods     = {GET HEAD OPTIONS PUT DELETE}
//	// s.statusCodes = {502 503 504}
//
// Example — negative jitter is rejected:
//
//	_, err := normalizePolicy(Policy{Jitter: -1})
//	// err == ErrInvalidPolicy
//
// Example — status code outside valid HTTP range is rejected:
//
//	_, err := normalizePolicy(Policy{StatusCodes: []int{999}})
//	// err == ErrInvalidPolicy
func normalizePolicy(p Policy) (settings, error) {
	if p.Attempts <= 0 {
		p.Attempts = 3
	}
	if p.Attempts < 1 {
		return settings{}, ErrInvalidPolicy
	}

	if p.Delay <= 0 {
		p.Delay = 50 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = time.Second
	}
	if p.MaxDelay < p.Delay {
		p.MaxDelay = p.Delay
	}
	if p.Jitter < 0 {
		return settings{}, ErrInvalidPolicy
	}

	methods := p.Methods
	if len(methods) == 0 {
		methods = []string{
			http.MethodGet,
			http.MethodHead,
			http.MethodOptions,
			http.MethodPut,
			http.MethodDelete,
		}
	}

	methodSet := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			return settings{}, ErrInvalidPolicy
		}
		methodSet[method] = struct{}{}
	}

	statusCodes := p.StatusCodes
	if len(statusCodes) == 0 {
		statusCodes = []int{502, 503, 504}
	}

	statusSet := make(map[int]struct{}, len(statusCodes))
	for _, code := range statusCodes {
		if code < 100 || code > 599 {
			return settings{}, ErrInvalidPolicy
		}
		statusSet[code] = struct{}{}
	}

	return settings{
		attempts:    p.Attempts,
		delay:       p.Delay,
		maxDelay:    p.MaxDelay,
		jitter:      p.Jitter,
		methods:     methodSet,
		statusCodes: statusSet,
	}, nil
}

// rng is a mutex-protected random number generator used to compute jitter.
//
// Why this exists:
// Jitter reduces retry bursts (a "thundering herd") when many requests fail at
// the same time and all retry at once. By adding a small random delay each
// instance waits a slightly different amount so the upstream is not hit all
// at once.
//
// Why the mutex:
// rand.Rand is not goroutine-safe. The transport may retry concurrent requests,
// so the mutex prevents a data race on the shared source.
//
// Example — two concurrent retries get different waits:
//
//	r := newRng()
//	delay1 := r.int63n(50_000_000) // might return 12ms
//	delay2 := r.int63n(50_000_000) // might return 37ms  ← different, no storm
type rng struct {
	mu  sync.Mutex
	src *rand.Rand
}

func newRng() *rng {
	return &rng{
		src: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0)),
	}
}

func (r *rng) int63n(n int64) int64 {
	if n <= 0 {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return int64(r.src.Uint64() % uint64(n))
}

// defaultMethodAllowed reports whether the given HTTP method is in the allowed set.
//
// The lookup is case-insensitive: "get", "GET", and "Get" all match.
//
// Example:
//
//	allowed := map[string]struct{}{"GET": {}, "HEAD": {}}
//	defaultMethodAllowed("get", allowed)  // true
//	defaultMethodAllowed("POST", allowed) // false
func defaultMethodAllowed(method string, allowed map[string]struct{}) bool {
	_, ok := allowed[strings.ToUpper(strings.TrimSpace(method))]
	return ok
}

// defaultStatusAllowed reports whether the given HTTP status code is in the allowed set.
//
// Example:
//
//	allowed := map[int]struct{}{502: {}, 503: {}, 504: {}}
//	defaultStatusAllowed(503, allowed) // true
//	defaultStatusAllowed(200, allowed) // false
func defaultStatusAllowed(code int, allowed map[int]struct{}) bool {
	_, ok := allowed[code]
	return ok
}

// sleepDuration is a function type that pauses execution for a given duration.
//
// Why this exists:
// The real implementation calls time.Sleep (or waitWithContext).
// In tests the transport can swap in a no-op function so tests run instantly
// without actually sleeping.
//
// Example — real usage:
//
//	var sleep sleepDuration = func(d time.Duration) error {
//	    return waitWithContext(ctx, d)
//	}
//
// Example — test usage (no actual sleep):
//
//	var sleep sleepDuration = func(d time.Duration) error { return nil }
type sleepDuration func(time.Duration) error

// waitWithContext sleeps for duration d, but wakes up early if ctx is canceled.
//
// Why this exists:
// A retry must stop the moment the client disconnects or times out. Without
// context awareness the gateway would keep sleeping and retrying after the
// client no longer cares about the response.
//
// How it works:
// A timer fires after d. A select races the timer against ctx.Done().
// Whichever happens first wins. If the context wins, ctx.Err() (typically
// context.Canceled or context.DeadlineExceeded) is returned so the caller
// can stop.
//
// Example — normal sleep completes:
//
//	ctx := context.Background()
//	err := waitWithContext(ctx, 200*time.Millisecond)
//	// sleeps ~200ms, err == nil
//
// Example — client cancels mid-sleep:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	go func() { time.Sleep(50*time.Millisecond); cancel() }()
//	err := waitWithContext(ctx, 5*time.Second)
//	// returns after ~50ms with err == context.Canceled
func waitWithContext(ctx interface {
	Done() <-chan struct{}
	Err() error
}, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// nextBackoff calculates how long to wait before the next retry attempt.
//
// Why this exists:
// Exponential backoff avoids pounding a struggling upstream with rapid retries.
// Each attempt doubles the wait compared to the previous one. The cap (max)
// prevents the wait from growing unboundedly. Jitter spreads concurrent retries
// so they do not all hit the upstream at the same instant.
//
// How the math works:
//
//	multiplier = 2^(attempt-1)
//	raw delay  = base * multiplier   (capped at max)
//	final      = raw + random(0, jitter)  (capped at max again)
//
// Timeline with base=100ms, max=1s, jitter=0:
//
//	attempt 1 (first request) → no delay yet (caller decides when to call)
//	attempt 2 (first retry)   → 100ms   (2^(2-1) * 100ms = 1 * 100ms)
//	attempt 3 (second retry)  → 200ms   (2^(3-1) * 100ms = 2 * 100ms)
//	attempt 4 (third retry)   → 400ms   (2^(4-1) * 100ms = 4 * 100ms)
//	attempt 5 (fourth retry)  → 800ms
//	attempt 6                 → 1000ms  (capped at max)
//
// Example — no jitter:
//
//	d := nextBackoff(100*time.Millisecond, time.Second, 0, 3, nil)
//	// d == 400ms  (2^(3-1) * 100ms)
//
// Example — with jitter (result varies):
//
//	r := newRng()
//	d := nextBackoff(100*time.Millisecond, time.Second, 50*time.Millisecond, 2, r)
//	// d is somewhere between 100ms and 150ms
func nextBackoff(base time.Duration, max time.Duration, jitter time.Duration, attempt int, random *rng) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	// multiplier doubles with each attempt using a left bit-shift.
	// 1 << (attempt-1) is equivalent to 2^(attempt-1).
	//
	// attempt=1 → 1 << 0 = 1  → delay = 1 × base  (first request, no extra wait)
	// attempt=2 → 1 << 1 = 2  → delay = 2 × base  (first retry)
	// attempt=3 → 1 << 2 = 4  → delay = 4 × base  (second retry)
	// attempt=4 → 1 << 3 = 8  → delay = 8 × base  (third retry)
	//
	// Why bit-shift instead of math.Pow:
	// Shifting an integer left by 1 is the same as multiplying by 2.
	// It avoids floating-point conversion and is the idiomatic Go way to
	// express powers of 2 over small integers.
	multiplier := 1 << (attempt - 1)

	delay := min(time.Duration(multiplier)*base, max)

	if jitter > 0 && random != nil {
		extra := time.Duration(random.int63n(int64(jitter) + 1))
		delay += extra
		if delay > max {
			delay = max
		}
	}

	return delay
}

// retryableBody reports whether the request body can be safely re-sent on a retry.
//
// Why this matters:
// An HTTP request body is a one-time stream — like reading from a pipe.
// Once the upstream server reads it, the stream is exhausted. If you retry
// the request without resetting the body, the second attempt sends an empty
// body, which silently corrupts the request (e.g. a POST with no JSON payload).
//
// The safe cases are:
//   - no body at all   → nothing to replay, always safe
//   - req.GetBody set  → http.NewRequest stores a factory function here that
//                        recreates the body from scratch, so replay is safe
//
// The unsafe case:
//   - body present but no GetBody → the stream was already consumed, cannot replay
//
// This function is a STUB (not yet implemented).
// It always returns false, meaning "treat every body as unsafe to replay."
// This is intentionally conservative: the transport will skip retrying any
// request that has a body until the real check is wired in.
//
//	_ = req   ← silences the "unused parameter" linter; req will be inspected
//	            once the real implementation replaces this stub.
func retryableBody(req any) bool {
	_ = req
	return false
}
