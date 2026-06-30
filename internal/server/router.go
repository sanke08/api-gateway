// Package server contains the HTTP router used by the API gateway.
//
// ============================================================================
// ROUTER ARCHITECTURE OVERVIEW
// ============================================================================
//
// This router is built around ONE central idea:
//
//	"Do all expensive work at startup. Keep the request path lock-free and fast."
//
// To achieve this, the router separates its lifecycle into two distinct phases:
//
// ----------------------------------------------------------------------------
// PHASE 1 — REGISTRATION TIME (happens at startup, only once)
// ----------------------------------------------------------------------------
//
//	router.GET("/users/{id}", handler)
//	          │
//	          ▼
//	   parsePattern()              // turns "/users/{id}" into segments
//	          │
//	          ▼
//	     route struct              // stores raw definition + handler
//	          │
//	          ▼
//	      rebuild()                // compiles everything into a trie
//	          │
//	          ▼
//	 immutable compiled trie       // optimized read-only routing tree
//	          │
//	          ▼
//	    atomic state swap          // new state visible to all goroutines
//
// ----------------------------------------------------------------------------
// PHASE 2 — REQUEST TIME (happens for every incoming HTTP request)
// ----------------------------------------------------------------------------
//
//	GET /users/123
//	          │
//	          ▼
//	     state.Load()              // grab immutable snapshot
//	          │
//	          ▼
//	    trie traversal             // O(path-depth) tree walk
//	          │
//	          ▼
//	    builtRoute found           // already has middleware baked in
//	          │
//	          ▼
//	  handler already wrapped      // no per-request wrapping needed
//	          │
//	          ▼
//	       response
//
// ============================================================================
// WHY THIS ARCHITECTURE MATTERS
// ============================================================================
//
// A naive router does this PER REQUEST:
//
//   - linear scan over a slice of routes
//   - rebuild middleware chain
//   - re-parse route patterns
//   - allocate temporary structures
//
// This router does this PER REQUEST:
//
//   - one trie lookup
//   - execute pre-wrapped handler
//
// The expensive compilation step (rebuild) happens ONCE at startup, and again
// only when new routes or middleware are registered (which should also only
// happen at startup). This is the same design used by production-grade
// gateways, proxies, and load balancers.
package server

import (
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"

	requesttypes "github.com/sanke08/api_gateway/internal/pkg/types"
)

// Middleware is a standard HTTP middleware function.
//
// It takes an http.Handler and returns a NEW http.Handler that wraps the
// original. Middlewares compose by nesting — the outermost middleware runs
// first, then delegates inward.
//
// ----------------------------------------------------------------------------
// Visual model
// ----------------------------------------------------------------------------
//
//	Logging( Auth( Handler ) )
//
//	Request
//	   │
//	   ▼
//	┌──────────────────┐
//	│ Logging          │  ← outermost
//	│   ┌────────────┐ │
//	│   │ Auth       │ │
//	│   │  ┌───────┐ │ │
//	│   │  │Handler│ │ │  ← innermost (actual business logic)
//	│   │  └───────┘ │ │
//	│   └────────────┘ │
//	└──────────────────┘
//
// ----------------------------------------------------------------------------
// Example usage
// ----------------------------------------------------------------------------
//
//	router.Use(LoggingMiddleware, RecoveryMiddleware)
//	router.GET("/users", usersHandler)
//
// Final execution order on a request:
//
//	Logging → Recovery → usersHandler
//
// ----------------------------------------------------------------------------
// Why middleware exists
// ----------------------------------------------------------------------------
//
// Middleware lets you express cross-cutting concerns ONCE instead of
// duplicating them in every handler. Typical uses:
//
//   - logging
//   - authentication / authorization
//   - request tracing
//   - metrics collection
//   - panic recovery
//   - rate limiting
type Middleware func(http.Handler) http.Handler

