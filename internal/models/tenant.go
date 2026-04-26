package models

import "time"

// TenantStatus represents the lifecycle state of a tenant.
//
// Why this exists:
// A tenant is the ownership boundary of the SaaS. We need to know whether
// that tenant is allowed to operate, suspended, or otherwise restricted.
type TenantStatus string

const (
	// TenantStatusActive means the tenant is allowed to use the platform.
	TenantStatusActive TenantStatus = "active"

	// TenantStatusSuspended means the tenant exists but must not be allowed to operate.
	TenantStatusSuspended TenantStatus = "suspended"
)

// MembershipRole represents what a user can do inside a specific tenant.
//
// Why this is separate from a user identity:
// A person can belong to multiple businesses, and the same person may have
// different permissions in each business. Role is a relationship property, not a global property of the person.
type MembershipRole string

const (
	// MembershipRoleOwner means full control of that tenant.
	MembershipRoleOwner MembershipRole = "owner"

	// MembershipRoleAdmin means administrative access for that tenant.
	MembershipRoleAdmin MembershipRole = "admin"

	// MembershipRoleMember means normal access without administrative control.
	MembershipRoleMember MembershipRole = "member"
)

// MembershipStatus represents whether the relationship between a user and a tenant is currently usable.
//
// Why this exists:
// A user can be invited, active, or suspended inside one tenant without affecting
// their membership in another tenant.
type MembershipStatus string

const (
	// MembershipStatusActive means the membership is usable.
	MembershipStatusActive MembershipStatus = "active"

	// MembershipStatusInvited means the user has been linked to the tenant but
	// may still need to accept an invitation later.
	MembershipStatusInvited MembershipStatus = "invited"

	// MembershipStatusSuspended means access through this membership is blocked.
	MembershipStatusSuspended MembershipStatus = "suspended"
)

// Tenant is the core ownership boundary in the system.
//
// Why this exists:
// Every request, API key, permission, and usage record must be attached to a tenant
// so isolation stays explicit and enforceable.
type Tenant struct {
	Id     string       `json:"id"`     // Unique identifier for the tenant
	Name   string       `json:"name"`   // Name of the tenant
	Slug   string       `json:"slug"`   // Slug of the tenant
	Status TenantStatus `json:"status"` // Status of the tenant

	CreatedAt time.Time `json:"created_at"` // Creation time of the tenant
	UpdatedAt time.Time `json:"updated_at"` // Update time of the tenant
}
