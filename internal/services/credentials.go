package services

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// PasswordHasher defines the contract for password hashing and verification.
//
// Why this exists:
// The onboarding flow must never store raw passwords. It should only store
// a password hash that can be verified later during login.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) (bool, error)
}

// APIKeyGenerator defines the contract for generating and hashing API keys.
//
// Why this exists:
// Raw API keys are secrets. They must be shown once and never stored in raw form.
type APIKeyGenerator interface {
	Generate() (raw, hash string, err error)
}

// StandardPasswordHasher is a standard-library-only password hashing implementation.
//
// Important note:
// This is not bcrypt or argon2id. It is a PBKDF2-HMAC-SHA256 style derivation
// implemented only with the standard library, because your project forbids
// third-party libraries.
type StandardPasswordHasher struct {
	Iterations int
	SaltSize   int
	KeySize    int
}

func NewStandardPasswordHasher() *StandardPasswordHasher {
	return &StandardPasswordHasher{
		Iterations: 210000,
		SaltSize:   16,
		KeySize:    32,
	}
}

// Hash converts a raw password into a storage-safe encoded string.
//
// Format:
// pbkdf2_sha256$<iterations>$<salt-b64>$<derived-key-b64>
func (h *StandardPasswordHasher) Hash(password string) (string, error) {
	if password == "" {
		return "", validationError("password.hash", "password is required")
	}

	salt := make([]byte, h.SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", internalError("password.hash", err)
	}

	derived := pbkdf2Key([]byte(password), salt, h.Iterations, h.KeySize)

	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedKey := base64.RawStdEncoding.EncodeToString(derived)

	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", h.Iterations, encodedSalt, encodedKey), nil
}

// Verify checks whether a raw password matches a stored encoded hash.
func (h *StandardPasswordHasher) Verify(password, encoded string) (bool, error) {
	parts := splitEncodedHash(encoded)
	if len(parts) != 4 {
		return false, validationError("password.verify", "invalid password hash format")
	}

	var iterations int
	if _, err := fmt.Sscanf(parts[1], "%d", &iterations); err != nil {
		return false, validationError("password.verify", "invalid iteration count")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, validationError("password.verify", "invalid salt encoding")
	}

	stored, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, validationError("password.verify", "invalid key encoding")
	}

	derived := pbkdf2Key([]byte(password), salt, iterations, len(stored))
	return hmac.Equal(derived, stored), nil
}
func splitEncodedHash(encoded string) []string {
	return splitByDollar(encoded)
}

// APIKeyGenerator implementation.

type StandardAPIKeyGenerator struct {
	Prefix    string
	SecretLen int
}

func NewStandardAPIKeyGenerator() *StandardAPIKeyGenerator {
	return &StandardAPIKeyGenerator{
		Prefix:    "gw_live_",
		SecretLen: 32,
	}
}

// Generate creates a raw API key and the hash that should be stored.
//
// Why two values are returned:
// - raw = shown once to the user
// - hash = stored in the database
func (g *StandardAPIKeyGenerator) Generate() (string, string, error) {
	rawBytes := make([]byte, g.SecretLen)
	if _, err := io.ReadFull(rand.Reader, rawBytes); err != nil {
		return "", "", internalError("apikey.generate", err)
	}

	raw := g.Prefix + base64.RawURLEncoding.EncodeToString(rawBytes)
	hashBytes := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(hashBytes[:])

	return raw, hash, nil
}

// newUUIDString creates a UUID-like identifier using the standard library only.
//
// Why this exists:
// The repositories expect application-generated IDs. This keeps ID generation
// explicit and avoids database-specific UUID functions in business code.
func newUUIDString() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", internalError("uuid.generate", err)
	}

	// Set version to 4 and variant bits according to RFC 4122.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	), nil
}

// pbkdf2Key is a standard-library implementation of PBKDF2 with HMAC-SHA256.
//
// Why this is here:
// The project forbids third-party libraries, so we implement the derivation ourselves.
func pbkdf2Key(password, salt []byte, iterations, keyLen int) []byte {
	hLen := 32
	numBlocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, numBlocks*hLen)

	for block := 1; block <= numBlocks; block++ {
		u := prf(password, append(salt, byte(block>>24), byte(block>>16), byte(block>>8), byte(block)))
		t := make([]byte, len(u))
		copy(t, u)

		for i := 1; i < iterations; i++ {
			u = prf(password, u)
			for j := range t {
				t[j] ^= u[j]
			}
		}

		out = append(out, t...)
	}

	return out[:keyLen]
}

func prf(key, msg []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(msg)
	return mac.Sum(nil)
}

// splitByDollar avoids importing strings into the hashing code for one tiny helper.
func splitByDollar(s string) []string {
	out := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '$' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// nowUTC is used to keep timestamps consistent in generated records.
func nowUTC() time.Time {
	return time.Now().UTC()
}
