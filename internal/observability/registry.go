package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Registry stores metrics in memory.
//
// Why this exists:
// The gateway needs a concurrency-safe, dependency-free metrics store.
// It can later be exported through a /metrics endpoint.
// Registry stores all metrics collected by the gateway.
//
// Think of Registry as the gateway's metrics database in memory.
//
// Example:
//
//	Request 1 arrives
//	    ↓
//	Increment("http_requests_total")
//
//	Request 2 arrives
//	    ↓
//	ObserveDuration("http_request_duration")
//
//	Request 3 arrives
//	    ↓
//	SetGauge("active_connections")
//
// All those metrics are stored inside Registry.
//
// Why this exists:
// The gateway needs one central place where every component can record metrics.
//
// Why in-memory:
// - very fast
// - no external dependency
// - simple to understand
// - easy to expose later through /metrics
//
// Example metrics:
//
// Counter:
//
//	http_requests_total = 1250
//
// Gauge:
//
//	active_connections = 37
//
// Duration:
//
//	http_request_duration
//	  count = 1250
//	  sum   = 84 seconds
//
// Concurrency:
// Multiple requests may update metrics simultaneously.
// sync.Map makes metric access safe across goroutines.
type Registry struct {

	// startedAt stores when the registry was created.
	//
	// Example:
	//
	//   Registry created at:
	//   2026-06-21 10:00:00 UTC
	//
	// Current time:
	//   2026-06-21 11:00:00 UTC
	//
	// Uptime:
	//   1 hour
	//
	// Why this matters:
	// Monitoring systems often expose process uptime.
	startedAt time.Time

	// counters stores all counter metrics.
	//
	// Counter means:
	// "value can only increase"
	//
	// Examples:
	//
	//   http_requests_total
	//   cache_hits_total
	//   cache_misses_total
	//   upstream_errors_total
	//
	// Example:
	//
	// Request arrives
	//   value = 1
	//
	// Another request arrives
	//   value = 2
	//
	// Another request arrives
	//   value = 3
	//
	// Counter NEVER decreases.
	//
	// Why sync.Map:
	// Different goroutines may update different metrics at the same time.
	counters sync.Map

	// gauges stores all gauge metrics.
	//
	// Gauge means:
	// "value can increase OR decrease"
	//
	// Examples:
	//
	// active_connections
	// queue_size
	// active_websockets
	//
	// Example:
	//
	// 10 clients connected
	// gauge = 10
	//
	// 2 clients disconnect
	// gauge = 8
	//
	// 5 new clients connect
	// gauge = 13
	//
	// Unlike counters, gauges move in both directions.
	gauges sync.Map

	// durations stores latency observations.
	//
	// Example:
	//
	// Request A = 100ms
	// Request B = 200ms
	// Request C = 300ms
	//
	// Stored internally:
	//
	// count = 3
	// sum   = 600ms
	//
	// Average latency:
	//
	// avg = sum / count
	// avg = 600 / 3
	// avg = 200ms
	//
	// Why store count + sum:
	// Very cheap.
	// We can compute averages later.
	durations sync.Map
}

// counterMetric represents one counter metric.
//
// Example metric:
//
// http_requests_total{method="GET"} = 15234
//
// Breakdown:
//
// name   = "http_requests_total"
// labels = {method="GET"}
// value  = 15234
//
// Why this struct exists:
// Each unique metric+label combination needs its own storage.
type counterMetric struct {

	// Metric name.
	//
	// Example:
	//
	// "http_requests_total"
	// "cache_hits_total"
	// "upstream_errors_total"
	name string

	// Labels attached to this metric.
	//
	// Example:
	//
	// {
	//   "method": "GET",
	//   "tenant": "tenant-123",
	// }
	//
	// Result:
	//
	// http_requests_total{
	//   method="GET",
	//   tenant="tenant-123"
	// }
	labels Labels

	// Current counter value.
	//
	// Example:
	//
	// value = 100
	//
	// Request arrives:
	// value = 101
	//
	// Another request:
	// value = 102
	//
	// Why atomic.Int64:
	// Multiple goroutines may increment simultaneously.
	// Atomic operations avoid race conditions without locks.
	value atomic.Int64
}

