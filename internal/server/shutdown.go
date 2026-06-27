package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
)

// ShutdownHook describes one background component that must be closed during shutdown.
//
// What is a shutdown hook?
// Think of it as a "cleanup task" that the gateway must execute before exiting.
//
// Example:
//
//	Gateway starts
//	    |
//	    +--> HTTP Server
//	    +--> Usage Tracker Worker
//	    +--> Cache Cleanup Worker
//	    +--> Metrics Exporter
//
// When the process receives SIGTERM (docker stop / kubernetes shutdown),
// every background component should be stopped gracefully instead of being
// killed immediately.
//
// A ShutdownHook tells the ShutdownManager:
//
//	"When shutting down, call THIS function to cleanly stop me."
//
// Why this exists:
// The HTTP server is not the only thing running inside the gateway.
// There may be many background goroutines performing work:
//
//   - async usage writers
//   - cache flushers
//   - metrics exporters
//   - background cleanup jobs
//   - scheduled refresh tasks
//
// Each one knows how to stop itself, so the shutdown manager only needs
// to store a function that it can call later.
type ShutdownHook struct {

	// Name is a human-readable label.
	//
	// Why this exists:
	// During shutdown we usually log what is happening.
	//
	// Example log:
	//
	//	closing hook: usage-tracker
	//	closing hook: cache-worker
	//	closing hook: metrics-exporter
	//
	// Without a name it would be difficult to know which component
	// is currently shutting down or which one failed.
	Name string

	// Close is the cleanup function for one component.
	//
	// Think of this as:
	//
	//	"When the application is shutting down,
	//	 call this function."
	//
	// Example:
	//
	//	hook := ShutdownHook{
	//	    Name: "usage-tracker",
	//	    Close: tracker.Close,
	//	}
	//
	// Later during shutdown:
	//
	//	hook.Close(ctx)
	//
	// executes:
	//
	//	tracker.Close(ctx)
	//
	// Why this is a function instead of an interface:
	//
	// We only care about ONE operation:
	//
	//	Close(context.Context) error
	//
	// Every component already has its own Close method,
	// so storing the function directly is much simpler than creating
	// another interface just for shutdown.
	//
	// This also makes registration very easy:
	//
	//	manager.Register(ShutdownHook{
	//	    Name: "usage-tracker",
	//	    Close: tracker.Close,
	//	})
	Close func(context.Context) error
}

// ShutdownManager coordinates graceful shutdown of the entire gateway.
//
// Think of this as the "shutdown coordinator" or "traffic controller."
//
// Normal lifecycle:
//
//	Client Requests
//	      |
//	      v
//	+-------------------+
//	| Shutdown Manager  |
//	+-------------------+
//	      |
//	      v
//	  HTTP Handlers
//	      |
//	      v
//	  Background Workers
//
// During shutdown:
//
//  1. Stop accepting new requests.
//  2. Wait for currently running requests to finish.
//  3. Stop background workers.
//  4. Exit process safely.
//
// Why this exists:
//
// If we simply terminate the process:
//
//	docker stop
//
// the operating system may kill everything immediately.
//
// Consequences:
//
//	Request A  ---> half completed
//	Request B  ---> DB write interrupted
//	Request C  ---> usage log lost
//
// Graceful shutdown avoids this by allowing current work to finish first.
//
// The HTTP server already knows how to stop accepting new connections,
// but it does NOT know anything about:
//
//   - usage tracker workers
//   - cache workers
//   - cleanup goroutines
//
// ShutdownManager coordinates all of them.
type ShutdownManager struct {

	// shuttingDown becomes true once shutdown begins.
	//
	// Example timeline:
	//
	//	Normal State
	//
	//	shuttingDown = false
	//
	//	Client Request
	//	     |
	//	     +--> Allowed
	//
	//
	// Shutdown starts
	//
	//	shuttingDown = true
	//
	//	New Client Request
	//	     |
	//	     +--> Immediately rejected
	//
	// Existing requests continue running.
	//
	// Why atomic.Bool?
	//
	// Hundreds of request goroutines may check this flag simultaneously.
	//
	// Using atomic allows safe concurrent reads and writes
	// without requiring a mutex.
	shuttingDown atomic.Bool

	// inFlight tracks how many requests are currently executing.
	//
	// Think of it like a counter:
	//
	//	Request A starts
	//	inFlight = 1
	//
	//	Request B starts
	//	inFlight = 2
	//
	//	Request C starts
	//	inFlight = 3
	//
	//	Request A finishes
	//	inFlight = 2
	//
	//	Request B finishes
	//	inFlight = 1
	//
	//	Request C finishes
	//	inFlight = 0
	//
	// During shutdown:
	//
	//	manager waits until
	//
	//	inFlight == 0
	//
	// before stopping background workers.
	//
	// Why WaitGroup?
	//
	// WaitGroup is specifically designed for waiting until
	// multiple goroutines complete.
	inFlight sync.WaitGroup

	// hooksMu protects access to the hooks slice.
	//
	// Why?
	//
	// Multiple goroutines may register shutdown hooks while
	// the application is starting.
	//
	// Since slices are not thread-safe, we protect them
	// with a mutex.
	//
	// Example:
	//
	//	Goroutine A registers Usage Tracker
	//
	//	Goroutine B registers Cache Worker
	//
	// Without hooksMu these writes could race and corrupt
	// the slice.
	hooksMu sync.Mutex

	// hooks stores every registered background component.
	//
	// Example contents:
	//
	//	hooks = [
	//	    {
	//	        Name: "usage-tracker",
	//	        Close: tracker.Close,
	//	    },
	//	    {
	//	        Name: "cache-worker",
	//	        Close: cache.Close,
	//	    },
	//	    {
	//	        Name: "metrics-exporter",
	//	        Close: metrics.Close,
	//	    },
	//	]
	//
	// During shutdown the manager simply loops over this slice:
	//
	//	for _, hook := range hooks {
	//	    hook.Close(ctx)
	//	}
	//
	// This design makes the shutdown manager generic.
	//
	// It doesn't need to know WHAT each component is.
	// It only knows:
	//
	//	"I have a function that closes it."
	hooks []ShutdownHook
}