// Router is the main HTTP router for the gateway.
//
// It is responsible for:
//
//	Incoming Request
//	       │
//	       ▼
//	   Find Route          (trie traversal)
//	       │
//	       ▼
//	 Execute Middleware    (already pre-wrapped)
//	       │
//	       ▼
//	    Run Handler
//
// ----------------------------------------------------------------------------
// Example
// ----------------------------------------------------------------------------
//
//	router.GET("/users/{id}", handler)
//
// On a request "GET /users/123" the router must:
//
//  1. match the pattern "/users/{id}"
//  2. extract the parameter "id=123"
//  3. run global + route-specific middleware
//  4. invoke the actual handler
//
// ----------------------------------------------------------------------------
// Two-plane design
// ----------------------------------------------------------------------------
//
// This router explicitly separates two planes of execution:
//
//	╔══════════════════════════════╗      ╔══════════════════════════════╗
//	║      CONTROL PLANE           ║      ║       DATA PLANE             ║
//	║──────────────────────────────║      ║──────────────────────────────║
//	║  • route registration        ║      ║  • request handling          ║
//	║  • middleware registration   ║      ║  • trie lookup               ║
//	║  • route compilation         ║      ║  • parameter extraction      ║
//	║  • locking + rebuilding      ║      ║  • lock-free, alloc-light    ║
//	╚══════════════════════════════╝      ╚══════════════════════════════╝
//
// The control plane can be slow (it only runs at startup). The data plane
// MUST be fast — it runs on every request. Almost all expensive work
// (parsing, middleware chaining, trie construction) is pushed into the
// control plane via the rebuild() step.
type Router struct {
	// mu protects router rebuilding (the control plane).
	//
	// ------------------------------------------------------------------------
	// Why a lock is still needed
	// ------------------------------------------------------------------------
	//
	// Route registration mutates shared in-memory state:
	//
	//	router.GET(...)
	//	router.POST(...)
	//
	// If two goroutines register routes concurrently — or one registers
	// while another is rebuilding — you can corrupt the routes slice or
	// produce an invalid trie.
	//
	// Dangerous example without locking:
	//
	//	goroutine A:  appending to routes
	//	goroutine B:  iterating routes during rebuild
	//
	// Result: data race, panics, lost routes.
	//
	// ------------------------------------------------------------------------
	// Important: requests do NOT take this lock
	// ------------------------------------------------------------------------
	//
	// The data plane reads only the compiled `state` field, which is
	// effectively immutable once published. That keeps request handling
	// lock-free and fast.
	mu sync.Mutex

	// routes stores RAW route definitions, before they are compiled into the
	// trie. Think of these as "source code" that rebuild() compiles into the
	// fast runtime representation.
	//
	// ------------------------------------------------------------------------
	// Example
	// ------------------------------------------------------------------------
	//
	//	router.GET("/users/{id}", handler)
	//
	// produces an entry:
	//
	//	&route{
	//	    method:  "GET",
	//	    pattern: "/users/{id}",
	//	    ...
	//	}
	//
	// ------------------------------------------------------------------------
	// Compiler analogy
	// ------------------------------------------------------------------------
	//
	//	source code  ─►  compiler  ─►  optimized binary
	//	  routes     ─►  rebuild   ─►       state
	routes []*route

	// middlewares are GLOBAL middlewares — applied to every route.
	//
	// ------------------------------------------------------------------------
	// Example
	// ------------------------------------------------------------------------
	//
	//	router.Use(Logging, Recovery, Metrics)
	//
	// Every request then executes:
	//
	//	Request → Logging → Recovery → Metrics → Handler
	//
	// ------------------------------------------------------------------------
	// Why global middleware matters
	// ------------------------------------------------------------------------
	//
	// Infrastructure concerns (observability, safety, security) should be
	// centralized. You should NOT have to remember to manually wire:
	//
	//	logging(); recovery(); tracing()
	//
	// inside every handler. Global middleware applies them automatically.
	middlewares []Middleware

	// state is the FULLY COMPILED, immutable runtime router.
	//
	// ------------------------------------------------------------------------
	// Contract
	// ------------------------------------------------------------------------
	//
	//	- Requests ONLY read from this field.
	//	- Requests NEVER mutate it.
	//	- rebuild() replaces this pointer atomically with a brand new state.
	//
	// ------------------------------------------------------------------------
	// Why this pattern is used
	// ------------------------------------------------------------------------
	//
	// "Read-mostly + replace-on-write" is the classic pattern used by:
	//
	//	- API gateways
	//	- reverse proxies
	//	- load balancers
	//	- service meshes
	//
	// It allows:
	//
	//	- lock-free reads on the hot path
	//	- safe concurrent rebuilds
	//	- no partial visibility — readers see either the old state or the
	//	  fully compiled new state, never something half-built
	state *state

	// notFoundHandler handles requests whose path doesn't exist anywhere
	// in the registered route tree (HTTP 404).
	notFoundHandler http.Handler

	// methodNotAllowedHandler handles requests whose path EXISTS but doesn't
	// support the requested HTTP method (HTTP 405).
	//
	// Example: a GET-only "/users" route receiving a DELETE request.
	methodNotAllowedHandler http.Handler
}

// route represents ONE registered route, BEFORE compilation into the trie.
//
// This is registration-time metadata — it is NOT what gets executed on a
// request. rebuild() consumes []*route and produces the optimized trie
// (made of `node` and `builtRoute` values).
//
// ----------------------------------------------------------------------------
// Example
// ----------------------------------------------------------------------------
//
//	router.GET("/users/{id}", handler)
//
// becomes:
//
//	&route{
//	    method:   "GET",
//	    pattern:  "/users/{id}",
//	    key:      "/users/{}",
//	    segments: [{literal: "users"}, {isParam: true, paramName: "id"}],
//	    handler:  handler,
//	}
type route struct {

	// method is the HTTP method this route responds to.
	//
	// Examples: "GET", "POST", "PUT", "PATCH", "DELETE"
	//
	// Method-aware routing is critical because these are DIFFERENT routes
	// even though they share a path:
	//
	//	GET  /users   →  list users
	//	POST /users   →  create a user
	method string

	// pattern is the normalized route pattern as registered.
	//
	// ------------------------------------------------------------------------
	// Normalization example
	// ------------------------------------------------------------------------
	//
	//	Input:       "users/{id}/"
	//	Normalized:  "/users/{id}"
	//
	// Without normalization these would all behave differently, even though
	// users clearly mean the same thing:
	//
	//	/users      /users/      users
	//
	// Normalization prevents:
	//
	//	- duplicate-route bugs
	//	- matching inconsistencies
	//	- surprising 404s on trailing slashes
	pattern string

	// key encodes the STRUCTURAL identity of the route — its shape with
	// parameter names stripped. Used purely for duplicate detection.
	//
	// ------------------------------------------------------------------------
	// Example
	// ------------------------------------------------------------------------
	//
	//	/users/{id}        ─►  /users/{}
	//	/users/{userID}    ─►  /users/{}
	//
	// Both produce the SAME key. Why does that matter?
	//
	// Because if both were allowed to register, a request "/users/123"
	// could match either route — and the router would have no principled
	// way to choose. To prevent that ambiguity, the router rejects two
	// routes with the same (method, key) pair at registration time.
	key string

	// segments is the parsed, structured form of the pattern. This is what
	// gets walked into the trie during rebuild().
	//
	// ------------------------------------------------------------------------
	// Example
	// ------------------------------------------------------------------------
	//
	//	"/users/{id}"
	//
	// becomes:
	//
	//	[
	//	    {literal: "users"},
	//	    {isParam: true, paramName: "id"},
	//	]
	//
	// ------------------------------------------------------------------------
	// Why parse at registration, not at request time
	// ------------------------------------------------------------------------
	//
	// Slow path (BAD):
	//
	//	request arrives → parse pattern → match
	//
	// Fast path (GOOD — what this router does):
	//
	//	startup → parse pattern ONCE → store segments
	//	request → walk pre-parsed trie
	//
	// Per-request work shrinks dramatically.
	segments []routeSegment

	// handler is the original endpoint handler as supplied by the caller.
	// It has NOT yet been wrapped with middleware — that happens in rebuild().
	handler http.Handler

	// middlewares are route-specific middlewares — applied only to this
	// route, in addition to the global ones.
	//
	// Example:
	//
	//	router.GET("/admin", handler, AuthMW, AuditMW)
	//
	// Final compiled handler (after rebuild) looks like:
	//
	//	Global1( Global2( AuthMW( AuditMW( handler ) ) ) )
	middlewares []Middleware
}

