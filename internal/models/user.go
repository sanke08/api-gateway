package models

import "time"

type User struct {
	ID       string `json:"id"`            // Unique identifier for the user
	TenantID string `json:"tenant_id"`     // ID of the tenant the user belongs to
	Email    string `json:"email"`         // Email of the user
	Password string `json:"password_hash"` // Hash of the user's password
	Role     string `json:"role"`          // Role of the user (admin, member)

	CreatedAt time.Time `json:"created_at"` // Creation time of the user
	UpdatedAt time.Time `json:"updated_at"` // Update time of the user
}
