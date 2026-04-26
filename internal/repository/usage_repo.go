package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

// postgresUsageRepo stores append-only request usage data.
type postgresUsageRepo struct {
	db sqlExecutor
}

// NewPostgresUsageRepository creates a PostgreSQL-backed usage repository.
func NewPostgresUsageRepo(db *sql.DB) UsageRepository {
	return &postgresUsageRepo{db: db}
}

// WithTx rebinds the repository to a transaction.
func (r *postgresUsageRepo) WithTx(tx *sql.Tx) UsageRepository {
	return &postgresUsageRepo{db: tx}
}

// Log inserts one usage row.
//
// Why this is write-only:
// Usage is event-style data. It is meant to be appended during request processing
// and queried later for analytics or billing preparation.
func (r *postgresUsageRepo) Log(ctx context.Context, record models.Usage) (models.Usage, error) {
	const op = "usage.log"

	if err := validateUsageRecordForCreate(record); err != nil {
		return models.Usage{}, err
	}

	const q = `
		INSERT INTO "usage" (tenant_id, api_key_id, path, method, "timestamp", bytes_in, bytes_out)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, tenant_id, api_key_id, path, method, "timestamp", bytes_in, bytes_out
	`

	var out models.Usage
	err := r.db.QueryRowContext(ctx, q,
		record.TenantID,
		record.APIKeyID,
		record.Endpoint,
		record.Method,
		record.Timestamp,
		record.BytesIn,
		record.BytesOut,
	).Scan(
		&out.ID,
		&out.TenantID,
		&out.APIKeyID,
		&out.Endpoint,
		&out.Method,
		&out.Timestamp,
		&out.BytesIn,
		&out.BytesOut,
	)
	if err != nil {
		return models.Usage{}, classifySQLError(op, "usage", err, true)
	}

	return out, nil
}

// ListByTenant returns usage rows for one tenant.
func (r *postgresUsageRepo) ListByTenant(ctx context.Context, tenantID string) ([]models.Usage, error) {
	const op = "usage.list_by_tenant"

	tenantID = trimString(tenantID)
	if tenantID == "" {
		return nil, validationError(op, "usage", "tenant_id is required")
	}

	const q = `
		SELECT id, tenant_id, api_key_id, path, method, "timestamp", bytes_in, bytes_out
		FROM "usage"
		WHERE tenant_id = $1
		ORDER BY "timestamp" DESC, id DESC
	`

	rows, err := r.db.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, classifySQLError(op, "usage", err, false)
	}
	defer rows.Close()

	out := make([]models.Usage, 0)
	for rows.Next() {
		var item models.Usage
		if err := rows.Scan(
			&item.ID,
			&item.TenantID,
			&item.APIKeyID,
			&item.Endpoint,
			&item.Method,
			&item.Timestamp,
			&item.BytesIn,
			&item.BytesOut,
		); err != nil {
			return nil, classifySQLError(op, "usage", err, false)
		}
		out = append(out, item)
	}

	if err := rows.Err(); err != nil {
		return nil, classifySQLError(op, "usage", err, false)
	}

	return out, nil
}
