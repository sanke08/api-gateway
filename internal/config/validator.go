package config

import (
	"errors"

	"github.com/sanke08/api_gateway/internal/observability"
)

func Validate(cfg *Config) error {
	if cfg.Port == "" {
		observability.Error("Port is required")
		return errors.New("port is required")
	}
	if cfg.DB.DSN == "" {
		observability.Error("DSN is required")
		return errors.New("DSN is required")
	}
	if cfg.DB.Driver == "" {
		observability.Error("Driver is required")
		return errors.New("driver is required")
	}
	if cfg.DB.MaxIdleConns == 0 {
		observability.Error("MaxIdleConns is required")
		return errors.New("max idle connections is required")
	}
	if cfg.DB.MaxOpenConns == 0 {
		observability.Error("MaxOpenConns is required")
		return errors.New("max open connections is required")
	}

	return nil
}