// gaugeMetric represents one gauge metric.
//
// Example:
//
// active_connections = 42
//
// Unlike counters:
//
// Counter:
//
//	1 → 2 → 3 → 4
//
// Gauge:
//
//	10 → 12 → 9 → 15 → 3
//
// Gauges move both directions.
type gaugeMetric struct {

	// Metric name.
	//
	// Example:
	//
	// "active_connections"
	// "queue_size"
	// "active_websockets"
	name string

	// Metric labels.
	//
	// Example:
	//
	// {
	//   "tenant": "tenant-123"
	// }
	labels Labels

	// Current gauge value.
	//
	// Example:
	//
	// active_connections = 10
	//
	// user connects:
	// active_connections = 11
	//
	// user disconnects:
	// active_connections = 10
	//
	// Why atomic:
	// Multiple goroutines may update the value simultaneously.
	value atomic.Int64
}

// durationMetric stores latency information.
//
// Example:
//
// Request #1 = 100ms
// Request #2 = 200ms
// Request #3 = 300ms
//
// Instead of storing every latency:
//
// [100,200,300]
//
// We store:
//
// count = 3
// sumNS = 600ms
//
// Then:
//
// average = sum / count
//
// Why this approach:
// - uses very little memory
// - fast updates
// - works well for monitoring
type durationMetric struct {

	// Metric name.
	//
	// Example:
	//
	// "http_request_duration"
	// "upstream_latency"
	name string

	// Labels for this latency metric.
	//
	// Example:
	//
	// {
	//   "route": "/products",
	//   "method": "GET"
	// }
	labels Labels

	// Number of observations recorded.
	//
	// Example:
	//
	// Request 1:
	// count = 1
	//
	// Request 2:
	// count = 2
	//
	// Request 3:
	// count = 3
	count atomic.Int64

	// Sum of all durations in nanoseconds.
	//
	// Example:
	//
	// Request 1 = 100ms
	// sumNS = 100ms
	//
	// Request 2 = 200ms
	// sumNS = 300ms
	//
	// Request 3 = 300ms
	// sumNS = 600ms
	//
	// Later:
	//
	// avg = sumNS / count
	// avg = 600ms / 3
	// avg = 200ms
	//
	// Why nanoseconds:
	// time.Duration internally uses nanoseconds.
	sumNS atomic.Int64
}

// NewRegistry creates a new metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		startedAt: time.Now().UTC(),
	}
}

// IncCounter increments one counter by 1.
//
// Why this exists:
// Most metrics are event counts: requests, errors, hits, misses, retries.
// IncCounter increments a counter by exactly 1.
//
// Think of a counter as:
//
//	"something happened"
//
// Every time the event happens, the number increases.
//
// Example:
//
// HTTP request arrives:
//
//	registry.IncCounter(
//	    "http_requests_total",
//	    Labels{"tenant":"tenant-123"},
//	)
//
// First request:
//
//	http_requests_total = 1
//
// Second request:
//
//	http_requests_total = 2
//
// Third request:
//
//	http_requests_total = 3
//
// Why this helper exists:
// Most metrics increase one event at a time, so callers should not have
// to write AddCounter(..., 1) everywhere.
func (r *Registry) IncCounter(name string, labels Labels) {
	r.addCounter(name, labels, 1)
}

