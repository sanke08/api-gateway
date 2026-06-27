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
		INSERT INTO "usage" (
			request_id,
			tenant_id,
			api_key_id,
			user_id,
			membership_id,
			path,
			method,
			status_code,
			duration_ms,
			"timestamp",
			bytes_in,
			bytes_out,
			cached,
			retried
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING
			id,
			request_id,
			tenant_id,
			api_key_id,
			user_id,
			membership_id,
			path,
			method,
			status_code,
			duration_ms,
			"timestamp",
			bytes_in,
			bytes_out,
			cached,
			retried
	`

	var out models.Usage
	var apiKeyID sql.NullString
	var userID sql.NullString
	var membershipID sql.NullString

	err := r.db.QueryRowContext(ctx, q,
		nullString(record.RequestID),
		record.TenantID,
		stringPtrToNull(record.APIKeyID),
		stringPtrToNull(record.UserID),
		stringPtrToNull(record.MembershipID),
		record.Path,
		record.Method,
		record.StatusCode,
		record.DurationMS,
		record.Timestamp,
		record.BytesIn,
		record.BytesOut,
		record.Cached,
		record.Retried,
	).Scan(
		&out.ID,
		&out.RequestID,
		&out.TenantID,
		&apiKeyID,
		&userID,
		&membershipID,
		&out.Path,
		&out.Method,
		&out.StatusCode,
		&out.DurationMS,
		&out.Timestamp,
		&out.BytesIn,
		&out.BytesOut,
		&out.Cached,
		&out.Retried,
	)
	if err != nil {
		return models.Usage{}, classifySQLError(op, "usage", err, false)
	}

	out.APIKeyID = nullStringPtrFromNull(apiKeyID)
	out.UserID = nullStringPtrFromNull(userID)
	out.MembershipID = nullStringPtrFromNull(membershipID)

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
			&item.Path,
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

func nullString(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

// func nullStringPtr(v *string) interface{} {
// 	if v == nil {
// 		return nil
// 	}
// 	if *v == "" {
// 		return nil
// 	}
// 	return *v
// }

func nullStringPtrFromNull(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
