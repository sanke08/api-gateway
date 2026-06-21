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
| 🟢 P2 — Build-out / roadmap              |  0   |   9   |
| **TOTAL**                               |  **0** | **24** |

_Last updated: 2026-06-21_

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
- [ ] **R9 — Wire the cache layer.** The cache packages (`cacheclient`,
  `cache.MemoryStore`, `cache.HybridStore`) are fully built but never
  instantiated. Four steps are required:
  1. **Config** — add `CacheConfig` to `internal/config/config.go` with env
     vars `CACHE_REMOTE_URL` (blank = local-only), `CACHE_TIMEOUT` (default 2s),
     `CACHE_NAMESPACE` (default `"gateway"`), `CACHE_TOKEN` (optional Bearer
     token for the remote cache service).
  2. **Bootstrap** — in `main.go` / `server.New`: construct a `RemoteClient`
     when `CACHE_REMOTE_URL` is non-blank, wrap it with `NewHybridStore`, and
     pass the store to middleware constructors.
  3. **Middleware injection** — `APIKeyAuthMiddleware` and
     `TenantResolutionMiddleware` should accept a `*cache.HybridStore` (or
     `cache.Store`) and use `Get` before hitting the DB; call `Set` on a miss.
     Suggested key scheme: `apikey:<sha256-hex>`, `tenant:<tenant-id>`,
     `membership:<user-id>:<tenant-id>`. Suggested TTL: 5 min (tune via config).
  4. **Pruning goroutine** — start a background `time.Ticker` (every 5 min) that
     calls `MemoryStore.PruneExpired()` to reclaim stale in-process entries.
  - Files: [internal/config/config.go](internal/config/config.go),
    [internal/cmd/gateway/main.go](internal/cmd/gateway/main.go),
    [internal/server/server.go](internal/server/server.go),
    [internal/middleware/api_key_auth_middleware.go](internal/middleware/api_key_auth_middleware.go),
    [internal/middleware/tenant_resolution_middleware.go](internal/middleware/tenant_resolution_middleware.go)
  - ⛓️ Depends on: B1 (app must be assembled first).

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
| Rate limiter (token bucket) | ⚠️ Built | In-memory; not routed (R1) |

> ✅ Done = complete as intended · ⚠️ Built = code exists & compiles but is not
> wired/runnable yet.

### ❌ Not started (no code yet)
- Refresh-token **endpoint** (service method exists, no handler) → **R5**
- Token revocation → **R6**
- `upstreams` persistence → **R3**
- Usage writing from the request path → **R4**
- Admin / dashboard API → **R8**
- Tests → **C6**
- Cache config env vars + middleware injection → **R9** (packages built; wiring not started)

---

## 🔗 Suggested order of attack

```
B2 ─┐
B3 ─┤
B4 ─┼─► (fix schema)  ─► B6 ─┐
B5 ─┘                        ├─► (repos work) ─► B1 (wire /onboard, /login)
                  B7 ─ B8 ─ B9 ┘                        │
                                                        ▼
                                  C1 C2 C3 C5 (cleanups)  ─► R1 (data plane) ─► R9 (cache wiring)
                                                              │
                                                R2 R3 R4 R5 … ┘  ─► C6 (tests throughout)
```

Fix the schema + repo bugs first (B2–B9) so the DB actually works, **then** wire
the app (B1), **then** layer on the data plane and roadmap (R*), adding tests
(C6) as you go.
