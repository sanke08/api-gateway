package models

import "time"

type APIKey struct {
	ID       string `json:"id"`        // Unique identifier for the API key
	TenantID string `json:"tenant_id"` // ID of the tenant the API key belongs to
	Key      string `json:"key"`       // The API key itself
	Active   bool   `json:"active"`    // Whether the API key is active

	CreatedAt time.Time `json:"created_at"` // Creation time of the API key
	UpdatedAt time.Time `json:"updated_at"` // Update time of the API key
}
