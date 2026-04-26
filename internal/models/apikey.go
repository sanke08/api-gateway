package models

import "time"

// APIKey represents a machine credential for a tenant.
//
// Why the raw key is not stored:
// The system should only store the hash. The raw key is shown once during creation
// and then discarded.
type APIKey struct {
	ID       string `json:"id"`        // Unique identifier for the API key
	TenantID string `json:"tenant_id"` // ID of the tenant the API key belongs to
	KeyHash  string `json:"key"`       // The API key itself
	Active   bool   `json:"active"`    // Whether the API key is active

	// Description is an optional human-readable label for the API key.
	//
	// Why this exists:
	// API keys are opaque and indistinguishable to humans. This field helps
	// identify where a key is used (e.g., "production backend", "mobile app"),
	// making key management, rotation, and debugging safer.
	Description *string

	CreatedAt time.Time `json:"created_at"` // Creation time of the API key
	UpdatedAt time.Time `json:"updated_at"` // Update time of the API key
}
