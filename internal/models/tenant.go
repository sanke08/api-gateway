package models

import "time"

type TenantStatus string

const (
	TenantStatusActive   TenantStatus = "active"
	TenantStatusInactive TenantStatus = "inactive"
)

// Tenant represents a company using the API Gateway.
type Tenant struct {
	ID     string       `json:"id"`     // Unique identifier for the tenant
	Name   string       `json:"name"`   // Name of the tenant
	Domain string       `json:"domain"` // Domain of the tenant
	Status TenantStatus `json:"status"` // Status of the tenant

	CreatedAt time.Time `json:"created_at"` // Creation time of the tenant
	UpdatedAt time.Time `json:"updated_at"` // Update time of the tenant
}
