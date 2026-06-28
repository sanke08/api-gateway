package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/middleware"
	"github.com/sanke08/api_gateway/internal/ratelimit"
)

// Config is loaded once at startup and passed to every component that needs it.
// All fields have safe defaults so the gateway boots with only DB_DSN set.
type Config struct {
	Port      string
	DB        DBConfig
	JWT       JWTConfig
	RateLimit ratelimit.Rule           // Reuse the real type — no separate RateLimitConfig needed.
	CORS      middleware.CORSPolicy    // Reuse the real type — no separate CORSConfig needed.
	Security  middleware.SecurityHeadersPolicy // Same — gets all fields including IncludeSubdomains, Preload, etc.
	Cache     CacheConfig
	Upstreams []UpstreamConfig
}

// JWTConfig holds the settings for the hand-rolled HS256 token manager.
//
// Why JWT_SECRET is required:
// The secret is the cryptographic key that signs every token. If it is blank,
// any attacker can forge tokens. So we keep it required (mustGetEnv) while
// everything else has a safe default.
type JWTConfig struct {
	Secret     string        // Required. At least 32 bytes.
	Issuer     string        // Identifies this gateway in the token's "iss" claim.
	AccessTTL  time.Duration // How long an access token lives.  Default: 15m.
	RefreshTTL time.Duration // How long a refresh token lives. Default: 7 days.
}

// RateLimit uses ratelimit.Rule directly — no wrapper needed, the fields are identical.
// CORS uses middleware.CORSPolicy directly — reusing avoids duplication and gives us
// all fields (IncludeSubdomains, Preload, PermissionsPolicy, etc.) for free.
// Security uses middleware.SecurityHeadersPolicy for the same reason.

// CacheConfig controls the optional remote cache layer.
//
// If RemoteURL is blank, the gateway runs local-only (MemoryStore).
// If RemoteURL is set, it wraps the remote client in a HybridStore that
// tries the remote first and falls back to memory on failure.
type CacheConfig struct {
	RemoteURL string        // Optional. HTTP URL of the remote cache service.
	Timeout   time.Duration // Request timeout for the remote cache. Default: 2s.
	Namespace string        // Key prefix to avoid collisions. Default: "gateway".
	Token     string        // Optional Bearer token for the remote cache service.
}

// UpstreamConfig is one tenant-to-backend mapping loaded from UPSTREAMS_JSON.
//
// Why JSON in an env var:
// It lets you define multiple upstreams without adding a DB table or a config
// file. In production you would move these to a database table (TODO R3).
//
// Example env value (must be valid JSON):
//
//	UPSTREAMS_JSON=[{"tenant_id":"t1","name":"orders","base_url":"http://orders:8080"}]
type UpstreamConfig struct {
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	StripPrefix string `json:"strip_prefix"` // Optional path prefix to strip before forwarding.
	AddPrefix   string `json:"add_prefix"`   // Optional path prefix to add before forwarding.
}