// AddCounter increments one counter by an arbitrary delta.
//
// Why this exists:
// Some events may contribute more than one unit to a metric.
// AddCounter increments a counter by any amount.
//
// Difference from IncCounter:
//
//	IncCounter(...)      -> always adds 1
//	AddCounter(..., 10)  -> adds 10
//
// Example:
//
// Current value:
//
//	cache_hits_total = 100
//
// Add 5:
//
//	registry.AddCounter(
//	    "cache_hits_total",
//	    Labels{"tenant":"tenant-123"},
//	    5,
//	)
//
// New value:
//
//	cache_hits_total = 105
//
// Internal flow:
//
// 1. Find the metric object inside the registry.
// 2. Atomically add delta to the current value.
// 3. No locks are needed because atomic.Int64 is concurrency-safe.
//
// Why delta can be larger than 1:
//
// Some operations may represent multiple events at once.
//
// Example:
//
// Processed 100 messages in a batch:
//
//	registry.AddCounter(
//	    "messages_processed_total",
//	    labels,
//	    100,
//	)
//
// instead of calling IncCounter 100 times.
func (r *Registry) addCounter(name string, labels Labels, delta int64) {
	if r == nil || name == "" || delta == 0 {
		return
	}

	metric := r.getCounter(name, labels)

	// Atomic add:
	//
	// Current value: 100
	// Delta:         5
	//
	// Result:        105
	//
	// Safe even when many goroutines update the metric simultaneously.
	metric.value.Add(delta)
}

// SetGauge stores a current state value.
//
// Unlike counters:
//
// Counter:
//
//	1 -> 2 -> 3 -> 4 -> 5
//
// (always increasing)
//
// Gauge:
//
//	10 -> 5 -> 20 -> 7 -> 0
//
// (can move up or down)
//
// Example:
//
// Number of active connections:
//
//	registry.SetGauge(
//	    "active_connections",
//	    labels,
//	    120,
//	)
//
// Later:
//
//	registry.SetGauge(
//	    "active_connections",
//	    labels,
//	    95,
//	)
//
// The old value is replaced.
//
// Another example:
//
// Circuit breaker state:
//
//	0 = closed
//	1 = half-open
//	2 = open
//
//	registry.SetGauge(
//	    "circuit_breaker_state",
//	    Labels{"upstream":"payments"},
//	    2,
//	)
//
// Why gauges exist:
// Some values represent current state, not accumulated events.
func (r *Registry) SetGauge(name string, labels Labels, value int64) {
	if r == nil || name == "" {
		return
	}

	metric := r.getGauge(name, labels)

	// Store replaces the current value.
	//
	// Previous: 120
	// New:       95
	//
	// Result:    95
	metric.value.Store(value)
}

// ObserveDuration records one latency measurement.
//
// Why this exists:
// We need a simple way to store latency sum and count for later export.
// ObserveDuration records one latency measurement.
//
// Why this exists:
//
// A duration metric answers:
//
//	"How long did operations take?"
//
// Example:
//
// Request completed in:
//
//	250ms
//
// Record it:
//
//	registry.ObserveDuration(
//	    "http_request_duration",
//	    Labels{"route":"/products"},
//	    250*time.Millisecond,
//	)
//
// Internally:
//
//	count = 1
//	sum   = 250ms
//
// Another request:
//
//	100ms
//
// Internally:
//
//	count = 2
//	sum   = 350ms
//
// Another request:
//
//	150ms
//
// Internally:
//
//	count = 3
//	sum   = 500ms
//
// Later we can calculate:
//
//	average = sum / count
//	        = 500ms / 3
//	        = 166ms
//
// Why store count and sum:
//
// It is much cheaper than storing every single request duration.
//
// Instead of:
//
//	[250ms,100ms,150ms,...]
//
// We store:
//
//	count=3
//	sum=500ms
func (r *Registry) ObserveDuration(name string, labels Labels, d time.Duration) {
	if r == nil || name == "" {
		return
	}

	metric := r.getDuration(name, labels)

	// One more observation occurred.
	//
	// count:
	//   5 -> 6
	metric.count.Add(1)

	// Add duration to the running total.
	//
	// sum:
	//   2s -> 2.25s
	metric.sumNS.Add(d.Nanoseconds())
}

