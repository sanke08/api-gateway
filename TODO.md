# ✅ TODO — Known Issues, Bugs & Build-Out Checklist

Living checklist for the gateway. The original "library of components that don't
run" phase is **over**: the app is now assembled, the blocking schema/SQL bugs
are fixed, and the full request path (onboard-seeded auth → proxy → rate limit →
retry → circuit breaker) has been exercised end-to-end. What remains is cleanup
and roadmap build-out.

> **How to use:** complete items top-to-bottom (priority order). When you finish
> one, change `- [ ]` to `- [x]`, update the **Progress** counter below, and move
> the line's summary into [✅ What's Complete](#-whats-complete-done).

---

## 📊 Progress

| Group                                   | Done | Total |
| --------------------------------------- | :--: | :---: |
| 🔴 P0 — Blocking (app cannot run)        |  9   |   9   |
| 🟡 P1 — Correctness / cleanliness        |  4   |   6   |
| 🟢 P2 — Build-out / roadmap              |  6   |  14   |
| **TOTAL**                               | **19** | **29** |

_Last updated: 2026-06-30. **All P0 blocking bugs (B1–B9) are fixed and the app
is wired.** The data plane, observability, cache, graceful shutdown, async usage
tracking, health endpoints, and edge/CORS are all instantiated in `main.go`.
Two additional bugs found by end-to-end simulation this round — an API-key
INSERT parameter-count mismatch and a missing router catch-all — are also fixed
(see [Recently fixed](#-recently-fixed-2026-06-30))._

> **Verification note (2026-06-30):** `go build ./...` and `go vet ./...` pass
> with exit 0. A full in-process simulation (real router + auth middleware +
> proxy to live `httptest` upstreams, with in-memory repositories standing in
> for Postgres) drove every subsystem and passed under `go test -race`:
> login + refresh, tenant isolation, multi-segment proxying, 401s, rate-limit
> 429 + `Retry-After`, retry recovery, and circuit-breaker open→half-open→close.
> The simulation/test files were not committed (kept the tree to source-only
> changes); see **C6** to land them permanently.

---

## 🆕 Recently fixed (2026-06-30)

- [x] **NEW-1 — API-key INSERT parameter-count mismatch (broke onboarding).**
  `apikey_repo.Create` listed 4 placeholders `($1..$4)` but passed **5** args
  (a leading, always-empty `normalized.ID`). `lib/pq` rejects this at runtime,
  so `OnboardTenant` failed and rolled back on every call. Removed the stray
  `normalized.ID` arg so 4 columns = 4 placeholders = 4 args; `id` comes from
  the DB via `RETURNING`.
  - File: [internal/repository/apikey_repo.go](internal/repository/apikey_repo.go)
- [x] **NEW-2 — Router catch-all (`/{path...}`) not supported (broke proxying).**
  `main.go` registers the data-plane proxy under `/{path...}`, but the trie
  router parsed that as an ordinary single-segment param named `path...`, so any
  request with 2+ path segments (`/orders/123`, `/v1/users/5`) returned 404 and
  never reached the upstream. Added real catch-all support: `parsePattern`
  recognizes `{name...}` (final segment only), the trie gained a `catchAllChild`
  tried after static + single-param children, and `match` captures the whole
  remainder (zero or more segments) under the param name.
  - File: [internal/server/router.go](internal/server/router.go)

---

## 🔴 P0 — Blocking — ✅ ALL DONE

All nine blocking items are resolved. The migrations now match the repository
SQL, the column/placeholder/scan counts line up, and `main.go` assembles and
serves the full application. See [✅ What's Complete](#-whats-complete-done).

- [x] **B1 — Assemble the app.** `main.go` opens the DB (with `PingContext`),
  builds repos + services + JWT manager + metrics registry + cache + proxy +
  rate limiter, constructs the trie router, registers `/onboard`, `/login`,
  `/metrics`, `/health`, `/ready`, and (when upstreams are configured) the
  proxied data-plane routes, then serves the router behind a `GracefulServer`.
- [x] **B2 — `001` trailing comma.** Fixed; `CREATE TABLE tenants` is valid.
- [x] **B3 — users password column.** Migration uses `password_hash`, matching
  `user_repo`.
- [x] **B4 — usage columns.** Migration uses `path` + `"timestamp"`, matching
  `usage_repo`.
- [x] **B5 — `api_keys.expires_at`.** Removed everywhere — no longer referenced
  in the migration, repo, or model.
- [x] **B6 — `user_repo.Create` RETURNING/Scan.** Returns 5 columns, scans 5.
- [x] **B7 — `apikey_repo` column mismatch.** Column list and Scan agree (7).
- [x] **B8 — `membership.Create` param/column shift.** INSERT lists 4 columns,
  passes 4 values; RETURNING 7, scans 7.
- [x] **B9 — ID + timestamp handling.** Repos omit `id`/timestamps on INSERT and
  take the DB-generated values via `RETURNING`; normalizers no longer require an
  app-supplied `id`. (The last stray ID arg was removed in NEW-1.)

---

## 🟡 P1 — Correctness & cleanliness

- [x] **C1 — Dead `case` in `mapRepositoryError`.** Resolved — one branch each
  for `validation`, `conflict`, and `not_found`; no unreachable duplicate.
  - File: [internal/services/errors.go](internal/services/errors.go)
- [x] **C2 — `tenants` status CHECK vs model.** Migration now allows
  `('active','suspended')`, matching the model/services.
  - File: [internal/db/migrations/001_create_tenants.sql](internal/db/migrations/001_create_tenants.sql)
- [x] **C3 — `Usage.Endpoint` naming end-to-end.** Reconciled to `path` across
  model, table column, and repo SQL.
- [x] **C5 — `db.Ping()` on startup.** `NewDatabase` now calls `PingContext`
  with a timeout and fails fast on a bad DSN.
  - File: [internal/config/database.go](internal/config/database.go)
- [ ] **C4 — Resolve retry scaffolding.** Still open: `retryableBody` (stub
  returning `false`, unused), `normalizeContextErr`, `bufferBody`,
  `bodyFromBytes`, and `var _ sync.Locker` are dead code. Either wire them in or
  delete them. (The live retry path uses `bodyReplayable`, not `retryableBody`.)
  - Files: [internal/retry/policy.go](internal/retry/policy.go),
    [internal/retry/transport.go](internal/retry/transport.go)
- [ ] **C6 — Add tests (commit them).** A full `-race` simulation was written and
  passed this round but was **not committed**. Land permanent tests: the
  pure units (PBKDF2 hash/verify, JWT issue/verify/refresh, router trie +
  catch-all matching, token-bucket limiter with injected clock, retry backoff)
  plus an integration-style suite that wires the router + auth + proxy against
  in-memory repos and `httptest` upstreams.
  - Suggested: `*_test.go` next to each package, e.g. `internal/sim/` for the
    integration suite.

---

## 🟡 P1.5 — Bugs found but NOT yet fixed (from this round's review/simulation)

These were surfaced by code review + the end-to-end simulation. None block the
happy path, but they are real. Ordered by severity.

- [ ] **N1 — Account-enumeration timing in login.** `AuthService.Login` returns
  immediately on an unknown email but runs full PBKDF2 (210k iters) for a known
  one. The timing gap lets an attacker enumerate registered emails. Fix: verify
  against a fixed decoy hash on the not-found path.
  - File: [internal/services/auth.go](internal/services/auth.go)
- [ ] **N2 — Retry backoff integer overflow.** `nextBackoff` computes
  `1 << (attempt-1)`; with no upper bound on `Policy.Attempts`, the shift
  overflows around attempt ~39 and the delay goes negative → treated as "no
  wait" → tight retry storm. Fix: cap the shift exponent (and/or `Attempts`).
  - File: [internal/retry/policy.go](internal/retry/policy.go)
- [ ] **N3 — Rate-limit `Retry-After` floored to 1s.** Sub-second waits are
  reported as a full second (then re-ceiled in the middleware), over-throttling
  high-rate limits. Minor; over-restrictive, not unsafe.
  - File: [internal/ratelimit/limiter.go](internal/ratelimit/limiter.go)
- [ ] **N4 — `tenant_repo.GetBySlug` never sets `Status`.** It scans `status`
  into a local var but returns the tenant without assigning `t.Status` (unlike
  `GetByID`). A tenant fetched by slug always reads as empty status.
  - File: [internal/repository/tenant_repo.go](internal/repository/tenant_repo.go)
- [ ] **N5 — Async usage tracker send-on-closed-channel race.** `Enqueue` checks
  an `atomic.Bool` then sends on the queue; `Close` stores the bool then closes
  the channel. A request racing shutdown can send on a closed channel and panic.
  Fix: guard the send (e.g. `sync.RWMutex` around send vs close, or a `done`
  select arm).
  - File: [internal/services/usage.go](internal/services/usage.go)
- [ ] **N6 — `LoggingMiddleware` drops `Flusher`/`Hijacker`/`Pusher`.** Its
  response wrapper only overrides `WriteHeader`, so SSE/streaming/WebSocket
  upgrades through the proxy break when logging is the outer middleware.
  - File: [internal/middleware/logging.go](internal/middleware/logging.go)
- [ ] **N7 — `RewritePath` clobbers the base-URL path prefix.** In the proxy
  Director, a configured `RewritePath` overwrites the full path *after*
  strip/add-prefix + base-path were applied, discarding the base path.
  - File: [internal/proxy/transform.go](internal/proxy/transform.go)
- [ ] **N8 — Router `r.state` is a plain pointer, not atomic.** Lock-free reads
  in `ServeHTTP` race the `rebuild` write under the Go memory model (harmless if
  routes are only registered at startup, which they are). Use
  `atomic.Pointer[state]`.
  - File: [internal/server/router.go](internal/server/router.go)
- [ ] **N9 — `edge.go` `ContentTypeNosniff` config knob is dead.**
  `policy.ContentTypeNosniff || !policy.ContentTypeNosniff` is always `true`, so
  the header can never be disabled. Fails secure, but the field does nothing.
  - File: [internal/middleware/edge.go](internal/middleware/edge.go)
- [ ] **N10 — Rate-limit keyfunc coupling.** The proxy chain serves both the
  API-key and Bearer paths; `main.go` uses `KeyTenant` (works for both), but
  `KeyAPIKey`/`KeyUser` would 500 on the path that doesn't resolve that
  identity. Document the constraint or make the keyfunc fall back gracefully.
  - File: [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go)

---

## 🟢 P2 — Build-out / roadmap

Done this phase:

- [x] **R1 — Wire the data plane.** `proxy.StaticRegistry` is built from config
  upstreams; proxied routes chain `APIKeyAuth`/`TenantResolution` → rate limit →
  usage → `proxy.Handler`. (Catch-all routing for these routes fixed in NEW-2.)
- [x] **R2 — Move secrets/policies into config.** JWT secret/issuer/TTLs,
  rate-limit rule, CORS/security policy, cache config, and upstreams all load
  from env in `internal/config/config.go`.
- [x] **R10 — Wire the observability layer.** `NewRegistry()` is constructed,
  `observability.Middleware(reg)` is a global middleware, and `/metrics` is
  routed. (See N-list: per-tenant metric label still reads `unknown` —
  tracked separately as a known limitation, not yet fixed.)
- [x] **R11 — Wire graceful shutdown.** `GracefulServer` + `ShutdownManager`
  wrap the router; SIGINT/SIGTERM trigger a 30s drain; usage tracker, cache
  pruner, and limiter pruner are registered as hooks.
- [x] **R12 — Wire async usage tracking.** `AsyncUsageTracker` is constructed
  and `UsageMiddleware` wraps proxied routes; `tracker.Close` is a shutdown
  hook. (See N5 for the shutdown race.)
- [x] **R13 — Wire health & readiness endpoints.** `/health` + `/ready` are
  routed against `HealthChecker` (DB ping + upstream probes).
- [x] **R14 — Wire edge (CORS + security headers).** `NewEdgeMiddleware` is a
  global `router.Use`. (See N9 for the dead nosniff knob.)
- [x] **R9 (partial) — Wire the cache layer.** `MemoryStore`/`HybridStore` are
  constructed from `CacheConfig`, response-cache middleware wraps proxied
  routes, and a prune ticker runs. Identity-cache injection into the auth
  middlewares (Get-before-DB) is **not** done yet — see R9-remainder below.

Still open:

- [ ] **R3 — Persist upstreams.** Upstreams come from `UPSTREAMS_JSON` env only;
  add an `upstreams` table + repository.
- [ ] **R4 — Verify usage rows are written from the proxy path.** The wiring
  (R12) exists; confirm rows actually land for both auth paths and add a test.
- [ ] **R5 — Refresh-token endpoint.** `JWTManager.RefreshAccessToken` works
  (exercised in simulation) but has no HTTP route/handler.
  - File: [internal/handlers/](internal/handlers/)
- [ ] **R6 — Token revocation using `jti`.** Track/blacklist token IDs.
- [ ] **R7 — Operational hardening.** A real migration runner (the SQL files are
  still applied by hand). Pruning goroutines for cache/limiter are wired (R9/R11).
- [ ] **R8 — Admin / dashboard API.** Tenant management + usage analytics.
- [ ] **R9-remainder — Identity-cache injection.** Have `APIKeyAuthMiddleware`
  and `TenantResolutionMiddleware` accept a `cache.Store` and check it before
  hitting the DB (keys `apikey:<hash>` · `tenant:<id>` ·
  `membership:<user>:<tenant>`, ~5 min TTL).
  - Files: [internal/middleware/api_key_auth_middleware.go](internal/middleware/api_key_auth_middleware.go),
    [internal/middleware/tenant_resolution_middleware.go](internal/middleware/tenant_resolution_middleware.go)
- [ ] **R10-remainder — Per-tenant metric label.** The observability middleware
  reads the tenant from the context it created *before* auth runs, so the
  `tenant` metric label is always `unknown`. Record the label after resolution
  (or read it from the shared `Trace`).
  - Files: [internal/observability/middleware.go](internal/observability/middleware.go),
    [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go)

---

## ✅ What's Complete (done)

Components that are **built, wired, and exercised**. "Live" means reachable on
the running server / verified by the end-to-end simulation.

| Component | State | Notes |
| --------- | ----- | ----- |
| Application bootstrap (`cmd/gateway/main.go`) | ✅ Live | Opens DB, builds everything, serves the router behind `GracefulServer` |
| Domain models (`internal/models`) | ✅ Done | Complete |
| Config load + validation + `db.Ping` | ✅ Live | All policies/secrets/upstreams from env; fails fast on bad DSN |
| Structured logging (`observability`) | ✅ Live | Global middleware |
| Metrics registry + `/metrics` + obs middleware | ✅ Live | Wired globally (per-tenant label still `unknown` — R10-remainder) |
| Repository layer (CRUD + `WithTx`) | ✅ Live | SQL matches migrations; counts verified |
| Onboarding service (atomic tx) | ✅ Live | API-key INSERT fixed (NEW-1); needs Postgres to run for real |
| Auth service / login + refresh | ✅ Live | Verified in simulation (login + token refresh) |
| JWT manager (HS256) | ✅ Live | Issue/verify/refresh; alg + type + expiry + issuer checks |
| Password hashing (PBKDF2) | ✅ Done | Hash/verify round-trip verified |
| API-key auth (SHA-256) + middleware | ✅ Live | Machine auth on the data plane |
| Tenant resolution + middleware | ✅ Live | Bearer + `X-Tenant-ID` path verified |
| Trie router (+ catch-all) | ✅ Live | Catch-all `/{path...}` fixed (NEW-2); static > param > catch-all precedence |
| Reverse proxy (per-tenant) | ✅ Live | Built from a `StaticRegistry` of config upstreams |
| Request/response transforms | ⚠️ Built | Headers + path only (see N7 RewritePath bug) |
| Retry transport (backoff+jitter) | ✅ Live | Recovers transient 5xx; see C4 dead code + N2 overflow |
| Circuit breaker (CLOSED/OPEN/HALF_OPEN + Transport) | ✅ Live | open→half-open→close verified |
| Cache (Memory/Hybrid + response-cache middleware) | ✅ Live | Wired on proxied routes + prune ticker; identity-cache injection pending (R9-remainder) |
| Rate limiter (token bucket) + middleware | ✅ Live | 429 + `Retry-After` verified (see N3 flooring) |
| Graceful shutdown (`server/shutdown.go`) | ✅ Live | `Wrap` + hooks + SIGINT/SIGTERM + 30s drain |
| Async usage tracking + usage middleware | ✅ Live | Wired on proxied routes (see N5 shutdown race) |
| Health checker + `/health` + `/ready` | ✅ Live | DB ping + upstream probes |
| Edge middleware (CORS + security headers) | ✅ Live | Global `Use` (see N9 dead nosniff knob) |

> ✅ Done/Live = wired & exercised · ⚠️ Built = compiles & wired but has a known
> open bug (see the N-list).

### ❌ Not started (no code yet)
- Refresh-token **endpoint** (service method works, no handler) → **R5**
- Token revocation → **R6**
- `upstreams` persistence (DB table) → **R3**
- Admin / dashboard API → **R8**
- Committed tests → **C6**
- Identity-cache injection into auth middleware → **R9-remainder**
- Migration runner → **R7**

---

## 🔗 Suggested order of attack

```
DONE:  B2–B9, B1 (schema + repos + wiring)  ─►  R1/R2/R9/R10/R11/R12/R13/R14 (subsystems wired)
                                                 NEW-1 (onboarding INSERT) · NEW-2 (router catch-all)

NEXT:  C6 (commit the tests)  ─►  N1/N5 (security: login timing, shutdown panic)
            │
            ├─► N2 N3 N4 N6 N7 N8 N9 N10  (remaining correctness bugs)
            │
            └─► C4 (retry dead code)  ─►  R5 R6 R3 R4 R7 R8  (roadmap features)
                                          R9-remainder / R10-remainder (finish cache + metrics wiring)
```

The blocking work is done and the system runs end-to-end. Prioritize **landing
the tests (C6)** so the fixes can't regress, then the security bugs (**N1**,
**N5**), then the remaining correctness items and roadmap.