func Load() (*Config, error) {
	cfg := &Config{
		Port: getEnv("PORT", "8080"),

		DB: DBConfig{
			DSN:             mustGetEnv("DB_DSN"),
			Driver:          getEnv("DB_DRIVER", "postgres"),
			MaxIdleConns:    getEnvAsInt("DB_MAX_IDLE_CONNS", 10),
			MaxOpenConns:    getEnvAsInt("DB_MAX_OPEN_CONNS", 100),
			ConnMaxLifetime: getEnvAsDuration("DB_CONN_MAX_LIFETIME", 1*time.Hour),
		},

		JWT: JWTConfig{
			Secret:     mustGetEnv("JWT_SECRET"),
			Issuer:     getEnv("JWT_ISSUER", "api-gateway"),
			AccessTTL:  getEnvAsDuration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL: getEnvAsDuration("JWT_REFRESH_TTL", 7*24*time.Hour),
		},

		RateLimit: ratelimit.Rule{
			TokensPerPeriod: getEnvAsInt("RATE_TOKENS_PER_PERIOD", 100),
			Period:          getEnvAsDuration("RATE_PERIOD", time.Minute),
			Capacity:        getEnvAsInt("RATE_CAPACITY", 0), // 0 → limiter uses TokensPerPeriod as burst
			Cost:            getEnvAsInt("RATE_COST", 1),
		},

		CORS: middleware.CORSPolicy{
			AllowAllOrigins:  getEnvAsBool("CORS_ALLOW_ALL_ORIGINS", false),
			AllowedOrigins:   getEnvAsStringSlice("CORS_ALLOWED_ORIGINS", nil),
			AllowedMethods:   getEnvAsStringSlice("CORS_ALLOWED_METHODS", []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}),
			AllowedHeaders:   getEnvAsStringSlice("CORS_ALLOWED_HEADERS", []string{"Content-Type", "Authorization", "X-Tenant-ID", "X-API-Key", "X-Request-ID"}),
			ExposedHeaders:   getEnvAsStringSlice("CORS_EXPOSED_HEADERS", []string{"X-Request-ID"}),
			AllowCredentials: getEnvAsBool("CORS_ALLOW_CREDENTIALS", false),
			// MaxAge is time.Duration in CORSPolicy; convert seconds from env.
			MaxAge: time.Duration(getEnvAsInt("CORS_MAX_AGE", 600)) * time.Second,
		},

		Security: middleware.SecurityHeadersPolicy{
			EnableHSTS:            getEnvAsBool("SEC_HSTS", false),
			HSTSMaxAge:            getEnvAsInt("SEC_HSTS_MAX_AGE", 31536000),
			FrameOptions:          getEnv("SEC_FRAME_OPTIONS", "DENY"),
			ContentTypeNosniff:    getEnvAsBool("SEC_CONTENT_TYPE_NOSNIFF", true),
			ReferrerPolicy:        getEnv("SEC_REFERRER_POLICY", "strict-origin-when-cross-origin"),
			ContentSecurityPolicy: getEnv("SEC_CSP", ""),
			// IncludeSubdomains, Preload, PermissionsPolicy, CrossOrigin* left at
			// zero-value (false/"") — add env vars for them when needed.
		},

		Cache: CacheConfig{
			RemoteURL: getEnv("CACHE_REMOTE_URL", ""),
			Timeout:   getEnvAsDuration("CACHE_TIMEOUT", 2*time.Second),
			Namespace: getEnv("CACHE_NAMESPACE", "gateway"),
			Token:     getEnv("CACHE_TOKEN", ""),
		},

		Upstreams: loadUpstreams(),
	}
	return cfg, Validate(cfg)
}

// loadUpstreams parses UPSTREAMS_JSON into a slice of UpstreamConfig.
// Returns nil (no upstreams) if the env var is blank or missing.
func loadUpstreams() []UpstreamConfig {
	raw := strings.TrimSpace(os.Getenv("UPSTREAMS_JSON"))
	if raw == "" {
		return nil
	}
	var out []UpstreamConfig
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		panic("UPSTREAMS_JSON is not valid JSON: " + err.Error())
	}
	return out
}

// ---------------HELPERS--------------------

// Optional env with fallback
func getEnv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// Required env — panics if missing, because missing required config is a
// programmer/ops error that should fail loudly at startup, not silently at
// runtime.
func mustGetEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic("environment variable " + key + " is not set")
	}
	return value
}

// Parses int env vars
func getEnvAsInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	i, err := strconv.Atoi(value)
	if err != nil {
		panic("environment variable " + key + " is not an integer")
	}

	return i
}

// Parses duration env vars (e.g. "15m", "1h", "7d" is not valid — use "168h")
func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	i, err := time.ParseDuration(value)
	if err != nil {
		panic("environment variable " + key + " is not a duration")
	}

	return i
}

// Parses bool env vars — "true", "1", "yes" → true; anything else → false.
func getEnvAsBool(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}

// Parses a comma-separated env var into a string slice.
// Returns defaultValue if the env var is blank.
func getEnvAsStringSlice(key string, defaultValue []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

