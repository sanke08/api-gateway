package ratelimit

import (
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

// ErrInvalidKey means the caller tried to rate-limit using an empty key.
// Why this exists:
// A rate limiter must always know what identity or scope it is limiting.
// An empty key usually means the middleware was wired incorrectly or the
// request context is missing the identity data we expected.
var ErrInvalidKey = errors.New("rate limit key is required")

// ErrInvalidRule means the supplied rate limit rule is not usable.
//
// Why this exists:
// A rule like "0 tokens per 0 seconds" does not make sense and should fail fast.
var ErrInvalidRule = errors.New("rate limit rule is invalid")

// Rule describes one token-bucket rate limit policy.
//
// Mental model:
//
// Imagine a bucket that stores tokens.
//
// Requests can only proceed if enough tokens exist.
//
// Every request consumes tokens.
// Time automatically refills tokens.
//
// Example:
//
//	TokensPerPeriod = 100
//	Period          = 1 minute
//	Capacity        = 200
//	Cost            = 1
//
// Means:
//
// Bucket size:
//
//	maximum 200 tokens
//
// Refill speed:
//
//	+100 tokens every minute
//
// Request cost:
//
//	-1 token per request
//
// Startup:
//
// Bucket starts full:
//
//	200 / 200 tokens
//
// Therefore:
//
//	first 200 requests can arrive instantly
//
// After bucket becomes empty:
//
//	new requests must wait for refill
//
// Refill example:
//
//	100 tokens per minute
//	= 1.666 tokens per second
//
// After:
//
//	30 seconds
//
// bucket receives:
//
//	~50 new tokens
//
// which allows roughly 50 more requests.
//
// Why this design exists:
//
// It allows:
//   - stable long-term request rate
//   - temporary traffic spikes
//   - smooth recovery after bursts
//
// without requiring fixed windows.
type Rule struct {
	// TokensPerPeriod controls refill speed.
	//
	// Think:
	//
	// "How many new tokens should appear during one Period?"
	//
	// Example:
	//
	//	TokensPerPeriod = 100
	//	Period          = 1 minute
	//
	// means:
	//
	//     +100 tokens every minute
	//
	// equivalent to:
	//
	//     +1.666 tokens per second
	//
	// Important:
	//
	// This is NOT the bucket size.
	//
	// It only controls refill speed.
	//
	// Bucket size is controlled by Capacity.
	TokensPerPeriod int

	// Period defines the refill window.
	//
	// Example:
	//
	//	TokensPerPeriod = 100
	//	Period          = 1 minute
	//
	// means:
	//
	//     100 tokens are added every minute
	//
	// Another example:
	//
	//	TokensPerPeriod = 1000
	//	Period          = 1 hour
	//
	// means:
	//
	//     1000 tokens are added every hour
	//
	// Think:
	//
	// "How often should refill happen?"
	Period time.Duration

	// Capacity is the maximum number of tokens
	// the bucket can store.
	//
	// Think:
	//
	// "How large is the bucket?"
	//
	// Example:
	//
	//	TokensPerPeriod = 100
	//	Period          = 1 minute
	//	Capacity        = 100
	//
	// Bucket:
	//
	//	[100 max tokens]
	//
	// Startup:
	//
	//	100 tokens available
	//
	// Therefore:
	//
	//	first 100 requests can happen immediately.
	//
	// ------------------------------------------------
	//
	// Example:
	//
	//	TokensPerPeriod = 100
	//	Period          = 1 minute
	//	Capacity        = 200
	//
	// Bucket:
	//
	//	[200 max tokens]
	//
	// Startup:
	//
	//	200 tokens available
	//
	// Therefore:
	//
	//	first 200 requests can happen immediately.
	//
	// Refill speed is STILL:
	//
	//	100 tokens per minute
	//
	// Capacity does NOT change refill speed.
	//
	// Capacity only controls how large a burst
	// can be absorbed.
	Capacity int

	// Cost is how many tokens one request consumes.
	//
	// Example:
	//
	//	Cost = 1
	//
	// request:
	//
	//     consumes 1 token
	//
	// --------------------------------
	//
	// Example:
	//
	//	Cost = 5
	//
	// request:
	//
	//     consumes 5 tokens
	//
	// Why this exists:
	//
	// Some endpoints are more expensive than others.
	//
	// Example:
	//
	// GET /health
	//     Cost = 1
	//
	// POST /generate-report
	//     Cost = 10
	//
	// This allows expensive operations to consume
	// more of the available rate-limit budget.
	Cost int
}

// Limiter manages all token buckets.
//
// Think:
//
// Every unique key gets
// its own bucket.
//
// Example:
//
// tenant:acme
// tenant:amazon
// tenant:flipkart
//
// Internally:
//
//	buckets = {
//	    "tenant:acme"     -> bucket,
//	    "tenant:amazon"   -> bucket,
//	    "tenant:flipkart" -> bucket,
//	}
//
// Each request:
//
// 1. find bucket
// 2. refill tokens
// 3. consume tokens
// 4. allow or reject
//
// This structure is safe for
// thousands of concurrent requests.
type Limiter struct {
	// buckets stores token buckets by identity.
	//
	// Example:
	//
	// Key:
	//
	// "tenant:acme"
	//
	// Bucket:
	//
	// {
	//     tokens: 78
	// }
	//
	// Another:
	//
	// "tenant:amazon"
	//
	// Bucket:
	//
	// {
	//     tokens: 34
	// }
	//
	// Why sync.Map:
	//
	// Thousands of goroutines may
	// read and write buckets concurrently.
	//
	// sync.Map provides safe concurrent access
	// without a single global mutex.
	buckets sync.Map

	// clock returns current time.
	//
	// Production:
	//
	// time.Now
	//
	// Tests:
	//
	// fakeClock.Now
	//
	// Example:
	//
	// testClock := func() time.Time {
	//     return fixedTime
	// }
	//
	// This allows tests to simulate
	// minutes or hours passing instantly
	// without sleeping.
	clock func() time.Time
}

// bucket stores the live token bucket state
// for one rate-limit key.
//
// Example:
//
// Key:
//
// tenant:acme
//
// Bucket:
//
//	{
//	    tokens: 42.5,
//	    lastRefill: 10:01:00,
//	}
//
// Another tenant:
//
// tenant:amazon
//
// gets a completely different bucket.
//
// Buckets never share state.
type bucket struct {
	mu sync.Mutex

	// availableTokens is the current token balance.
	//
	// Example:
	//
	// Capacity = 200
	//
	// Startup:
	//
	//     availableTokens = 200
	//
	// Request arrives:
	//
	//     availableTokens = 199
	//
	// Another request:
	//
	//     availableTokens = 198
	//
	// Time passes:
	//
	//     refill adds tokens back
	//
	// Example:
	//
	// after 30 seconds
	//
	//     availableTokens = 248
	//
	// but never exceeds Capacity.
	//
	// Think:
	//
	// "Current balance in the bucket."
	availableTokens float64

	// lastRefill tracks when the bucket
	// was last updated.
	//
	// Example:
	//
	// Last refill:
	//
	// 10:00:00
	//
	// Current time:
	//
	// 10:00:05
	//
	// Elapsed:
	//
	// 5 seconds
	//
	// Limiter computes:
	//
	// refillAmount = rate * elapsed
	//
	// Then updates tokens.
	lastRefillAt time.Time

	// lastSeenAt tracks the most recent request
	// touching this bucket.
	//
	// Example:
	//
	// Bucket:
	//
	// tenant:acme
	//
	// Last request:
	//
	// 3 hours ago
	//
	// Cleanup worker may remove it.
	//
	// Why:
	//
	// Prevent memory growth from
	// inactive users or tenants.
	lastSeenAt time.Time
}

// ruleState is the optimized internal version
// of Rule.
//
// User configuration:
//
// Requests = 100
// Per      = 1 minute
//
// becomes:
//
// rate  = 1.6667
// capacity = 100
// cost  = 1
//
// Why:
//
// Validation and calculations happen once.
//
// Request path should be as small
// and as fast as possible.
type ruleState struct {
	// tokensPerSecond is the refill rate after converting the rule to time.
	tokensPerSecond float64

	// bucketCapacity is the maximum token count a bucket can hold.
	bucketCapacity int

	// requestCost is how many tokens one request consumes.
	requestCost float64
}

// NewLimiter creates a limiter that uses time.Now.
//
// Why this constructor exists:
// It is the simplest production-friendly entry point.
func NewLimiter() *Limiter {
	return NewLimiterWithClock(time.Now)
}

// NewLimiterWithClock creates a limiter with a custom clock.
//
// Why this exists:
// Tests can inject a fake clock and advance time without waiting in real time.
func NewLimiterWithClock(clock func() time.Time) *Limiter {
	if clock == nil {
		clock = time.Now
	}

	return &Limiter{
		clock: clock,
	}
}

// Allow is the public entry point.
//
// Request
//
//	|
//	v
//
// Validate Rule
//
//	|
//	v
//
// Normalize Rule
//
//	|
//	v
//
// Internal Allow
//
//	|
//	v
//
// # Decision
//
// Example:
//
// allowed, retryAfter, err := limiter.Allow(
//
//	"tenant:acme",
//	Rule{
//	    Requests:100,
//	    Per:time.Minute,
//	},
//
// )
//
// Result:
//
// allowed=true
//
// or
//
// allowed=false
// retryAfter=15s
func (l *Limiter) Allow(key string, rule Rule) (bool, time.Duration, error) {
	normalized, err := normalizeRule(rule)
	if err != nil {
		return false, 0, err
	}

	return l.allow(key, normalized)
}

// allow is the hot path.
//
// This executes on every request.
//
// Steps:
//
// 1. Validate key
// 2. Load bucket
// 3. Create bucket if missing
// 4. Refill tokens
// 5. Consume tokens
// 6. Return decision
//
// Example:
//
// Request:
//
// key = "tenant:acme"
//
// Bucket lookup:
//
// buckets["tenant:acme"]
//
// Found:
//
// tokens = 5
//
// Cost:
//
// 1
//
// Remaining:
//
// 4
//
// Result:
//
// allowed = true
//
// This path must stay extremely fast
// because it may execute millions of times
// per minute.
func (l *Limiter) allow(key string, rule ruleState) (bool, time.Duration, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return false, 0, ErrInvalidKey
	}

	now := l.now().UTC()

	value, _ := l.buckets.LoadOrStore(key, newBucket(now, rule))
	b := value.(*bucket)

	allowed, retryAfter := b.allow(now, rule)
	return allowed, retryAfter, nil

}