// RecordRequest records one complete request observation.
//
// What it records:
// - request count
// - request duration
// - response bytes
//
// Why this helper exists:
// Request observability is a very common grouped operation.
// RecordRequest records all important metrics for one completed request.
//
// Why this helper exists:
//
// Almost every HTTP request needs the same observability updates:
//
// 1. Increase total request count
// 2. Record request latency
// 3. Record response size
//
// Instead of writing:
//
//	registry.IncCounter(...)
//	registry.ObserveDuration(...)
//	registry.AddCounter(...)
//
// everywhere, we put the common behavior in one place.
//
// Example:
//
// Request:
//
//	GET /products
//
// Duration:
//
//	250ms
//
// Response size:
//
//	2048 bytes
//
// Labels:
//
//	tenant=tenant-123
//	route=/products
//	method=GET
//	status=200
//
// Internal result:
//
//	gateway_requests_total            += 1
//	gateway_request_duration_seconds += 250ms
//	gateway_response_bytes_total     += 2048
//
// Why response bytes are tracked:
//
// It helps answer:
//
//	How much traffic is the gateway serving?
//
// Example:
//
//	1 KB response
//	5 KB response
//	10 KB response
//
// Total:
//
//	16 KB served
func (r *Registry) RecordRequest(labels Labels, duration time.Duration, responseBytes int64) {
	r.IncCounter("gateway_requests_total", labels)
	r.ObserveDuration("gateway_request_duration_seconds", labels, duration)
	r.addCounter("gateway_response_bytes_total", labels, responseBytes)
}

// RecordError records one request error.
//
// Why this exists:
// Error counts help us distinguish success traffic from broken traffic.
// RecordError records one failed request.
//
// Why this exists:
//
// Success count alone is not enough.
//
// Example:
//
//	gateway_requests_total = 1000
//
// That does not tell us whether requests succeeded.
//
// We also need:
//
//	gateway_request_errors_total
//
// Example:
//
// Total requests:
//
//	1000
//
// Errors:
//
//	50
//
// Success rate:
//
//	950 / 1000 = 95%
//
// Common reasons:
//
// - upstream timeout
// - circuit breaker open
// - retry exhausted
// - proxy failure
//
// Every error increments the counter by 1.
func (r *Registry) RecordError(labels Labels) {
	r.IncCounter("gateway_request_errors_total", labels)
}

// RecordCacheHit records a cache hit.
//
// Why this exists:
// Cache efficiency is one of the most important gateway performance signals.
// RecordCacheHit records a successful cache lookup.
//
// Cache hit means:
//
// Client requests data.
//
// Gateway finds the response in cache.
//
// Upstream service is NOT called.
//
// Example:
//
// Request:
//
//	GET /products
//
// Cache contains response.
//
// Result:
//
// Returned immediately from cache.
//
// Metric:
//
//	gateway_cache_hits_total += 1
//
// Why this matters:
//
// Higher cache hits usually mean:
//
// - lower latency
// - fewer upstream requests
// - less infrastructure cost
func (r *Registry) RecordCacheHit(labels Labels) {
	r.IncCounter("gateway_cache_hits_total", labels)
}

// RecordCacheMiss records a cache miss.
func (r *Registry) RecordCacheMiss(labels Labels) {
	r.IncCounter("gateway_cache_misses_total", labels)
}

// RecordRetry records one retry attempt.
//
// Retry means:
//
// First upstream attempt failed.
//
// Gateway waits.
//
// Gateway tries again.
//
// Example:
//
// Attempt #1 -> timeout
// Attempt #2 -> success
//
// Metrics:
//
//	gateway_retries_total += 1
//
// Another example:
//
// Attempt #1 -> 503
// Attempt #2 -> 503
// Attempt #3 -> success
//
// Metrics:
//
//	gateway_retries_total += 2
//
// Why this matters:
//
// High retry counts often indicate:
//
// - unstable upstreams
// - network issues
// - overloaded services
func (r *Registry) RecordRetry(labels Labels) {
	r.IncCounter("gateway_retries_total", labels)
}

