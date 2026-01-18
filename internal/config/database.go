package config

import (
	"database/sql"
	"time"
)

type DBConfig struct {
	DSN             string        // SQL connection string (example: postgres://user:pass@localhost:5432/app_db)
	Driver          string        // SQL driver (example: postgres)
	MaxIdleConns    int           // Maximum number of idle connections
	MaxOpenConns    int           // Maximum number of open connections
	ConnMaxLifetime time.Duration // Maximum lifetime of a connection
}

func NewDatabase(cfg *Config) (*sql.DB, error) {
	db, err := sql.Open(cfg.DB.Driver, cfg.DB.DSN)

	if err != nil {
		return nil, err
	}

	db.SetMaxIdleConns(cfg.DB.MaxIdleConns)
	db.SetMaxOpenConns(cfg.DB.MaxOpenConns)
	db.SetConnMaxLifetime(cfg.DB.ConnMaxLifetime)

	return db, nil

}
