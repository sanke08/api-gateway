package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

// postgresTenantMembershipRepo stores the relationship between a user and a tenant.
type postgresTenantMembershipRepo struct {
	db sqlExecutor
}

// NewPostgresTenantMembershipRepo creates the PostgreSQL-backed membership repository.
func NewPostgresTenantMembershipRepo(db *sql.DB) TenantMembershipRepository {
	return &postgresTenantMembershipRepo{db: db}
}

// WithTx rebinds the repository to a transaction.
func (r *postgresTenantMembershipRepo) WithTx(tx *sql.Tx) TenantMembershipRepository {
	return &postgresTenantMembershipRepo{db: tx}
}

// Create inserts a tenant membership and returns the stored row.
//
// Why this matters:
// A user can belong to multiple tenants, and this table is the source of truth
// for that relationship. It also stores role and status for that specific tenant.
func (r *postgresTenantMembershipRepo) Create(ctx context.Context, membership models.TenantMembership) (models.TenantMembership, error) {
	const op = "membership.create"

	normalized, err := normalizeMembershipForCreate(membership)
	if err != nil {
		return models.TenantMembership{}, err
	}

	const q = `
		INSERT INTO tenant_memberships (user_id, tenant_id, role, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, tenant_id, role, status, created_at, updated_at
	`

	var out models.TenantMembership
	var role string
	var status string

	err = r.db.QueryRowContext(ctx, q,
		normalized.UserId,
		normalized.TenantId,
		string(normalized.Role),
		string(normalized.Status),
	).Scan(
		&out.ID,
		&out.UserId,
		&out.TenantId,
		&role,
		&status,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return models.TenantMembership{}, classifySQLError(op, "tenant_membership", err, true)
	}

	out.Role = models.MembershipRole(role)
	out.Status = models.MembershipStatus(status)
	return out, nil
}

// GetByID loads a membership by its identifier.
func (r *postgresTenantMembershipRepo) GetByID(ctx context.Context, id string) (models.TenantMembership, error) {
	const op = "membership.get_by_id"

	id = trimString(id)
	if id == "" {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "id is required")
	}

	const q = `
		SELECT id, user_id, tenant_id, role, status, created_at, updated_at
		FROM tenant_memberships
		WHERE id = $1
	`

	var out models.TenantMembership
	var role string
	var status string

	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&out.ID,
		&out.UserId,
		&out.TenantId,
		&role,
		&status,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return models.TenantMembership{}, classifySQLError(op, "tenant_membership", err, true)
	}

	out.Role = models.MembershipRole(role)
	out.Status = models.MembershipStatus(status)
	return out, nil
}

// GetByUserAndTenant loads one membership for one user inside one tenant.
func (r *postgresTenantMembershipRepo) GetByUserAndTenant(ctx context.Context, userID, tenantID string) (models.TenantMembership, error) {
	const op = "membership.get_by_user_and_tenant"

	userID = trimString(userID)
	tenantID = trimString(tenantID)

	if userID == "" {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "user_id is required")
	}
	if tenantID == "" {
		return models.TenantMembership{}, validationError(op, "tenant_membership", "tenant_id is required")
	}

	const q = `
		SELECT id, user_id, tenant_id, role, status, created_at, updated_at
		FROM tenant_memberships
		WHERE user_id = $1 AND tenant_id = $2
	`

	var out models.TenantMembership
	var role string
	var status string

	err := r.db.QueryRowContext(ctx, q, userID, tenantID).Scan(
		&out.ID,
		&out.UserId,
		&out.TenantId,
		&role,
		&status,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return models.TenantMembership{}, classifySQLError(op, "tenant_membership", err, true)
	}

	out.Role = models.MembershipRole(role)
	out.Status = models.MembershipStatus(status)
	return out, nil
}

// ListByUser returns all tenant memberships for one user.
func (r *postgresTenantMembershipRepo) ListByUser(ctx context.Context, userID string) ([]models.TenantMembership, error) {
	const op = "membership.list_by_user"

	userID = trimString(userID)
	if userID == "" {
		return nil, validationError(op, "tenant_membership", "user_id is required")
	}

	const q = `
		SELECT id, user_id, tenant_id, role, status, created_at, updated_at
		FROM tenant_memberships
		WHERE user_id = $1
		ORDER BY created_at ASC, id ASC
	`

	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, classifySQLError(op, "tenant_membership", err, false)
	}
	defer rows.Close()

	out := make([]models.TenantMembership, 0)
	for rows.Next() {
		var item models.TenantMembership
		var role string
		var status string

		if err := rows.Scan(
			&item.ID,
			&item.UserId,
			&item.TenantId,
			&role,
			&status,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, classifySQLError(op, "tenant_membership", err, false)
		}

		item.Role = models.MembershipRole(role)
		item.Status = models.MembershipStatus(status)
		out = append(out, item)
	}

	if err := rows.Err(); err != nil {
		return nil, classifySQLError(op, "tenant_membership", err, false)
	}

	return out, nil
}

// ListByTenant returns all memberships that belong to one tenant.
func (r *postgresTenantMembershipRepo) ListByTenant(ctx context.Context, tenantID string) ([]models.TenantMembership, error) {
	const op = "membership.list_by_tenant"

	tenantID = trimString(tenantID)
	if tenantID == "" {
		return nil, validationError(op, "tenant_membership", "tenant_id is required")
	}

	const q = `
		SELECT id, user_id, tenant_id, role, status, created_at, updated_at
		FROM tenant_memberships
		WHERE tenant_id = $1
		ORDER BY created_at ASC, id ASC
	`

	rows, err := r.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, classifySQLError(op, "tenant_membership", err, false)
	}
	defer rows.Close()

	out := make([]models.TenantMembership, 0)
	for rows.Next() {
		var item models.TenantMembership
		var role string
		var status string

		if err := rows.Scan(
			&item.ID,
			&item.UserId,
			&item.TenantId,
			&role,
			&status,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, classifySQLError(op, "tenant_membership", err, false)
		}

		item.Role = models.MembershipRole(role)
		item.Status = models.MembershipStatus(status)
		out = append(out, item)
	}

	if err := rows.Err(); err != nil {
		return nil, classifySQLError(op, "tenant_membership", err, false)
	}

	return out, nil
}