// RecordBreakerOpen records that a circuit breaker is open.
//
// Why this exists:
// Circuit breaker state should be visible in metrics.
// RecordBreakerOpen records that a circuit breaker entered open state.
//
// Open means:
//
// Gateway has detected repeated upstream failures.
//
// Traffic is temporarily blocked.
//
// Example:
//
// Failure threshold:
//
//	5 failures
//
// Upstream fails:
//
//	1
//	2
//	3
//	4
//	5
//
// Circuit becomes OPEN.
//
// Metric:
//
//	gateway_circuit_breaker_open = 1
//
// Why gauge is used:
//
// Circuit state is current state,
// not an accumulating count.
func (r *Registry) RecordBreakerOpen(labels Labels) {
	r.SetGauge("gateway_circuit_breaker_open", labels, 1)
}

// RecordBreakerClosed records that a circuit breaker is healthy.
//
// Closed means:
//
// Traffic is allowed normally.
//
// Example:
//
// Circuit was OPEN.
//
// Recovery probes succeed.
//
// Circuit transitions back to CLOSED.
//
// Metric:
//
//	gateway_circuit_breaker_open = 0
//
// Meaning:
//
// 0 = closed (healthy)
// 1 = open (blocked)
//
// Why gauge is used:
//
// We care about current breaker state,
// not how many times it changed.
func (r *Registry) RecordBreakerClosed(labels Labels) {
	r.SetGauge("gateway_circuit_breaker_open", labels, 0)
}

// MetricsHandler returns an HTTP handler that exposes metrics in text format.
//
// Why this exists:
// Monitoring systems need a scrape endpoint.
// This is the standard place to expose gateway metrics.
func (r *Registry) MetricsHandler() http.Handler {
	return http.HandlerFunc(r.serveHTTP)
}

// serveHTTP writes all stored metrics in Prometheus text format.
//
// Example output:
//
//	# HELP gateway_requests_total Total gateway requests.
//	# TYPE gateway_requests_total counter
//	gateway_requests_total{tenant="t1",method="GET"} 150
//
//	# HELP gateway_circuit_breaker_open Circuit breaker state.
//	# TYPE gateway_circuit_breaker_open gauge
//	gateway_circuit_breaker_open{tenant="t1"} 1
//
// Why this exists:
// Prometheus periodically calls GET /metrics.
// This method converts everything stored in memory into text that
// Prometheus can scrape and store.
func (r *Registry) serveHTTP(w http.ResponseWriter, _ *http.Request) {

	// Tell Prometheus what format we are returning.
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// We build all metric lines first, then write them once at the end.
	lines := make([]string, 0, 128)

	// ---------------------------------------------------------------------
	// UPTIME METRIC
	// ---------------------------------------------------------------------
	//
	// startedAt was saved when the registry was created.
	//
	// Example:
	//
	// Registry started at:
	//   10:00:00
	//
	// Current time:
	//   10:05:00
	//
	// Uptime:
	//   300 seconds
	//
	// Output:
	//
	// gateway_up_time_seconds 300
	//
	lines = append(lines,
		"# HELP gateway_up_time_seconds Gateway uptime in seconds.",
		"# TYPE gateway_up_time_seconds gauge",
		fmt.Sprintf(
			"gateway_up_time_seconds %0.0f",
			time.Since(r.startedAt).Seconds(),
		),
	)

	// ---------------------------------------------------------------------
	// COUNTERS
	// ---------------------------------------------------------------------
	//
	// counters is a sync.Map:
	//
	// key   -> metric identifier
	// value -> *counterMetric
	//
	// Example:
	//
	// counters:
	//
	// {
	//   "gateway_requests_total|tenant=t1"
	//      =>
	//      &counterMetric{
	//          name: "gateway_requests_total",
	//          value: 150,
	//      }
	// }
	//
	// Range() iterates over every stored counter.
	//
	r.counters.Range(func(_, value any) bool {

		metric, ok := value.(*counterMetric)
		if !ok || metric == nil {
			return true
		}

		// Convert internal metric struct into Prometheus text lines.
		//
		// Example:
		//
		// gateway_requests_total{tenant="t1"} 150
		//
		lines = append(lines, renderCounter(metric)...)

		return true
	})

	// ---------------------------------------------------------------------
	// GAUGES
	// ---------------------------------------------------------------------
	//
	// Example stored gauge:
	//
	// gateway_circuit_breaker_open{tenant="t1"} = 1
	//
	// Meaning:
	//
	// 1 = open
	// 0 = closed
	//
	r.gauges.Range(func(_, value any) bool {

		metric, ok := value.(*gaugeMetric)
		if !ok || metric == nil {
			return true
		}

		lines = append(lines, renderGauge(metric)...)

		return true
	})

	// ---------------------------------------------------------------------
	// DURATION METRICS
	// ---------------------------------------------------------------------
	//
	// Example:
	//
	// Request #1 = 100ms
	// Request #2 = 200ms
	// Request #3 = 300ms
	//
	// Stored internally:
	//
	// count = 3
	// sumNS = 600ms
	//
	// Prometheus can later calculate:
	//
	// average latency
	// percentiles
	// request throughput
	//
	r.durations.Range(func(_, value any) bool {

		metric, ok := value.(*durationMetric)
		if !ok || metric == nil {
			return true
		}

		lines = append(lines, renderDuration(metric)...)

		return true
	})

	// ---------------------------------------------------------------------
	// SORT OUTPUT
	// ---------------------------------------------------------------------
	//
	// sync.Map iteration order is random.
	//
	// Without sorting:
	//
	// Request 1:
	//   requests
	//   errors
	//   cache_hits
	//
	// Request 2:
	//   cache_hits
	//   requests
	//   errors
	//
	// That makes testing annoying.
	//
	// Sorting gives stable output every time.
	//
	sort.Strings(lines)

	// ---------------------------------------------------------------------
	// WRITE RESPONSE
	// ---------------------------------------------------------------------
	//
	// Join all metric lines into one text response.
	//
	// Example:
	//
	// gateway_requests_total{tenant="t1"} 100
	// gateway_cache_hits_total{tenant="t1"} 20
	// gateway_request_errors_total{tenant="t1"} 2
	//
	_, _ = fmt.Fprintln(
		w,
		strings.Join(lines, "\n"),
	)
}

