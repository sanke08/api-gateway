package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

// TenantRepository defines DB operations for Tenant entity.
// Services only depend on this interface, making DB swappable.
type TenantRepository interface {
	Create(ctx context.Context, tenant *models.Tenant) error
	GetByID(ctx context.Context, id string) (*models.Tenant, error)
	GetByDomain(ctx context.Context, domain string) (*models.Tenant, error)
	Update(ctx context.Context, tenant *models.Tenant) error
}

// PostgresTenantRepo implements TenantRepository using Postgres
type PostgresTenantRepo struct {
	db *sql.DB
}

// NewPostgresTenantRepo creates a new PostgresTenantRepo instance
func NewPostgresTenantRepo(db *sql.DB) TenantRepository {
	return &PostgresTenantRepo{db: db}
}

func (r *PostgresTenantRepo) Create(ctx context.Context, tenant *models.Tenant) error {
	query := `
		INSERT INTO tenants (domain, name, status) 
		VALUES ($1, $2, $3)
		ON CONFLICT (domain) DO NOTHING
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(
		ctx, query,
		tenant.Domain, tenant.Name, tenant.Status,
	).Scan(&tenant.ID, &tenant.CreatedAt, &tenant.UpdatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return ErrTenantExists
		}
		return err
	}
	return nil
}

func (r *PostgresTenantRepo) GetByID(ctx context.Context, id string) (*models.Tenant, error) {
	var t models.Tenant

	query := `
		SELECT id, domain, name, status, created_at, updated_at
		FROM tenants
		WHERE id = $1
	`

	err := r.db.QueryRowContext(
		ctx, query,
		id,
	).Scan(&t.ID, &t.Domain, &t.Name, &t.Status, &t.CreatedAt, &t.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil

}

func (r *PostgresTenantRepo) GetByDomain(ctx context.Context, domain string) (*models.Tenant, error) {
	var t models.Tenant

	query := `
		SELECT id, domain, name, status, created_at, updated_at
		FROM tenants
		WHERE domain = $1
	`

	err := r.db.QueryRowContext(
		ctx, query,
		domain,
	).Scan(&t.ID, &t.Domain, &t.Name, &t.Status, &t.CreatedAt, &t.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrTenantNotFound
	}

	if err != nil {
		return nil, err
	}

	return &t, nil
}

func (r *PostgresTenantRepo) Update(ctx context.Context, tenant *models.Tenant) error {

	query := `
		UPDATE tenants
		SET domain = $2, name = $3, status = $4
		WHERE id = $1
	`

	res, err := r.db.ExecContext(
		ctx, query,
		tenant.ID, tenant.Domain, tenant.Name, tenant.Status,
	)

	if err != nil {
		return err
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrTenantNotFound
	}

	return nil

}