// routeSegment represents ONE segment of a parsed route path.
//
// A path "/users/{id}/posts" is parsed into three segments:
//
//	[ literal:"users" ] [ param:"id" ] [ literal:"posts" ]
//
// There are exactly two kinds of segments:
//
//  1. Literal segment   — static text, e.g. "users"
//  2. Parameter segment — captured by name,   e.g. "{id}"
type routeSegment struct {
	// isParam distinguishes the two segment kinds. If true, this segment
	// matches ANY single path component and captures it under paramName.
	isParam bool

	// isCatchAll marks a trailing wildcard segment of the form "{name...}".
	// Unlike a plain parameter (which matches exactly one path component), a
	// catch-all matches the ENTIRE remainder of the path — zero or more
	// components — and captures it (joined by "/") under paramName. It is only
	// valid as the LAST segment of a pattern.
	//
	// Example: "/{path...}" matches "/", "/orders", "/orders/123", "/v1/u/5".
	isCatchAll bool

	// literal is the static text for literal segments.
	//
	// Example: for segment "users" in "/users/{id}", literal = "users".
	literal string

	// paramName is the parameter name for parameter segments.
	//
	// Example: for segment "{id}" in "/users/{id}", paramName = "id".
	paramName string
}

// builtRoute is the FINAL, fully-compiled runtime representation of a route.
//
// It lives inside the trie (node.routes) and is what ServeHTTP actually
// executes. By the time we have a builtRoute, all middleware is already
// wrapped — no per-request wrapping happens.
//
// ----------------------------------------------------------------------------
// Slow vs fast handler construction
// ----------------------------------------------------------------------------
//
// Naive approach (BAD):
//
//	on every request:
//	    handler = Logging(Recovery(Auth(originalHandler)))
//	    handler.ServeHTTP(w, r)
//
// This allocates closures per request.
//
// This router (GOOD):
//
//	on startup (once):
//	    builtRoute.handler = Logging(Recovery(Auth(originalHandler)))
//
//	on every request:
//	    builtRoute.handler.ServeHTTP(w, r)
type builtRoute struct {
	// method is the HTTP method this compiled route serves.
	method string

	// handler is the FULLY middleware-wrapped handler — ready to execute.
	//
	// Conceptual structure:
	//
	//	Logging(
	//	    Recovery(
	//	        Auth(
	//	            originalHandler
	//	        )
	//	    )
	//	)
	//
	// All this wrapping happens during rebuild(), NEVER during a request.
	handler http.Handler

	// paramNames lists parameter names in path order — used to zip captured
	// values back into a name→value map at request time.
	//
	// Example route: "/users/{user_id}/posts/{post_id}"
	//
	//	paramNames = ["user_id", "post_id"]
	//
	// Request: "/users/123/posts/999"
	//
	//	captured values = ["123", "999"]
	//	final params    = {"user_id": "123", "post_id": "999"}
	paramNames []string
}

// node is one node in the routing trie.
//
// ----------------------------------------------------------------------------
// Why a trie (and not a slice of routes)?
// ----------------------------------------------------------------------------
//
// A naive router scans EVERY registered route on every request. That's O(N)
// where N = number of routes — fine for 5 routes, terrible for 500.
//
// A trie shares common path prefixes, so lookup is O(path-depth) regardless
// of how many routes are registered.
//
// Example registered routes:
//
//	/users
//	/users/{id}
//	/users/settings
//	/posts
//	/posts/{id}
//
// Resulting trie:
//
//	root
//	 ├── "users"
//	 │     ├── "settings"           (leaf: GET /users/settings)
//	 │     └── {param}              (leaf: GET /users/{id})
//	 │     (and the "users" node itself is a leaf: GET /users)
//	 └── "posts"
//	       └── {param}              (leaf: GET /posts/{id})
//
// Walking "/users/123":
//
//	root → staticChildren["users"] → paramChild ✓
//
// Only THREE pointer hops. No slice scan. No string compare against every
// route. This is why production routers use tries.
type node struct {
	// staticChildren holds children matched by exact literal segments.
	// Lookup is O(1) via map.
	//
	// Example:
	//
	//	if a "/users/settings" route exists:
	//	    root.staticChildren["users"].staticChildren["settings"]
	staticChildren map[string]*node

	// paramChild holds the single "any value" child for parameter segments.
	//
	// A node has at most ONE paramChild because two parameter siblings
	// would be ambiguous — both would match the same path component.
	//
	// ------------------------------------------------------------------------
	// Static-first precedence
	// ------------------------------------------------------------------------
	//
	// During matching, staticChildren are tried BEFORE paramChild. So given:
	//
	//	/users/settings        (static)
	//	/users/{id}            (param)
	//
	// the request "/users/settings" matches the STATIC route. This is the
	// expected behavior — exact matches always win over wildcards.
	paramChild *node

	// catchAllChild holds the single trailing-wildcard child for a "{name...}"
	// segment. It is tried LAST — only after both staticChildren and paramChild
	// fail to produce a match — so more specific routes always win. When it
	// matches, it consumes ALL remaining path segments at once.
	//
	// catchAllParam is the parameter name under which the captured remainder
	// (joined by "/") is exposed to the handler.
	catchAllChild *node
	catchAllParam string

	// routes maps HTTP method → compiled route at THIS node.
	//
	// Why method-keyed:
	//
	//	GET  /users   and   POST /users
	//
	// share the exact same trie path but execute different handlers.
	//
	//	routes["GET"]  → list-users builtRoute
	//	routes["POST"] → create-user builtRoute
	routes map[string]*builtRoute

	// allowedMethods is a precomputed, sorted list of HTTP methods this node
	// supports. Used to build the "Allow:" response header on a 405 reply.
	//
	// Precomputed during rebuild() so request handling never has to iterate
	// the routes map just to construct that header.
	allowedMethods []string
}

// state is the immutable, fully-compiled router snapshot.
//
// Every request reads from a `*state`. New routes/middleware = new state.
// The router atomically swaps `Router.state` to publish a new version.
//
// ----------------------------------------------------------------------------
// Why immutability matters here
// ----------------------------------------------------------------------------
//
// A request that started reading the OLD state can safely continue reading
// it even while a rebuild is producing a NEW state. Once rebuild finishes,
// subsequent requests pick up the new state. No request ever sees a
// half-built trie.
//
// This avoids:
//
//   - request-side locking
//   - request blocking during rebuilds
//   - partial-state visibility bugs
type state struct {
	// root is the trie root. Every match starts here.
	root *node

	// notFound is the handler invoked when no trie path matches.
	notFound http.Handler

	// methodNotAllowed is the handler invoked when the path matches but the
	// HTTP method is not registered for that path.
	methodNotAllowed http.Handler
}