// getCounter returns the counter for a specific metric instance.
//
// Example:
//
//	name   = "gateway_requests_total"
//	labels = {
//	    tenant="tenant-1",
//	    method="GET",
//	}
//
// Generated key:
//
//	gateway_requests_total|method="GET",tenant="tenant-1"
//
// Why this function exists:
//
// Every unique combination of:
//
//	metric name
//	+
//	label set
//
// represents a completely separate metric.
//
// Example:
//
//	gateway_requests_total{tenant="t1"} = 100
//	gateway_requests_total{tenant="t2"} = 50
//
// These must not share the same counter.
//
// This helper guarantees:
//
// - load existing metric if already created
// - otherwise create it once
// - remain safe under concurrent requests
func (r *Registry) getCounter(name string, labels Labels) *counterMetric {

	// Build deterministic lookup key.
	//
	// Example:
	//
	// gateway_requests_total|method="GET",tenant="t1"
	//
	key := metricKey(name, labels)

	// Fast path:
	//
	// Try loading an existing metric.
	//
	// Example:
	//
	// counters:
	// {
	//   "gateway_requests_total|..."
	//       => *counterMetric
	// }
	//
	if value, ok := r.counters.Load(key); ok {

		// Ensure the stored value has the expected type.
		if metric, ok := value.(*counterMetric); ok && metric != nil {
			return metric
		}
	}

	// Metric does not exist yet.
	//
	// Create a new one.
	//
	// Example:
	//
	// &counterMetric{
	//     name: "gateway_requests_total",
	//     labels: {...},
	//     value: 0,
	// }
	//
	metric := &counterMetric{
		name: name,

		// Clone labels so future caller modifications do not affect stored metric labels.
		labels: labels.Clone(),
	}

	// LoadOrStore handles races safely.
	//
	// Example:
	//
	// Goroutine A creates metric
	// Goroutine B creates metric
	//
	// Only one actually gets stored.
	//
	actual, _ := r.counters.LoadOrStore(key, metric)

	// Return the stored metric.
	//
	// This may be:
	//
	// - ours
	// - another goroutine's
	//
	if stored, ok := actual.(*counterMetric); ok && stored != nil {
		return stored
	}

	return metric
}

