package config

import (
	"os"
	"strconv"
	"time"
)

// Config is loaded once at startup.
type Config struct {
	Port string
	DB   DBConfig
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
	}
	return cfg, Validate(cfg)
}

// ---------------HELPERS--------------------

// Optional env with fallback
// Example: APP_ENV=production
func getEnv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// Required env (crashes app if missing)
// Example: DATABASE_DSN=postgres://user:pass@host/db
func mustGetEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic("environment variable " + key + " is not set")
	}
	return value
}

// Parses int env vars
// Example: DB_MAX_OPEN_CONNS=25
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

// Parses duration env vars
// Example: DB_CONN_MAX_LIFETIME=1h
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