// normalizeRule validates the rule and converts it into precomputed values.
//
// Why this exists:
// A limiter rule must be sane before it can be used safely.
func normalizeRule(rule Rule) (ruleState, error) {
	if rule.TokensPerPeriod <= 0 {
		return ruleState{}, ErrInvalidRule
	}
	if rule.Period <= 0 {
		return ruleState{}, ErrInvalidRule
	}

	capacity := rule.Capacity
	if capacity <= 0 {
		// If capacity is not set, default to the refill amount.
		// This gives a sensible burst size without extra configuration.
		capacity = rule.TokensPerPeriod
	}
	if capacity < 1 {
		return ruleState{}, ErrInvalidRule
	}

	cost := rule.Cost
	if cost <= 0 {
		cost = 1
	}

	tokensPerSecond := float64(rule.TokensPerPeriod) / rule.Period.Seconds()
	if tokensPerSecond <= 0 {
		return ruleState{}, ErrInvalidRule
	}

	return ruleState{
		tokensPerSecond: tokensPerSecond,
		bucketCapacity:  capacity,
		requestCost:     float64(cost),
	}, nil
}

// newBucket creates a fresh bucket with a full token balance.
//
// Why this matters:
// The first request for a key should usually be allowed immediately.
func newBucket(now time.Time, rule ruleState) *bucket {
	return &bucket{
		availableTokens: float64(rule.bucketCapacity),
		lastRefillAt:    now,
		lastSeenAt:      now,
	}
}

