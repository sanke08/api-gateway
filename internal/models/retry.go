package models

import "time"

// RetryPolicy describes how the gateway should retry a failed upstream request.
//
// Why this struct exists:
// Different tenants or upstreams may need different retry behavior.
//
// What the fields mean:
//
//   - Attempts
//     Total number of tries, including the first attempt.
//     Example: Attempts = 3 means 1 initial try + 2 retries.
//
//   - Delay
//     The base delay before the first retry.
//
//   - MaxDelay
//     The maximum delay allowed by exponential backoff.
//
//   - Jitter
//     A small random extra delay added to avoid many requests retrying at once.
//
// Why this matters:
// Retrying is useful for temporary upstream failures, but it must stay controlled.
// Without a clear policy, retries can make outages worse instead of better.
type RetryPolicy struct {
	Attempts int
	Delay    time.Duration
	MaxDelay time.Duration
	Jitter   time.Duration
}
