package services

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/observability"
	"github.com/sanke08/api_gateway/internal/repository"
)

// UsageTracker defines the contract for recording usage events.
//
// What is this?
// This is an interface that describes what a usage tracking system should be
// able to do. It does not care HOW usage is stored; it only defines WHAT
// operations are available.
//
// Why an interface instead of directly using AsyncUsageTracker?
// Depending directly on a concrete implementation creates tight coupling.
// By programming against an interface, the HTTP layer becomes independent
// of the storage mechanism.
//
// Today we may store usage asynchronously into PostgreSQL.
//
// Tomorrow we could:
//
//   - Publish usage events to Kafka
//   - Store them directly in PostgreSQL
//   - Send them to another microservice
//   - Store them inside ClickHouse for analytics
//
// None of those changes require modifying the HTTP handlers because they only
// know about the UsageTracker interface.
//
// Example:
//
//	      HTTP Handler
//	           │
//	           │ tracker.Enqueue(...)
//	           ▼
//	   UsageTracker Interface
//	           │
//	┌──────────┴──────────┐
//	│                     │
//	▼                     ▼
//
// AsyncUsageTracker      KafkaUsageTracker
//
//	     │                     │
//	     ▼                     ▼
//	PostgreSQL            Kafka Topic
//
// The handler never knows which implementation is actually being used.
//
// This is one of the biggest advantages of Go interfaces—they reduce coupling
// between different layers of the application.
type UsageTracker interface {

	// Enqueue accepts one usage record and schedules it for storage.
	//
	// Notice the wording "schedules" instead of "stores".
	//
	// The request does NOT wait for the database write.
	//
	// Instead:
	//
	//     HTTP Request
	//          │
	//          ▼
	//  Create Usage Record
	//          │
	//          ▼
	//  Enqueue(record)
	//          │
	//          ▼
	//  Queue
	//          │
	//          ▼
	//  Return HTTP Response
	//
	// Meanwhile, a completely different goroutine removes the record from
	// the queue and writes it into PostgreSQL.
	//
	// This makes request processing much faster because database latency is
	// moved off the hot path.
	//
	// Return value:
	//
	// true
	//     The event successfully entered the queue.
	//
	// false
	//     The queue was full, already closed, or unable to accept the event.
	Enqueue(ctx context.Context, record models.Usage) bool

	// Close gracefully shuts down the tracker.
	//
	// Why this exists:
	//
	// During server shutdown we do NOT want to lose queued usage events.
	//
	// Imagine:
	//
	// Queue:
	//
	//   Event1
	//   Event2
	//   Event3
	//
	// If the process exits immediately,
	// all three events disappear forever.
	//
	// Close() tells the worker:
	//
	//     "Stop accepting new events,
	//      finish processing everything already in the queue,
	//      then exit."
	//
	// This is called graceful shutdown.
	Close(ctx context.Context) error
}