// NewRouter creates and returns a fresh Router instance, ready to use.
//
// ----------------------------------------------------------------------------
// What it does
// ----------------------------------------------------------------------------
//
//  1. Creates an empty Router value.
//  2. Installs the default 404 and 405 handlers.
//  3. Calls rebuild() once so the Router has a VALID (empty) state
//     immediately — even before any routes are registered.
//
// ----------------------------------------------------------------------------
// Why rebuild() is called eagerly here
// ----------------------------------------------------------------------------
//
// If you forget to call rebuild and a request lands, ServeHTTP would
// dereference a nil state. The invariant we want is:
//
//	"At every observable moment, the Router has a valid runtime state."
//
// Calling rebuild() in the constructor guarantees that.
//
// Example:
//
//	r := NewRouter()
//	// even with zero routes registered, this safely returns 404:
//	r.ServeHTTP(w, req)
func NewRouter() *Router {
	r := &Router{
		notFoundHandler:         http.HandlerFunc(defaultNotFoundHandler),
		methodNotAllowedHandler: http.HandlerFunc(defaultMethodNotAllowedHandler),
	}

	r.rebuild()

	return r
}

// defaultNotFoundHandler is the fallback HTTP 404 handler.
//
// ----------------------------------------------------------------------------
// When it fires
// ----------------------------------------------------------------------------
//
//	Request:  GET /this/path/does/not/exist
//	Response: 404 Not Found
//
// ----------------------------------------------------------------------------
// Why it's a separate function
// ----------------------------------------------------------------------------
//
// Real applications usually want a custom 404 — JSON body, gateway error
// envelope, HTML page, etc. Keeping the default separate makes it trivial
// to override via Router.SetNotFoundHandler.
func defaultNotFoundHandler(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// defaultMethodNotAllowedHandler is the fallback HTTP 405 handler.
//
// ----------------------------------------------------------------------------
// 404 vs 405 — important distinction
// ----------------------------------------------------------------------------
//
//	404 Not Found           → "this path doesn't exist at all"
//	405 Method Not Allowed  → "this path exists, but not for this method"
//
// Example:
//
//	Registered:  GET /users
//	Request:     DELETE /users
//	Response:    405 Method Not Allowed
//	             Allow: GET
//
// ----------------------------------------------------------------------------
// Why this matters
// ----------------------------------------------------------------------------
//
// Returning correct 404 vs 405 is part of being a well-behaved HTTP server.
// Clients, SDKs, browsers, proxies, and API tooling all rely on this
// distinction to give users meaningful error messages.
func defaultMethodNotAllowedHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusMethodNotAllowed)
	_, _ = w.Write([]byte(http.StatusText(http.StatusMethodNotAllowed)))
}

// Use registers GLOBAL middleware — middleware that wraps every route.
//
// ----------------------------------------------------------------------------
// Example
// ----------------------------------------------------------------------------
//
//	router.Use(Logging)
//	router.Use(Recovery)
//	router.GET("/users", usersHandler)
//
// Effective handler chain for "GET /users":
//
//	Logging( Recovery( usersHandler ) )
//
// ----------------------------------------------------------------------------
// Why it triggers a rebuild
// ----------------------------------------------------------------------------
//
// Adding a new global middleware changes EVERY handler's effective chain —
// so the trie's builtRoute handlers must all be re-wrapped. That's done
// by rebuilding the compiled state.
//
// This rebuild is intended to happen at startup, NOT during traffic.
// Calling Use() in a hot loop at runtime would be a serious anti-pattern.
func (r *Router) Use(middleware ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, m := range middleware {
		if m != nil {
			r.middlewares = append(r.middlewares, m)
		}
	}

	r.rebuild()
}