// getGauge loads or creates one gauge metric.
// getGauge works exactly like getCounter, but for gauge metrics.
//
// Example:
//
// gateway_circuit_breaker_open{tenant="t1"} = 1
//
// Gauge metrics represent current state, not accumulated events.
//
// Example:
//
// 0 = breaker closed
// 1 = breaker open
//
// The function:
//
// 1. Builds lookup key
// 2. Tries loading existing gauge
// 3. Creates gauge if missing
// 4. Stores safely using LoadOrStore
// 5. Returns gauge instance
//
// This guarantees:
//
// one unique gauge object
// per metric-name + label combination.
func (r *Registry) getGauge(name string, labels Labels) *gaugeMetric {
	key := metricKey(name, labels)

	if value, ok := r.gauges.Load(key); ok {
		if metric, ok := value.(*gaugeMetric); ok && metric != nil {
			return metric
		}
	}

	metric := &gaugeMetric{
		name:   name,
		labels: labels.Clone(),
	}

	actual, _ := r.gauges.LoadOrStore(key, metric)
	if stored, ok := actual.(*gaugeMetric); ok && stored != nil {
		return stored
	}

	return metric
}

// getDuration works exactly like getCounter,
// but creates durationMetric objects.
//
// Example metric:
//
// gateway_request_duration_seconds
//
// Stored data:
//
// count = number of observations
// sumNS = total latency accumulated
//
// Example:
//
// Request #1 = 100ms
// Request #2 = 200ms
// Request #3 = 300ms
//
// Stored:
//
// count = 3
// sumNS = 600ms
//
// Later Prometheus can calculate:
//
// average latency
// throughput
// trends
//
// Like other helpers:
//
// - load existing metric
// - create if missing
// - store safely
// - return metric
func (r *Registry) getDuration(name string, labels Labels) *durationMetric {
	key := metricKey(name, labels)

	if value, ok := r.durations.Load(key); ok {
		if metric, ok := value.(*durationMetric); ok && metric != nil {
			return metric
		}
	}

	metric := &durationMetric{
		name:   name,
		labels: labels.Clone(),
	}

	actual, _ := r.durations.LoadOrStore(key, metric)
	if stored, ok := actual.(*durationMetric); ok && stored != nil {
		return stored
	}

	return metric
}

// metricKey creates the unique identifier used inside sync.Map.
//
// Why this exists:
//
// Metric names alone are not enough.
//
// Example:
//
// gateway_requests_total{tenant="t1"}
// gateway_requests_total{tenant="t2"}
//
// Same metric name.
//
// Different label values.
//
// Must be stored separately.
//
// Example:
//
// name:
//
//	gateway_requests_total
//
// labels:
//
//	tenant="t1"
//	method="GET"
//
// Generated key:
//
//	gateway_requests_total|method="GET",tenant="t1"
//
// That string becomes the sync.Map key.
func metricKey(name string, labels Labels) string {
	if len(labels) == 0 {
		return name
	}

	return name + "|" + labels.String()
}

// renderCounter converts one counter metric into Prometheus text format.
//
// Example internal metric:
//
// name:
//
//	gateway_requests_total
//
// labels:
//
//	tenant="t1"
//
// value:
//
//	150
//
// Generated output:
//
// # TYPE gateway_requests_total counter
// gateway_requests_total{tenant="t1"} 150
//
// Why this exists:
//
// Prometheus cannot read Go structs.
//
// It reads text.
//
// This function converts internal data
// into Prometheus-compatible lines.
func renderCounter(metric *counterMetric) []string {
	if metric == nil {
		return nil
	}

	labels := formatLabels(metric.labels)
	value := metric.value.Load()

	lines := []string{
		"# TYPE " + metric.name + " counter",
	}

	if labels == "" {
		lines = append(lines, fmt.Sprintf("%s %d", metric.name, value))
		return lines
	}

	lines = append(lines, fmt.Sprintf("%s{%s} %d", metric.name, labels, value))
	return lines
}