// AsyncUsageTracker is the default implementation of UsageTracker.
//
// What does it do?
//
// Instead of writing every request directly into PostgreSQL,
// it places usage events into an in-memory queue.
//
// A background worker continuously removes events from that queue
// and stores them into the database.
//
// Request Flow:
//
//	   Client Request
//	         │
//	         ▼
//	  Gateway Handler
//	         │
//	         ▼
//	Create Usage Record
//	         │
//	         ▼
//	    queue <- record
//	         │
//	         ▼
//	  HTTP Response Returned
//
// ------------------------------
//
// Background Worker:
//
//	   queue
//	     │
//	     ▼
//	Read Record
//	     │
//	     ▼
//
// INSERT INTO usage ...
//
// Why is this useful?
//
// Database writes are relatively slow compared to in-memory operations.
//
// Queue insertion:
//
//	~ microseconds
//
// Database insert:
//
//	~ milliseconds
//
// By moving database work into another goroutine, API latency becomes much
// lower because users no longer wait for INSERT queries to finish.
type AsyncUsageTracker struct {

	// repo is responsible for actually persisting usage records.
	//
	// AsyncUsageTracker itself does NOT know SQL.
	//
	// Instead it delegates storage to the repository layer.
	//
	// This keeps responsibilities separated:
	//
	// Tracker:
	//     manages queue + worker
	//
	// Repository:
	//     manages SQL
	repo repository.UsageRepository

	// queue temporarily stores usage events waiting to be written.
	//
	// This is a buffered Go channel.
	//
	// Example:
	//
	// Queue capacity = 1024
	//
	// Before:
	//
	// [ ]
	//
	// After 3 requests:
	//
	// [Usage1]
	// [Usage2]
	// [Usage3]
	//
	// Background worker removes them one by one:
	//
	// [Usage2]
	// [Usage3]
	//
	// Eventually:
	//
	// [ ]
	//
	// The queue absorbs short traffic bursts without immediately forcing
	// every request to wait for PostgreSQL.
	queue chan models.Usage

	// logger records operational problems.
	//
	// Examples:
	//
	// - queue full
	// - database insert failed
	// - invalid usage event
	//
	// Logging is for humans.
	logger *slog.Logger

	// metrics records operational statistics.
	//
	// Examples:
	//
	// gateway_usage_events_total
	// gateway_usage_events_dropped_total
	// gateway_usage_persisted_total
	//
	// Metrics are for dashboards and monitoring systems.
	metrics *observability.Registry

	// clock returns the current time.
	//
	// Why not directly call time.Now() everywhere?
	//
	// During testing we can replace this function with a fake clock,
	// making time-dependent code completely deterministic.
	//
	// Production:
	//
	//     clock = time.Now
	//
	// Tests:
	//
	//     clock = fakeClock
	clock func() time.Time

	// closed indicates whether the tracker has stopped accepting new events.
	//
	// atomic.Bool allows many goroutines to safely read/write this value
	// without requiring a mutex.
	closed atomic.Bool

	// closeOnce guarantees shutdown happens exactly once.
	//
	// Even if Close() is accidentally called many times:
	//
	//     tracker.Close()
	//     tracker.Close()
	//     tracker.Close()
	//
	// only the first call performs shutdown.
	//
	// Without sync.Once,
	// closing an already closed channel would panic.
	closeOnce sync.Once

	// done is closed when the background worker exits.
	//
	// Close() waits for this signal before returning.
	//
	// Think of it as:
	//
	// Worker:
	//      "I'm finished."
	//
	// Close():
	//      "Okay, shutdown complete."
	done chan struct{}
}

// NewAsyncUsageTracker constructs a fully configured usage tracker.
//
// Responsibilities:
//
//   - Validate configuration
//   - Create the queue
//   - Create shutdown channel
//   - Store dependencies
//   - Start the background worker
//
// After this function returns, the tracker is immediately ready
// to accept usage events.
func NewAsyncUsageTracker(
	repo repository.UsageRepository,
	bufferSize int,
	logger *slog.Logger,
	metrics *observability.Registry,
) *AsyncUsageTracker {

	// If the caller provides an invalid buffer size,
	// choose a reasonable production default.
	//
	// A queue size of 1024 allows small traffic spikes to be absorbed
	// without immediately dropping events.
	if bufferSize <= 0 {
		bufferSize = 1024
	}

	// If no logger was supplied,
	// use Go's default structured logger.
	//
	// This avoids nil pointer checks everywhere.
	if logger == nil {
		logger = slog.Default()
	}

	// Allocate and initialize the tracker.
	//
	// Every dependency is stored so the background worker can use it later.
	t := &AsyncUsageTracker{
		repo: repo,

		// make(chan, bufferSize)
		//
		// Creates a buffered channel capable of storing
		// "bufferSize" usage events before senders begin blocking.
		queue: make(chan models.Usage, bufferSize),

		logger: logger,

		metrics: metrics,

		// Default clock implementation.
		clock: time.Now,

		// Used during graceful shutdown.
		done: make(chan struct{}),
	}

	// Start the background worker.
	//
	// The keyword "go" creates a completely new goroutine.
	//
	// From this point onward there are now TWO concurrent executions:
	//
	// Main Goroutine
	//     Handles HTTP Requests
	//
	// Background Goroutine
	//     Writes usage records into PostgreSQL
	//
	// Both execute independently.
	go t.run()

	// Return the fully initialized tracker.
	return t
}