// Handle registers ONE route with the router.
//
// ----------------------------------------------------------------------------
// Example
// ----------------------------------------------------------------------------
//
//	router.Handle(http.MethodGet, "/users/{id}", handler)
//
// ----------------------------------------------------------------------------
// Registration pipeline
// ----------------------------------------------------------------------------
//
//  1. Normalize method ("get" → "GET")
//  2. Normalize pattern ("users/{id}/" → "/users/{id}")
//  3. Validate inputs (method, pattern, handler non-empty)
//  4. Parse pattern into structured segments
//  5. Build the route's structural key for duplicate detection
//  6. Reject if (method, key) already exists
//  7. Append to r.routes
//  8. Trigger rebuild() to recompile the trie
//
// ----------------------------------------------------------------------------
// Why validation is strict
// ----------------------------------------------------------------------------
//
// Bad patterns should fail LOUDLY at startup, not silently at request time.
//
// Invalid examples that will be rejected:
//
//	"/users/{}"        empty parameter name
//	"/users/{id"       missing closing brace
//	"/users//posts"    empty segment
//
// ----------------------------------------------------------------------------
// Why duplicate detection exists
// ----------------------------------------------------------------------------
//
// These two routes are structurally identical (parameter names don't affect
// matching):
//
//	/users/{id}
//	/users/{userID}
//
// Both produce the same key "/users/{}". Allowing both would make routing
// ambiguous — a request "/users/123" could match either. So the router
// refuses to register the second one.
func (r *Router) Handle(method, pattern string, handler http.Handler, middleware ...Middleware) error {
	const op = "router_handle"

	method = strings.ToUpper(strings.TrimSpace(method))
	pattern = normalizePattern(pattern)

	if method == "" {
		return fmt.Errorf("%s: method is required", op)
	}
	if pattern == "" {
		return fmt.Errorf("%s: pattern is required", op)
	}
	if handler == nil {
		return fmt.Errorf("%s: handler is required", op)
	}

	segments, err := parsePattern(pattern)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	rt := &route{
		method:   method,
		pattern:  pattern,
		key:      buildRouteKey(segments),
		segments: segments,
		handler:  handler,
		// Defensive copy of the middleware slice.
		//
		// Why we copy here:
		//
		// The caller might keep using their own slice and later append to
		// it. If we stored their slice directly, that append could mutate
		// our route's middleware list — a spooky-action-at-a-distance bug
		// that's painful to debug. Copying isolates us.
		middlewares: append([]Middleware(nil), middleware...),
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Duplicate-route check.
	//
	// We compare (method, key) — same shape + same method = conflict.
	// Different methods with the same shape are FINE (GET /users vs
	// POST /users), because method dispatch happens at the trie leaf.
	for _, existing := range r.routes {
		if existing.method == rt.method && existing.key == rt.key {
			return fmt.Errorf("%s: duplicate route for %s %s", op, method, pattern)
		}
	}

	r.routes = append(r.routes, rt)

	r.rebuild()

	return nil
}

// HandleFunc is a convenience wrapper around Handle that accepts a plain
// function instead of an http.Handler.
//
// Example:
//
//	router.HandleFunc(http.MethodGet, "/health", func(w http.ResponseWriter, r *http.Request) {
//	    w.Write([]byte("ok"))
//	})
func (r *Router) HandleFunc(method, pattern string, handler func(http.ResponseWriter, *http.Request), middleware ...Middleware) error {
	return r.Handle(method, pattern, http.HandlerFunc(handler), middleware...)
}

// GET registers a GET route. Convenience wrapper over Handle.
//
// Example:
//
//	router.GET("/users/{id}", usersHandler)
func (r *Router) GET(pattern string, handler http.Handler, middleware ...Middleware) error {
	return r.Handle(http.MethodGet, pattern, handler, middleware...)
}

// POST registers a POST route. Convenience wrapper over Handle.
//
// Example:
//
//	router.POST("/users", createUserHandler)
func (r *Router) POST(pattern string, handler http.Handler, middleware ...Middleware) error {
	return r.Handle(http.MethodPost, pattern, handler, middleware...)
}

// PUT registers a PUT route. Convenience wrapper over Handle.
//
// Example:
//
//	router.PUT("/users/{id}", replaceUserHandler)
func (r *Router) PUT(pattern string, handler http.Handler, middleware ...Middleware) error {
	return r.Handle(http.MethodPut, pattern, handler, middleware...)
}

// PATCH registers a PATCH route. Convenience wrapper over Handle.
//
// Example:
//
//	router.PATCH("/users/{id}", updateUserHandler)
func (r *Router) PATCH(pattern string, handler http.Handler, middleware ...Middleware) error {
	return r.Handle(http.MethodPatch, pattern, handler, middleware...)
}

// DELETE registers a DELETE route. Convenience wrapper over Handle.
//
// Example:
//
//	router.DELETE("/users/{id}", deleteUserHandler)
func (r *Router) DELETE(pattern string, handler http.Handler, middleware ...Middleware) error {
	return r.Handle(http.MethodDelete, pattern, handler, middleware...)
}

// ServeHTTP is the main entrypoint for every incoming HTTP request.
//
// It satisfies the http.Handler interface so a Router can be passed
// directly to http.ListenAndServe(...).
//
// ----------------------------------------------------------------------------
// Request flow
// ----------------------------------------------------------------------------
//
//	Client
//	   │
//	   ▼
//	ServeHTTP
//	   │
//	   ├─► load compiled state (snapshot)
//	   │
//	   ├─► normalize + split path
//	   │
//	   ├─► trie traversal → match()
//	   │
//	   │     ┌─ matched & method ok    ─► execute compiled handler
//	   │     ├─ matched, wrong method  ─► 405 + Allow header
//	   │     └─ no match               ─► 404
//	   │
//	   ▼
//	response
//
// ----------------------------------------------------------------------------
// Things ServeHTTP does NOT do
// ----------------------------------------------------------------------------
//
// On purpose, this function does not:
//
//   - parse route patterns
//   - (re)build middleware chains
//   - scan slices of routes
//   - allocate large structures
//
// All of that happened once at registration / rebuild time. The hot path
// here is intentionally tiny.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Snapshot the current compiled state.
	//
	// Even if a rebuild happens mid-request, this local `current` keeps
	// pointing at the consistent snapshot we started with. That's what
	// makes the data plane lock-free and safe.
	current := r.state
	if current == nil || current.root == nil {
		http.NotFound(w, req)
		return
	}
	method := strings.ToUpper(req.Method)
	path := normalizeRequestPath(req.URL.Path)
	parts := splitPath(path)

	// Walk the trie.
	//
	// Example for "GET /users/123/posts":
	//
	//	root
	//	  │
	//	  ▼  staticChildren["users"]
	//	"users"
	//	  │
	//	  ▼  paramChild           (captures "123")
	//	{param}
	//	  │
	//	  ▼  staticChildren["posts"]
	//	"posts"  ← matched node
	//
	// match() returns:
	//
	//	matchedNode → the trie node where the route lives
	//	values      → captured param values in order, e.g. ["123"]
	//	ok          → whether traversal reached a leaf with routes
	matchedNode, values, ok := current.root.match(parts, 0, nil)

	if !ok {
		current.notFound.ServeHTTP(w, req)
		return
	}

	// Method dispatch.
	//
	// Same trie node can carry multiple methods. For example, "/users":
	//
	//	matchedNode.routes["GET"]   → list-users handler
	//	matchedNode.routes["POST"]  → create-user handler
	route, exists := matchedNode.routes[method]

	if !exists {
		// Path exists but this method isn't supported → 405.
		// We attach the precomputed Allow header so the client knows
		// which methods ARE valid for this path.
		if len(matchedNode.allowedMethods) > 0 {
			w.Header().Set(
				"Allow",
				strings.Join(matchedNode.allowedMethods, ", "),
			)
		}

		current.methodNotAllowed.ServeHTTP(w, req)
		return
	}

	// Zip captured values with parameter names to produce the final
	// name→value map.
	//
	// Route:     "/users/{user_id}/posts/{post_id}"
	// Request:   "/users/123/posts/999"
	//
	//	values     = ["123", "999"]
	//	paramNames = ["user_id", "post_id"]
	//
	// Result:
	//
	//	{"user_id": "123", "post_id": "999"}
	params := make(map[string]string, len(route.paramNames))

	for i, name := range route.paramNames {
		if i < len(values) {
			params[name] = values[i]
		}
	}

	// Attach the params to the request context so downstream handlers can
	// retrieve them via requesttypes.PathParam / PathParamsFromContext.
	ctx := requesttypes.WithPathParams(req.Context(), params)

	// Execute the already-wrapped handler.
	//
	// Conceptually this is equivalent to:
	//
	//	Logging(Recovery(Auth(originalHandler))).ServeHTTP(w, r)
	//
	// but the wrapping happened ONCE at rebuild time — not on this request.
	// That's the core optimization the whole router is built around.
	route.handler.ServeHTTP(w, req.WithContext(ctx))
}

