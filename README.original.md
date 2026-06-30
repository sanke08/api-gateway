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

> 📜 **This is the original, aspirational README, kept for history.** When it
> was written, the components below existed but the application was **not
> assembled** and several schema/SQL mismatches stopped it from running. **That
> is no longer true.** As of **2026-06-30** the app is fully wired, the blocking
> bugs are fixed, and the full request path has been exercised end-to-end. For
> the accurate, current status see **[README.md](README.md)** and the checklist
> in **[TODO.md](TODO.md)**. The sections below are preserved as a snapshot of
> the project's original intent.

## 🧩 Built Components — now wired and running
What this README originally listed as "built but not assembled" is now wired
into `main.go` and exercised end-to-end:
- [x] **Atomic Onboarding**: `OnboardTenant` creates Tenant + Admin User + Owner Membership + API Key in one transaction. *(An API-key INSERT param-count bug that broke this was fixed on 2026-06-30.)*
- [x] **Custom Security**: hand-rolled PBKDF2 password hashing, SHA-256 API-key hashing, HS256 JWT — `crypto/*` only.
- [x] **Transaction Support**: `WithTx` rebinding shared across all repositories.
- [x] **Multi-Tenant Schema**: users belong to many tenants via `TenantMembership`.
- [x] **Authentication & JWT** + login + refresh — routed at `/login`.
- [x] **Tenant Resolution** + **API-Key Auth** middleware — guarding the data plane.
- [x] **Reverse Proxy, Rate Limiting, Retry, Circuit Breaker, Cache layer** — wired on proxied routes.
- [x] **Observability**: metrics registry + per-request trace + middleware + `/metrics`.
- [x] **Graceful shutdown, async usage tracking, health/readiness checks, edge (CORS + security headers).**

## 🟢 Actually live today
The server assembles the full pipeline and serves real routes:
`/onboard`, `/login`, `/metrics`, `/health`, `/ready`, and (when upstreams are
configured) the proxied data-plane routes. Structured JSON logging via `slog`
with Request-ID tracing runs on every request, alongside the metrics and edge
middleware. (The old "empty mux, 404 for every path" state is gone.)

## 🛠️ Remaining Tasks (Roadmap)
The blocking work is complete. What remains is cleanup and feature build-out
(full detail in [TODO.md](TODO.md)):
- [ ] **Commit the test suite** — a full `-race` end-to-end simulation passes but isn't yet committed.
- [ ] **Address known bugs** found by review/simulation — login timing side-channel, async-usage shutdown race, retry backoff overflow, `GetBySlug` status, a few middleware/proxy edge cases.
- [ ] **Persist upstreams** in a DB table (currently from `UPSTREAMS_JSON`).
- [ ] **Refresh-token endpoint** (service method exists, no route), **token revocation**, **admin dashboard API**.
- [ ] **Finish cache/metrics wiring** — identity-cache injection into auth middleware; per-tenant metric label.

## 🛠️ Technology Stack
- **Language**: Go 1.25.4 (standard library only for logic; `lib/pq` is the one third-party dep)
- **Database**: PostgreSQL (pgcrypto for UUIDs)
- **Architecture**: Clean Architecture / Domain-Driven Design
