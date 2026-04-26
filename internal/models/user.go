package models

import "time"

// User is the global identity record for one person.
//
// Why this design is correct for multi-business support:
// A user should not be tied to only one tenant. The same person may belong to
// several businesses. This table represents the person themselves, not their access.
//
// PasswordHash stores the hashed password only; the raw password must never be stored.

type User struct {
	Id           string `json:"id"`            // Unique identifier for the user
	Email        string `json:"email"`         // Email of the user
	PasswordHash string `json:"password_hash"` // Hash of the user's password

	CreatedAt time.Time `json:"created_at"` // Creation time of the user
	UpdatedAt time.Time `json:"updated_at"` // Update time of the user
}
