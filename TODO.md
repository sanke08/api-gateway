# ✅ TODO — Known Issues, Bugs & Build-Out Checklist

Living checklist for getting the gateway from **"library of components"** to a
**running, end-to-end multi-tenant API gateway**. Tick items off (`[ ]` → `[x]`)
as you complete them. Full context for each item is in
[README.md › Known Issues](README.md#known-issues--bugs-must-fix-before-it-runs).

> **How to use:** complete items top-to-bottom (priority order). When you finish
> one, change `- [ ]` to `- [x]`, update the **Progress** counter below, and move
> the line's summary into [✅ What's Complete](#-whats-complete-done).

---

## 📊 Progress

| Group                                   | Done | Total |
| --------------------------------------- | :--: | :---: |
| 🔴 P0 — Blocking (app cannot run)        |  0   |   9   |
| 🟡 P1 — Correctness / cleanliness        |  0   |   6   |
| 🟢 P2 — Build-out / roadmap              |  0   |  14   |
| **TOTAL**                               |  **0** | **29** |

_Last updated: 2026-06-28 (added health checks, async usage tracking, graceful
shutdown, and edge/CORS middleware — all built, none wired; verified that **no
P0/P1 items have been fixed yet** — code still builds & `go vet`-clean because
the SQL bugs live in query strings, not compiled code)._

> **Verification note (2026-06-28):** `go build ./...` and `go vet ./...` both
> pass with exit 0, and there are still **0 `_test.go` files**. The blocking
> bugs B1–B9 and cleanups C1–C6 are **all still open** — a clean build does not
> mean the queries run. Four new subsystems landed since this list was first
> written (see **R11–R14** and the new rows in [What's Complete](#-whats-complete-done)).

---

## 🔴 P0 — Blocking (must fix before anything runs)

### Wiring
- [ ] **B1 — Assemble the app.** `main.go` / `server.New` currently serve an
  empty `http.ServeMux` + logging middleware only; DB, repos, services,
  handlers, router, proxy, and rate limiter are never instantiated. Build a
  bootstrap that opens the DB, constructs repos + services + JWT manager + the
  router, registers routes, and serves the router instead of a bare mux.
  - Files: [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go),
    [internal/server/server.go](internal/server/server.go)
  - ⛓️ Depends on: B2–B9 (DB must actually work for routed handlers to succeed).

### Schema / SQL mismatches (queries error at runtime)
- [ ] **B2 — Fix `001` trailing comma.** Remove the trailing comma after
  `updated_at` (line 33) so `CREATE TABLE tenants` is valid SQL.
  - File: [internal/db/migrations/001_create_tenants.sql](internal/db/migrations/001_create_tenants.sql) (line 33–34)
- [ ] **B3 — Reconcile users password column.** Migration defines `password`;
  `user_repo` uses `password_hash`. Pick one (code expects `password_hash`) and
  make both agree.
  - Files: [internal/db/migrations/002_create_users.sql](internal/db/migrations/002_create_users.sql) (line 14),
    [internal/repository/user_repo.go](internal/repository/user_repo.go)
- [ ] **B4 — Reconcile usage columns.** Migration defines `endpoint` +
  `created_at`/`updated_at` (no `timestamp`); `usage_repo` uses `path` +
  `"timestamp"`. Align column names in the migration and the repo.
  - Files: [internal/db/migrations/004_usage.sql](internal/db/migrations/004_usage.sql),
    [internal/repository/usage_repo.go](internal/repository/usage_repo.go)
- [ ] **B5 — Resolve `api_keys.expires_at`.** Repo queries `SELECT … expires_at …`
  but the column doesn't exist in `003` and the `APIKey` model has no such field.
  Either add the column + model field, or remove `expires_at` from all 4 queries.
  - Files: [internal/repository/apikey_repo.go](internal/repository/apikey_repo.go),
    [internal/db/migrations/003_api_keys.sql](internal/db/migrations/003_api_keys.sql),
    [internal/models/apikey.go](internal/models/apikey.go)

### Scan / column-count mismatches (runtime scan errors)
- [ ] **B6 — Fix `user_repo.Create` RETURNING/Scan.** SQL `RETURNING id`
  (1 col) but `.Scan()` binds 5 fields. Return all 5 columns (and insert/return
  `id` consistently — see B9) or scan only what's returned.
  - File: [internal/repository/user_repo.go](internal/repository/user_repo.go)
- [ ] **B7 — Fix `apikey_repo` 8-vs-7 column mismatch.** `SELECT`/`RETURNING`
  list 8 columns (incl. `expires_at`) while `.Scan()` binds 7. Make column list
  and scan targets match (ties into B5).
  - File: [internal/repository/apikey_repo.go](internal/repository/apikey_repo.go)
- [ ] **B8 — Fix `membership.Create` param/column shift.** INSERT lists 4 columns
  `(user_id, tenant_id, role, status)` but passes 5 values starting with
  `normalized.ID` → values shift by one. Also `RETURNING` 5 cols scanned into 7.
  Align columns, params, RETURNING, and Scan.
  - File: [internal/repository/membership.go](internal/repository/membership.go)
- [ ] **B9 — Make ID + timestamp handling consistent.** Services generate UUIDs
  via `newUUIDString()` and pass them, but most INSERTs omit the `id` column
  (relying on `gen_random_uuid()`), silently dropping them; `tenant_repo.Create`
  inserts `id` but also writes zero-value `created_at`/`updated_at`. Decide one
  strategy (recommended: let the DB generate `id` + timestamps; stop passing
  them) and apply it across tenant/user/apikey/membership repos + services.
  - Files: [internal/repository/](internal/repository/),
    [internal/services/onboarding.go](internal/services/onboarding.go)

---

## 🟡 P1 — Correctness & cleanliness (non-blocking)

- [ ] **C1 — Remove dead `case` in `mapRepositoryError`.** Two
  `case ErrValidation.Kind:` branches; the second is unreachable.
  - File: [internal/services/errors.go](internal/services/errors.go)
- [ ] **C2 — Fix `tenants` status CHECK vs model.** Migration allows
  `('active','inactive')`; model/services use `active`/`suspended`. Change the
  CHECK to `('active','suspended')` (or align the model).
  - File: [internal/db/migrations/001_create_tenants.sql](internal/db/migrations/001_create_tenants.sql) (line 30)
- [ ] **C3 — Reconcile `Usage.Endpoint` naming end-to-end** (model JSON tag,
  table column, repo SQL) once B4 is decided.
  - Files: [internal/models/usage.go](internal/models/usage.go), usage repo + migration
- [ ] **C4 — Resolve retry scaffolding.** `retryableBody` stub returns `false`
  and is unused; `normalizeContextErr`, `bufferBody`, `bodyFromBytes`,
  `var _ sync.Locker` are unused. Either wire them in or delete them.
  - Files: [internal/retry/policy.go](internal/retry/policy.go),
    [internal/retry/transport.go](internal/retry/transport.go)
- [ ] **C5 — Add `db.Ping()` on startup.** `NewDatabase` never verifies
  connectivity, so a bad DSN only fails on first query.
  - File: [internal/config/database.go](internal/config/database.go)
- [ ] **C6 — Add tests.** Zero `_test.go` files today. Start with the pure/
  testable units: PBKDF2 hash/verify, JWT issue/verify, router trie matching,
  token-bucket limiter (inject clock), retry backoff math.
  - Suggested: `*_test.go` next to each package.

---

## 🟢 P2 — Build-out / roadmap (new functionality)

- [ ] **R1 — Wire the data plane.** Build a `proxy.StaticRegistry` of
  `UpstreamTarget`s; chain `APIKeyAuth` / `TenantResolution` → `ratelimit` →
  `proxy.Handler` on proxied routes. Wire `circuitbreaker.Transport` (per
  upstream) and `retry.Transport` as the proxy's transport stack.
- [ ] **R2 — Move secrets/policies into config.** JWT secret/issuer/TTLs,
  rate-limit rules, upstream definitions → env or config file/DB.
  - File: [internal/config/config.go](internal/config/config.go)
- [ ] **R3 — Persist upstreams.** Add an `upstreams` table + repository so
  `UpstreamTarget`s aren't in-memory only.
- [ ] **R4 — Write usage rows from the proxy path.** Capture bytes in/out and
  call `Usage.Log` (the model + repo already exist; nothing writes to them).
- [ ] **R5 — Add a refresh-token endpoint.** `JWTManager.RefreshAccessToken`
  exists but has no handler/route.
  - File: [internal/handlers/](internal/handlers/)
- [ ] **R6 — Token revocation using `jti`.** Track/blacklist token IDs.
- [ ] **R7 — Operational hardening.** Graceful shutdown, a `PruneIdle`
  goroutine for the rate limiter, and a real migration runner.
- [ ] **R8 — Admin / dashboard API.** Tenant management + usage analytics
  endpoints (not started).
- [ ] **R10 — Wire the observability layer.** The metrics registry, trace, and
  observability middleware are fully built but never instantiated. Five steps:
  1. **Bootstrap** — call `observability.NewRegistry()` in `main.go` and pass
     the `*Registry` to every component that needs to record metrics.
  2. **Router middleware** — add `router.Use(observability.Middleware(reg))`
     so every request automatically gets a trace and timing metrics without
     any per-handler code.
  3. **`/metrics` endpoint** — register `router.GET("/metrics", reg.MetricsHandler())`
     so Prometheus (or any scraper) can pull metrics. Consider protecting it
     behind an internal-only route or basic auth.
  4. **Component wiring** — pass `reg` to:
     - `retry.Transport` → call `reg.RecordRetry(labels)` per retry attempt
     - `circuitbreaker.Breaker` (or its `Transport`) → call `reg.RecordBreakerOpen/Closed`
       when state transitions occur
     - `ResponseCacheMiddleware` → call `observability.RecordCacheHit/Miss`
     - `APIKeyAuthMiddleware` / `TenantResolutionMiddleware` → call
       `RecordCacheHit/Miss` for identity cache lookups
  5. **Trace propagation** — downstream handlers that need the `RequestID`
     (e.g. proxy error handler, access logs) should call `TraceFromContext(ctx)`.
  - Files: [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go),
    [internal/server/server.go](internal/server/server.go),
    [internal/retry/transport.go](internal/retry/transport.go),
    [internal/circuit_breaker/transport.go](internal/circuit_breaker/transport.go),
    [internal/middleware/response_cache_middleware.go](internal/middleware/response_cache_middleware.go),
    [internal/middleware/api_key_auth_middleware.go](internal/middleware/api_key_auth_middleware.go),
    [internal/middleware/tenant_resolution_middleware.go](internal/middleware/tenant_resolution_middleware.go)
  - ⛓️ Depends on: B1 (app must be assembled first).
- [ ] **R9 — Wire the cache layer.** All cache packages are fully built and
  compile cleanly but nothing instantiates them. Five steps are required:
  1. **Config** — add `CacheConfig` struct to `internal/config/config.go` with
     env vars `CACHE_REMOTE_URL` (blank = local-only), `CACHE_TIMEOUT` (default
     2s), `CACHE_NAMESPACE` (default `"gateway"`), `CACHE_TOKEN` (optional
     Bearer token for the remote cache service).
  2. **Bootstrap** — in `main.go` / `server.New`: if `CACHE_REMOTE_URL` is
     non-blank, call `cacheclient.NewRemoteClient(...)` and wrap with
     `cache.NewHybridStore(remote, nil)`. Otherwise use
     `cache.NewHybridStore(nil, nil)` (local-only). Pass the store to all
     middleware constructors.
  3. **Identity cache injection** — `APIKeyAuthMiddleware` and
     `TenantResolutionMiddleware` should accept `cache.Store` and use `Get`
     before hitting the DB; call `Set` on a miss. Key scheme:
     `apikey:<sha256-hex>` · `tenant:<tenant-id>` ·
     `membership:<user-id>:<tenant-id>`. TTL: 5 min (tune via config).
  4. **Response cache injection** — for each proxied route that should cache,
     wrap the proxy handler with `NewResponseCacheMiddleware(store, policy)`.
     Configure `ResponseCachePolicy` per route (TTL, MaxBodyBytes, VaryHeaders,
     CacheableStatuses, KeyPrefix). Only GET + 200 OK + no no-store + no
     Set-Cookie responses are stored.
  5. **Pruning goroutine** — start a background `time.Ticker` (every 5 min)
     that calls `memoryStore.PruneExpired()` to reclaim stale in-process
     entries.
  - Files: [internal/config/config.go](internal/config/config.go),
    [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go),
    [internal/server/server.go](internal/server/server.go),
    [internal/middleware/api_key_auth_middleware.go](internal/middleware/api_key_auth_middleware.go),
    [internal/middleware/tenant_resolution_middleware.go](internal/middleware/tenant_resolution_middleware.go),
    [internal/middleware/response_cache_middleware.go](internal/middleware/response_cache_middleware.go)
  - ⛓️ Depends on: B1 (app must be assembled first).
- [ ] **R11 — Wire graceful shutdown.** `ShutdownManager` + `GracefulServer`
  exist but `main.go` still uses `server.New` (bare mux) and calls
  `srv.Start()` with no signal handling. Switch the bootstrap to build a
  `GracefulServer`, wrap the handler with `ShutdownManager.Wrap`, listen for
  `SIGINT`/`SIGTERM`, and call `Shutdown(ctx)` with a timeout. Register every
  background worker (R12 usage tracker, R9 cache pruner, R7 limiter pruner) via
  `RegisterHook` so they drain on exit.
  - Files: [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go),
    [internal/server/shutdown.go](internal/server/shutdown.go),
    [internal/server/server.go](internal/server/server.go)
  - ⛓️ Depends on: B1.
- [ ] **R12 — Wire async usage tracking.** `AsyncUsageTracker`
  (`services/usage.go`) + `UsageMiddleware` (`middleware/usage_middleware.go`)
  are built but never constructed. In the bootstrap: construct
  `NewAsyncUsageTracker(repos.Usage, bufferSize, logger, reg)`, wrap proxied
  routes with `NewUsageMiddleware(tracker)`, and register `tracker.Close` as a
  shutdown hook (R11). This is the concrete implementation of the old **R4**
  ("write usage rows from the proxy path") — R4 stays as the umbrella goal.
  - Files: [internal/services/usage.go](internal/services/usage.go),
    [internal/middleware/usage_middleware.go](internal/middleware/usage_middleware.go),
    [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go)
  - ⛓️ Depends on: B1, B4 (usage repo SQL must match the table first), R11.
- [ ] **R13 — Wire health & readiness endpoints.** `HealthChecker`
  (`services/health.go`), the `/health` + `/ready` handlers
  (`handlers/health_handlers.go`), and the health models are built but never
  instantiated or routed. Construct `NewHealthChecker(db, reg)`, register
  `GET /health` (liveness) and `GET /ready` (readiness — pings DB + probes
  upstreams) on the router. Readiness should flip to 503 once R11 shutdown
  begins.
  - Files: [internal/services/health.go](internal/services/health.go),
    [internal/handlers/health_handlers.go](internal/handlers/health_handlers.go),
    [internal/models/health.go](internal/models/health.go),
    [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go)
  - ⛓️ Depends on: B1, C5 (`db.Ping` makes the DB probe meaningful).
- [ ] **R14 — Wire edge (CORS + security headers) middleware.** `EdgePolicy` /
  `NewEdgeMiddleware` (`middleware/edge.go`) build CORS + security-header
  handling but are never applied. Move the policy into config (allowed origins,
  HSTS, CSP, frame options), construct the middleware, and add it as a global
  `router.Use(...)` so it runs on every route (it handles `OPTIONS` preflight
  itself).
  - Files: [internal/middleware/edge.go](internal/middleware/edge.go),
    [internal/config/config.go](internal/config/config.go),
    [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go)
  - ⛓️ Depends on: B1, R2 (policy belongs in config).

---

## ✅ What's Complete (done)

Components that are **built and individually coherent** today (compile clean,
`go vet` clean). They are NOT yet reachable at runtime until **B1** wires them in
— "built" ≠ "live."

| Component | State | Notes |
| --------- | ----- | ----- |
| Domain models (`internal/models`) | ✅ Done | Complete |
| Structured logging (`observability`) | ✅ Done & **live** | The one wired middleware |
| Config load + validation | ⚠️ Built | DB never opened yet (see B1/C5) |
| Repository layer (CRUD + `WithTx`) | ⚠️ Built | Blocked by SQL mismatches (B3–B9) |
| Onboarding service (atomic tx) | ⚠️ Built | Logic complete; blocked by DB issues |
| Auth service / login | ⚠️ Built | Not routed (B1) |
| JWT manager (HS256) | ⚠️ Built | Issue/verify/refresh implemented |
| Password hashing (PBKDF2) | ✅ Done | Complete |
| API-key auth (SHA-256) + middleware | ⚠️ Built | Not routed (B1) |
| Tenant resolution + middleware | ⚠️ Built | Not routed (B1) |
| Trie router | ⚠️ Built | Never instantiated (B1) |
| Reverse proxy (per-tenant) | ⚠️ Built | No registry populated (R1/R3) |
| Request/response transforms | ⚠️ Built | Headers + path only (no body) |
| Retry transport (backoff+jitter) | ⚠️ Built | Reachable only via the proxy |
| Circuit breaker (CLOSED/OPEN/HALF_OPEN + Transport) | ⚠️ Built | Transport layer implemented; not wired end-to-end yet (R1) |
| Remote cache client (`cacheclient.RemoteClient`) | ⚠️ Built | HTTP + base64; no env config, not injected into middleware (R9) |
| In-process cache (`cache.MemoryStore`) | ⚠️ Built | RWMutex, TTL, clock injection, PruneExpired; not injected (R9) |
| Hybrid cache (`cache.HybridStore`) | ⚠️ Built | Remote-first + local fallback; not wired into auth path (R9) |
| `CachedResponse` + key builders (`cache/response.go`) | ⚠️ Built | Serialisation, replay, eligibility guards; consumed by response cache middleware (R9) |
| Response cache middleware (`middleware/response_cache_middleware.go`) | ⚠️ Built | captureWriter tee, per-policy normalisation, tenant-isolated keys; not registered on any route (R9) |
| Metrics registry (`observability/registry.go`) | ⚠️ Built | counters/gauges/durations, Prometheus text `/metrics`; `NewRegistry()` never called (R10) |
| Labels + Trace (`observability/labels.go`, `trace.go`) | ⚠️ Built | Stable label strings, per-request ID/timing; not attached to any request path (R10) |
| Observability middleware (`observability/middleware.go`) | ⚠️ Built | Records timing+bytes+status per request; not wired to router (R10) |
| Cache observability helpers (`observability/helper.go`) | ⚠️ Built | `RecordCacheHit/Miss`; cache middleware doesn't call them yet (R10) |
| Rate limiter (token bucket) | ⚠️ Built | In-memory; not routed (R1) |
| Graceful shutdown (`server/shutdown.go`: `ShutdownManager`, `GracefulServer`) | ⚠️ Built | `Wrap` + `RegisterHook` + 3-phase `Shutdown`; `main.go` still calls bare `srv.Start()` (R11) |
| Async usage tracking (`services/usage.go` `AsyncUsageTracker` + `middleware/usage_middleware.go`) | ⚠️ Built | Buffered queue + background writer + `Close` drain + metrics; never constructed (R12, R4) |
| Health checker (`services/health.go`) | ⚠️ Built | Liveness + readiness (DB ping + upstream probes); `NewHealthChecker` never called (R13) |
| Health endpoints (`handlers/health_handlers.go`, `models/health.go`) | ⚠️ Built | `GET /health` + `GET /ready` handlers + response models; not routed (R13) |
| Edge middleware (`middleware/edge.go`: `EdgePolicy`, CORS + security headers) | ⚠️ Built | CORS preflight + `X-Frame-Options`/HSTS/CSP/etc.; `NewEdgeMiddleware` never applied (R14) |

> ✅ Done = complete as intended · ⚠️ Built = code exists & compiles but is not
> wired/runnable yet.

### ❌ Not started (no code yet)
- Refresh-token **endpoint** (service method exists, no handler) → **R5**
- Token revocation → **R6**
- `upstreams` persistence → **R3**
- Usage writing from the request path → **R4**
- Admin / dashboard API → **R8**
- Tests → **C6**
- Cache config env vars + identity middleware injection → **R9** (packages built; wiring not started)
- Response cache middleware registration on proxied routes → **R9**
- `NewRegistry()` + `observability.Middleware` + `/metrics` endpoint → **R10** (packages built; wiring not started)
- `RecordRetry`, `RecordBreakerOpen/Closed`, `RecordCacheHit/Miss` wired into transports/middleware → **R10**
- Graceful shutdown wiring: `GracefulServer` + signal handling + `RegisterHook` → **R11** (packages built; `main.go` not switched over)
- Async usage tracker + usage middleware instantiation on proxied routes → **R12** (concrete form of **R4**)
- Health/readiness endpoints (`HealthChecker` + `/health` + `/ready`) routing → **R13**
- Edge CORS + security-header middleware applied globally → **R14**

---

## 🔗 Suggested order of attack

```
B2 ─┐
B3 ─┤
B4 ─┼─► (fix schema)  ─► B6 ─┐
B5 ─┘                        ├─► (repos work) ─► B1 (wire /onboard, /login)
                  B7 ─ B8 ─ B9 ┘                        │
                                                        ▼
              C1 C2 C3 C5 (cleanups) ─► R1 (data plane) ─► R9 (cache) ─► R10 (observability)
                                            │
                                            ├─► R11 (graceful shutdown) ─► R12 (usage tracking) / R13 (health)
                                            │
                          R2 R3 R4 R5 R14 … ┘  ─► C6 (tests throughout)
```

Fix the schema + repo bugs first (B2–B9) so the DB actually works, **then** wire
the app (B1), **then** layer on the data plane and roadmap (R*), adding tests
(C6) as you go.
