# Go API Gateway (Multi-Tenant)

A multi-tenant **API Gateway** written in Go using (almost) only the standard
library. It is designed to be the single secure entry point for a SaaS platform:
it onboards tenants, authenticates humans (JWT) and machines (API keys),
resolves which tenant a request belongs to, applies rate limits, and reverse-
proxies traffic to per-tenant upstream backends with request/response
transformation and automatic retries.

The single external dependency is `github.com/lib/pq` (the PostgreSQL driver).
All security primitives (password hashing, API-key hashing, JWT signing) are
hand-rolled on top of `crypto/*`.

> ⚠️ **Project status — read this first.**
> This repository is a **library of well-built, individually-correct components
> that are not yet assembled into a running application**, and it currently has
> several **schema/code mismatches and bugs that prevent it from running end to
> end**. `main.go` starts an HTTP server that serves **only an empty mux + a
> logging middleware** — none of the router, database, services, handlers,
> proxy, or rate limiter is wired in. See
> [What Actually Runs Today](#what-actually-runs-today) and
> [Known Issues & Bugs](#known-issues--bugs-must-fix-before-it-runs) before
> trying to use it. The component descriptions below document the code **as
> written**; the issues section documents what stops it from working.
>
> 📋 **A complete, prioritized checklist of every fix and build-out task lives in
> [TODO.md](TODO.md)** — work through it one item at a time (B1–B9 blocking,
> C1–C6 cleanups, R1–R8 roadmap). It also tracks **what's done vs. remaining**.

---

## 🎨 Diagrams & Visual Style Guide

Each major section below has both an **ASCII diagram** (always-correct, version-
controlled fallback) and a **Gemini image prompt** in a collapsible block, with
an image placeholder you can fill in. Generate each image in Gemini, save it to
`docs/images/<name>.png`, and the placeholder will render it.

**How to use:** copy the **Shared Style Preamble** below, then paste it in front
of any individual diagram prompt before sending to Gemini. This keeps every
image in the same hand-drawn Excalidraw / Eraser.io look.

> 📁 Suggested image folder: `docs/images/` — create it with
> `mkdir -p docs/images`. Filenames are given with each prompt.

<details>
<summary><b>📋 Shared Style Preamble (paste before every diagram prompt)</b></summary>

```text
STYLE: Hand-drawn whiteboard diagram in the Excalidraw / Eraser.io aesthetic.
- Rough, sketchy, slightly wobbly hand-drawn strokes (Virgil / Excalifont-style
  handwritten font for ALL text labels).
- Rounded rectangles with subtle hand-drawn shadows for components/boxes.
- Solid arrows for the main flow; dashed arrows for optional/secondary flow.
  Every arrow MUST have a clear direction (arrowhead) and, where noted, a short
  label on the arrow line.
- Soft, muted "sticky-note" pastel fills with darker hand-drawn outlines:
    • Blue   = client / external / network edge
    • Green  = HTTP / server / router / middleware layer
    • Yellow = services / business logic
    • Orange = data / repository / database
    • Purple = proxy / upstream / outbound
    • Red    = errors / failures / blocking issues
    • Gray   = disabled / "not wired yet" components (draw these with a dashed
               outline and a small 🚧 or "NOT WIRED" tag)
- Use simple line icons inside boxes where helpful: 🌐 client, 🔒 auth/lock,
  🔑 API key, 🪪 JWT/token, 🗄️ database, ⚙️ service, 🚦 rate limit, 🔁 retry,
  📨 request, 🏢 tenant, 👤 user, ➡️ proxy/forward.
- Clean generous spacing, NO overlapping lines, left-to-right or top-to-bottom
  reading order. Title in a hand-drawn banner at the top.
- White or very light paper background. Landscape orientation unless told
  otherwise. High resolution, legible text. NO photorealism, NO 3D, NO gradients
  beyond soft flat pastel fills.
```

</details>

---

## Table of Contents

1. [What Is This?](#what-is-this)
2. [Technology Stack](#technology-stack)
3. [Architecture Overview](#architecture-overview)
4. [Directory & File Map](#directory--file-map)
5. [Layer-by-Layer Walkthrough](#layer-by-layer-walkthrough)
   - [Entry Point & Server](#1-entry-point--server)
   - [Configuration](#2-configuration)
   - [Observability](#3-observability)
   - [Domain Models](#4-domain-models)
   - [Database & Migrations](#5-database--migrations)
   - [Repository Layer](#6-repository-layer)
   - [Service Layer](#7-service-layer)
   - [HTTP Handlers](#8-http-handlers)
   - [Middleware](#9-middleware)
   - [Router](#10-router)
   - [Reverse Proxy](#11-reverse-proxy)
   - [Rate Limiting](#12-rate-limiting)
   - [Retry Transport](#13-retry-transport)
   - [Circuit Breaker](#14-circuit-breaker)
   - [Request Context](#15-request-context)
6. [Function Reference](#function-reference)
7. [Data Model & Relationships](#data-model--relationships)
8. [User Flows](#user-flows)
9. [Data Flow (Request Lifecycle)](#data-flow-request-lifecycle)
10. [Error Handling Pipeline](#error-handling-pipeline)
11. [Setup & Installation](#setup--installation)
12. [Configuration Reference](#configuration-reference)
13. [Usage / API Reference](#usage--api-reference)
14. [What Actually Runs Today](#what-actually-runs-today)
15. [Known Issues & Bugs](#known-issues--bugs-must-fix-before-it-runs)
16. [Feature Status](#feature-status)
17. [Roadmap & Next Steps](#roadmap--next-steps)

---

## What Is This?

A **multi-tenant API gateway**. In a SaaS platform, many separate customer
organizations ("tenants") share one deployment. The gateway is the front door:

- A **tenant** is the ownership/isolation boundary (a business/organization).
- A **user** is a global person identity that can belong to **many** tenants.
- A **membership** links one user to one tenant, carrying a role and status.
- An **API key** is a machine credential scoped to one tenant.
- An **upstream target** describes where a tenant's traffic should be proxied.

The gateway's job per request is to: identify the caller (human via JWT or
machine via API key), resolve which tenant they are acting under, enforce rate
limits, then forward the request to that tenant's backend and shape the
request/response along the way.

---

## Technology Stack

| Concern            | Choice                                                        |
| ------------------ | ------------------------------------------------------------- |
| Language           | Go (`go.mod` declares `go 1.25.4`; toolchain seen: `go1.26.3`) |
| Module path        | `github.com/sanke08/api_gateway`                              |
| HTTP               | `net/http` standard library + `net/http/httputil.ReverseProxy` |
| Database           | PostgreSQL via `github.com/lib/pq` v1.12.3 (only 3rd-party dep) |
| UUIDs              | `pgcrypto` `gen_random_uuid()` in DB **and** a hand-rolled `crypto/rand` UUIDv4 generator in code |
| Password hashing   | Hand-rolled **PBKDF2-HMAC-SHA256** (210,000 iterations) via `crypto/*` |
| API-key hashing    | **SHA-256** (hex) via `crypto/sha256`                         |
| JWT                | Hand-rolled **HS256** (HMAC-SHA256) via `crypto/hmac`         |
| Logging            | `log/slog` JSON handler to stdout                             |
| Architecture       | Clean / layered architecture (handler → service → repository) |
| Resilience         | Retry transport (exponential backoff + jitter) + Circuit breaker (CLOSED/OPEN/HALF_OPEN state machine) |

> **Note on "zero dependencies":** the original README claimed "zero third-party
> dependencies." That is *aspirational* — `lib/pq` is a third-party dependency.
> What is true is that **all business and security logic** uses only the standard
> library; only the Postgres wire driver is external.

---

## Architecture Overview

The intended design is a clean, layered architecture with a fast request path:

```
                         ┌───────────────────────────────────────────┐
                         │                  CLIENT                     │
                         └───────────────────────┬─────────────────────┘
                                                 │ HTTP
                                                 ▼
   ┌──────────────────────────────────────────────────────────────────────────┐
   │                              GATEWAY PROCESS                                │
   │                                                                            │
   │  Server (net/http)                                                         │
   │    └── LoggingMiddleware  (request ID + structured access log)             │
   │          └── Router (trie-based, lock-free hot path)                       │
   │                                                                            │
   │   ── intended pipeline per route ──────────────────────────────────────   │
   │                                                                            │
   │   [Control-plane / human routes]        [Data-plane / proxied routes]      │
   │     POST /onboard  ──► OnboardingHandler   X-API-Key  ──► APIKeyAuth MW     │
   │     POST /login    ──► AuthHandler           OR Bearer ──► TenantResolve MW │
   │                                              └► RateLimit MW                │
   │                                                  └► Proxy Handler           │
   │                                                       └► RetryTransport     │
   │                                                            └► Upstream      │
   │                                                                            │
   │   Service layer:  Onboarding · Auth · APIKeyAuth · TenantResolution · JWT   │
   │   Repository layer (thin, SQL-only, transaction-aware via WithTx)          │
   │   PostgreSQL                                                               │
   └──────────────────────────────────────────────────────────────────────────┘
```

<!-- IMAGE: replace this line with the generated diagram -->
![Architecture Overview](docs/images/01-architecture-overview.png)

<details>
<summary><b>🎨 Gemini prompt — Architecture Overview</b> (save as <code>docs/images/01-architecture-overview.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Multi-Tenant API Gateway — System Architecture"

Draw one large rounded container titled "GATEWAY PROCESS (Go)" in the center.
On the far LEFT, a blue box "🌐 CLIENT" with a solid arrow labeled "HTTP request"
pointing into the gateway. On the far RIGHT, two purple boxes stacked:
"➡️ Tenant A Upstream (api.acme.internal)" and "➡️ Tenant B Upstream
(api.globex.internal)".

INSIDE the gateway container, stack these layers TOP to BOTTOM, each a labeled
horizontal band:

1) GREEN band "net/http Server" containing a green box
   "📨 LoggingMiddleware (assigns X-Request-ID, structured access log)".
   NOTE TAG on this box: "✅ the only thing actually wired today".

2) GREEN box "Router (trie-based, lock-free hot path)" — draw it with a DASHED
   gray outline and a small 🚧 "NOT WIRED YET" tag.

3) Split the next area into TWO columns with a vertical dotted divider:
   LEFT column header "CONTROL PLANE (no auth)":
     - green box "POST /onboard ➡️ OnboardingHandler"
     - green box "POST /login ➡️ AuthHandler"
   RIGHT column header "DATA PLANE (proxied, intended)":
     - green box "🔑 X-API-Key ➡️ APIKeyAuth MW"  (dashed/gray, 🚧)
     - green box "🪪 Bearer + X-Tenant-ID ➡️ TenantResolution MW" (dashed/gray, 🚧)
     - then a downward solid arrow to green box "🚦 RateLimit MW" (dashed/gray)
     - then arrow to purple box "➡️ Proxy Handler" (dashed/gray)
     - then arrow to purple box "🔁 RetryTransport" (dashed/gray)
   The RetryTransport box has a solid arrow to the RIGHT-side Upstream boxes,
   labeled "forward + inject X-Gateway-* headers".

4) YELLOW band "Service Layer" with five small yellow ⚙️ boxes in a row:
   "Onboarding", "Auth", "APIKeyAuth", "TenantResolution", "JWT (HS256)".

5) ORANGE band "Repository Layer (thin, SQL-only, WithTx transactions)" as one
   long orange box.

6) ORANGE box "🗄️ PostgreSQL" at the very bottom, with a double-headed arrow to
   the Repository band labeled "database/sql + lib/pq".

Add a small LEGEND box in a corner explaining: solid arrow = active flow,
dashed gray box = "built but NOT wired into main.go yet".

Landscape, lots of breathing room, hand-drawn Excalidraw style.
```

</details>

Three design principles run through the code:

1. **Do expensive work once at startup, keep the request path cheap.**
   The router compiles routes into an immutable trie and pre-wraps middleware;
   the proxy builds one `ReverseProxy` per tenant at boot; the retry transport
   normalizes its policy once.
2. **Layered separation with a 3-tier error pipeline.** Repositories speak SQL
   and emit `RepoError`; services speak business rules and emit `ServiceError`;
   handlers/middleware map `ServiceError` kinds to HTTP status codes. Internal
   details never leak to the client.
3. **The gateway is the only trusted boundary.** Client-supplied identity
   headers are never trusted; the gateway resolves identity itself and injects
   authoritative `X-Gateway-*` headers downstream.

---

## Directory & File Map

```
api-gateway/
├── .env                              # Env var template (all values blank)
├── go.mod / go.sum                   # Module + lib/pq dependency
├── README.md                         # (this file)
└── internal/
    ├── cmd/gateway/main.go           # Process entry point (currently minimal wiring)
    │
    ├── config/
    │   ├── config.go                 # Load() env → Config; typed env helpers
    │   ├── database.go               # DBConfig + NewDatabase() sql.DB pool
    │   └── validator.go              # Validate(Config) required-field checks
    │
    ├── observability/
    │   └── logger.go                 # slog JSON logger: InitLogger/Info/Error
    │
    ├── models/                       # Pure domain structs (no behavior)
    │   ├── tenant.go                 # Tenant + status/role/membership enums
    │   ├── user.go                   # User (global identity)
    │   ├── tenant_membership.go      # User↔Tenant link
    │   ├── apikey.go                 # APIKey (stores hash only)
    │   ├── usage.go                  # Usage (metering record)
    │   ├── upstream.go               # UpstreamTarget (proxy routing config)
    │   ├── transform.go              # Request/ResponseTransform
    │   ├── retry.go                  # RetryPolicy (per-upstream)
    │   └── circuit_breaker.go        # CircuitBreakerPolicy (failure threshold, open duration, probes)
    │
    ├── db/migrations/                # Raw SQL DDL (no migration runner present)
    │   ├── 001_create_tenants.sql    # tenants + updated_at trigger fn
    │   ├── 002_create_users.sql      # users
    │   ├── 003_api_keys.sql          # api_keys
    │   ├── 004_usage.sql             # usage
    │   └── 005_create_tenant_membership.sql
    │
    ├── repository/                   # Thin SQL layer, interface-driven
    │   ├── interfaces.go             # All repo interfaces + Repositories bundle
    │   ├── error.go                  # RepoError + classifySQLError (pq codes)
    │   ├── postgres_helper.go        # sqlExecutor iface + normalize/validate
    │   ├── tenant_repo.go            # PostgresTenantRepo
    │   ├── user_repo.go              # PostgresUserRepo
    │   ├── membership.go             # postgresTenantMembershipRepo
    │   ├── apikey_repo.go            # postgresAPIKeyRepo
    │   └── usage_repo.go             # postgresUsageRepo
    │
    ├── services/                     # Business logic / use cases
    │   ├── auth.go                   # AuthService.Login (human login)
    │   ├── jwt.go                    # JWTManager (HS256 issue/verify/refresh)
    │   ├── credentials.go            # PBKDF2 hasher, API-key gen, UUID, PRF
    │   ├── api_key_auth.go           # APIKeyAuthenticator (machine auth)
    │   ├── onboarding.go             # OnboardingService (atomic tenant create)
    │   ├── tenant_resolution.go      # TenantResolver (token+tenant+membership)
    │   ├── errors.go                 # ServiceError + mapRepositoryError
    │   ├── auth_errors.go            # ErrUnauthorized helper
    │   └── authorization_errors.go   # ErrForbidden helper
    │
    ├── handlers/                     # Thin HTTP adapters
    │   ├── auth_handler.go           # POST /login
    │   ├── onboarding_handlers.go    # POST /onboard
    │   └── json.go                   # writeJSON / writeJSONError
    │
    ├── middleware/
    │   ├── logging.go                # LoggingMiddleware (+ request ID)
    │   ├── api_key_auth_middleware.go        # X-API-Key → tenant in context
    │   └── tenant_resolution_middleware.go   # Bearer + X-Tenant-ID → context
    │
    ├── server/
    │   ├── server.go                 # http.Server wrapper (Start)
    │   └── router.go                 # Trie router (control/data plane split)
    │
    ├── proxy/
    │   ├── handler.go                # ReverseProxy builder + forwarding handler
    │   ├── registry.go               # StaticRegistry (in-memory upstreams)
    │   └── transform.go              # Header add/remove + path rewrite
    │
    ├── ratelimit/
    │   ├── limiter.go                # Token-bucket Limiter (sync.Map of buckets)
    │   ├── middleware.go             # NewMiddleware → 429 + Retry-After
    │   └── keys.go                   # KeyFunc builders (tenant/user/key/route…)
    │
    ├── retry/
    │   ├── policy.go                 # Policy + normalizePolicy + backoff/jitter
    │   └── transport.go              # http.RoundTripper that retries safely
    │
    ├── circuit_breaker/
    │   ├── breaker.go                # Breaker state machine (CLOSED/OPEN/HALF_OPEN) + Policy
    │   └── transport.go              # http.RoundTripper that gates traffic through the breaker
    │
    └── pkg/types/
        ├── path_params.go            # Path-param context helpers
        └── request_context.go        # Typed context keys for resolved identity
```

<!-- IMAGE: replace this line with the generated diagram -->
![Component / Package Map](docs/images/02-component-map.png)

<details>
<summary><b>🎨 Gemini prompt — Component / Package Map</b> (save as <code>docs/images/02-component-map.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Package Map & Dependency Direction (internal/)"

Draw the Go packages as rounded boxes arranged in horizontal TIERS, with solid
arrows showing "depends on / calls" pointing DOWNWARD (higher tier calls lower
tier). Title banner at top: "internal/ — layered dependencies".

TIER 1 (top, GREEN): "cmd/gateway/main.go (entry point)".
  Arrow down to "server (http.Server + Router)".

TIER 2 (GREEN): "server", "middleware", "handlers" side by side.
  - "middleware" boxes: LoggingMiddleware, APIKeyAuthMiddleware,
    TenantResolutionMiddleware.
  - "handlers" boxes: OnboardingHandler, AuthHandler.

TIER 3 (YELLOW): "services" — one box listing: Onboarding, Auth, APIKeyAuth,
  TenantResolution, JWTManager, PasswordHasher, APIKeyGenerator.
  Arrows from handlers AND middleware down into services.

TIER 4 (ORANGE): "repository" — one box listing the 5 repos (Tenant, User,
  Membership, APIKey, Usage) + "RepoError / classifySQLError". Arrow from
  services down into repository.

TIER 5 (ORANGE): "🗄️ PostgreSQL + db/migrations (raw SQL DDL)".
  Arrow from repository down into PostgreSQL.

SIDE COLUMN (PURPLE, to the right, spanning tiers): a vertical group titled
"Data-plane packages" containing: "proxy (ReverseProxy + StaticRegistry +
transform)", "ratelimit (token bucket Limiter + keys)", "retry (RoundTripper +
backoff)". Show "proxy" depending on "retry" (arrow down), and both "proxy" and
"ratelimit" reading from a small shared box "pkg/types (request context keys)".

BOTTOM-LEFT shared box (GRAY): "observability (slog logger)" and "config (env →
Config, NewDatabase)" — draw thin dotted lines from main.go to both.

CROSS-CUTTING box (small, bottom): "pkg/types — typed context keys
(ResolvedTenant, AuthenticatedUser, ResolvedMembership, ResolvedAPIKey,
PathParams)" with dotted lines to middleware, proxy, ratelimit.

Mark "Router", "proxy", "ratelimit", and the two auth middlewares with a small
🚧 "not wired yet" tag (dashed gray outline). Keep arrows tidy, no crossings.
Hand-drawn Excalidraw style, portrait or square orientation.
```

</details>

---

## Layer-by-Layer Walkthrough

### 1. Entry Point & Server

**`internal/cmd/gateway/main.go`** — the `main()` function:

1. `observability.InitLogger()` — set up the JSON slog logger.
2. `config.Load()` — read env vars into a `Config` (and validate).
3. `server.New(cfg.Port)` — build the HTTP server.
4. `srv.Start()` — `ListenAndServe`.

**`internal/server/server.go`** — `Server` wraps an `*http.Server`.
`New(addr)` creates an **empty `http.NewServeMux()`** wrapped only with
`middleware.LoggingMiddleware`. `Start()` calls `ListenAndServe()`.

> 🔴 **Critical gap:** `New()` never registers any routes and never touches the
> `Router`, database, services, handlers, proxy, or rate limiter. As written,
> the running server logs requests and returns **404 for every path**. All the
> other components exist but are unreferenced by the live process. This is the
> single biggest thing to fix to get a working gateway (see
> [What Actually Runs Today](#what-actually-runs-today)).

### 2. Configuration

**`internal/config/config.go`** — `Config{ Port, DB DBConfig }`. `Load()`
reads env with typed helpers:

- `getEnv(key, default)` — optional string with fallback.
- `mustGetEnv(key)` — **panics** if missing (used for `DB_DSN`).
- `getEnvAsInt(key, default)` — panics if not an int.
- `getEnvAsDuration(key, default)` — panics if not a Go duration (e.g. `1h`).

**`internal/config/database.go`** — `DBConfig` holds DSN, driver, pool sizing.
`NewDatabase(cfg)` opens `sql.Open(driver, dsn)` and configures
`SetMaxIdleConns` / `SetMaxOpenConns` / `SetConnMaxLifetime`.

> Note: `sql.Open` does **not** verify connectivity. There is no `db.Ping()`
> anywhere, and `NewDatabase` is never called by `main.go`.

**`internal/config/validator.go`** — `Validate(cfg)` requires `Port`, `DB.DSN`,
`DB.Driver`, and non-zero `MaxIdleConns` / `MaxOpenConns`.

### 3. Observability

**`internal/observability/logger.go`** — a package-global `*slog.Logger`.
`InitLogger()` installs a `slog.NewJSONHandler(os.Stdout, nil)`.
`Info(msg, kv...)` / `Error(msg, kv...)` are thin wrappers.

> If any `Info`/`Error` is called before `InitLogger()`, the global is `nil` and
> it will panic. `config.Validate` logs errors via `observability.Error`, but
> `main.go` does call `InitLogger()` first, so the normal path is safe.

### 4. Domain Models

Pure data structs with no behavior (`internal/models/`):

- **`Tenant`** — `Id, Name, Slug, Status` + timestamps. Enums:
  `TenantStatus` = `active|suspended`; `MembershipRole` = `owner|admin|member`;
  `MembershipStatus` = `active|invited|suspended`.
- **`User`** — `Id, Email, PasswordHash` + timestamps (global identity).
- **`TenantMembership`** — `ID, UserId, TenantId, Role, Status` + timestamps.
- **`APIKey`** — `ID, TenantID, KeyHash, Active, Description*` + timestamps.
  Stores only the hash; the raw key is shown once.
- **`Usage`** — `ID, TenantID, APIKeyID, Endpoint, Method, BytesIn, BytesOut,
  Timestamp` (metering record).
- **`UpstreamTarget`** — proxy routing config: `TenantID, Name, BaseURL,
  StripPrefix, AddPrefix, PreserveHost, Timeout, Retry, RequestTransform,
  ResponseTransform` + timestamps.
- **`RequestTransform` / `ResponseTransform`** — `AddHeaders`,
  `RemoveHeaders`, plus `RewritePath` (request side only).
- **`RetryPolicy`** — `Attempts, Delay, MaxDelay, Jitter` (per upstream).

### 5. Database & Migrations

Five raw SQL files in `internal/db/migrations/`. There is **no migration runner
in the codebase** — you apply these manually (e.g. via `psql`).

| File  | Creates           | Notes                                                            |
| ----- | ----------------- | --------------------------------------------------------------- |
| `001` | `tenants`         | Enables `pgcrypto`; defines `update_updated_at_column()` trigger fn; index on `Slug`; status CHECK is `active`/`inactive` |
| `002` | `users`           | Email unique + index; column named **`password`**               |
| `003` | `api_keys`        | FK→tenants `ON DELETE CASCADE`; `key_hash` unique               |
| `004` | `usage`           | FKs→tenants & api_keys; column named **`endpoint`**; has `created_at`/`updated_at`, **no `timestamp`** |
| `005` | `tenant_memberships` | Role/status CHECKs; unique `(user_id, tenant_id)`; both-direction indexes |

> 🔴 Several of these DDL files **do not match the SQL the repositories run**, and
> `001` has a syntax error. See [Known Issues](#known-issues--bugs-must-fix-before-it-runs).

### 6. Repository Layer

A "thin repository" pattern: repositories only execute SQL and translate errors.
Services depend on **interfaces**, not concrete types, so the storage backend is
swappable.

**`interfaces.go`** declares one interface per entity plus a `Repositories`
bundle and `NewPostgresRepositories(db)` that wires all five concrete repos.
Every interface includes `WithTx(tx *sql.Tx)` for transaction sharing.

| Interface                    | Methods                                                                 |
| ---------------------------- | ----------------------------------------------------------------------- |
| `TenantRepository`           | `WithTx, Create, GetByID, GetBySlug`                                    |
| `UserRepository`             | `WithTx, Create, GetById, GetByEmail`                                   |
| `TenantMembershipRepository` | `WithTx, Create, GetByID, GetByUserAndTenant, ListByUser, ListByTenant` |
| `APIKeyRepository`           | `WithTx, Create, GetByID, GetByHash, ListByTenant`                      |
| `UsageRepository`            | `WithTx, Log, ListByTenant`                                             |

**`error.go`** — the `RepoError{Kind, Op, Entity, Err}` type implements
`Error()/Unwrap()/Is()` so `errors.Is(err, ErrNotFound)` works by `Kind`.
`classifySQLError` maps driver errors to kinds:

| Source                    | → Kind        |
| ------------------------- | ------------- |
| `sql.ErrNoRows`           | `not_found`   |
| pq `23505` (unique)       | `conflict`    |
| pq `23502`/`22001`        | `validation`  |
| pq `23503` (FK)           | `not_found` if `fkAsNotFound`, else `conflict` |
| anything else             | `internal`    |

**`postgres_helper.go`** — the `sqlExecutor` interface (satisfied by both
`*sql.DB` and `*sql.Tx`) lets every repo run inside or outside a transaction.
Also holds `normalize*ForCreate` validators and `null*Ptr` converters.

**The five Postgres repos** (`tenant_repo`, `user_repo`, `membership`,
`apikey_repo`, `usage_repo`) each store a `sqlExecutor`, expose `WithTx` that
returns a new instance bound to the tx, and implement their CRUD via
`QueryRowContext`/`QueryContext` + `classifySQLError`.

### 7. Service Layer

The business brain. Each service is an interface + a private implementation +
a `New…` constructor, and depends on repositories and security primitives.

- **`OnboardingService.OnboardTenant`** — the flagship operation. In **one DB
  transaction** it creates a Tenant + admin User + owner Membership + first
  API key, then returns everything plus the **raw API key** (shown once).
  Uses `WithTx` so all four repos share the transaction; rolls back on any
  error. Validates tenant name/slug (slug regex `^[a-z0-9]+(?:-[a-z0-9]+)*$`),
  email, and an 8-char minimum password.
- **`AuthService.Login`** — verifies email+password, loads memberships, requires
  ≥1 **active** membership, then issues an access + refresh JWT. Returns
  generic "invalid credentials" on a missing user (no account enumeration).
- **`APIKeyAuthenticator.Authenticate`** — hashes the presented raw key with
  SHA-256, looks it up by hash, checks the key is `Active`, loads the owning
  tenant, and checks the tenant is `active`.
- **`TenantResolver.Resolve`** — verifies the access token, loads the user from
  the token subject, loads the requested tenant, checks tenant is active,
  loads the membership for (user, tenant), and checks the membership is active.
- **`JWTManager`** — hand-rolled HS256. `IssueAccessToken`/`IssueRefreshToken`,
  `RefreshAccessToken`, `VerifyAccessToken`/`VerifyRefreshToken`. Validates
  alg=`HS256`, typ=`JWT`, token-type, issuer, signature (`hmac.Equal`,
  constant-time), expiry, and a 60s clock-skew tolerance. Requires a secret
  ≥32 bytes.
- **`credentials.go`** — `StandardPasswordHasher` (PBKDF2-HMAC-SHA256, 210k
  iters, 16-byte salt, 32-byte key; format
  `pbkdf2_sha256$<iter>$<salt-b64>$<key-b64>`), `StandardAPIKeyGenerator`
  (prefix `gw_live_` + 32 random bytes, SHA-256 hash stored),
  `newUUIDString()` (RFC-4122 v4 from `crypto/rand`), and the PBKDF2 `prf`.

**Error helpers** (`errors.go`, `auth_errors.go`, `authorization_errors.go`):
`ServiceError{Kind, Op, Err}` with kinds `validation`, `conflict`, `internal`,
`unauthorized`, `forbidden`; `mapRepositoryError` converts `RepoError` →
`ServiceError`.

### 8. HTTP Handlers

Deliberately thin adapters (`internal/handlers/`):

- **`OnboardingHandler`** (`POST /onboard`) — decodes JSON (with
  `DisallowUnknownFields`), calls `OnboardTenant`, returns `201 Created` with
  the tenant/user/membership/api-key and the raw secret. Maps errors to
  400/409/500.
- **`AuthHandler`** (`POST /login`) — decodes JSON, calls `Login`, returns
  `200 OK` with user, memberships, and tokens. Maps errors to 400/401/409/500.
- **`json.go`** — `writeJSON(w, status, payload)` and
  `writeJSONError(w, status, code, message)` (envelope: `{error, message}`).

Both handlers build "safe" response DTOs that **omit the password hash** and the
API-key hash.

### 9. Middleware

- **`LoggingMiddleware`** (`logging.go`) — generates/propagates an
  `X-Request-ID`, wraps the `ResponseWriter` to capture the status code, and
  emits one structured access log line (method, path, status, duration µs).
  **This is the only middleware currently attached to the live server.**
- **`APIKeyAuthMiddleware`** (`api_key_auth_middleware.go`) — reads `X-API-Key`,
  calls `APIKeyAuthenticator.Authenticate`, and on success stores the resolved
  **tenant** and **API key** in the request context. Lets `OPTIONS` pass
  through. Maps service errors to 400/401/403/409/500.
- **`TenantResolutionMiddleware`** (`tenant_resolution_middleware.go`) — parses
  the `Authorization: Bearer <token>` header and `X-Tenant-ID`, calls
  `TenantResolver.Resolve`, and stores the resolved **user**, **tenant**, and
  **membership** in context. Lets `OPTIONS` pass through.

### 10. Router

**`internal/server/router.go`** is a from-scratch, production-style HTTP router
(~1700 lines, heavily documented inline). Design:

- **Two planes.** *Control plane* (registration) is allowed to be slow and is
  mutex-guarded; *data plane* (request handling) is lock-free.
- **Trie matching.** Routes compile into a trie of `node`s with
  `staticChildren` (O(1) map) and a single `paramChild`. Lookup is
  O(path-depth), and **static segments beat parameters**.
- **Compile once.** `rebuild()` constructs a fresh immutable trie, pre-wraps
  each handler with global+route middleware, precomputes `Allow` headers for
  405s, and atomically publishes a new `*state`.
- **Registration API.** `Handle/HandleFunc/GET/POST/PUT/PATCH/DELETE`, plus
  `Use(...)` for global middleware. Patterns like `/users/{id}` are parsed and
  validated; duplicate (method, structural-key) routes are rejected.
- **Param extraction.** Matched param values are zipped with their names and
  attached via `requesttypes.WithPathParams` for handlers to read.
- **Custom 404/405.** `SetNotFoundHandler` / `SetMethodNotAllowedHandler`.

> 🔴 This router is fully built and self-consistent but **is not instantiated
> anywhere** (`NewRouter()` is only referenced in its own doc comment). The live
> server uses a bare `http.ServeMux` instead.

<!-- IMAGE: replace this line with the generated diagram -->
![Router Trie & Two-Plane Design](docs/images/10-router-trie.png)

<details>
<summary><b>🎨 Gemini prompt — Router Trie & Control/Data Plane</b> (save as <code>docs/images/10-router-trie.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Trie Router — compile once (control plane), match fast (data plane)"

Split the canvas into TWO panels side by side with a vertical divider.

LEFT PANEL — "CONTROL PLANE (startup, mutex-guarded, slow OK)" (green tint):
  Flow of boxes with downward arrows:
  "router.GET('/users/{id}', h)" → "parsePattern() → segments
   [literal 'users'][param 'id']" → "rebuild(): build immutable trie + pre-wrap
   middleware + precompute Allow headers" → "atomic state swap (publish new
   *state)". Small note: "duplicate (method, structural-key) routes rejected".

RIGHT PANEL — "DATA PLANE (per request, lock-free, fast)" (blue tint):
  Draw an actual TRIE TREE of nodes (rounded boxes), root at top:
    root
     ├── "users" (static)
     │      ├── "settings" (static, leaf: GET /users/settings)
     │      └── "{param}"  (paramChild, leaf: GET /users/{id})
     └── "posts" (static)
            └── "{param}" (leaf: GET /posts/{id})
  Use SOLID arrows for static-child links and a DASHED arrow for paramChild
  links. Add a small legend: "static children matched FIRST (O(1) map), param
  child is the fallback".
  Then show a sample request walk: a highlighted path "GET /users/123" tracing
  root → 'users' → '{param}', with a callout "captures id=123 → request
  context (WithPathParams)".
  Add two small result chips off the matched node: "method match → run
  pre-wrapped handler", "method mismatch → 405 + precomputed Allow header",
  and a separate chip "no match → 404".

Tag the whole router with a 🚧 "NOT WIRED into main.go yet" sticky note.
Hand-drawn Excalidraw style, landscape, tidy tree with no crossing edges.
```

</details>

### 11. Reverse Proxy

**`internal/proxy/`** wraps `httputil.ReverseProxy`, one per tenant.

- **`NewHandler(registry, logger)`** — at startup, calls `registry.All()` and
  builds a `map[tenantID]*httputil.ReverseProxy`. Rejects empty/duplicate
  tenant IDs.
- **`Handler.ServeHTTP`** — reads the resolved tenant from context, looks up its
  proxy (`502` if none), and forwards. It does **not** authenticate or resolve —
  that must already have happened upstream.
- **`buildProxy(target)`** sets up the proxy's:
  - **`Director`** — `rewriteRequest` (scheme/host/path, strip+add prefix,
    optional host preservation) → `applyRequestTransform` (header add/remove,
    path rewrite) → `injectGatewayHeaders` (authoritative `X-Gateway-*` and
    `X-Tenant-ID` from context).
  - **`ModifyResponse`** — `applyResponseTransform` + `X-Gateway-Upstream`.
  - **`ErrorHandler`** — maps transport failures to `502` (or `504` on timeout),
    logs them, and ignores client cancellations.
  - **`Transport`** — a `retry.Transport` wrapping a cloned
    `http.DefaultTransport` (with `ResponseHeaderTimeout`/`ExpectContinueTimeout`
    set from `target.Timeout`). The circuit breaker transport can be layered
    between `retry.Transport` and the inner transport to gate traffic before
    retries even start.
- **`StaticRegistry`** (`registry.go`) — an in-memory, validated,
  deterministically-ordered `map[tenantID]UpstreamTarget`. `NewStaticRegistry`
  fails fast on missing tenant/base URL or duplicates. Exposes `All()` and
  `Get()`.
- **`transform.go`** — shared header add/remove + request path rewrite helpers
  (headers and path only; **bodies are not transformed**).

> The proxy is wired to take its upstreams from a `Registry`, but **no registry
> is ever constructed or populated** (`UpstreamTarget` rows have no table and no
> repository). So even once routes are wired, there are no upstreams to forward
> to until a registry is built.

### 12. Rate Limiting

**`internal/ratelimit/`** is an in-memory **token-bucket** limiter.

- **`Rule`** — `TokensPerPeriod`, `Period`, `Capacity` (burst size; defaults to
  `TokensPerPeriod`), `Cost` (tokens per request; defaults to 1).
- **`Limiter`** — a `sync.Map` of per-key `bucket`s, each with its own mutex.
  `Allow(key, rule)` validates+normalizes the rule, lazily creates a full
  bucket, refills based on elapsed time, and either consumes `Cost` tokens
  (allow) or computes a `Retry-After` wait (deny). `PruneIdle(maxIdle)` evicts
  stale buckets (must be called manually).
- **`NewMiddleware(limiter, rule, keyFunc)`** — standard middleware that builds
  a key, checks `Allow`, and returns **`429 Too Many Requests`** with a
  `Retry-After` header when over limit. Defaults `keyFunc` to `KeyRoute`.
- **`keys.go`** — `KeyFunc` builders: `KeyTenant`, `KeyUser`, `KeyAPIKey`,
  `KeyRoute` (`METHOD path`), and combos `KeyTenantRoute`, `KeyTenantUser`,
  `KeyTenantAPIKey`. These read resolved identity from context, so they must run
  **after** an auth/resolution middleware.

> Buckets live in process memory, so limits are **per-instance**, not shared
> across replicas. Not wired into the server yet.

<!-- IMAGE: replace this line with the generated diagram -->
![Token-Bucket Rate Limiter](docs/images/11-token-bucket.png)

<details>
<summary><b>🎨 Gemini prompt — Token-Bucket Rate Limiter</b> (save as <code>docs/images/11-token-bucket.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Token-Bucket Rate Limiting (per-identity buckets)"

Draw a clear hand-drawn METAPHOR plus the decision logic.

TOP — the metaphor: a bucket icon being filled from above by a tap labeled
"refill: TokensPerPeriod / Period  (e.g. +100 tokens / minute = +1.667 / sec)".
The bucket has a max-line labeled "Capacity (max burst, e.g. 200)". A request
icon 📨 draws tokens OUT from the bottom labeled "each request costs `Cost`
tokens (default 1)". Show the bucket "starts FULL" with a small note.

MIDDLE — multiple buckets: draw 3 separate buckets side by side, each labeled
with a different key: "tenant:acme", "tenant:globex", "api_key:key-789".
Caption: "Limiter = sync.Map of independent buckets, one per key. Keys built by
KeyTenant / KeyUser / KeyAPIKey / KeyRoute / combos."

BOTTOM — decision flow (small flowchart): "request arrives" →
diamond "tokens ≥ cost?":
   • YES (green arrow) → "consume tokens → ✅ allow → next handler"
   • NO  (red arrow)   → "compute wait → ❌ 429 Too Many Requests + Retry-After
     header"
Add a small note box: "refill is lazy/time-based on each request; PruneIdle()
evicts stale buckets (must be called manually)" and a 🚧 tag "limits are
per-process (not shared across replicas); not wired yet".

Hand-drawn Excalidraw style, friendly icons, landscape.
```

</details>

### 13. Retry Transport

**`internal/retry/`** is an `http.RoundTripper` that retries safe upstream
failures, used by the proxy's transport.

- **`Policy`** — `Attempts` (incl. first try; default 3), `Delay` (base; 50ms),
  `MaxDelay` (1s), `Jitter` (0), `Methods` (default `GET/HEAD/OPTIONS/PUT/DELETE`
  — **POST/PATCH excluded**), `StatusCodes` (default `502/503/504`).
  `normalizePolicy` fills defaults and rejects invalid values
  (negative jitter, out-of-range status).
- **`Transport.RoundTrip`** — clones the request per attempt; retries only when
  the method is allowed **and** the body is replayable; retries on retryable
  status codes or retryable network errors (timeouts/temporary); honors context
  cancellation while waiting (`waitWithContext`); drains+closes bodies between
  attempts.
- **Backoff** — `nextBackoff` = `2^(attempt-1) * Delay`, capped at `MaxDelay`,
  plus up to `Jitter` random extra (mutex-protected RNG, seeded from
  `time.Now().UnixNano()`).

> ⚠️ `bodyReplayable` returns `true` only when `req.GetBody` is set or there's no
> body. There is also a **stub `retryableBody(req) → false`** that is currently
> unused. Net effect: only requests with `GetBody` (or no body) are ever retried
> — which is the safe default.

<!-- IMAGE: replace this line with the generated diagram -->
![Retry Transport — Backoff Timeline](docs/images/12-retry-backoff.png)

<details>
<summary><b>🎨 Gemini prompt — Retry Transport & Exponential Backoff</b> (save as <code>docs/images/12-retry-backoff.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Retry Transport — exponential backoff + jitter (safe retries only)"

TWO parts stacked vertically.

PART 1 — "Should we even retry?" decision gate (top): a small flow with two
diamonds before any attempt:
  diamond "method in {GET,HEAD,OPTIONS,PUT,DELETE}?"  (note: "POST/PATCH
  excluded by default") → NO → red chip "single attempt only".
  diamond "body replayable? (no body OR req.GetBody set)" → NO → red chip
  "single attempt only".
  YES on both → green arrow into PART 2.

PART 2 — a horizontal TIMELINE of attempts (left → right) with base=100ms,
max=1s, showing exponential backoff waits between attempts:
  [Attempt 1] --(timeout ✗)--> wait ≈ 100ms (2^0·base)
  [Attempt 2] --(502 ✗)------> wait ≈ 200ms (2^1·base)
  [Attempt 3] --(timeout ✗)--> wait ≈ 400ms (2^2·base)
  [Attempt 4] --(200 ✓)------> ✅ return response
  Each "wait" gap is a rounded bracket labeled with the formula
  "2^(attempt-1) × Delay, capped at MaxDelay, + random(0..Jitter)".
  Draw the waits as growing-width gaps to visually convey doubling.

Add side notes (small sticky chips):
  - "retry triggers: status ∈ {502,503,504} OR timeout / temporary net error"
  - "🛑 context canceled / deadline exceeded → stop immediately (client gave up)"
  - "between retries: drain + close response body (no connection leak)"
  - "jitter spreads concurrent retries → avoids thundering-herd"
  - footer: "used as the proxy's http.RoundTripper Transport".

Hand-drawn Excalidraw style, clear left-to-right timeline, growing gaps.
```

</details>

### 14. Circuit Breaker

**`internal/circuit_breaker/`** protects upstream services from repeated failures
by stopping traffic when a backend is unhealthy.

- **`CircuitBreakerPolicy`** (`models/circuit_breaker.go`) — the public
  configuration type: `FailureThreshold` (consecutive failures before opening),
  `OpenDuration` (how long the circuit stays open), `ProbeLimit` (max concurrent
  probe requests during half-open), `SuccessThreshold` (probes needed to close),
  `FailureStatusCodes` (HTTP codes that count as failures; default
  `500/502/503/504`).
- **State machine** (`State`: `closed` → `open` → `half_open` → `closed`):
  - **CLOSED** — normal operation; all traffic flows. A success resets the
    consecutive-failure counter.
  - **OPEN** — upstream is unhealthy; all requests are rejected immediately
    with an `OpenError` (carries `RetryAfter`, `Target`). Transitions to
    `HALF_OPEN` once `OpenDuration` elapses.
  - **HALF_OPEN** — recovery testing; only `ProbeLimit` concurrent probes are
    allowed. A probe failure re-opens the circuit; `SuccessThreshold` successful
    probes close it.
- **`Breaker`** — the state machine. `New(policy)` validates and normalizes the
  policy once at startup. `Allow(now)` (lock-free fast path) returns
  `(allowed bool, retryAfter duration)`. `ReportSuccess(now)` /
  `ReportFailure(now)` update state based on results. `State()` returns current
  state for monitoring.
- **`Transport`** (`transport.go`) — wraps any `http.RoundTripper`. On every
  `RoundTrip`: calls `Allow` first — if denied returns an `OpenError`;
  otherwise forwards to the inner transport and calls `ReportFailure` /
  `ReportSuccess` depending on the outcome and the configured failure status
  codes. Client cancellations and deadline-exceeded errors do **not** count as
  failures (`isFailureError` excludes `context.Canceled` /
  `context.DeadlineExceeded`).
- **Helpers** — `IsOpenError(err) bool` (for proxy error handler to return
  `503`), `RetryAfterDuration(err) (duration, ok)` (to set the `Retry-After`
  header).

The circuit breaker sits at the transport layer, **between** `retry.Transport`
and the actual upstream — giving it the most granular view of failures.

> The circuit breaker package is fully built and compiles cleanly. Like the retry
> transport, it is reachable only via the proxy, which is itself not yet wired
> into `main.go`.

<!-- IMAGE: replace this line with the generated diagram -->
![Circuit Breaker State Machine](docs/images/14-circuit-breaker.png)

<details>
<summary><b>🎨 Gemini prompt — Circuit Breaker State Machine</b> (save as <code>docs/images/14-circuit-breaker.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Circuit Breaker — CLOSED / OPEN / HALF-OPEN state machine"

Draw a classic state-machine diagram with three large rounded state boxes:

1) GREEN "⚡ CLOSED" (left) — "normal: all traffic flows".
2) RED "🚫 OPEN" (top-right) — "unhealthy: all requests rejected instantly".
3) YELLOW "🔍 HALF-OPEN" (bottom-right) — "recovery testing: only ProbeLimit probes allowed".

TRANSITIONS (solid arrows with labels):
- CLOSED → OPEN: "consecutiveFailures ≥ FailureThreshold  (e.g. 5)"
- OPEN → HALF-OPEN: "OpenDuration elapsed  (e.g. 10s)"
- HALF-OPEN → CLOSED: "successfulProbes ≥ SuccessThreshold  AND  no probes in-flight"
- HALF-OPEN → OPEN: "any probe fails"

Below the state machine draw a small transport flow:
  Request → breaker.Allow()
    ├─ OPEN  → OpenError{RetryAfter, Target} (no upstream call)  → HTTP 503
    └─ CLOSED / HALF-OPEN allowed → next.RoundTrip()
         ├─ error (net, timeout) → ReportFailure()
         ├─ status ∈ FailureStatusCodes (500/502/503/504) → ReportFailure()
         └─ success → ReportSuccess()

Add a small note: "client cancel / deadline exceeded → NOT counted as failure (isFailureError skips context errors)".

Hand-drawn Excalidraw style, clear state boxes, labeled arrows, portrait or square.
```

</details>

### 15. Request Context

**`internal/pkg/types/`** centralizes typed context keys so identity flows
through the pipeline without globals:

- **`request_context.go`** — `WithAuthenticatedUser`/`…FromContext`,
  `WithResolvedTenant`, `WithResolvedMembership`, `WithResolvedAPIKey` (and their
  getters). Uses an unexported `contextKey` type to avoid collisions.
- **`path_params.go`** — `WithPathParams`, `PathParamsFromContext`, and
  `PathParam(ctx, name)` (defensively copies the map in and out).

---

## Function Reference

A condensed map of the most important exported functions/types by package.

### `config`
- `Load() (*Config, error)` — env → Config, then `Validate`.
- `NewDatabase(*Config) (*sql.DB, error)` — open + configure pool.
- `Validate(*Config) error` — required-field checks.

### `observability`
- `InitLogger()`, `Info(msg, kv...)`, `Error(msg, kv...)`.

### `server`
- `New(addr string) *Server`, `(*Server).Start() error`.
- `NewRouter() *Router`; `(*Router).GET/POST/PUT/PATCH/DELETE/Handle/HandleFunc/Use`;
  `SetNotFoundHandler`, `SetMethodNotAllowedHandler`, `ServeHTTP`.

### `repository`
- `NewPostgresRepositories(*sql.DB) Repositories`.
- Per entity: `Create`, `GetBy…`, `ListBy…`, `WithTx`, plus `Usage.Log`.
- `classifySQLError(op, entity, err, fkAsNotFound) error`.

### `services`
- `NewOnboardingService(db, repos) OnboardingService` → `OnboardTenant`.
- `NewAuthService(repos, tokenService) AuthService` → `Login`.
- `NewAPIKeyAuthService(repos) APIKeyAuthenticator` → `Authenticate`.
- `NewTenantResolver(repos, tokenService) TenantResolver` → `Resolve`.
- `NewJWTManager(secret, issuer, accessTTL, refreshTTL) (*JWTManager, error)`
  → `IssueAccessToken`, `IssueRefreshToken`, `RefreshAccessToken`,
  `VerifyAccessToken`, `VerifyRefreshToken`.
- `NewStandardPasswordHasher()` → `Hash`, `Verify`.
- `NewStandardAPIKeyGenerator()` → `Generate() (raw, hash, err)`.

### `handlers`
- `NewOnboardingHandler(svc)` / `NewAuthHandler(svc)` → `ServeHTTP`.

### `middleware`
- `LoggingMiddleware(next) http.Handler`.
- `NewAPIKeyAuthMiddleware(next, authenticator) http.Handler`.
- `NewTenantResolutionMiddleware(next, resolver) http.Handler`.

### `proxy`
- `NewHandler(reg Registry, logger) (*Handler, error)` → `ServeHTTP`.
- `NewStaticRegistry([]UpstreamTarget) (*StaticRegistry, error)` → `All`, `Get`.

### `ratelimit`
- `NewLimiter()` / `NewLimiterWithClock(clock)` → `Allow`, `PruneIdle`.
- `NewMiddleware(limiter, rule, keyFunc) (func(http.Handler) http.Handler, error)`.
- Key builders: `KeyTenant/KeyUser/KeyAPIKey/KeyRoute/KeyTenantRoute/KeyTenantUser/KeyTenantAPIKey`.

### `retry`
- `NewTransport(next http.RoundTripper, policy Policy) (*Transport, error)` → `RoundTrip`.

### `circuit_breaker`
- `New(policy models.CircuitBreakerPolicy) (*Breaker, error)` — normalize policy + start CLOSED.
- `(*Breaker).Allow(now time.Time) (bool, time.Duration)` — gate a request; returns `retryAfter` when denied.
- `(*Breaker).ReportSuccess(now time.Time)` / `ReportFailure(now time.Time)` — feed back result.
- `(*Breaker).State() State` — read current state (`StateClosed / StateOpen / StateHalfOpen`).
- `NewTransport(next http.RoundTripper, breaker *Breaker, target string) (*Transport, error)` → `RoundTrip`.
- `IsOpenError(err) bool`, `RetryAfterDuration(err) (time.Duration, bool)` — helpers for the proxy error handler.

---

## Data Model & Relationships

```
            ┌──────────────┐                ┌──────────────┐
            │    users     │                │   tenants    │
            │──────────────│                │──────────────│
            │ id (uuid) PK │                │ id (uuid) PK │
            │ email UNIQUE │                │ name         │
            │ password*    │                │ slug UNIQUE  │
            └──────┬───────┘                │ status       │
                   │                        └──────┬───────┘
                   │   ┌────────────────────────┐  │
                   │   │  tenant_memberships     │  │
                   └──►│─────────────────────────│◄─┘
                       │ id PK                    │
                       │ user_id  FK → users      │
                       │ tenant_id FK → tenants   │   UNIQUE(user_id, tenant_id)
                       │ role  (owner/admin/member)│
                       │ status(active/invited/…) │
                       └──────────────────────────┘
                                                   ▲
            ┌──────────────┐                       │ (tenant_id FK)
            │   api_keys    │───────────────────────┘
            │──────────────│
            │ id PK        │       ┌──────────────────────────────┐
            │ tenant_id FK │──┐    │            usage              │
            │ key_hash UQ  │  │    │──────────────────────────────│
            │ active       │  └───►│ id PK                          │
            └──────────────┘       │ tenant_id FK → tenants         │
                                   │ api_key_id FK → api_keys       │
       UpstreamTarget (in-memory   │ endpoint*, method, bytes_in/out│
       model only — no table yet)  └────────────────────────────────┘
```

<!-- IMAGE: replace this line with the generated diagram -->
![Data Model / ERD](docs/images/03-data-model-erd.png)

<details>
<summary><b>🎨 Gemini prompt — Data Model (ERD)</b> (save as <code>docs/images/03-data-model-erd.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Database Schema & Relationships (Entity-Relationship Diagram)"

Draw a hand-drawn ERD. Each table is a rounded "card" with a bold title bar and
a list of fields; mark PK (primary key) and FK (foreign key) on the relevant
fields. Use ORANGE fills for tables. Use crow's-foot notation on the connector
lines to show cardinality, and label each relationship.

TABLES:

1) "users" (top-left)
   - id : UUID (PK)
   - email : UNIQUE
   - password_hash   ← add a small RED tag: "⚠️ migration calls this 'password'"

2) "tenants" (top-right)
   - id : UUID (PK)
   - name
   - slug : UNIQUE
   - status (active / suspended)  ← small RED tag: "⚠️ migration CHECK says
     active/inactive"

3) "tenant_memberships" (center, between users and tenants)
   - id : UUID (PK)
   - user_id : UUID (FK → users.id)
   - tenant_id : UUID (FK → tenants.id)
   - role (owner / admin / member)
   - status (active / invited / suspended)
   - UNIQUE(user_id, tenant_id)
   Connect users —< tenant_memberships >— tenants (many-to-many through the
   join table). Label both connectors "1 .. many" and note "ON DELETE CASCADE".

4) "api_keys" (bottom-left)
   - id : UUID (PK)
   - tenant_id : UUID (FK → tenants.id)
   - key_hash : UNIQUE (SHA-256)
   - active : bool
   Connect tenants —< api_keys (one tenant has many keys), "ON DELETE CASCADE".

5) "usage" (bottom-right)
   - id : UUID (PK)
   - tenant_id : UUID (FK → tenants.id)
   - api_key_id : UUID (FK → api_keys.id)
   - endpoint, method, bytes_in, bytes_out
   Connect tenants —< usage and api_keys —< usage.

6) "UpstreamTarget" (off to the side, drawn with a DASHED gray outline and a
   🚧 tag "in-memory Go struct only — NO database table / repository yet").
   Fields: tenant_id, base_url, strip_prefix, add_prefix, preserve_host,
   timeout, retry policy, request/response transforms. No connectors to the DB.

Put a small RED legend box: "⚠️ red tags = column/value mismatches between the
SQL migrations and the repository code (see Known Issues)."

Clean spacing, crow's-foot cardinality, hand-drawn Excalidraw ERD style.
```

</details>

- A **user** ↔ many **tenants** through **tenant_memberships** (many-to-many).
- A **tenant** has many **api_keys** and many **usage** rows (one-to-many,
  `ON DELETE CASCADE`).
- **UpstreamTarget** exists only as a Go struct + in-memory `StaticRegistry`;
  there is no SQL table or repository for it.
- (`*` marks columns that currently mismatch the repository code — see
  [Known Issues](#known-issues--bugs-must-fix-before-it-runs).)

---

## User Flows

### Flow A — Tenant Onboarding (control plane)

```
Client                OnboardingHandler         OnboardingService             Postgres (1 TX)
  │  POST /onboard          │                          │                            │
  │  {tenant_name, slug,    │                          │                            │
  │   admin_email,          │                          │                            │
  │   admin_password,       │                          │                            │
  │   api_key_label?}       │                          │                            │
  │────────────────────────►│ decode + validate JSON   │                            │
  │                         │─────────────────────────►│ normalize+validate input   │
  │                         │                          │ BEGIN TX                    │
  │                         │                          │ tenants.Create ────────────►│
  │                         │                          │ hash password (PBKDF2)      │
  │                         │                          │ users.Create  ─────────────►│
  │                         │                          │ memberships.Create(owner)──►│
  │                         │                          │ generate api key (raw+hash) │
  │                         │                          │ api_keys.Create ───────────►│
  │                         │                          │ COMMIT                      │
  │  201 {tenant, user,     │◄─────────────────────────│ OnboardingResult            │
  │   membership, api_key,  │                          │ (incl. RAW api key once)    │
  │   api_key_secret_raw}   │                          │                            │
  │◄────────────────────────│                          │                            │
```

<!-- IMAGE: replace this line with the generated diagram -->
![Flow A — Tenant Onboarding](docs/images/04-flow-onboarding.png)

<details>
<summary><b>🎨 Gemini prompt — Flow A: Tenant Onboarding (sequence)</b> (save as <code>docs/images/04-flow-onboarding.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Flow A — Tenant Onboarding (atomic, one DB transaction)"

Draw a hand-drawn SEQUENCE diagram with 4 vertical lifelines (labeled columns),
left to right:
  1) 🌐 "Client"            (blue)
  2) "OnboardingHandler"    (green)
  3) "OnboardingService"    (yellow)
  4) 🗄️ "PostgreSQL"        (orange)

Show solid arrows (with arrowheads + short labels) between lifelines, top to
bottom, in this order:

A. Client → Handler:  "POST /onboard {tenant_name, slug, admin_email,
   admin_password, api_key_label?}"
B. Handler:           self-loop note "decode JSON (DisallowUnknownFields)"
C. Handler → Service: "OnboardTenant(req)"
D. Service:           self-loop note "normalize + validate (slug regex, email,
   password ≥ 8)"

Then wrap the next group in a labeled rounded box spanning lifelines 3–4 titled
"BEGIN TRANSACTION (all-or-nothing)":
E. Service → Postgres: "INSERT tenant"
F. Service:            self-loop "hash password (PBKDF2-HMAC-SHA256, 210k iters)"
G. Service → Postgres: "INSERT user"
H. Service → Postgres: "INSERT membership (role=owner, status=active)"
I. Service:            self-loop "generate API key (raw + SHA-256 hash)"
J. Service → Postgres: "INSERT api_key"
K. label at bottom of the box: "COMMIT ✅  (any error → ROLLBACK, nothing saved)"

Return arrows (dashed) back up:
L. Service → Handler (dashed): "OnboardingResult (incl. RAW api key — shown ONCE)"
M. Handler → Client (dashed):  "201 Created {tenant, user, membership, api_key,
   api_key_secret_raw 🔑}"

Add a small RED sticky note pointing at the transaction box:
"🚧 currently blocked by SQL/schema mismatches — see Known Issues".

Hand-drawn Excalidraw sequence-diagram style, clear vertical time order.
```

</details>

If any step fails, the deferred `tx.Rollback()` undoes everything — onboarding
is **atomic**.

### Flow B — Human Login (control plane)

```
Client → AuthHandler → AuthService.Login → repos + JWTManager
  POST /login {email, password}
    → load user by email      (generic 401 if missing → no enumeration)
    → verify password (PBKDF2, constant-time)
    → load memberships, require ≥1 ACTIVE
    → issue access + refresh JWT (HS256)
  200 {user, memberships, access_token, refresh_token, …expires_at}
```

<!-- IMAGE: replace this line with the generated diagram -->
![Flow B — Human Login](docs/images/05-flow-login.png)

<details>
<summary><b>🎨 Gemini prompt — Flow B: Human Login (sequence)</b> (save as <code>docs/images/05-flow-login.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Flow B — Human Login (issue JWT access + refresh tokens)"

Hand-drawn SEQUENCE diagram with 5 vertical lifelines, left to right:
  1) 🌐 "Client" (blue)
  2) "AuthHandler" (green)
  3) "AuthService" (yellow)
  4) "UserRepo + MembershipRepo" (orange)
  5) 🪪 "JWTManager (HS256)" (yellow)

Arrows top → bottom with labels:
A. Client → AuthHandler: "POST /login {email, password}"
B. AuthHandler → AuthService: "Login(req)"
C. AuthService → Repo: "GetByEmail(email)"
   - dashed return: "user (or not found)"
   - RED side-note: "if user missing → generic 401 'invalid credentials'
     (NO account enumeration)"
D. AuthService self-loop: "🔒 verify password (PBKDF2, constant-time hmac.Equal)"
E. AuthService → Repo: "ListByUser(userID)"
   - dashed return: "memberships"
   - note: "require ≥ 1 ACTIVE membership, else 401"
F. AuthService → JWTManager: "IssueAccessToken(user)"  → dashed return "access JWT + expiry"
G. AuthService → JWTManager: "IssueRefreshToken(user)" → dashed return "refresh JWT + expiry"
H. AuthService → AuthHandler (dashed): "LoginResult"
I. AuthHandler → Client (dashed): "200 OK {user, memberships, access_token,
   refresh_token, expires_at...}"

Add a small 🪪 callout near JWTManager listing token claims: "sub, email, typ,
iss, jti, iat, exp — signed HS256, secret ≥ 32 bytes, 60s clock-skew".

Hand-drawn Excalidraw sequence-diagram style.
```

</details>

### Flow C — Machine Request (data plane, intended)

```
Client (X-API-Key: gw_live_…)
  → APIKeyAuthMiddleware → Authenticate (SHA-256 hash → lookup → active? tenant active?)
      → context: ResolvedTenant, ResolvedAPIKey
  → RateLimit MW (e.g. KeyTenantAPIKey) → 429 if over budget
  → Proxy Handler → proxies[tenant.Id] → RetryTransport → Upstream
      → injects X-Gateway-* headers, strips/adds path prefixes
  ← upstream response (ModifyResponse: header transform + X-Gateway-Upstream)
```

<!-- IMAGE: replace this line with the generated diagram -->
![Flow C — Machine Request via API Key](docs/images/06-flow-machine-request.png)

<details>
<summary><b>🎨 Gemini prompt — Flow C: Machine Request (API key → proxy)</b> (save as <code>docs/images/06-flow-machine-request.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Flow C — Machine Request (authenticate with X-API-Key, then proxy)"

Hand-drawn LEFT-TO-RIGHT pipeline (horizontal swimlane of stages). Each stage is
a rounded box; solid arrows connect them with labels. Show where the request can
be REJECTED with small RED branch arrows going downward to red "reject" chips.

STAGES (left → right):
1) 🌐 "Client"  — arrow labeled "GET /orders  •  header: 🔑 X-API-Key: gw_live_…"
2) 🔒 GREEN "APIKeyAuth Middleware"
     - internal note: "SHA-256 hash the key → GetByHash → key.Active? →
       tenant.Active?"
     - RED downward branch: "401 invalid key / 403 inactive"
     - on success, note: "context += ResolvedTenant 🏢, ResolvedAPIKey 🔑"
3) 🚦 GREEN "RateLimit Middleware (key = tenant|api_key, token bucket)"
     - RED downward branch: "429 Too Many Requests + Retry-After"
4) ➡️ PURPLE "Proxy Handler → proxies[tenant.Id]"
     - RED downward branch: "502 if no upstream configured for tenant"
5) PURPLE "ReverseProxy.Director"
     - bullet notes inside: "rewrite scheme/host/path (strip+add prefix)",
       "apply request transform (add/remove headers)",
       "inject 🪪 X-Gateway-Tenant-ID / -API-Key-ID / X-Tenant-ID"
6) 🔁 PURPLE "RetryTransport (backoff + jitter, safe methods only)"
7) 🟪 "Upstream Service (api.acme.internal)"  — solid arrow in, then a dashed
   RESPONSE arrow back leftwards labeled "response → ModifyResponse: header
   transform + X-Gateway-Upstream → Client"

Draw the whole pipeline inside a faint dashed container tagged
"🚧 INTENDED data-plane flow — not wired into main.go yet".

Hand-drawn Excalidraw style, horizontal reading order, tidy reject branches.
```

</details>

### Flow D — Human-Acting-On-Tenant Request (data plane, intended)

```
Client (Authorization: Bearer <jwt>, X-Tenant-ID: <id>)
  → TenantResolutionMiddleware → Resolve
      (verify token → load user → load tenant(active) → membership(active))
      → context: AuthenticatedUser, ResolvedTenant, ResolvedMembership
  → RateLimit MW (e.g. KeyTenantUser)
  → Proxy Handler → Upstream
```

<!-- IMAGE: replace this line with the generated diagram -->
![Flow D — Human-Acting-On-Tenant Request](docs/images/07-flow-human-request.png)

<details>
<summary><b>🎨 Gemini prompt — Flow D: Human request (Bearer JWT + X-Tenant-ID → proxy)</b> (save as <code>docs/images/07-flow-human-request.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "Flow D — Human Acting On a Tenant (verify JWT + membership, then proxy)"

Hand-drawn LEFT-TO-RIGHT pipeline, same visual language as Flow C, with RED
downward reject branches.

STAGES (left → right):
1) 🌐 "Client"
   arrow label (two lines): "GET /orders
   headers: 🪪 Authorization: Bearer <jwt>  •  🏢 X-Tenant-ID: <tenant id>"
2) 🔒 GREEN "TenantResolution Middleware"
   internal numbered notes:
     "1. parse Bearer token + read X-Tenant-ID"
     "2. JWTManager.VerifyAccessToken (alg HS256, exp, issuer, signature)"
     "3. load user from token.sub"
     "4. load tenant → must be ACTIVE"
     "5. load membership(user,tenant) → must be ACTIVE"
   RED downward branches: "401 invalid/expired token", "400 missing X-Tenant-ID",
     "403 tenant/membership not active or access denied"
   success note: "context += 👤 AuthenticatedUser, 🏢 ResolvedTenant,
     ResolvedMembership(role)"
3) 🚦 GREEN "RateLimit Middleware (key = tenant|user)"
   RED branch: "429 + Retry-After"
4) ➡️ PURPLE "Proxy Handler → proxies[tenant.Id]"  (RED branch: "502 no upstream")
5) PURPLE "ReverseProxy.Director — inject 🪪 X-Gateway-User-ID / -User-Email /
   -Membership-Role / -Tenant-ID"
6) 🔁 PURPLE "RetryTransport"
7) 🟪 "Upstream Service" with a dashed return arrow:
   "response → ModifyResponse (header transform + X-Gateway-Upstream) → Client"

Wrap everything in a faint dashed container tagged "🚧 INTENDED flow — not wired
yet". Contrast note vs Flow C: "Flow C authenticates a MACHINE (API key); Flow D
authenticates a HUMAN (JWT) acting under a chosen tenant."

Hand-drawn Excalidraw style, horizontal order, clean reject branches.
```

</details>

> Flows C and D are the **intended** request lifecycle. They are not reachable
> today because the middleware/proxy/router are not wired into `main.go`.

---

## Data Flow (Request Lifecycle)

The fully-assembled request path the components are built for:

```
  HTTP request
      │
      ▼
  http.Server ── LoggingMiddleware  (assign X-Request-ID, time the request)
      │
      ▼
  Router.ServeHTTP ── trie match → pre-wrapped handler chain
      │
      ├── control-plane routes (no auth):  /onboard, /login
      │
      └── data-plane routes:
            │
            ▼
        Auth/Resolution middleware  ── put identity in request context
            │  (X-API-Key → tenant+key)   OR   (Bearer + X-Tenant-ID → user+tenant+membership)
            ▼
        Rate-limit middleware  ── token bucket by key → 429 + Retry-After if over
            │
            ▼
        Proxy Handler  ── proxies[tenant.Id]
            │
            ▼
        ReverseProxy.Director:
            rewriteRequest (scheme/host/path, strip+add prefix)
            applyRequestTransform (headers, path)
            injectGatewayHeaders (X-Gateway-Tenant-ID, -User-ID, -Membership-Role, …)
            │
            ▼
        retry.Transport.RoundTrip  ── attempt → (retryable? backoff+jitter, replay) → upstream
            │
            ▼
        Upstream service
            │
            ▼
        ReverseProxy.ModifyResponse: applyResponseTransform + X-Gateway-Upstream
            │
            ▼
        Client response   (ErrorHandler → 502/504 on upstream failure)
```

<!-- IMAGE: replace this line with the generated diagram -->
![Request Lifecycle / Data Flow](docs/images/08-request-lifecycle.png)

<details>
<summary><b>🎨 Gemini prompt — Request Lifecycle (end-to-end data flow)</b> (save as <code>docs/images/08-request-lifecycle.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "End-to-End Request Lifecycle (fully-assembled gateway)"

Draw ONE tall TOP-TO-BOTTOM flow of rounded stage boxes connected by solid
downward arrows (the request path), plus a parallel UPWARD dashed arrow on the
right for the response path. Title banner: "Request Lifecycle — the path the
components are built for".

DOWNWARD request stages (top → bottom):
1) 🌐 blue "HTTP request"
2) GREEN "http.Server → LoggingMiddleware" (note: "assign X-Request-ID, start
   timer")
3) GREEN "Router.ServeHTTP" (note: "trie match → pre-wrapped handler chain")
4) A FORK into two labeled branches:
   • LEFT branch GREEN "Control-plane routes (no auth): POST /onboard, POST
     /login" → arrow to yellow "Handlers → Services → Repos → 🗄️ DB" → response.
   • RIGHT branch (the data plane) continues downward:
5) GREEN "Auth / Resolution Middleware"
   note: "X-API-Key → tenant+key   OR   Bearer + X-Tenant-ID → user+tenant+
   membership; identity stored in request context"
6) GREEN "🚦 Rate-limit Middleware" (note: "token bucket by key → 429 +
   Retry-After if over budget") with a small RED side-exit "429".
7) PURPLE "➡️ Proxy Handler → proxies[tenant.Id]" with RED side-exit "502 no
   upstream".
8) PURPLE "ReverseProxy.Director" listing: "rewriteRequest (scheme/host/path,
   strip+add prefix)", "applyRequestTransform (headers/path)",
   "injectGatewayHeaders (🪪 X-Gateway-Tenant-ID / -User-ID / -Membership-Role)".
9) 🔁 PURPLE "retry.Transport.RoundTrip" (note: "attempt → retryable? backoff +
   jitter, replay body → upstream").
10) 🟪 "Upstream Service".

UPWARD response path (dashed, on the right side, bottom → top):
- from Upstream: "ReverseProxy.ModifyResponse (applyResponseTransform +
  X-Gateway-Upstream)"
- then up to "Client response".
- a RED dashed branch from RoundTrip/Upstream: "ErrorHandler → 502 / 504 on
  upstream failure or timeout".

Put a 🚧 banner across the data-plane stages: "built but NOT wired into main.go
yet". Keep the column tidy and vertical; hand-drawn Excalidraw style, portrait
orientation.
```

</details>

---

## Error Handling Pipeline

A disciplined 3-tier mapping keeps internal details from leaking:

```
   PostgreSQL / driver error
            │  classifySQLError()
            ▼
   RepoError{ Kind: not_found | conflict | validation | internal }
            │  mapRepositoryError()
            ▼
   ServiceError{ Kind: validation | conflict | internal | unauthorized | forbidden }
            │  handler/middleware switch on errors.Is(err, services.Err*)
            ▼
   HTTP status + JSON envelope { "error": <code>, "message": <text> }
```

<!-- IMAGE: replace this line with the generated diagram -->
![Error Handling Pipeline](docs/images/09-error-pipeline.png)

<details>
<summary><b>🎨 Gemini prompt — 3-Tier Error Pipeline</b> (save as <code>docs/images/09-error-pipeline.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "3-Tier Error Translation Pipeline (no internal details leak to client)"

Draw a TOP-TO-BOTTOM funnel of 4 layers connected by solid downward arrows, each
arrow labeled with the function that performs the translation. Title banner:
"Errors get classified, never leaked".

LAYER 1 (orange, top): "🗄️ PostgreSQL / driver error"
  e.g. sample chips: "sql.ErrNoRows", "pq 23505 unique", "pq 23502 not-null",
  "pq 23503 FK".
  Arrow down labeled "classifySQLError()".

LAYER 2 (orange): "RepoError { Kind }" with four small chips:
  "not_found", "conflict", "validation", "internal".
  Arrow down labeled "mapRepositoryError()".

LAYER 3 (yellow): "ServiceError { Kind }" with chips:
  "validation", "conflict", "internal", "unauthorized", "forbidden".
  Arrow down labeled "handler / middleware switch on errors.Is(err, services.Err*)".

LAYER 4 (green, bottom): a MAPPING TABLE drawn as a tidy 2-column hand-drawn
grid titled "HTTP status + JSON { error, message }":
  validation   → 400 Bad Request
  unauthorized → 401 Unauthorized
  forbidden    → 403 Forbidden
  conflict     → 409 Conflict
  internal/else→ 500 Internal Server Error

To the RIGHT, a separate RED highlighted callout box with a 🔒 icon:
"Security note: login collapses 'no such user' AND 'wrong password' into ONE
generic 401 — prevents account enumeration."

Use a subtle 'funnel / narrowing' visual so it reads as raw → stable → safe.
Hand-drawn Excalidraw style, portrait orientation.
```

</details>

| ServiceError kind | HTTP status                 |
| ----------------- | --------------------------- |
| `validation`      | `400 Bad Request`           |
| `unauthorized`    | `401 Unauthorized`          |
| `forbidden`       | `403 Forbidden`             |
| `conflict`        | `409 Conflict`              |
| `internal` / else | `500 Internal Server Error` |

Login intentionally collapses "no such user" and "wrong password" into one
generic `401` to prevent account enumeration.

---

## Setup & Installation

### Prerequisites

- **Go** 1.25+ (a 1.26 toolchain works).
- **PostgreSQL** 13+ (needs the `pgcrypto` extension, available by default).
- `psql` or any client to run the migrations (there is no migration runner).

### 1. Clone & fetch deps

```bash
git clone <your-fork-url> api-gateway
cd api-gateway
go mod download
```

### 2. Create the database & apply migrations

> ⚠️ The migration files currently have bugs (see
> [Known Issues](#known-issues--bugs-must-fix-before-it-runs)) — in particular
> `001` has a trailing-comma syntax error, and columns in `002`/`004` don't
> match the repository SQL. Fix those before (or while) applying them.

```bash
createdb api_gateway     # or: CREATE DATABASE api_gateway;

# apply in order (001 → 005)
for f in internal/db/migrations/0*.sql; do
  echo "applying $f"
  psql "postgres://user:pass@localhost:5432/api_gateway?sslmode=disable" -f "$f"
done
```

### 3. Configure environment

Copy `.env` and fill it in (the checked-in `.env` has empty values):

```bash
PORT=8080
DB_DSN=postgres://user:pass@localhost:5432/api_gateway?sslmode=disable
DB_DRIVER=postgres
DB_MAX_IDLE_CONNS=10
DB_MAX_OPEN_CONNS=100
DB_CONN_MAX_LIFETIME=1h
```

> The app does **not** auto-load `.env`. Export the variables (e.g.
> `set -a; . ./.env; set +a`) or use a tool like `direnv` / `dotenv` before
> running. `DB_DSN` is required (the app panics without it).

### 4. Build & run

```bash
go build ./...                 # compiles cleanly today
go run ./internal/cmd/gateway  # starts the server on $PORT
```

What you get right now: a server that logs every request and returns **404**
(see [What Actually Runs Today](#what-actually-runs-today)).

---

## Configuration Reference

| Env Var                | Required | Default      | Meaning                                            |
| ---------------------- | -------- | ------------ | -------------------------------------------------- |
| `PORT`                 | no       | `8080`       | TCP port the HTTP server binds (`:PORT`)           |
| `DB_DSN`               | **yes**  | —            | Postgres connection string (**panics if missing**) |
| `DB_DRIVER`            | no       | `postgres`   | `database/sql` driver name                         |
| `DB_MAX_IDLE_CONNS`    | no       | `10`         | Idle connection pool size                          |
| `DB_MAX_OPEN_CONNS`    | no       | `100`        | Max open connections                               |
| `DB_CONN_MAX_LIFETIME` | no       | `1h`         | Max connection lifetime (Go duration, e.g. `30m`)  |

There are **no env vars yet** for the JWT secret/issuer/TTLs, rate-limit rules,
or upstream targets — those are constructor parameters in code and would need to
be added to `config` as you wire the app together.

---

## Usage / API Reference

> These endpoints are **implemented as handlers** but are **not yet routed**.
> The request/response shapes below come directly from the handler code and will
> work once the handlers are registered on a router/mux (and the DB issues are
> fixed).

### `POST /onboard` — create a tenant + admin + first API key

Request:

```json
{
  "tenant_name": "Acme Inc",
  "tenant_slug": "acme-inc",
  "admin_email": "admin@acme.test",
  "admin_password": "supersecret",
  "api_key_label": "production backend"
}
```

Response `201 Created`:

```json
{
  "tenant":     { "id": "…", "name": "Acme Inc", "slug": "acme-inc", "status": "active", "created_at": "…", "updated_at": "…" },
  "user":       { "id": "…", "email": "admin@acme.test", "created_at": "…", "updated_at": "…" },
  "membership": { "ID": "…", "UserId": "…", "TenantId": "…", "Role": "owner", "Status": "active", … },
  "api_key":    { "id": "…", "tenant_id": "…", "active": true, "description": "production backend", … },
  "api_key_secret_raw": "gw_live_…"   // shown ONCE — store it now
}
```

Errors: `400` invalid JSON/validation, `409` slug/email conflict, `500` internal.

### `POST /login` — authenticate a user, get tokens

Request:

```json
{ "email": "admin@acme.test", "password": "supersecret" }
```

Response `200 OK`:

```json
{
  "user": { "id": "…", "email": "admin@acme.test", … },
  "memberships": [ { "id": "…", "tenant_id": "…", "role": "owner", "status": "active", … } ],
  "access_token": "<jwt>",
  "access_token_expires_at": "…",
  "refresh_token": "<jwt>",
  "refresh_token_expires_at": "…",
  "logged_in_at": "…"
}
```

Errors: `400` validation, `401` invalid credentials / no active membership,
`500` internal.

### Intended data-plane headers (once the proxy is wired)

- Machine calls: `X-API-Key: gw_live_…`
- Human calls: `Authorization: Bearer <access_token>` **and** `X-Tenant-ID: <tenant id>`
- The gateway injects downstream: `X-Gateway-Tenant-ID`, `X-Gateway-Tenant-Slug`,
  `X-Tenant-ID`, `X-Gateway-User-ID`, `X-Gateway-User-Email`,
  `X-Gateway-Membership-ID/Role/Status`, `X-Gateway-API-Key-ID/Active`, and adds
  `X-Gateway-Upstream` to responses.

---

## What Actually Runs Today

Running `go run ./internal/cmd/gateway` today does exactly this:

1. Initializes the JSON logger.
2. Loads & validates config (panics if `DB_DSN` is unset).
3. Starts `http.Server` with handler = `LoggingMiddleware(emptyMux)`.

Because the mux has **no routes** and nothing else is wired:

- Every request is logged and answered with **`404 page not found`**.
- The database is **never opened** (`config.NewDatabase` is unused).
- Repositories, services, handlers, router, proxy, rate limiter, and circuit
  breaker are **compiled but never instantiated** at runtime.

In other words: **the building blocks are written and individually coherent, but
the application is not assembled.** Wiring it together (in `server.New` or a new
bootstrap function) is the primary integration task.

<!-- IMAGE: replace this line with the generated diagram -->
![What Runs Today vs What's Built](docs/images/13-wired-vs-built.png)

<details>
<summary><b>🎨 Gemini prompt — "Wired vs. Built" reality check</b> (save as <code>docs/images/13-wired-vs-built.png</code>)</summary>

```text
[PASTE THE SHARED STYLE PREAMBLE FIRST]

DIAGRAM: "What Actually Runs Today vs. What's Built (not yet wired)"

Split the canvas into TWO clearly separated zones with a bold hand-drawn divider
down the middle. Title banner: "Reality check: compiled ≠ assembled".

LEFT ZONE — header in GREEN: "✅ LIVE TODAY (reachable at runtime)".
  Draw a simple vertical flow with solid arrows:
  🌐 "HTTP request" → green "http.Server" → green "LoggingMiddleware
  (X-Request-ID + access log)" → green "empty http.ServeMux" → red chip
  "404 for every path".
  Small note: "config loads (panics if DB_DSN unset); JSON logger active".

RIGHT ZONE — header in GRAY/RED: "🚧 BUILT BUT NOT WIRED (never instantiated at
runtime)". Draw these as a loose grid of DASHED-outline gray boxes, each with a
🚧 tag:
  "Router (trie)", "PostgreSQL connection (NewDatabase unused)",
  "Repositories ×5", "Onboarding / Auth / APIKeyAuth / TenantResolution
  services", "JWTManager", "APIKeyAuth & TenantResolution middleware",
  "Handlers (/onboard, /login)", "Proxy + StaticRegistry", "RateLimiter",
  "RetryTransport", "CircuitBreakerTransport".
  Caption under the grid: "All compile; go vet is clean; none are referenced by
  main.go."

BOTTOM — a bridge arrow from RIGHT zone back to LEFT labeled in big friendly
handwriting: "TODO: assemble in a bootstrap → open DB, build repos+services,
construct Router, register routes, serve router instead of empty mux".

Make the contrast obvious: LEFT solid/colored/alive, RIGHT dashed/gray/dormant.
Hand-drawn Excalidraw style, landscape.
```

</details>

A minimal assembly would look roughly like:

```go
db, _ := config.NewDatabase(cfg)
repos := repository.NewPostgresRepositories(db)
jwt, _ := services.NewJWTManager(secret, "api-gateway", 15*time.Minute, 24*time.Hour)

onboarding := services.NewOnboardingService(db, repos)
auth       := services.NewAuthService(repos, jwt)
apiKeyAuth := services.NewAPIKeyAuthService(repos)
resolver   := services.NewTenantResolver(repos, jwt)

r := server.NewRouter()
r.Use(middleware.LoggingMiddleware)
r.POST("/onboard", handlers.NewOnboardingHandler(onboarding))
r.POST("/login",   handlers.NewAuthHandler(auth))
// + data-plane routes wrapped with APIKeyAuth/TenantResolution + ratelimit → proxy
```

…plus building a `proxy.StaticRegistry` of `UpstreamTarget`s and pointing
`server.New` at the router instead of a bare mux.

---

## Known Issues & Bugs (must fix before it runs)

These were found by reading the code against the migrations. The project
**compiles** and `go vet` is clean, but the following will fail at runtime.

> 📋 **Tracked as a checklist in [TODO.md](TODO.md)** — tick items off there as
> you fix them. IDs below (B#/C#) match the TODO so the two stay in sync.
> Replace `[ ]` with `[x]` here too when an item is done.

**Status:** 🔴 0 / 9 blocking fixed · 🟡 0 / 6 cleanups done.

### 🔴 Blocking — app is not assembled
- [ ] **B1 — Nothing is wired.** `main.go`/`server.New` serve an empty mux; DB,
  repos, services, handlers, router, proxy, and rate limiter are never
  instantiated. The live server returns 404 for everything.

### 🔴 Blocking — schema/SQL mismatches (queries will error)
- [ ] **B2 — `001_create_tenants.sql` has a trailing comma** after `updated_at`
  (line 33), so the `CREATE TABLE tenants` statement is invalid SQL and won't
  apply.
- [ ] **B3 — `002_create_users.sql` defines column `password`**, but `user_repo`
  INSERTs/SELECTs **`password_hash`**. One of them must change (the code expects
  `password_hash`).
- [ ] **B4 — `004_usage.sql` defines `endpoint`** (and `created_at`/`updated_at`,
  **no `timestamp`**), but `usage_repo` INSERT/SELECT use columns **`path`** and
  **`"timestamp"`**. These names don't exist in the table.
- [ ] **B5 — `api_keys` queries reference `expires_at`.** `apikey_repo`'s
  `Create`, `GetByID`, `GetByHash`, and `ListByTenant` all `SELECT … expires_at …`,
  but migration `003` has no `expires_at` column **and** the `APIKey` model has
  no such field — so the queries reference a non-existent column.

### 🔴 Blocking — `Scan`/column count mismatches (runtime scan errors)
- [ ] **B6 — `user_repo.Create`** runs `RETURNING id` (1 column) but `.Scan()`
  into **5** destinations (`Id, Email, PasswordHash, CreatedAt, UpdatedAt`). This
  will fail with a scan-count error.
- [ ] **B7 — `apikey_repo` reads 8 columns but Scans 7.** The `SELECT`/`RETURNING`
  lists 8 columns (incl. `expires_at`) while `.Scan()` binds 7 fields → count
  mismatch (on top of the missing column in B5).
- [ ] **B8 — `membership.Create` parameter/column shift.** The INSERT lists 4
  columns `(user_id, tenant_id, role, status)` but passes **5** values starting
  with `normalized.ID`, so values are shifted (the membership `ID` is sent into
  the `user_id` slot). It also `RETURNING` 5 columns but `.Scan()` into 7.
- [ ] **B9 — App-generated UUID vs DB default mismatch.** Services generate UUIDs
  with `newUUIDString()` and pass them, but most INSERTs **omit the `id` column**
  (relying on `gen_random_uuid()`), so the generated IDs are silently dropped
  for users/api_keys/memberships. `tenant_repo.Create` *does* insert `id` but
  also writes the zero-value `created_at`/`updated_at` (services never set them),
  which conflicts with the `DEFAULT CURRENT_TIMESTAMP` intent.

### 🟡 Non-blocking — correctness / cleanliness
- [ ] **C1 — Dead `case` in `mapRepositoryError`.** `errors.go` has two
  `case ErrValidation.Kind:` branches; the second is unreachable (it compiles
  only because the case values are runtime field reads, not constants).
- [ ] **C2 — `tenants` status CHECK vs model.** Migration `001` allows
  `('active','inactive')`, but the model/services use `active`/**`suspended`**.
  Inserting a suspended tenant would violate the CHECK.
- [ ] **C3 — `Usage.Endpoint` JSON tag** is `endpoint` in the model but the table
  column (post-fix) should be reconciled with the repo's `path`/`endpoint`
  choice.
- [ ] **C4 — `retry.retryableBody` is an unused stub** (`policy.go`) returning
  `false`; `normalizeContextErr`, `bufferBody`, `bodyFromBytes`, and the
  `var _ sync.Locker` in `transport.go` are scaffolding that isn't currently
  exercised.
- [ ] **C5 — No connectivity check.** `NewDatabase` never `Ping`s, so a bad DSN
  only surfaces on first query.
- [ ] **C6 — No tests.** There are zero `_test.go` files despite extensive doc
  comments referencing test scenarios.

See [TODO.md](TODO.md) for the P2 build-out items (R1–R8: data-plane wiring,
config secrets, upstream persistence, usage writes, refresh endpoint, revocation,
hardening, admin API) and a suggested order of attack.

---

## Feature Status

The **Notes** column links each area to the relevant [TODO.md](TODO.md) item(s).

| Area                                   | Built? | Wired & working? | Notes (→ TODO id) |
| -------------------------------------- | :----: | :--------------: | ----- |
| Domain models                          |   ✅   |        —         | Complete |
| Config loading & validation            |   ✅   |     ⚠️ partial    | DB never opened → B1, C5 |
| Structured logging (slog + req ID)     |   ✅   |        ✅         | The one live component |
| SQL migrations                         |   ⚠️   |        ❌        | Buggy/mismatched → B2–B5, C2 |
| Repository layer (CRUD + tx)           |   ✅   |        ❌        | SQL mismatches → B3–B9 |
| Onboarding service (atomic)            |   ✅   |        ❌        | Blocked by DB issues → B1–B9 |
| Auth service / login                   |   ✅   |        ❌        | Not routed → B1 |
| Custom JWT (HS256)                     |   ✅   |        ❌        | Issue/verify/refresh done; not routed → B1 |
| Password hashing (PBKDF2)              |   ✅   |        —         | Complete |
| API key auth (SHA-256)                 |   ✅   |        ❌        | Built; not routed → B1, R1 |
| Tenant resolution                      |   ✅   |        ❌        | Built; not routed → B1, R1 |
| Trie router                            |   ✅   |        ❌        | Never instantiated → B1 |
| Reverse proxy (per-tenant)             |   ✅   |        ❌        | No registry → R1, R3 |
| Request/response transforms            |   ✅   |        ❌        | Headers + path only (no body) |
| Retry transport (backoff+jitter)       |   ✅   |     ⚠️ via proxy  | Reachable only via proxy; cleanup → C4 |
| Circuit breaker (CLOSED/OPEN/HALF_OPEN)|   ✅   |     ⚠️ via proxy  | Transport layer; not wired end-to-end yet → R1 |
| Rate limiting (token bucket)           |   ✅   |        ❌        | In-memory; not routed → R1 |
| Usage metering                         |   ⚠️   |        ❌        | Model+repo exist; no writer → B4, R4 |
| Refresh-token endpoint                 |   ❌   |        ❌        | Service exists; no handler → R5 |
| Admin/dashboard API                    |   ❌   |        ❌        | Not started → R8 |
| Tests                                  |   ❌   |        ❌        | None → C6 |

Legend: ✅ done · ⚠️ partial/buggy · ❌ missing · — n/a

> Full checklist with completion tracking, priorities, and a suggested order of
> attack: **[TODO.md](TODO.md)**.

---

## Roadmap & Next Steps

In rough priority order to get from "library" to "running gateway":

1. **Fix the migrations & repo SQL** so they agree (items 2–9 above): rename
   columns (`password`→`password_hash`, `endpoint`/`path`, add/remove
   `expires_at`, `timestamp`), fix the `001` trailing comma, reconcile the
   tenant status CHECK, and make ID/timestamp handling consistent (either let the
   DB generate IDs everywhere, or insert the app-generated ones everywhere).
2. **Assemble the app** in a bootstrap function: open the DB, build repos +
   services + JWT manager, construct the router, register `/onboard` and
   `/login`, and have `server.New` serve the router.
3. **Wire the data plane:** build a `proxy.StaticRegistry` of `UpstreamTarget`s,
   chain `APIKeyAuth`/`TenantResolution` → `ratelimit` → `proxy.Handler` on the
   proxied routes.
4. **Move secrets/policies into config:** JWT secret/issuer/TTLs, rate-limit
   rules, and upstream definitions (env or a config file / DB table).
5. **Persist upstreams & usage:** add an `upstreams` table + repository, and
   write `Usage` rows from the proxy path (capture bytes in/out).
6. **Add a refresh endpoint** and (later) token revocation using the `jti`.
7. **Operational hardening:** `db.Ping()` on startup, graceful shutdown,
   `PruneIdle` goroutine for the limiter, and a real migration runner.
8. **Add tests** — the components are highly testable (clock injection, repo
   interfaces, pure helpers) and currently have none.

---

*The previous, shorter README has been preserved as `README.original.md`.*
