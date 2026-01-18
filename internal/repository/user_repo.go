package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

type UserRepository interface {
	Create(ctx context.Context, user *models.User) error
	GetByEmail(ctx context.Context, email string) (*models.User, error)
}

type PostgresUserRepo struct {
	db *sql.DB
}

func NewPostgresUserRepo(db *sql.DB) UserRepository {
	return &PostgresUserRepo{db: db}
}

func (r *PostgresUserRepo) Create(ctx context.Context, user *models.User) error {
	query := `
		INSERT INTO users (id, email, password, tenant_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`

	err := r.db.QueryRowContext(
		ctx, query,
		user.ID, user.Email, user.Password, user.TenantID,
	).Scan(&user.ID)

	if err != nil {
		if err == sql.ErrNoRows {
			return ErrUserExists
		}
		return err
	}

	return nil
}

func (r *PostgresUserRepo) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	query := `
		SELECT id, email, password, role tenant_id
		FROM users
		WHERE email = $1
	`

	err := r.db.QueryRowContext(
		ctx, query,
		email,
	).Scan(&u.ID, &u.Email, &u.Password, &u.Role, &u.TenantID)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}