// renderGauge converts one gauge metric
// into Prometheus text format.
//
// Example:
//
// name:
//
//	gateway_circuit_breaker_open
//
// labels:
//
//	tenant="t1"
//
// value:
//
//	1
//
// Output:
//
// # TYPE gateway_circuit_breaker_open gauge
// gateway_circuit_breaker_open{tenant="t1"} 1
//
// Meaning:
//
// 0 = closed
// 1 = open
//
// Why gauge:
//
// The value can move both up and down.
//
// Unlike counters:
//
// 1
// 2
// 3
// 4
//
// gauges may be:
//
// 0
// 1
// 0
// 1
func renderGauge(metric *gaugeMetric) []string {
	if metric == nil {
		return nil
	}

	labels := formatLabels(metric.labels)
	value := metric.value.Load()

	lines := []string{
		"# TYPE " + metric.name + " gauge",
	}

	if labels == "" {
		lines = append(lines, fmt.Sprintf("%s %d", metric.name, value))
		return lines
	}

	lines = append(lines, fmt.Sprintf("%s{%s} %d", metric.name, labels, value))
	return lines
}

// renderDuration converts one duration metric into text lines.
//
// Why this is a summary-style output:
// It gives us count and total sum, which is enough for average latency
// and operational debugging without complex histogram plumbing.
// renderDuration converts durationMetric
// into Prometheus summary-style output.
//
// Example stored metric:
//
// count = 3
//
// sumNS =
// 100ms +
// 200ms +
// 300ms
//
// = 600ms
//
// Converted:
//
// count = 3
// sum   = 0.6 seconds
//
// Output:
//
// # TYPE gateway_request_duration_seconds summary
//
// gateway_request_duration_seconds_count{tenant="t1"} 3
//
// gateway_request_duration_seconds_sum{tenant="t1"} 0.600000000
//
// Why store count + sum:
//
// Average latency can be calculated:
//
// average = sum / count
//
// 0.6 / 3
//
// = 0.2 seconds
//
// = 200ms
//
// Much simpler than maintaining
// full histogram buckets.
func renderDuration(metric *durationMetric) []string {
	if metric == nil {
		return nil
	}

	labels := formatLabels(metric.labels)
	count := metric.count.Load()
	sumSeconds := float64(metric.sumNS.Load()) / float64(time.Second)

	lines := []string{
		"# TYPE " + metric.name + " summary",
	}

	if labels == "" {
		lines = append(lines,
			fmt.Sprintf("%s_count %d", metric.name, count),
			fmt.Sprintf("%s_sum %0.9f", metric.name, sumSeconds),
		)
		return lines
	}

	lines = append(lines,
		fmt.Sprintf("%s_count{%s} %d", metric.name, labels, count),
		fmt.Sprintf("%s_sum{%s} %0.9f", metric.name, labels, sumSeconds),
	)

	return lines
}

// formatLabels returns sorted label text.
//
// Why this helper exists:
// Metric output should be deterministic and easy to parse.
// formatLabels converts Labels into the string
// format expected by Prometheus.
//
// Example:
//
//	Labels{
//	    "tenant": "t1",
//	    "method": "GET",
//	}
//
// Produces:
//
// method="GET",tenant="t1"
//
// Why this helper exists:
//
// Every metric renderer needs labels
// formatted the same way.
//
// Keeping formatting in one place:
//
// - avoids duplicated code
// - guarantees consistent output
// - guarantees sorted label order
//
// The actual formatting work is delegated
// to Labels.String().
func formatLabels(labels Labels) string {
	return labels.String()
}
