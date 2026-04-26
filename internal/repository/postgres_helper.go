package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/sanke08/api_gateway/internal/models"
)

// sqlExecutor defines a common interface implemented by both *sql.DB and *sql.Tx.
//
// Why this exists:
// The standard library types *sql.DB (connection pool) and *sql.Tx (transaction)
// both expose the same query execution methods (ExecContext, QueryContext,
// QueryRowContext), but they do not share a common interface.
//
// This interface allows repository code to work with either a database connection
// or a transaction transparently, without duplicating logic.
//
// How it is used:
// Repository implementations select the correct executor at runtime:
//
//	func (r *PostgresTenantRepo) executor() sqlExecutor {
//	    if r.tx != nil {
//	        return r.tx // execute within transaction
//	    }
//	    return r.db // execute using default connection
//	}
//
// This enables the same repository methods to run:
//
//   - normally (using *sql.DB)
//   - inside a transaction (using *sql.Tx via WithTx)
//
// Benefits:
// - Avoids repeating "if tx != nil" checks in every method
// - Keeps repository code clean and consistent
// - Makes transaction support reusable across all repositories
//
// Important:
// If repository methods directly use r.db instead of this interface,
// transaction support (WithTx) will not work correctly.
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func trimString(s string) string {
	return strings.TrimSpace(s)
}

func trimStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

// nullStringPtr and nullTimePtr convert SQL nullable values into Go pointers, mapping NULL → nil and value → pointer.
func nullStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func nullTimePtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	return &nt.Time
}

// normalizeTenantForCreate validates a tenant before insertion.
func normalizeTenantForCreate(tenant models.Tenant) (models.Tenant, error) {
	const op = "tenant.create"

	// tenant.ID = trimString(tenant.ID)
	tenant.Name = trimString(tenant.Name)
	tenant.Slug = trimString(tenant.Slug)

	// if tenant.ID == "" {
	// 	return models.Tenant{}, validationError(op, "tenant", "id is required")
	// }
	if tenant.Name == "" {
		return models.Tenant{}, validationError(op, "tenant", "name is required")
	}
	if tenant.Slug == "" {
		return models.Tenant{}, validationError(op, "tenant", "Slug is required")
	}
	if tenant.Status == "" {
		tenant.Status = models.TenantStatusActive
	}

	if tenant.Status != models.TenantStatusActive && tenant.Status != models.TenantStatusSuspended {
		return models.Tenant{}, validationError(op, "tenant", "invalid status")
	}

	return tenant, nil
}

// normalizeUserForCreate validates a user before insertion.
func normalizeUserForCreate(user models.User) (models.User, error) {
	const op = "user.create"

	// user.ID = trimString(user.ID)
	user.Email = trimString(user.Email)
	user.PasswordHash = trimString(user.PasswordHash)

	// if user.ID == "" {
	// 	return models.User{}, validationError(op, "user", "id is required")
	// }
	if user.Email == "" {
		return models.User{}, validationError(op, "user", "email is required")
	}
	if user.PasswordHash == "" {
		return models.User{}, validationError(op, "user", "password_hash is required")
	}

	return user, nil
}

// normalizeMembershipForCreate validates a tenant membership before insertion.
func normalizeMembershipForCreate(m models.TenantMembership) (models.TenantMembership, error) {
	const op = "membership.create"

	m.UserId = trimString(m.UserId)
	m.TenantId = trimString(m.TenantId)

	if m.UserId == "" {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "user_id is required")
	}
	if m.TenantId == "" {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "tenant_id is required")
	}
	if m.CreatedAt.IsZero() {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "created_at is required")
	}
	if m.UpdatedAt.IsZero() {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "updated_at is required")
	}
	if m.Role == "" {
		m.Role = models.MembershipRoleMember
	}
	if m.Role != models.MembershipRoleOwner &&
		m.Role != models.MembershipRoleAdmin &&
		m.Role != models.MembershipRoleMember {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "invalid role")
	}
	if m.Status == "" {
		m.Status = models.MembershipStatusActive
	}
	if m.Status != models.MembershipStatusActive &&
		m.Status != models.MembershipStatusInvited &&
		m.Status != models.MembershipStatusSuspended {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "invalid status")
	}

	return m, nil
}

// normalizeAPIKeyForCreate validates an API key before insertion.
func normalizeAPIKeyForCreate(key models.APIKey) (models.APIKey, error) {
	const op = "apikey.create"

	// key.ID = trimString(key.ID)
	key.TenantID = trimString(key.TenantID)
	key.KeyHash = trimString(key.KeyHash)
	key.Description = trimStringPtr(key.Description)

	// if key.ID == "" {
	// 	return models.APIKey{}, validationError(op, "api_key", "id is required")
	// }
	if key.TenantID == "" {
		return models.APIKey{}, validationError(op, "api_key", "tenant_id is required")
	}
	if key.KeyHash == "" {
		return models.APIKey{}, validationError(op, "api_key", "key_hash is required")
	}
	if key.CreatedAt.IsZero() {
		return models.APIKey{}, validationError(op, "api_key", "created_at is required")
	}
	if key.UpdatedAt.IsZero() {
		return models.APIKey{}, validationError(op, "api_key", "updated_at is required")
	}

	return key, nil
}

// validateUsageRecordForCreate validates a usage record before insertion.
func validateUsageRecordForCreate(record models.Usage) error {
	const op = "usage.log"

	record.TenantID = trimString(record.TenantID)
	record.APIKeyID = trimString(record.APIKeyID)
	record.Endpoint = trimString(record.Endpoint)
	record.Method = trimString(record.Method)

	if record.TenantID == "" {
		return validationError(op, "usage", "tenant_id is required")
	}
	if record.APIKeyID == "" {
		return validationError(op, "usage", "api_key_id is required")
	}
	if record.Endpoint == "" {
		return validationError(op, "usage", "Endpoint is required")
	}
	if record.Method == "" {
		return validationError(op, "usage", "method is required")
	}
	if record.Timestamp.IsZero() {
		return validationError(op, "usage", "timestamp is required")
	}

	return nil
}