// allow performs the actual token bucket check.
//
// How token bucket works:
// - tokens refill over time up to capacity
// - each request consumes tokens
// - if there are not enough tokens, the request is denied
//
// Why this algorithm is good here:
// It allows short bursts while still enforcing a long-term average rate.
func (b *bucket) allow(now time.Time, rule ruleState) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Protect against clock drift or bad test clock behavior.
	if now.Before(b.lastRefillAt) {
		now = b.lastRefillAt
	}

	elapsedSeconds := now.Sub(b.lastRefillAt).Seconds()
	if elapsedSeconds > 0 {
		b.availableTokens += elapsedSeconds * rule.tokensPerSecond

		maxTokens := float64(rule.bucketCapacity)
		if b.availableTokens > maxTokens {
			b.availableTokens = maxTokens
		}

		b.lastRefillAt = now
	}

	b.lastSeenAt = now

	if b.availableTokens >= rule.requestCost {
		b.availableTokens -= rule.requestCost
		return true, 0
	}

	missingTokens := rule.requestCost - b.availableTokens
	waitSeconds := missingTokens / rule.tokensPerSecond
	if waitSeconds < 0 {
		waitSeconds = 0
	}

	wait := time.Duration(math.Ceil(waitSeconds * float64(time.Second)))
	if wait < time.Second {
		wait = time.Second
	}

	return false, wait
}

// now returns the current time.
//
// Why this helper exists:
// It keeps time access centralized and testable.
func (l *Limiter) now() time.Time {
	if l.clock == nil {
		return time.Now()
	}
	return l.clock()
}

// PruneIdle removes buckets that have not been used for longer than maxIdle.
//
// Why this exists:
// If the gateway sees many distinct keys over time, we should not keep dead
// buckets forever.
//
// How to use it:
// Call it periodically from a background goroutine or an admin task.
//
// Example:
//
//	limiter.PruneIdle(10 * time.Minute)
//
// This is optional cleanup, not required for correctness.
func (l *Limiter) PruneIdle(maxIdle time.Duration) int {
	if maxIdle <= 0 {
		return 0
	}

	now := l.now().UTC()
	removed := 0

	l.buckets.Range(func(key, value any) bool {
		b, ok := value.(*bucket)
		if !ok || b == nil {
			l.buckets.Delete(key)
			return true
		}

		b.mu.Lock()
		idleFor := now.Sub(b.lastSeenAt)
		b.mu.Unlock()

		if idleFor > maxIdle {
			l.buckets.Delete(key)
			removed++
		}

		return true
	})

	return removed
}
