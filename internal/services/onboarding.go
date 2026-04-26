package services

import (
	"context"
	"database/sql"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/repository"
)

// OnboardingRequest is the input for tenant onboarding.
//
// What it represents:
// A new business is being created, along with its first human owner and first machine credential.
type OnboardingRequest struct {
	TenantName    string
	TenantSlug    string
	AdminEmail    string
	AdminPassword string
	APIKeyLabel   *string
}

// OnboardingResult is the output of a successful onboarding operation.
//
// Why the raw API key is included:
// The raw secret is only available once. The caller must store it safely immediately.
type OnboardingResult struct {
	Tenant          models.Tenant
	User            models.User
	Membership      models.TenantMembership
	APIKey          models.APIKey
	APIKeySecretRaw string
}

// OnboardingService defines the business operation for first-time tenant creation.
type OnboardingService interface {
	OnboardTenant(ctx context.Context, req OnboardingRequest) (OnboardingResult, error)
}

type onboardingService struct {
	db              *sql.DB
	repos           repository.Repositories
	passwordHasher  PasswordHasher
	apiKeyGenerator APIKeyGenerator
	clock           func() time.Time
}

// NewOnboardingService creates the onboarding service.
func NewOnboardingService(db *sql.DB, repos repository.Repositories) OnboardingService {
	return &onboardingService{
		db:              db,
		repos:           repos,
		passwordHasher:  NewStandardPasswordHasher(),
		apiKeyGenerator: NewStandardAPIKeyGenerator(),
		clock:           nowUTC,
	}
}