// rebuild recompiles the entire runtime router state.
//
// This is the MOST IMPORTANT control-plane operation. Every expensive
// piece of routing work happens here, exactly once per registration event.
//
// ----------------------------------------------------------------------------
// What rebuild does
// ----------------------------------------------------------------------------
//
//  1. Construct a fresh trie root.
//  2. For each registered route:
//     a. Wrap its handler with: global middlewares → route middlewares
//     b. Build a `builtRoute` carrying the wrapped handler + paramNames
//     c. Insert it into the trie under its segments + method
//  3. Walk the trie and precompute `allowedMethods` for every node (for
//     fast 405 responses).
//  4. Publish the new state by replacing r.state.
//
// ----------------------------------------------------------------------------
// Why this matters
// ----------------------------------------------------------------------------
//
// Without rebuild, the request path would have to:
//
//	parse  →  build middleware chain  →  scan routes  →  execute
//
// With rebuild, the request path is just:
//
//	trie lookup  →  execute pre-wrapped handler
//
// All the heavy lifting is amortized to startup, where it's free.
func (r *Router) rebuild() {
	// Allocate a fresh trie root.
	//
	// We always allocate a brand new tree (rather than mutating the old
	// one) so concurrent readers of the OLD state are unaffected.
	root := &node{
		staticChildren: make(map[string]*node),
		routes:         make(map[string]*builtRoute),
	}

	for _, rt := range r.routes {
		// Build the full middleware chain for this specific route:
		//
		//	global middlewares  (outermost)
		//	      │
		//	      ▼
		//	route middlewares
		//	      │
		//	      ▼
		//	original handler   (innermost)
		//
		// chain() walks the slice from the END backwards, so the FIRST
		// middleware in the slice ends up OUTERMOST in the call stack.
		handler := chain(
			rt.handler,
			append(
				append([]Middleware(nil), r.middlewares...),
				rt.middlewares...,
			)...,
		)

		// Assemble the final runtime route.
		//
		// paramNames preserves left-to-right order, which is what ServeHTTP
		// needs to zip captured values back into a named map.
		built := &builtRoute{
			method:     rt.method,
			handler:    handler,
			paramNames: extractParamNames(rt.segments),
		}

		insertRoute(
			root,
			rt.method,
			rt.segments,
			built,
		)
	}

	// Precompute Allow headers so 405 responses are zero-allocation on the
	// hot path.
	buildAllowedMethods(root)

	// Publish the new compiled state. Subsequent requests will see it;
	// in-flight requests continue using the old state safely.
	r.state = &state{
		root:             root,
		notFound:         r.notFoundHandler,
		methodNotAllowed: r.methodNotAllowedHandler,
	}
}

// buildRouteKey produces a structural identity string for a parsed route.
//
// Parameter names are erased; only the SHAPE matters.
//
// ----------------------------------------------------------------------------
// Examples
// ----------------------------------------------------------------------------
//
//	/users/{id}              ─►  "/users/{}"
//	/users/{userID}          ─►  "/users/{}"
//	/users/profile           ─►  "/users/profile"
//	/users/{id}/posts/{pid}  ─►  "/users/{}/posts/{}"
//
// ----------------------------------------------------------------------------
// Why this exists
// ----------------------------------------------------------------------------
//
// Two routes that differ ONLY in their parameter names are structurally
// the same and would create ambiguous matches. The key lets Handle()
// detect that and reject the duplicate.
func buildRouteKey(segments []routeSegment) string {
	if len(segments) == 0 {
		return "/"
	}

	parts := make([]string, len(segments))

	for i, seg := range segments {
		if seg.isCatchAll {
			parts[i] = "{...}"
			continue
		}
		if seg.isParam {
			parts[i] = "{}"
			continue
		}

		parts[i] = seg.literal
	}

	return "/" + strings.Join(parts, "/")
}

// splitPath splits a URL path into its segment list.
//
// Examples:
//
//	"/users/123/posts"  ─►  ["users", "123", "posts"]
//	"/"                 ─►  []
//	""                  ─►  []
//
// The trie is walked segment-by-segment, so this is the natural shape to
// feed it.
func splitPath(p string) []string {
	if p == "/" {
		return []string{}
	}

	trimmed := strings.Trim(p, "/")

	if trimmed == "" {
		return []string{}
	}

	return strings.Split(trimmed, "/")
}

