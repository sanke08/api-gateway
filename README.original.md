# Go API Gateway (Multi-Tenant)

A high-performance API Gateway built with Go and PostgreSQL. Designed for high security and explicit tenant isolation.

> Note: "zero-dependency" is aspirational — all **business/security logic** uses
> only the Go standard library, but `github.com/lib/pq` (the Postgres driver) is
> a third-party dependency.

## 🚀 Vision
A production-ready entry point that handles the "Hard Parts" of SaaS: Atomic onboarding, secure credential management, and detailed usage metering, with no third-party dependencies in the **core logic** (only the Postgres wire driver is external).

## 🏗️ Technical Architecture

### 1. The "Thin" Repository Pattern
Repositories are "dumb" and focus on SQL execution. They support:
- **Transaction Rebinding (`WithTx`)**: Allows multiple repositories to share the same database transaction.
- **Error Classification**: Maps low-level PostgreSQL codes (e.g., `23505`) into stable `RepoError` types.

### 2. Service Layer (Business Brain)
- **Onboarding Service**: Orchestrates the complex creation of a new business entity (Tenant + Admin User + Owner Membership + API Key) in one atomic transaction.
- **Security Services**: Custom implementations of PBKDF2-HMAC-SHA256 for passwords and SHA-256 for API keys using only `crypto/*` packages.

### 3. Authentication & Tenant Resolution
- **Custom JWT Implementation**: Full JWT generation, signing (HMAC-SHA256), and verification using only the standard `crypto` package.
- **Tenant Resolution Middleware**: Validates access tokens and resolves tenant membership dynamically per request.

### 4. Error Pipeline
We use a three-tier error system to ensure implementation details never leak to the client:
- `RepoError`: Categorizes DB failures (not_found, conflict).
- `ServiceError`: Categorizes business failures (validation, internal).
- `Handler`: Maps Service errors to standard HTTP status codes (400, 409, 500).

> ⚠️ **This is the original, aspirational README, kept for history.** It
> overstates the current status: the items below are **written and individually
> coherent, but the application is not assembled and several schema/SQL
> mismatches stop it from running end to end.** For the accurate, verified
> status see **[README.md](README.md)** and the prioritized checklist in
> **[TODO.md](TODO.md)**. (Last reconciled: 2026-06-28 — still builds &
> `go vet`-clean, 0 tests, none of the blocking bugs B1–B9 fixed.)

## 🧩 Built Components (code exists & compiles — not yet wired/running)
- [x] **Atomic Onboarding** logic: `OnboardTenant` creates Tenant + Admin User + Owner Membership + API Key in one transaction. *(Handler not routed; blocked by repo/SQL bugs.)*
- [x] **Custom Security**: hand-rolled PBKDF2 password hashing, SHA-256 API-key hashing, HS256 JWT — `crypto/*` only.
- [x] **Transaction Support**: `WithTx` rebinding shared across all repositories.
- [x] **Multi-Tenant Schema**: users belong to many tenants via `TenantMembership`.
- [x] **Authentication & JWT** + login flow logic. *(Not routed.)*
- [x] **Tenant Resolution** + **API-Key Auth** middleware. *(Not routed.)*
- [x] **Reverse Proxy, Rate Limiting, Retry, Circuit Breaker, Cache layer.** *(Built; no registry/wiring.)*
- [x] **Observability**: metrics registry + per-request trace + middleware. *(Only the slog logger is actually live.)*
- [x] **Graceful shutdown, async usage tracking, health/readiness checks, edge (CORS + security headers).** *(All built; never instantiated.)*

## 🟢 Actually live today
- [x] **Structured Logging**: JSON logging via `slog` with Request ID tracing — the **one** wired middleware. The server otherwise serves an empty mux (404 for every path).

## 🛠️ Remaining Tasks (Roadmap)
- [ ] **Fix schema/SQL mismatches** so queries run (trailing comma, `password`/`password_hash`, usage `path`/`timestamp` vs `endpoint`/`created_at`, `api_keys.expires_at`, Scan column counts, ID/timestamp handling).
- [ ] **Assemble the app**: open DB, build repos/services, construct the router, register `/onboard` + `/login`, serve the router instead of a bare mux.
- [ ] **Wire the data plane**: populate a proxy registry; chain auth → rate-limit → proxy.
- [ ] **Wire the operational subsystems**: graceful shutdown + signal handling, async usage tracking, `/health`+`/ready`, edge/CORS.
- [ ] **Admin Dashboard API**, **refresh-token endpoint**, **token revocation**, **tests**.

## 🛠️ Technology Stack
- **Language**: Go 1.25.4 (standard library only for logic; `lib/pq` is the one third-party dep)
- **Database**: PostgreSQL (pgcrypto for UUIDs)
- **Architecture**: Clean Architecture / Domain-Driven Design
