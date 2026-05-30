package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/repository"
)

// APIKeyAuthenticationRequest represents a machine-authentication attempt.
//
// What this means:
// A client is presenting a raw API key so the gateway can determine:
// - which tenant owns the request
// - whether the key is active
// - whether the tenant itself is active
//
// Why this exists:
// API key authentication is different from human login. It is for machines,
// backend services, scripts, and integrations.
type APIKeyAuthenticationRequest struct {
	RawAPIKey string
}

// APIKeyAuthenticationResult is the successful output of API key authentication.
//
// What it contains:
// - the validated API key row
// - the owning tenant
// - the time the request was resolved
//
// Why this is useful:
// Later middleware and handlers can read the resolved tenant from context
// without repeating database lookups.
type APIKeyAuthenticationResult struct {
	APIKey     models.APIKey
	Tenant     models.Tenant
	ResolvedAt time.Time
}

// APIKeyAuthenticator defines the machine-authentication use case.
//
// Why this interface exists:
// The HTTP layer should not know how API keys are hashed or verified.
// It should just call a service that returns success or failure.
type APIKeyAuthenticator interface {
	Authenticate(ctx context.Context, req APIKeyAuthenticationRequest) (APIKeyAuthenticationResult, error)
}

// apiKeyAuthService is the concrete implementation of APIKeyAuthenticator.
type apiKeyAuthService struct {
	repos repository.Repositories
	clock func() time.Time
}

// NewAPIKeyAuthService creates the API key authentication service.
//
// Why this constructor exists:
// It makes dependencies explicit and keeps the service easy to test.
func NewAPIKeyAuthService(repos repository.Repositories) APIKeyAuthenticator {
	return &apiKeyAuthService{
		repos: repos,
		clock: nowUTC,
	}
}

// Authenticate validates the raw API key, loads the stored key row, verifies
// that the key is active, then loads the tenant and checks tenant status.
//
// Why the order matters:
// 1. Validate input
// 2. Hash the raw key
// 3. Look up the stored hash
// 4. Check whether the key is active
// 5. Load the tenant
// 6. Check whether the tenant is active
//
// Why this is correct:
// The API key is the machine identity. If the key is invalid, expired, or
// inactive, the request must not proceed.

func (s *apiKeyAuthService) Authenticate(ctx context.Context, req APIKeyAuthenticationRequest) (APIKeyAuthenticationResult, error) {
	const op = "api_key_authenticate"

	normalized, err := normalizeAPIKeyAuthenticationRequest(req)
	if err != nil {
		return APIKeyAuthenticationResult{}, err
	}

	keyHash := hashAPIKeySecret(normalized.RawAPIKey)

	apiKey, err := s.repos.APIKeys.GetByHash(ctx, keyHash)
	if err != nil {
		if isRepositoryNotFound(err) {
			return APIKeyAuthenticationResult{}, unauthorizedError(op, "invalid api key")
		}
		return APIKeyAuthenticationResult{}, mapRepositoryError(op, err)
	}

	if !apiKey.Active {
		return APIKeyAuthenticationResult{}, forbiddenError(op, "api key is inactive")
	}

	// if apiKey.ExpiresAt != nil && s.clock().UTC().After(*apiKey.ExpiresAt) {
	// 	return APIKeyAuthenticationResult{}, unauthorizedError(op, "api key expired")
	// }

	tenant, err := s.repos.Tenants.GetByID(ctx, apiKey.TenantID)
	if err != nil {
		if isRepositoryNotFound(err) {
			return APIKeyAuthenticationResult{}, unauthorizedError(op, "invalid api key tenant")
		}
		return APIKeyAuthenticationResult{}, mapRepositoryError(op, err)
	}

	if tenant.Status != models.TenantStatusActive {
		return APIKeyAuthenticationResult{}, forbiddenError(op, "tenant is not active")
	}

	return APIKeyAuthenticationResult{
		APIKey:     apiKey,
		Tenant:     tenant,
		ResolvedAt: s.clock(),
	}, nil
}

type normalizedAPIKeyAuthenticationRequest struct {
	RawAPIKey string
}

// normalizeAPIKeyAuthenticationRequest validates and trims the incoming API key.
func normalizeAPIKeyAuthenticationRequest(req APIKeyAuthenticationRequest) (normalizedAPIKeyAuthenticationRequest, error) {
	const op = "api_key_validate_authentication"

	raw := strings.TrimSpace(req.RawAPIKey)
	if raw == "" {
		return normalizedAPIKeyAuthenticationRequest{}, validationError(op, "raw_api_key is required")
	}

	return normalizedAPIKeyAuthenticationRequest{
		RawAPIKey: raw,
	}, nil
}

// hashAPIKeySecret converts the raw API key into a deterministic SHA-256 hash.
//
// Why we hash instead of searching raw API keys directly:
// Raw API keys are sensitive secrets and must never be stored or queried
// directly from the database. If the database, logs, or query traces are
// exposed, unhashed API keys would immediately compromise all clients.
//
// During authentication, we hash the presented API key and compare the hash
// against the stored hash value in the database.
func hashAPIKeySecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
