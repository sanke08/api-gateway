package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

// postgresAPIKeyRepo stores tenant-scoped API key hashes.
type postgresAPIKeyRepo struct {
	db sqlExecutor
}

// NewPostgresAPIKeyRepo creates a PostgreSQL-backed API key repository.
func NewPostgresAPIKeyRepo(db *sql.DB) APIKeyRepository {
	return &postgresAPIKeyRepo{db: db}
}

// WithTx rebinds the repository to a transaction.
func (r *postgresAPIKeyRepo) WithTx(tx *sql.Tx) APIKeyRepository {
	return &postgresAPIKeyRepo{db: tx}
}

// Create inserts an API key hash and returns the stored row.
//
// Why only the hash is stored:
// The raw key must never be stored in the database. It is shown once during
// creation and discarded immediately after.
func (r *postgresAPIKeyRepo) Create(ctx context.Context, key models.APIKey) (models.APIKey, error) {
	const op = "apikey.create"

	normalized, err := normalizeAPIKeyForCreate(key)
	if err != nil {
		return models.APIKey{}, err
	}

	const q = `
		INSERT INTO api_keys (tenant_id, key_hash, description, active)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, key_hash, description, active, created_at, updated_at
	`

	var out models.APIKey

	err = r.db.QueryRowContext(ctx, q,
		normalized.ID,
		normalized.TenantID,
		normalized.KeyHash,
		normalized.Description,
		normalized.Active,
	).Scan(
		&out.ID,
		&out.TenantID,
		&out.KeyHash,
		&out.Description,
		&out.Active,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return models.APIKey{}, classifySQLError(op, "api_key", err, true)
	}

	return out, nil
}

// GetByID loads one API key row by identifier.
func (r *postgresAPIKeyRepo) GetByID(ctx context.Context, id string) (models.APIKey, error) {
	const op = "apikey.get_by_id"

	id = trimString(id)
	if id == "" {
		return models.APIKey{}, validationError(op, "api_key", "id is required")
	}

	const q = `
		SELECT id, tenant_id, key_hash, description, active, created_at, updated_at
		FROM api_keys
		WHERE id = $1
	`

	var out models.APIKey

	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&out.ID,
		&out.TenantID,
		&out.KeyHash,
		&out.Description,
		&out.Active,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return models.APIKey{}, classifySQLError(op, "api_key", err, true)
	}

	return out, nil
}

// GetByHash loads an API key row using the stored hash.
func (r *postgresAPIKeyRepo) GetByHash(ctx context.Context, hash string) (models.APIKey, error) {
	const op = "apikey.get_by_hash"

	hash = trimString(hash)
	if hash == "" {
		return models.APIKey{}, validationError(op, "api_key", "hash is required")
	}

	const q = `
		SELECT id, tenant_id, key_hash, description, active, created_at, updated_at
		FROM api_keys
		WHERE key_hash = $1
	`

	var out models.APIKey

	err := r.db.QueryRowContext(ctx, q, hash).Scan(
		&out.ID,
		&out.TenantID,
		&out.KeyHash,
		&out.Description,
		&out.Active,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return models.APIKey{}, classifySQLError(op, "api_key", err, true)
	}

	return out, nil
}

// ListByTenant returns all API keys for one tenant.
func (r *postgresAPIKeyRepo) ListByTenant(ctx context.Context, tenantID string) ([]models.APIKey, error) {
	const op = "apikey.list_by_tenant"

	tenantID = trimString(tenantID)
	if tenantID == "" {
		return nil, validationError(op, "api_key", "tenant_id is required")
	}

	const q = `
		SELECT id, tenant_id, key_hash, description, active, created_at, updated_at
		FROM api_keys
		WHERE tenant_id = $1
		ORDER BY created_at ASC, id ASC
	`

	rows, err := r.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, classifySQLError(op, "api_key", err, false)
	}
	defer rows.Close()

	out := make([]models.APIKey, 0)
	for rows.Next() {
		var item models.APIKey

		if err := rows.Scan(
			&item.ID,
			&item.TenantID,
			&item.KeyHash,
			&item.Description,
			&item.Active,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, classifySQLError(op, "api_key", err, false)
		}

		out = append(out, item)
	}

	if err := rows.Err(); err != nil {
		return nil, classifySQLError(op, "api_key", err, false)
	}

	return out, nil
}