// NewShutdownManager creates a new shutdown coordinator.
//
// Why this constructor exists:
// It gives the application one small place to register background closers and
// one place to coordinate shutdown behavior.
func NewShutdownManager() *ShutdownManager {
	return &ShutdownManager{
		hooks: make([]ShutdownHook, 0),
	}
}

// Wrap adds graceful-shutdown behavior around an HTTP handler.
//
// What this wrapper does:
// - if shutdown has begun, return 503 immediately
// - otherwise track the request as in-flight
// - let the request run normally
//
// Why this is important:
// Even if the server is already shutting down, this prevents late requests from
// doing more work and keeps shutdown behavior predictable.
func (m *ShutdownManager) Wrap(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeShutdownJSON(w, http.StatusServiceUnavailable, "shutdown_error", "handler is not configured")
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}

		// If shutdown already started, reject new work quickly.
		if m.shuttingDown.Load() {
			writeShutdownJSON(w, http.StatusServiceUnavailable, "shutting_down", "gateway is shutting down")
			return
		}

		m.inFlight.Add(1)
		defer m.inFlight.Done()

		// Check again after adding to the in-flight count.
		// This closes a tiny race window where shutdown starts between the first
		// check and the Add call.
		if m.shuttingDown.Load() {
			writeShutdownJSON(w, http.StatusServiceUnavailable, "shutting_down", "gateway is shutting down")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RegisterHook registers one background shutdown hook.
//
// Example:
//
//	shutdown.RegisterHook("usage-tracker", usageTracker.Close)
//
// Why this exists:
// The shutdown manager should not need to know the concrete type of every
// background worker. It only needs a close function.
func (m *ShutdownManager) RegisterHook(name string, closeFn func(context.Context) error) {
	if m == nil || closeFn == nil {
		return
	}

	m.hooksMu.Lock()
	defer m.hooksMu.Unlock()

	m.hooks = append(m.hooks, ShutdownHook{
		Name:  name,
		Close: closeFn,
	})
}

// Shutdown performs a graceful shutdown of the gateway.
//
// Shutdown happens in three distinct phases:
//
//  1. Stop accepting new work.
//     New incoming HTTP requests are immediately rejected with HTTP 503
//     (Service Unavailable) by the ShutdownManager wrapper.
//
//  2. Wait for all currently executing requests to finish.
//     Existing requests are allowed to complete normally so that users do not
//     experience interrupted responses or partially completed operations.
//
//  3. Close all registered background workers.
//     Once no request can enqueue new work, background components such as the
//     usage tracker, cache workers, or future async jobs are shut down safely.
//
// Why this order matters:
//
// Imagine a request is currently being processed:
//
//	Client Request
//	      │
//	      ▼
//	  HTTP Handler
//	      │
//	      ▼
//	  Usage Tracker  ← background worker
//
// If the usage tracker is stopped *before* the request finishes,
// the request may try to write usage data into a worker that no longer exists,
// causing data loss or runtime errors.
//
// Therefore the correct shutdown order is:
//
//	Reject New Requests
//	         ↓
//	Wait Active Requests
//	         ↓
//	Close Background Workers
//
// If the provided context expires while waiting, shutdown is aborted and the
// context error is returned. This prevents the application from hanging forever
// during shutdown.
func (m *ShutdownManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}

	// -------------------------------------------------------------------------
	// Phase 1: Prevent any new requests from entering the gateway.
	//
	// The HTTP wrapper checks this atomic flag before executing handlers.
	// Once this flag becomes true, every new request immediately receives
	// HTTP 503 (Service Unavailable).
	//
	// Existing requests are NOT interrupted. They continue normally.
	// -------------------------------------------------------------------------
	m.shuttingDown.Store(true)

	// -------------------------------------------------------------------------
	// Phase 2: Wait for all in-flight requests to complete.
	//
	// WaitGroup.Wait() blocks until every request that previously called
	// Add(1) has eventually called Done().
	//
	// Wait() itself cannot be cancelled, so it runs inside a separate goroutine.
	// This allows us to simultaneously wait for either:
	//
	//   • all requests to finish
	//   • the shutdown context timeout/cancellation
	//
	// whichever happens first.
	// -------------------------------------------------------------------------
	waitDone := make(chan struct{})

	go func() {
		m.inFlight.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		// Every active request finished successfully.
		// It is now safe to shut down background workers.

	case <-ctx.Done():
		// Shutdown timeout expired before requests completed.
		// Returning here prevents waiting forever.
		return ctx.Err()
	}

	// -------------------------------------------------------------------------
	// Phase 3: Shut down background workers.
	//
	// Since no requests are still running, no component can enqueue additional
	// asynchronous work.
	//
	// Examples:
	//   - Usage Tracker
	//   - Cache Cleanup Worker
	//   - Metrics Exporter
	//   - Future Message Queue Consumer
	//
	// Each hook is executed independently. One failure should not prevent
	// the remaining hooks from being closed.
	// -------------------------------------------------------------------------
	hooks := m.copyHooks()

	var errs []error

	for _, hook := range hooks {
		if hook.Close == nil {
			continue
		}

		if err := hook.Close(ctx); err != nil {
			// Collect every shutdown error instead of stopping at the first one.
			// This gives the caller complete information about which components
			// failed to shut down cleanly.
			errs = append(errs, errors.New(hook.Name+": "+err.Error()))
		}
	}

	// Join all collected errors into one.
	//
	// If no hook failed, errors.Join(nil...) returns nil.
	return errors.Join(errs...)
}

