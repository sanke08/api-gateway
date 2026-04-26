package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

// WithTx returns a new repository instance that executes all operations
// using the provided database transaction (tx) instead of the default DB connection.
//
// Why this exists:
// Some operations require multiple database writes that must either all succeed
// or all fail together (atomicity). By using the same transaction across multiple
// repositories, we ensure consistency.
//
// ----------------------------------------------------
//
// [MAIN]

// TenantRepository defines DB operations for Tenant entity.
// Services only depend on this interface, making DB swappable.
type TenantRepository interface {
	WithTx(tx *sql.Tx) TenantRepository
	Create(ctx context.Context, tenant models.Tenant) (models.Tenant, error)
	GetByID(ctx context.Context, id string) (models.Tenant, error)
	GetBySlug(ctx context.Context, Slug string) (models.Tenant, error)
}

// UserRepository manages global identity records.
type UserRepository interface {
	WithTx(tx *sql.Tx) UserRepository
	Create(ctx context.Context, user models.User) (models.User, error)
	GetById(ctx context.Context, id string) (models.User, error)
	GetByEmail(ctx context.Context, email string) (models.User, error)
}

// TenantMembershipRepository manages the relationship between users and tenants.
type TenantMembershipRepository interface {
	WithTx(tx *sql.Tx) TenantMembershipRepository
	Create(ctx context.Context, membership models.TenantMembership) (models.TenantMembership, error)
	GetByID(ctx context.Context, id string) (models.TenantMembership, error)
	GetByUserAndTenant(ctx context.Context, userID, tenantID string) (models.TenantMembership, error)
	ListByUser(ctx context.Context, userID string) ([]models.TenantMembership, error)
	ListByTenant(ctx context.Context, tenantID string) ([]models.TenantMembership, error)
}

// APIKeyRepository manages tenant-scoped machine credentials.
type APIKeyRepository interface {
	WithTx(tx *sql.Tx) APIKeyRepository
	Create(ctx context.Context, key models.APIKey) (models.APIKey, error)
	GetByID(ctx context.Context, id string) (models.APIKey, error)
	GetByHash(ctx context.Context, hash string) (models.APIKey, error)
	ListByTenant(ctx context.Context, tenantID string) ([]models.APIKey, error)
}

// UsageRepository stores request usage data.
type UsageRepository interface {
	WithTx(tx *sql.Tx) UsageRepository
	Log(ctx context.Context, record models.Usage) (models.Usage, error)
	ListByTenant(ctx context.Context, tenantID string) ([]models.Usage, error)
}

// Repositories groups all repository interfaces together.
type Repositories struct {
	Tenants     TenantRepository
	Users       UserRepository
	Memberships TenantMembershipRepository
	APIKeys     APIKeyRepository
	Usage       UsageRepository
}

// NewPostgresRepositories wires the PostgreSQL-backed implementations.
func NewPostgresRepositories(db *sql.DB) Repositories {
	return Repositories{
		Tenants:     NewPostgresTenantRepo(db),
		Users:       NewPostgresUserRepo(db),
		Memberships: NewPostgresTenantMembershipRepo(db),
		APIKeys:     NewPostgresAPIKeyRepo(db),
		Usage:       NewPostgresUsageRepo(db),
	}
}