// Enqueue validates one usage record and schedules it for background persistence.
//
// What does this method actually do?
//
// This is the entry point used by the HTTP request path.
//
// Every incoming request eventually produces one Usage record.
//
// Instead of immediately inserting that record into PostgreSQL,
// Enqueue() simply validates it and pushes it into an in-memory queue.
//
// The request therefore finishes almost immediately while another goroutine
// performs the database work later.
//
// Request Flow:
//
//	    Client
//	      │
//	      ▼
//	 HTTP Handler
//	      │
//	      ▼
//	Create Usage Record
//	      │
//	      ▼
//	   Enqueue()
//	      │
//	      ▼
//	   Queue (Memory)
//	      │
//	      ├──────────────► HTTP Response Returned
//	      │
//	      ▼
//
// Background Worker
//
//	│
//	▼
//
// # PostgreSQL INSERT
//
// Why is this design important?
//
// Imagine PostgreSQL takes:
//
//	15 ms
//
// to complete one INSERT.
//
// If every request waited for that INSERT:
//
//	    Request
//	        │
//	        ▼
//	INSERT INTO usage...
//	        │
//	        ▼
//	    15ms delay
//	        │
//	        ▼
//	 Response
//
// Every request becomes at least 15 ms slower.
//
// Instead:
//
//	    Request
//	        │
//	        ▼
//	queue <- usage
//	        │
//	    <1 millisecond
//	        │
//	        ▼
//	 Response
//
// The database work happens independently in another goroutine.
func (t *AsyncUsageTracker) Enqueue(ctx context.Context, record models.Usage) bool {

	// Defensive programming.
	//
	// If the tracker itself or repository has not been initialized,
	// there is nowhere to store usage events.
	//
	// Returning false tells the caller the event was not accepted.
	if t == nil || t.repo == nil {
		return false
	}

	// Respect request cancellation.
	//
	// The incoming HTTP request may already have been cancelled.
	//
	// Examples:
	//
	// • Client disconnected
	// • Request timeout
	// • Server shutdown
	//
	// Context carries that cancellation information.
	//
	// ctx.Err() returns nil when everything is still active.
	//
	// Otherwise it returns:
	//
	//     context.Canceled
	//
	// or
	//
	//     context.DeadlineExceeded
	//
	// In those situations we simply stop processing.
	if ctx != nil && ctx.Err() != nil {
		return false
	}

	// Validate the usage record before placing it into the queue.
	// Why validate here instead of inside the worker?
	// Because invalid data should never consume queue space.
	// Rejecting early keeps the queue available for valid events.
	if err := validateUsageRecord(record); err != nil {

		// Record the validation failure in logs.
		//
		// Example:
		//
		// usage event rejected
		// error="tenant_id is required"
		if t.logger != nil {
			t.logger.Warn(
				"usage event rejected",
				"error",
				err.Error(),
			)
		}

		// Update metrics.
		//
		// Operators can later see:
		//
		// gateway_usage_events_dropped_total 27
		//
		// indicating some events were discarded.
		if t.metrics != nil {
			t.metrics.IncCounter(
				"gateway_usage_events_dropped_total",
				nil,
			)
		}

		return false
	}

	// Has shutdown already started?
	//
	// Once Close() begins,
	// we no longer accept new events.
	//
	// Otherwise requests could continue filling the queue while
	// the worker is trying to shut down.
	if t.closed.Load() {

		if t.metrics != nil {
			t.metrics.IncCounter(
				"gateway_usage_events_dropped_total",
				nil,
			)
		}

		return false
	}

	// Try placing the record into the buffered queue.
	//
	// This uses a non-blocking select.
	//
	// Why?
	//
	// A normal send:
	//
	//     t.queue <- record
	//
	// may block forever if the queue is full.
	//
	// Blocking HTTP requests because metrics cannot be stored
	// is unacceptable for an API gateway.
	select {

	// Queue has available capacity.
	case t.queue <- record:

		// Successfully accepted.
		//
		// Nothing has been written to PostgreSQL yet.
		//
		// The event is only waiting in memory.
		if t.metrics != nil {
			t.metrics.IncCounter(
				"gateway_usage_events_total",
				nil,
			)
		}

		return true

	// Queue is already full.
	default:

		// Why intentionally drop?
		//
		// During very heavy traffic,
		// keeping API requests fast is usually more important
		// than recording every single usage row.
		//
		// Example:
		//
		// Queue Capacity:
		//
		// 1024
		//
		// Current Size:
		//
		// 1024
		//
		// Incoming request:
		//
		//    ❌ Cannot enqueue.
		//
		// Rather than waiting several seconds,
		// the gateway simply discards the usage event
		// and continues serving client traffic.

		if t.metrics != nil {
			t.metrics.IncCounter(
				"gateway_usage_events_dropped_total",
				nil,
			)
		}

		if t.logger != nil {
			t.logger.Warn(
				"usage queue full, dropping event",
			)
		}

		return false
	}
}