// match walks the trie against an incoming path's segments.
//
// ----------------------------------------------------------------------------
// Parameters
// ----------------------------------------------------------------------------
//
//	segments  — the request path split into parts, e.g. ["users","123"]
//	idx       — index into segments we're currently consuming
//	values    — parameter values captured so far (in path order)
//
// Returns:
//
//	(matched node, captured values, true)   on success
//	(nil,          nil,             false)  on failure
//
// ----------------------------------------------------------------------------
// Matching rules
// ----------------------------------------------------------------------------
//
//  1. Static children are tried FIRST. Exact matches always beat wildcards.
//  2. Parameter child is tried only if no static match succeeded — and only
//     if it leads to a valid leaf.
//  3. A node "matches" only if we've consumed ALL segments AND it has at
//     least one registered route.
//
// ----------------------------------------------------------------------------
// Example
// ----------------------------------------------------------------------------
//
// Registered:
//
//	GET /users/settings
//	GET /users/{id}
//
// Request "/users/settings":
//
//	root → staticChildren["users"] → staticChildren["settings"]  ✓ STATIC WINS
//
// Request "/users/123":
//
//	root → staticChildren["users"] → staticChildren["123"]  ✗ (not present)
//	root → staticChildren["users"] → paramChild             ✓ (captures "123")
//
// ----------------------------------------------------------------------------
// Why recursion is fine here
// ----------------------------------------------------------------------------
//
// Path depth is bounded by the registered route structure (typically very
// shallow — 3 to 6 segments). Recursion cost is negligible and the code
// stays much cleaner than an explicit stack.
func (n *node) match(segments []string, idx int, values []string) (*node, []string, bool) {
	if n == nil {
		return nil, nil, false
	}

	// We've consumed every segment: it's a hit if THIS node has routes.
	if idx == len(segments) && len(n.routes) > 0 {
		return n, values, true
	}

	// Try a more specific child first: static, then single-segment param.
	// Both only apply while segments remain.
	if idx < len(segments) {
		segment := segments[idx]

		if child, ok := n.staticChildren[segment]; ok {
			if matchedNode, matchedValues, ok := child.match(segments, idx+1, values); ok {
				return matchedNode, matchedValues, true
			}
		}

		if n.paramChild != nil {
			if matchedNode, matchedValues, ok := n.paramChild.match(segments, idx+1, append(values, segment)); ok {
				return matchedNode, matchedValues, true
			}
		}
	}

	// Finally, fall back to a catch-all. It consumes ALL remaining segments
	// (joined by "/") as one captured value — and matches zero of them too, so
	// segments[idx:] is "" when the path is already exhausted. Tried last so
	// static and single-param routes always take precedence.
	if n.catchAllChild != nil && len(n.catchAllChild.routes) > 0 {
		return n.catchAllChild, append(values, strings.Join(segments[idx:], "/")), true
	}

	return nil, nil, false
}

// chain wraps a handler with the provided middlewares, returning a single
// composed handler.
//
// ----------------------------------------------------------------------------
// Composition order
// ----------------------------------------------------------------------------
//
// Given:
//
//	chain(handler, A, B, C)
//
// The returned handler behaves as:
//
//	A( B( C( handler ) ) )
//
// So A runs FIRST on a request, then B, then C, then handler.
//
// ----------------------------------------------------------------------------
// Why iterate from the end backwards
// ----------------------------------------------------------------------------
//
// We want the FIRST middleware in the slice to be OUTERMOST. To build that
// onion from the inside out, we start with `handler` and wrap one layer
// at a time — applying middlewares[len-1] first (innermost) and
// middlewares[0] last (outermost). Iterating backwards achieves exactly
// that.
//
// nil entries are skipped so callers can pass conditional middlewares
// without having to filter the slice themselves.
func chain(
	final http.Handler,
	middlewares ...Middleware,
) http.Handler {
	wrapped := final

	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i] == nil {
			continue
		}

		wrapped = middlewares[i](wrapped)
	}

	return wrapped
}

// extractParamNames returns parameter names in left-to-right path order.
//
// Example:
//
//	"/users/{user_id}/posts/{post_id}"
//
// segments → ["users", {user_id}, "posts", {post_id}]
//
// extractParamNames(segments) → ["user_id", "post_id"]
//
// This ordering is what ServeHTTP uses to align captured values back to
// their names.
func extractParamNames(segments []routeSegment) []string {
	names := make([]string, 0)

	for _, seg := range segments {
		if seg.isParam {
			names = append(names, seg.paramName)
		}
	}

	return names
}

// insertRoute inserts ONE compiled route into the trie.
//
// ----------------------------------------------------------------------------
// How it walks
// ----------------------------------------------------------------------------
//
// For each segment of the route:
//
//   - if it's a parameter, descend into (or create) paramChild
//   - if it's a literal,   descend into (or create) staticChildren[literal]
//
// At the final node, record the route under its HTTP method:
//
//	current.routes[method] = route
//
// ----------------------------------------------------------------------------
// Example
// ----------------------------------------------------------------------------
//
// Inserting "GET /users/{id}/posts":
//
//	root
//	 │  staticChildren["users"]            (created if missing)
//	 ▼
//	"users"
//	 │  paramChild                         (created if missing)
//	 ▼
//	{param}
//	 │  staticChildren["posts"]            (created if missing)
//	 ▼
//	"posts"        ← routes["GET"] = route
func insertRoute(
	root *node,
	method string,
	segments []routeSegment,
	route *builtRoute,
) {
	current := root

	for _, seg := range segments {
		if seg.isCatchAll {
			if current.catchAllChild == nil {
				current.catchAllChild = &node{
					staticChildren: make(map[string]*node),
					routes:         make(map[string]*builtRoute),
				}
			}

			current.catchAllParam = seg.paramName
			current = current.catchAllChild

			continue
		}

		if seg.isParam {
			if current.paramChild == nil {
				current.paramChild = &node{
					staticChildren: make(map[string]*node),
					routes:         make(map[string]*builtRoute),
				}
			}

			current = current.paramChild

			continue
		}

		child, exists := current.staticChildren[seg.literal]
		if !exists {
			child = &node{
				staticChildren: make(map[string]*node),
				routes:         make(map[string]*builtRoute),
			}

			current.staticChildren[seg.literal] = child
		}

		current = child
	}

	current.routes[method] = route
}

// buildAllowedMethods walks the entire trie and precomputes, at every leaf
// node, the sorted list of HTTP methods it supports.
//
// ----------------------------------------------------------------------------
// Why precompute?
// ----------------------------------------------------------------------------
//
// On a 405 response, HTTP requires an "Allow:" header listing valid
// methods for the path. If we computed it at request time we'd have to
// iterate `routes` and sort — small but unnecessary work on the hot path.
//
// By computing it once at rebuild, the 405 path becomes:
//
//	w.Header().Set("Allow", strings.Join(node.allowedMethods, ", "))
//
// Pure, allocation-free, deterministic.
//
// ----------------------------------------------------------------------------
// Sort order
// ----------------------------------------------------------------------------
//
// Methods are sorted lexicographically so the Allow header is stable and
// testable — same routes always produce the same header string.
func buildAllowedMethods(n *node) {
	if n == nil {
		return
	}

	if len(n.routes) > 0 {
		methods := make([]string, 0, len(n.routes))

		for method := range n.routes {
			methods = append(methods, method)
		}

		sort.Strings(methods)

		n.allowedMethods = methods
	}

	for _, child := range n.staticChildren {
		buildAllowedMethods(child)
	}

	if n.paramChild != nil {
		buildAllowedMethods(n.paramChild)
	}

	if n.catchAllChild != nil {
		buildAllowedMethods(n.catchAllChild)
	}
}

