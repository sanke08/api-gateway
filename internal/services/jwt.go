package services

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
)

// JWTManager is the custom JSON Web Token manager for this system.
//
// Why this exists:
// The system needs a standard way to issue and verify signed identity tokens
// after a user has already authenticated with email and password.
//
// Why we implement this ourselves:
// The project rules forbid third-party JWT libraries, so we build the token
// machinery using only the Go standard library.
type JWTManager struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	clock      func() time.Time
}

// JWTHeader represents the JSON Web Token header.
//
// What it does:
// The header describes how the token is signed and what type of token it is.
//
// Why it matters:
// When a token is parsed later, the system must verify that the token really
// uses the expected signing algorithm and token type.
type JWTHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

// AccessTokenClaims is the payload for an access token.
//
// What it stores:
// - Subject: the authenticated user ID
// - Email: the authenticated user's email
// - TokenType: identifies whether this is an access or refresh token
// - IssuedAt: when the token was created
// - ExpiresAt: when the token stops being valid
// - Issuer: which service created it
// - JWTID: a unique token identifier for future revocation support
//
// Why these fields matter:
// The token must carry enough information to prove the logged-in identity
// without exposing passwords or raw secrets.
type AccessTokenClaims struct {
	Subject   string `json:"sub"`
	Email     string `json:"email"`
	TokenType string `json:"typ"`
	Issuer    string `json:"iss,omitempty"`
	JWTID     string `json:"jti,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// TokenService defines the JWT operations used by authentication and middleware.
//
// Why this interface exists:
// It keeps the login service independent from the concrete JWT implementation.
// That makes the code easier to test and easier to replace later if needed.
type TokenService interface {
	IssueAccessToken(user models.User) (string, time.Time, error)
	IssueRefreshToken(user models.User) (string, time.Time, error)
	RefreshAccessToken(refreshToken string) (string, time.Time, error)
	VerifyAccessToken(token string) (AccessTokenClaims, error)
	VerifyRefreshToken(token string) (AccessTokenClaims, error)
}

// NewJWTManager creates a JWT manager with explicit configuration.
//
// Why this constructor exists:
// It makes the signing secret, issuer, and token lifetimes explicit.
// That is safer than hiding token behavior in globals.
func NewJWTManager(secret, issuer string, accessTTL, refreshTTL time.Duration) (*JWTManager, error) {
	const op = "jwt.new"

	secret = strings.TrimSpace(secret)
	issuer = strings.TrimSpace(issuer)

	if len(secret) < 32 {
		return nil, validationError(op, "secret must be at least 32 bytes long")
	}
	if accessTTL <= 0 {
		return nil, validationError(op, "access token ttl must be greater than zero")
	}
	if refreshTTL <= 0 {
		return nil, validationError(op, "refresh token ttl must be greater than zero")
	}

	return &JWTManager{
		secret:     []byte(secret),
		issuer:     issuer,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		clock:      nowUTC,
	}, nil
}

// IssueAccessToken creates a signed access token for a logged-in user.
//
// Why this token exists:
// It allows the client to carry authenticated identity across requests
// without sending the password again.
func (m *JWTManager) IssueAccessToken(user models.User) (string, time.Time, error) {
	return m.issueToken(user, "access", m.accessTTL)
}

// IssueRefreshToken creates a signed refresh token for a logged-in user.
//
// Why this token exists:
// Access tokens should stay short-lived. Refresh tokens allow the client
// to obtain a new access token without asking the user to log in again.
func (m *JWTManager) IssueRefreshToken(user models.User) (string, time.Time, error) {
	return m.issueToken(user, "refresh", m.refreshTTL)
}

// RefreshAccessToken validates a refresh token and returns a new access token.
//
// Why this exists:
// Access tokens should expire. Refresh tokens let the client extend the session
// safely without storing the password.
func (m *JWTManager) RefreshAccessToken(refreshToken string) (string, time.Time, error) {
	const op = "jwt.refresh_access_token"

	claims, err := m.VerifyRefreshToken(refreshToken)
	if err != nil {
		return "", time.Time{}, err
	}

	if claims.Subject == "" {
		return "", time.Time{}, unauthorizedError(op, "invalid refresh token")
	}

	user := models.User{
		Id:    claims.Subject,
		Email: claims.Email,
	}

	return m.IssueAccessToken(user)
}

// VerifyAccessToken validates a token and ensures that it is an access token.
func (m *JWTManager) VerifyAccessToken(token string) (AccessTokenClaims, error) {
	return m.verifyToken(token, "access")
}

// VerifyRefreshToken validates a token and ensures that it is a refresh token.
func (m *JWTManager) VerifyRefreshToken(token string) (AccessTokenClaims, error) {
	return m.verifyToken(token, "refresh")
}

// issueToken is the shared internal token creation flow.
//
// Why this helper exists:
// Access and refresh tokens use the same signing machinery and only differ
// in token type and expiration time.
func (m *JWTManager) issueToken(user models.User, tokenType string, ttl time.Duration) (string, time.Time, error) {
	const op = "jwt.issue_token"

	if strings.TrimSpace(user.Id) == "" {
		return "", time.Time{}, validationError(op, "user id is required")
	}
	if strings.TrimSpace(user.Email) == "" {
		return "", time.Time{}, validationError(op, "user email is required")
	}

	now := m.clock().UTC()
	expiresAt := now.Add(ttl)

	jti, err := newUUIDString()
	if err != nil {
		return "", time.Time{}, err
	}

	claims := AccessTokenClaims{
		Subject:   user.Id,
		Email:     strings.ToLower(strings.TrimSpace(user.Email)),
		TokenType: tokenType,
		Issuer:    m.issuer,
		JWTID:     jti,
		IssuedAt:  now.Unix(),
		ExpiresAt: expiresAt.Unix(),
	}

	token, err := m.encode(claims)
	if err != nil {
		return "", time.Time{}, err
	}

	return token, expiresAt, nil
}

// verifyToken validates the signature and the claims for a token.
func (m *JWTManager) verifyToken(token string, expectedType string) (AccessTokenClaims, error) {
	const op = "jwt.verify_token"

	token = strings.TrimSpace(token)
	if token == "" {
		return AccessTokenClaims{}, unauthorizedError(op, "token is required")
	}

	header, claims, signingInput, signature, err := m.decode(token)
	if err != nil {
		return AccessTokenClaims{}, unauthorizedError(op, "invalid token")
	}

	if header.Algorithm != "HS256" {
		return AccessTokenClaims{}, unauthorizedError(op, "unsupported token algorithm")
	}
	if header.Type != "JWT" {
		return AccessTokenClaims{}, unauthorizedError(op, "invalid token type")
	}
	if claims.TokenType != expectedType {
		return AccessTokenClaims{}, unauthorizedError(op, "unexpected token purpose")
	}
	if m.issuer != "" && claims.Issuer != m.issuer {
		return AccessTokenClaims{}, unauthorizedError(op, "invalid token issuer")
	}

	if !m.verifySignature(signingInput, signature) {
		return AccessTokenClaims{}, unauthorizedError(op, "invalid token signature")
	}

	now := m.clock().UTC().Unix()
	if claims.ExpiresAt <= 0 || now >= claims.ExpiresAt {
		return AccessTokenClaims{}, unauthorizedError(op, "token expired")
	}
	if claims.IssuedAt <= 0 {
		return AccessTokenClaims{}, unauthorizedError(op, "invalid token issued-at time")
	}

	// Small clock skew tolerance prevents false negatives when server clocks drift slightly.
	const clockSkewSeconds = int64(60)
	if claims.IssuedAt > now+clockSkewSeconds {
		return AccessTokenClaims{}, unauthorizedError(op, "token issued in the future")
	}

	return claims, nil
}

// encode turns claims into a signed JWT string.
//
// JWT format:
// base64url(header).base64url(payload).base64url(signature)
func (m *JWTManager) encode(claims AccessTokenClaims) (string, error) {
	header := JWTHeader{
		Algorithm: "HS256",
		Type:      "JWT",
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", internalError("jwt.encode", err)
	}

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", internalError("jwt.encode", err)
	}

	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerPart + "." + payloadPart

	signature := m.sign([]byte(signingInput))
	signaturePart := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + signaturePart, nil
}

// decode parses a JWT string into its parts.
//
// Why this helper exists:
// Verification needs the header, payload, and raw signing input separately.
func (m *JWTManager) decode(token string) (JWTHeader, AccessTokenClaims, string, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return JWTHeader{}, AccessTokenClaims{}, "", nil, errors.New("invalid token format")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return JWTHeader{}, AccessTokenClaims{}, "", nil, err
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return JWTHeader{}, AccessTokenClaims{}, "", nil, err
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return JWTHeader{}, AccessTokenClaims{}, "", nil, err
	}

	var header JWTHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return JWTHeader{}, AccessTokenClaims{}, "", nil, err
	}

	var claims AccessTokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return JWTHeader{}, AccessTokenClaims{}, "", nil, err
	}

	signingInput := parts[0] + "." + parts[1]
	return header, claims, signingInput, signature, nil
}

// sign creates a HMAC-SHA256 signature.
//
// HMAC means Hash-based Message Authentication Code.
// SHA means Secure Hash Algorithm.
// 256 means 256-bit output.
//
// Why this is used:
// It lets the server sign the token with a secret only it knows.
func (m *JWTManager) sign(message []byte) []byte {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

// verifySignature compares the expected signature with the provided signature
// using constant-time comparison.
//
// Why this matters:
// Constant-time comparison reduces timing-based attacks that try to learn
// information about the secret signature value.
func (m *JWTManager) verifySignature(signingInput string, provided []byte) bool {
	expected := m.sign([]byte(signingInput))
	return hmac.Equal(expected, provided)
}

// randomTokenSecret can be used in tests or bootstrap flows to generate a
// random signing secret.
//
// Why this helper exists:
// The JWT secret must be unpredictable. A weak secret would break the system.
func randomTokenSecret(length int) (string, error) {
	if length < 32 {
		length = 32
	}

	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
