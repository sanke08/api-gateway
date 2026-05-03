package services

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
	"github.com/sanke08/api_gateway/internal/repository"
)

// LoginRequest is the input for the login flow.
//
// What this struct represents:
// A person is proving their identity by sending an email and password.
// No token is issued here. This phase only verifies the credential pair.
type LoginRequest struct {
	Email    string
	Password string
}

// LoginResult is the output of a successful login.
//
// Why this is returned:
// After login succeeds, the application needs the authenticated user and the
// list of tenant memberships so the client can choose which business to operate in.
type LoginResult struct {
	User                  models.User
	Memberships           []models.TenantMembership
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
	LoggedInAt            time.Time
}

// AuthService defines the authentication use case.
//
// Why this interface exists:
// It keeps the HTTP layer thin and prevents login rules from leaking into handlers.
type AuthService interface {
	Login(ctx context.Context, req LoginRequest) (LoginResult, error)
}

// authService is the concrete implementation of AuthService.
//
// Why the dependencies are stored here:
// Authentication needs access to repositories and password verification.
// That logic belongs in the service layer, not in the handler and not in the repository.
type authService struct {
	repos          repository.Repositories
	passwordHasher PasswordHasher
	tokenService   TokenService
	clock          func() time.Time
}

// NewAuthService creates a new authentication service.
//
// Why this constructor exists:
// It makes the dependencies explicit and keeps the service easy to test.
func NewAuthService(repos repository.Repositories, tokenService TokenService) AuthService {
	return &authService{
		repos:          repos,
		passwordHasher: NewStandardPasswordHasher(),
		tokenService:   tokenService,
		clock:          nowUTC,
	}
}

// Login verifies the email and password, then returns the authenticated user
// together with the user's tenant memberships.
//
// Why this method is necessary:
// It is the foundation of human authentication. A user proves knowledge of the
// password first. Only after that can the system issue a JWT in the next phase.
// Why this is the correct flow:
// 1) validate input
// 2) load the user by email
// 3) verify the password
// 4) load memberships
// 5) ensure the user has at least one active membership
// 6) issue access and refresh tokens
func (s *authService) Login(ctx context.Context, req LoginRequest) (LoginResult, error) {
	const op = "auth.login"

	norm, err := normalizeLoginRequest(req)
	if err != nil {
		return LoginResult{}, err
	}

	user, err := s.repos.Users.GetByEmail(ctx, norm.Email)
	if err != nil {
		// We intentionally do not tell the client whether the user does not exist.
		// That prevents easy account enumeration.
		if isRepositoryNotFound(err) {
			return LoginResult{}, unauthorizedError(op, "invalid credentials")
		}
		return LoginResult{}, mapRepositoryError(op, err)
	}

	ok, err := s.passwordHasher.Verify(norm.Password, user.PasswordHash)
	if err != nil {
		return LoginResult{}, internalError(op, err)
	}
	if !ok {
		return LoginResult{}, unauthorizedError(op, "invalid credentials")
	}

	memberships, err := s.repos.Memberships.ListByUser(ctx, user.Id)
	if err != nil {
		return LoginResult{}, mapRepositoryError(op, err)
	}

	activeMemberships := filterActiveMemberships(memberships)
	if len(activeMemberships) == 0 {
		return LoginResult{}, unauthorizedError(op, "account is not active in any tenant")
	}

	if s.tokenService == nil {
		return LoginResult{}, internalError(op, errors.New("token service is not configured"))
	}

	accessToken, accessExpiresAt, err := s.tokenService.IssueAccessToken(user)
	if err != nil {
		return LoginResult{}, internalError(op, err)
	}

	refreshToken, refreshExpiresAt, err := s.tokenService.IssueRefreshToken(user)
	if err != nil {
		return LoginResult{}, internalError(op, err)
	}

	return LoginResult{
		User:                  user,
		Memberships:           memberships,
		LoggedInAt:            s.clock(),
		AccessToken:           accessToken,
		AccessTokenExpiresAt:  accessExpiresAt,
		RefreshToken:          refreshToken,
		RefreshTokenExpiresAt: refreshExpiresAt,
	}, nil
}

// normalizedLoginRequest is the cleaned form of the login request.
//
// Why this exists:
// The service should work with trimmed and validated inputs only.
// That keeps the login rules stable and prevents whitespace bugs.
type normalizedLoginRequest struct {
	Email    string
	Password string
}

// normalizeLoginRequest validates and normalizes login input.
//
// What it does:
// - trims whitespace
// - validates email format
// - ensures password is present
//
// Why this is important:
// Bad input should fail before any database work happens.
func normalizeLoginRequest(req LoginRequest) (normalizedLoginRequest, error) {
	const op = "auth.validate_login"

	email := strings.TrimSpace(strings.ToLower(req.Email))
	password := strings.TrimSpace(req.Password)

	if email == "" {
		return normalizedLoginRequest{}, validationError(op, "email is required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return normalizedLoginRequest{}, validationError(op, "email is invalid")
	}
	if password == "" {
		return normalizedLoginRequest{}, validationError(op, "password is required")
	}

	return normalizedLoginRequest{
		Email:    email,
		Password: password,
	}, nil
}

// filterActiveMemberships keeps only memberships that are currently usable.
func filterActiveMemberships(memberships []models.TenantMembership) []models.TenantMembership {
	out := make([]models.TenantMembership, 0, len(memberships))
	for _, m := range memberships {
		if m.Status == models.MembershipStatusActive {
			out = append(out, m)
		}
	}
	return out
}

// isRepositoryNotFound checks whether the repository returned a "not found" error.
//
// Why this helper exists:
// Login should return a generic unauthorized error when the user is missing,
// instead of exposing whether the email exists.
func isRepositoryNotFound(err error) bool {
	var repoErr *repository.RepoError
	if !errors.As(err, &repoErr) {
		return false
	}
	return repoErr.Kind == repository.ErrNotFound.Kind
}

// Full forms used here
// JWT = JSON Web Token
// HMAC = Hash-based Message Authentication Code
// SHA = Secure Hash Algorithm
// TTL = Time To Live
// JSON = JavaScript Object Notation
// UTC = Coordinated Universal Time
