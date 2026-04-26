# Go API Gateway (Multi-Tenant)

A high-performance, zero-dependency API Gateway built with Go and PostgreSQL. Designed for high security and explicit tenant isolation.

## 🚀 Vision
A production-ready entry point that handles the "Hard Parts" of SaaS: Atomic onboarding, secure credential management, and detailed usage metering, all while maintaining zero third-party dependencies in the core logic.

## 🏗️ Technical Architecture

### 1. The "Thin" Repository Pattern
Repositories are "dumb" and focus on SQL execution. They support:
- **Transaction Rebinding (`WithTx`)**: Allows multiple repositories to share the same database transaction.
- **Error Classification**: Maps low-level PostgreSQL codes (e.g., `23505`) into stable `RepoError` types.

### 2. Service Layer (Business Brain)
- **Onboarding Service**: Orchestrates the complex creation of a new business entity (Tenant + Admin User + Owner Membership + API Key) in one atomic transaction.
- **Security Services**: Custom implementations of PBKDF2-HMAC-SHA256 for passwords and SHA-256 for API keys using only `crypto/*` packages.

### 3. Error Pipeline
We use a three-tier error system to ensure implementation details never leak to the client:
- `RepoError`: Categorizes DB failures (not_found, conflict).
- `ServiceError`: Categorizes business failures (validation, internal).
- `Handler`: Maps Service errors to standard HTTP status codes (400, 409, 500).

## ✅ Completed Features
- [x] **Atomic Onboarding**: `POST /onboard` creates a complete business environment.
- [x] **Custom Security**: Hand-rolled password hashing and key generation (Zero Dependencies).
- [x] **Transaction Support**: Full atomicity across all repository operations.
- [x] **Multi-Tenant Schema**: Users can belong to many Tenants via `TenantMembership`.
- [x] **Usage Metering**: Log-based tracking of traffic volume (Bytes In/Out).
- [x] **Structured Logging**: JSON logging via `slog` with Request ID tracing.

## 🛠️ Remaining Tasks (Roadmap)
- [ ] **Auth Middleware**: Implement JWT/Session verification for user endpoints.
- [ ] **Gateway Proxy**: Implement the reverse-proxy logic to route traffic to backends.
- [ ] **Key Validation Middleware**: The "Hot Path" to validate hashed API keys on every request.
- [ ] **Admin Dashboard API**: Endpoints for tenant management and usage analytics.
- [ ] **Rate Limiting**: Tenant-level throttling.

## 🛠️ Technology Stack
- **Language**: Go 1.25.4 (Standard Library only for logic)
- **Database**: PostgreSQL (pgcrypto for UUIDs)
- **Architecture**: Clean Architecture / Domain-Driven Design
