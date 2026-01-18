package repository

import "errors"

var (
	ErrTenantNotFound = errors.New("tenant not found")
	ErrTenantExists   = errors.New("tenant already exists")
	ErrUserNotFound   = errors.New("user not found")
	ErrUserExists     = errors.New("user already exists")
	ErrAPIKeyNotFound = errors.New("api key not found")
	ErrNoRowsAffected = errors.New("no rows affected")
	ErrDataIntegrity  = errors.New("data integrity violation")
)