// Close gracefully shuts down the background usage tracker.
//
// What is graceful shutdown?
//
// A graceful shutdown means:
//
//	"Stop accepting new work,
//	 finish everything already in progress,
//	 then exit safely."
//
// Imagine the queue currently contains:
//
//	Usage1
//	Usage2
//	Usage3
//
// If the application simply exits:
//
//	❌ Usage1 lost
//	❌ Usage2 lost
//	❌ Usage3 lost
//
// Those usage records are never written to PostgreSQL.
//
// Instead:
//
//	   Close()
//	       │
//	       ▼
//	Stop accepting new events
//	       │
//	       ▼
//
// Worker processes Usage1
// Worker processes Usage2
// Worker processes Usage3
//
//	│
//	▼
//
// Queue becomes empty
//
//	│
//	▼
//
// Worker exits
//
//	│
//	▼
//
// # Close() returns
//
// This guarantees that everything already queued has a chance
// to be persisted before the application shuts down.
//
// In production this is typically called when:
//
// • Kubernetes terminates the pod
// • Docker container stops
// • SIGTERM is received
// • Application shuts down normally
func (t *AsyncUsageTracker) Close(ctx context.Context) error {

	// A nil tracker has nothing to shut down.
	//
	// Returning nil keeps shutdown logic simple because callers
	// do not need to perform nil checks themselves.
	if t == nil {
		return nil
	}

	// sync.Once guarantees this block executes exactly one time.
	//
	// Why is this necessary?
	//
	// Closing an already closed channel causes a panic.
	//
	// Example:
	//
	// close(queue)
	// close(queue)   ← panic
	//
	// If several goroutines accidentally call Close(),
	// sync.Once ensures only the first call performs shutdown.
	t.closeOnce.Do(func() {

		// Mark the tracker as closed.
		//
		// Enqueue() checks this flag and immediately rejects
		// any new usage events.
		//
		// This prevents new requests from adding work while
		// shutdown is already in progress.
		t.closed.Store(true)

		// Closing the queue does NOT immediately destroy
		// everything inside it.
		//
		// Instead:
		//
		// Existing queued items remain available.
		//
		// New sends become impossible.
		//
		// The worker continues reading until the queue
		// becomes completely empty.
		close(t.queue)
	})

	// Wait until one of two things happens.
	select {

	// Background worker finished successfully.
	//
	// run() closes the done channel just before exiting.
	case <-t.done:
		return nil

	// Caller cancelled shutdown.
	//
	// Example:
	//
	// ctx, cancel := context.WithTimeout(..., 5*time.Second)
	//
	// If the worker hasn't finished after 5 seconds,
	// shutdown returns with a timeout error.
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the background worker responsible for persisting usage events.
//
// This goroutine starts once during application startup:
//
//	go t.run()
//
// and continues running for the entire lifetime of the gateway.
//
// Main Goroutine:
//
//	  Request
//	     │
//	     ▼
//	queue <- record
//
// Worker Goroutine:
//
//	range queue
//	     │
//	     ▼
//	INSERT INTO usage
//
// Because these run independently,
// client requests never wait for database writes.
func (t *AsyncUsageTracker) run() {

	// When this function exits,
	// notify Close() that shutdown has completed.
	//
	// defer guarantees this executes regardless of
	// how the function returns.
	defer close(t.done)

	// Read usage events until the channel is closed.
	//
	// This is one of Go's nicest channel features.
	//
	// Before Close():
	//
	// Queue:
	//
	// Usage1
	// Usage2
	// Usage3
	//
	// range automatically reads:
	//
	// record = Usage1
	// record = Usage2
	// record = Usage3
	//
	// Once the queue is closed AND empty,
	// the loop exits automatically.
	for record := range t.queue {

		// Create a fresh background context.
		//
		// Why not reuse the original request context?
		//
		// Because the HTTP request has already finished.
		//
		// Its context is probably cancelled.
		//
		// The worker still needs enough time
		// to write the usage row.
		//
		// Therefore it creates its own context.
		ctx, cancel := context.WithTimeout(
			context.Background(),
			2*time.Second,
		)

		// Store the usage record.
		//
		// Repository handles SQL.
		//
		// Worker only coordinates background processing.
		_, err := t.repo.Log(ctx, record)

		// Always release resources associated with
		// the timeout context.
		cancel()

		// Database write failed.
		//
		// Example reasons:
		//
		// • PostgreSQL unavailable
		// • Network issue
		// • Constraint violation
		// • Timeout
		if err != nil && t.logger != nil {

			// Log enough information so operators
			// can identify exactly which request failed.
			t.logger.Error(
				"usage record write failed",

				"tenant_id",
				record.TenantID,

				"request_id",
				record.RequestID,

				"error",
				err.Error(),
			)
		}

		// Successfully persisted.
		//
		// Record operational metrics.
		//
		// Example:
		//
		// gateway_usage_persisted_total 182391
		if err == nil && t.metrics != nil {
			t.metrics.IncCounter(
				"gateway_usage_persisted_total",
				nil,
			)
		}
	}
}

// validateUsageRecord performs basic validation before a usage event
// enters the queue.
//
// Why validate here?
//
// Imagine invalid records are allowed:
//
// Queue:
//
//	Invalid
//	Invalid
//	Invalid
//	Invalid
//
// Worker:
//
//	INSERT ...
//	INSERT ...
//	INSERT ...
//
// Every insert fails.
//
// Meanwhile,
// valid events are waiting behind invalid ones.
//
// By validating first,
// only meaningful usage events consume queue capacity.
//
// This function intentionally performs lightweight validation.
//
// It does NOT:
//
// • query the database
// • verify foreign keys
// • check tenant existence
//
// Those responsibilities belong elsewhere.
//
// Here we only verify that the record is complete enough
// to be worth storing.
func validateUsageRecord(record models.Usage) error {

	// Every usage record must belong to a tenant.
	//
	// Without tenant information,
	// billing and analytics become impossible.
	if record.TenantID == "" {
		return errors.New("tenant_id is required")
	}

	// Path identifies which endpoint
	// generated this usage event.
	//
	// Example:
	//
	// /v1/products
	// /v1/orders
	if record.Path == "" {
		return errors.New("path is required")
	}

	// Method identifies the HTTP operation.
	//
	// Example:
	//
	// GET
	// POST
	// DELETE
	if record.Method == "" {
		return errors.New("method is required")
	}

	// Status code tells whether the request
	// succeeded or failed.
	//
	// Examples:
	//
	// 200
	// 404
	// 500
	//
	// Zero indicates it was never populated.
	if record.StatusCode <= 0 {
		return errors.New("status_code is required")
	}

	// Timestamp records when this usage
	// event occurred.
	//
	// Zero time usually means the caller
	// forgot to populate it.
	if record.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}

	// Record looks valid.
	return nil
}
