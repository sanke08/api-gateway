package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

// PostgresTenantRepo implements TenantRepository using Postgres
type PostgresTenantRepo struct {
	db sqlExecutor
}

// NewPostgresTenantRepo creates a new PostgresTenantRepo instance
func NewPostgresTenantRepo(db *sql.DB) TenantRepository {
	return &PostgresTenantRepo{db: db}
}

// WithTx rebinds the repository to a transaction.
func (r *PostgresTenantRepo) WithTx(tx *sql.Tx) TenantRepository {
	return &PostgresTenantRepo{db: tx}
}

func (r *PostgresTenantRepo) Create(ctx context.Context, tenant models.Tenant) (models.Tenant, error) {
	const op = "tenant.create"

	normalized, err := normalizeTenantForCreate(tenant)
	if err != nil {
		return models.Tenant{}, err
	}

	const q = `
		INSERT INTO tenants (name, slug, status)
		VALUES ($1, $2, $3)
		RETURNING id, name, slug, status, created_at, updated_at
	`

	var out models.Tenant
	var status string

	err = r.db.QueryRowContext(ctx, q,
		normalized.Name,
		normalized.Slug,
		string(normalized.Status),
	).Scan(
		&out.Id,
		&out.Name,
		&out.Slug,
		&status,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return models.Tenant{}, classifySQLError(op, "tenant", err, true)
	}

	out.Status = models.TenantStatus(status)
	return out, nil
}

func (r *PostgresTenantRepo) GetByID(ctx context.Context, id string) (models.Tenant, error) {

	const op = "tenant.get_by_id"

	id = trimString(id)
	if id == "" {
		return models.Tenant{}, validationError(op, "tenant", "id is required")
	}

	var t models.Tenant
	var status string

	query := `
		SELECT id, Slug, name, status, created_at, updated_at
		FROM tenants
		WHERE id = $1
	`

	err := r.db.QueryRowContext(
		ctx, query,
		id,
	).Scan(&t.Id, &t.Slug, &t.Name, &status, &t.CreatedAt, &t.UpdatedAt)

	if err != nil {
		return models.Tenant{}, classifySQLError(op, "tenant", err, true)
	}

	t.Status = models.TenantStatus(status)

	return t, nil
}

func (r *PostgresTenantRepo) GetBySlug(ctx context.Context, Slug string) (models.Tenant, error) {
	const op = "tenant.get_by_Slug"

	Slug = trimString(Slug)
	if Slug == "" {
		return models.Tenant{}, validationError(op, "tenant", "Slug is required")
	}

	var t models.Tenant
	var status string

	query := `
		SELECT id, Slug, name, status, created_at, updated_at
		FROM tenants
		WHERE Slug = $1
	`

	err := r.db.QueryRowContext(
		ctx, query,
		Slug,
	).Scan(&t.Id, &t.Slug, &t.Name, &status, &t.CreatedAt, &t.UpdatedAt)

	if err != nil {
		return models.Tenant{}, classifySQLError(op, "tenant", err, true)
	}

	return t, nil
}

// func (r *PostgresTenantRepo) Update(ctx context.Context, tenant *models.Tenant) error {

// 	query := `
// 		UPDATE tenants
// 		SET Slug = $2, name = $3, status = $4
// 		WHERE id = $1
// 	`

// 	res, err := r.db.ExecContext(
// 		ctx, query,
// 		tenant.ID, tenant.Slug, tenant.Name, tenant.Status,
// 	)

// 	if err != nil {
// 		return err
// 	}

// 	rows, _ := res.RowsAffected()
// 	if rows == 0 {
// 		return ErrTenantNotFound
// 	}

// 	return nil

// }
