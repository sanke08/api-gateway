package models

import "time"

// TenantMembership connects one user to one tenant.
//
// Why this table is necessary:
// A user can belong to many tenants, and a tenant can have many users.
// The relationship also stores role and status, which are tenant-specific.
type TenantMembership struct {
	ID       string
	UserId   string
	TenantId string
	Role     MembershipRole
	Status   MembershipStatus

	CreatedAt time.Time
	UpdatedAt time.Time
}
