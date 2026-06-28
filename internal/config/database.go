package config

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type DBConfig struct {
	DSN             string        // SQL connection string (example: postgres://user:pass@localhost:5432/app_db)
	Driver          string        // SQL driver (example: postgres)
	MaxIdleConns    int           // Maximum number of idle connections
	MaxOpenConns    int           // Maximum number of open connections
	ConnMaxLifetime time.Duration // Maximum lifetime of a connection
}

// NewDatabase opens the connection pool and immediately verifies connectivity
// with a Ping. Failing fast at startup (rather than on the first real query)
// gives a clear error message and prevents the gateway from accepting traffic
// when it cannot reach its database.
func NewDatabase(cfg *Config) (*sql.DB, error) {
	db, err := sql.Open(cfg.DB.Driver, cfg.DB.DSN)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxIdleConns(cfg.DB.MaxIdleConns)
	db.SetMaxOpenConns(cfg.DB.MaxOpenConns)
	db.SetConnMaxLifetime(cfg.DB.ConnMaxLifetime)

	// Ping verifies that the DSN is reachable. sql.Open never dials; this is
	// the first real network call. Give it 5 s so a slow container start
	// doesn't immediately fail, but a genuinely wrong DSN fails quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return db, nil
}