// copyHooks makes a safe copy of the hook list.
//
// Why this exists:
// Shutdown should not hold the hook mutex while calling arbitrary close functions.
func (m *ShutdownManager) copyHooks() []ShutdownHook {
	m.hooksMu.Lock()
	defer m.hooksMu.Unlock()

	out := make([]ShutdownHook, len(m.hooks))
	copy(out, m.hooks)
	return out
}

// IsShuttingDown reports whether shutdown has started.
//
// Why this exists:
// Some code paths may want to quickly check whether the gateway is still accepting work.
func (m *ShutdownManager) IsShuttingDown() bool {
	if m == nil {
		return false
	}
	return m.shuttingDown.Load()
}

// writeShutdownJSON writes a small JSON error response.
//
// Why this exists:
// A request that arrives during shutdown should get a clean, stable error body.
func writeShutdownJSON(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   code,
		"message": message,
	})
}

// GracefulServer ties together an HTTP server and the shutdown manager.
//
// Why this exists:
// It gives the application one small helper to start and stop the gateway cleanly.
type GracefulServer struct {
	Server  *http.Server
	Manager *ShutdownManager
}

// Shutdown stops the HTTP server and then closes background hooks.
//
// Why this method exists:
// http.Server.Shutdown stops accepting new connections and waits for active
// handlers to return. After that, we close the async workers.
func (g *GracefulServer) Shutdown(ctx context.Context) error {
	if g == nil {
		return nil
	}

	var errs []error

	if g.Server != nil {
		if err := g.Server.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if g.Manager != nil {
		if err := g.Manager.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// Start runs the HTTP server.
//
// Why this exists:
// It keeps startup wiring simple and lets the application treat the server as
// one unit.
func (g *GracefulServer) Start() error {
	if g == nil || g.Server == nil {
		return errors.New("server is not configured")
	}

	return g.Server.ListenAndServe()
}