// SetNotFoundHandler replaces the default HTTP 404 handler.
//
// Typical use: applications usually want a JSON error envelope, not the
// default "404 page not found" text.
//
// Example:
//
//	router.SetNotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	    w.Header().Set("Content-Type", "application/json")
//	    w.WriteHeader(http.StatusNotFound)
//	    w.Write([]byte(`{"error":"not_found"}`))
//	}))
//
// A nil handler is silently ignored — the router refuses to enter a state
// with no 404 handler at all.
func (r *Router) SetNotFoundHandler(handler http.Handler) {
	if handler == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.notFoundHandler = handler
}

// SetMethodNotAllowedHandler replaces the default HTTP 405 handler.
//
// Typical use: JSON-shaped 405 responses for API clients.
//
// Example:
//
//	router.SetMethodNotAllowedHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	    w.Header().Set("Content-Type", "application/json")
//	    w.WriteHeader(http.StatusMethodNotAllowed)
//	    w.Write([]byte(`{"error":"method_not_allowed"}`))
//	}))
//
// As with SetNotFoundHandler, a nil handler is silently ignored.
func (r *Router) SetMethodNotAllowedHandler(handler http.Handler) {
	if handler == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.methodNotAllowedHandler = handler
}

// normalizePattern canonicalizes a route pattern at registration time.
//
// ----------------------------------------------------------------------------
// Transformations applied
// ----------------------------------------------------------------------------
//
//   - trim surrounding whitespace
//   - ensure leading "/"
//   - collapse "." / ".." and duplicate slashes via path.Clean
//   - map the "." result (empty path) to "/"
//
// Examples:
//
//	"  users/{id}/  "  ─►  "/users/{id}"
//	"users"            ─►  "/users"
//	"/users//{id}"     ─►  "/users/{id}"
//	""                 ─►  ""           (left empty for caller to reject)
//
// ----------------------------------------------------------------------------
// Why this matters
// ----------------------------------------------------------------------------
//
// Without normalization, the same logical route registered with slightly
// different spelling would silently become two different routes — a
// classic source of "why isn't my endpoint matching?" bugs.
func normalizePattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ""
	}

	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}

	cleaned := path.Clean(pattern)
	if cleaned == "." {
		return "/"
	}

	return cleaned
}

// parsePattern converts a normalized route pattern into structured segments.
//
// ----------------------------------------------------------------------------
// Example
// ----------------------------------------------------------------------------
//
//	"/users/{user_id}/posts/{post_id}"
//
// becomes:
//
//	[
//	    {literal: "users"},
//	    {isParam: true, paramName: "user_id"},
//	    {literal: "posts"},
//	    {isParam: true, paramName: "post_id"},
//	]
//
// ----------------------------------------------------------------------------
// Supported segment forms
// ----------------------------------------------------------------------------
//
//	Literal segment:    "/users/posts"
//	Parameter segment:  "/users/{user_id}"
//
// ----------------------------------------------------------------------------
// Validation rules (each produces an error)
// ----------------------------------------------------------------------------
//
//   - empty segments              "/users//posts"
//   - unmatched braces            "/users/{id"        or  "/users/id}"
//   - empty parameter name        "/users/{}"
//
// ----------------------------------------------------------------------------
// Why the parser is strict
// ----------------------------------------------------------------------------
//
// A broken pattern is a programmer mistake. It's much better to crash
// loudly at boot than to silently never-match the route in production.
func parsePattern(pattern string) ([]routeSegment, error) {
	pattern = normalizePattern(pattern)
	if pattern == "/" {
		return []routeSegment{}, nil
	}

	rawSegments := strings.Split(strings.Trim(pattern, "/"), "/")
	segments := make([]routeSegment, 0, len(rawSegments))

	for i, seg := range rawSegments {
		if seg == "" {
			return nil, fmt.Errorf("invalid pattern %q: empty segment", pattern)
		}

		// If the segment starts OR ends with a brace, it MUST be a complete
		// "{name}" form — otherwise it's malformed (e.g. "{id" or "id}").
		if strings.HasPrefix(seg, "{") || strings.HasSuffix(seg, "}") {
			if !(strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")) {
				return nil, fmt.Errorf("invalid pattern %q: malformed path parameter %q", pattern, seg)
			}

			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}"))

			// A "{name...}" segment is a catch-all: it matches the entire
			// remainder of the path, so it is only valid as the LAST segment.
			if strings.HasSuffix(name, "...") {
				if i != len(rawSegments)-1 {
					return nil, fmt.Errorf("invalid pattern %q: catch-all parameter %q must be the final segment", pattern, seg)
				}

				name = strings.TrimSpace(strings.TrimSuffix(name, "..."))
				if name == "" {
					return nil, fmt.Errorf("invalid pattern %q: empty catch-all parameter name", pattern)
				}

				segments = append(segments, routeSegment{
					isParam:    true,
					isCatchAll: true,
					paramName:  name,
				})
				continue
			}

			if name == "" {
				return nil, fmt.Errorf("invalid pattern %q: empty path parameter name", pattern)
			}

			segments = append(segments, routeSegment{
				isParam:   true,
				paramName: name,
			})
			continue
		}

		segments = append(segments, routeSegment{
			literal: seg,
		})
	}

	return segments, nil
}

// normalizeRequestPath canonicalizes an INCOMING request path so it matches
// the same way registered patterns do.
//
// ----------------------------------------------------------------------------
// Why a separate function from normalizePattern?
// ----------------------------------------------------------------------------
//
// They do similar things but are conceptually different sides of the
// router: one prepares STORED patterns, the other prepares INCOMING paths.
// Keeping them separate makes future divergence (e.g. percent-decoding,
// case folding) easier without affecting the other side.
//
// ----------------------------------------------------------------------------
// Examples
// ----------------------------------------------------------------------------
//
//	" /tenants/ "        ─►  "/tenants"
//	"tenants"            ─►  "/tenants"
//	"/tenants//abc"      ─►  "/tenants/abc"
//	""                   ─►  "/"
func normalizeRequestPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}

	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	cleaned := path.Clean(p)
	if cleaned == "." {
		return "/"
	}

	return cleaned
}