// OnboardTenant creates a tenant, global user, tenant membership, and API key in one transaction.
//
// Why this must be one transaction:
// Onboarding is one logical event. Partial onboarding is broken onboarding.
// Either everything is created, or nothing is kept.
func (s *onboardingService) OnboardTenant(ctx context.Context, req OnboardingRequest) (OnboardingResult, error) {
	const op = "onboarding.onboard_tenant"

	norm, err := normalizeOnboardingRequest(req)
	if err != nil {
		return OnboardingResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OnboardingResult{}, internalError(op, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	tenantRepo := s.repos.Tenants.WithTx(tx)
	userRepo := s.repos.Users.WithTx(tx)
	membershipRepo := s.repos.Memberships.WithTx(tx)
	apiKeyRepo := s.repos.APIKeys.WithTx(tx)

	tenantID, err := newUUIDString()
	if err != nil {
		return OnboardingResult{}, err
	}

	tenant := models.Tenant{
		Id:     tenantID,
		Name:   norm.TenantName,
		Slug:   norm.TenantSlug,
		Status: models.TenantStatusActive,
	}

	createdTenant, err := tenantRepo.Create(ctx, tenant)
	if err != nil {
		return OnboardingResult{}, mapRepositoryError(op, err)
	}

	hashedPassword, err := s.passwordHasher.Hash(norm.AdminPassword)
	if err != nil {
		return OnboardingResult{}, err
	}

	userID, err := newUUIDString()
	if err != nil {
		return OnboardingResult{}, err
	}

	user := models.User{
		Id:           userID,
		Email:        norm.AdminEmail,
		PasswordHash: hashedPassword,
	}

	createdUser, err := userRepo.Create(ctx, user)
	if err != nil {
		return OnboardingResult{}, mapRepositoryError(op, err)
	}

	membershipID, err := newUUIDString()
	if err != nil {
		return OnboardingResult{}, err
	}

	membership := models.TenantMembership{
		ID:       membershipID,
		UserId:   createdUser.Id,
		TenantId: createdTenant.Id,
		Role:     models.MembershipRoleOwner,
		Status:   models.MembershipStatusActive,
	}

	createdMembership, err := membershipRepo.Create(ctx, membership)
	if err != nil {
		return OnboardingResult{}, mapRepositoryError(op, err)
	}

	rawAPIKey, apiKeyHash, err := s.apiKeyGenerator.Generate()
	if err != nil {
		return OnboardingResult{}, err
	}

	apiKeyID, err := newUUIDString()
	if err != nil {
		return OnboardingResult{}, err
	}

	apiKey := models.APIKey{
		ID:          apiKeyID,
		TenantID:    createdTenant.Id,
		KeyHash:     apiKeyHash,
		Description: norm.APIKeyLabel,
		Active:      true,
	}

	createdAPIKey, err := apiKeyRepo.Create(ctx, apiKey)
	if err != nil {
		return OnboardingResult{}, mapRepositoryError(op, err)
	}

	if err := tx.Commit(); err != nil {
		return OnboardingResult{}, internalError(op, err)
	}

	return OnboardingResult{
		Tenant:          createdTenant,
		User:            createdUser,
		Membership:      createdMembership,
		APIKey:          createdAPIKey,
		APIKeySecretRaw: rawAPIKey,
	}, nil
}

type normalizedOnboardingRequest struct {
	TenantName    string
	TenantSlug    string
	AdminEmail    string
	AdminPassword string
	// Why APIKeyLabel is a *string instead of string:
	//
	// In Go, a normal string cannot distinguish between:
	//   1. Field not provided by caller
	//   2. Field provided as an empty string
	//   3. Field provided with whitespace (e.g. "   ")
	//
	// All of these eventually become "" after trimming, which removes intent.
	//
	// Using *string allows us to preserve "presence information":
	//
	//   - nil  → field was NOT provided by the client
	//   - ""   → field was provided but empty (can be treated as invalid or ignored)
	//   - "x"  → field was explicitly provided with a valid value
	//
	// This distinction is important in APIs where:
	//   - some fields are optional
	//   - defaults are applied when field is missing
	//   - empty value may have different meaning than missing value
	//
	// Example behavior:
	//
	//   JSON: {}                     → APIKeyLabel = nil  (use default)
	//   JSON: {"label": ""}         → APIKeyLabel = ""    (explicit empty, may normalize or reject)
	//   JSON: {"label": "prod"}     → APIKeyLabel = "prod"
	//
	// Without pointer (*string), all cases collapse into "" and we lose intent,
	// which can lead to incorrect defaults or incorrect validation behavior.

	// Why this matters in real systems
	// Let’s say later your API behaves like this:
	// Case A: field NOT provided
	// → use default label: "auto-generated"
	// Case B: field explicitly empty
	// → reject request (validation error)
	APIKeyLabel *string
}

func normalizeOnboardingRequest(req OnboardingRequest) (normalizedOnboardingRequest, error) {
	const op = "onboarding.validate"

	tenantName := strings.TrimSpace(req.TenantName)
	tenantSlug := strings.ToLower(strings.TrimSpace(req.TenantSlug))
	adminEmail := strings.ToLower(strings.TrimSpace(req.AdminEmail))
	adminPassword := strings.TrimSpace(req.AdminPassword)

	if tenantName == "" {
		return normalizedOnboardingRequest{}, validationError(op, "tenant_name is required")
	}
	if tenantSlug == "" {
		return normalizedOnboardingRequest{}, validationError(op, "tenant_slug is required")
	}
	if !slugPattern.MatchString(tenantSlug) {
		return normalizedOnboardingRequest{}, validationError(op, "tenant_slug must contain only lowercase letters, numbers, and hyphens")
	}
	if adminEmail == "" {
		return normalizedOnboardingRequest{}, validationError(op, "admin_email is required")
	}
	if _, err := mail.ParseAddress(adminEmail); err != nil {
		return normalizedOnboardingRequest{}, validationError(op, "admin_email is invalid")
	}
	if len(adminPassword) < 8 {
		return normalizedOnboardingRequest{}, validationError(op, "admin_password must be at least 8 characters")
	}

	var apiKeyLabel *string
	if req.APIKeyLabel != nil {
		v := strings.TrimSpace(*req.APIKeyLabel)
		if v != "" {
			apiKeyLabel = &v
		}
	}

	return normalizedOnboardingRequest{
		TenantName:    tenantName,
		TenantSlug:    tenantSlug,
		AdminEmail:    adminEmail,
		AdminPassword: adminPassword,
		APIKeyLabel:   apiKeyLabel,
	}, nil
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
