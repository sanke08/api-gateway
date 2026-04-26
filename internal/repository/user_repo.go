package repository

import (
	"context"
	"database/sql"

	"github.com/sanke08/api_gateway/internal/models"
)

type PostgresUserRepo struct {
	db sqlExecutor
}

func NewPostgresUserRepo(db *sql.DB) UserRepository {
	return &PostgresUserRepo{db: db}
}

// WithTx rebinds the repository to a transaction.
func (r *PostgresUserRepo) WithTx(tx *sql.Tx) UserRepository {
	return &PostgresUserRepo{db: tx}
}

func (r *PostgresUserRepo) Create(ctx context.Context, user models.User) (models.User, error) {
	const op = "user.create"

	normalized, err := normalizeUserForCreate(user)
	if err != nil {
		return models.User{}, err
	}

	var out models.User

	query := `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id
	`

	err = r.db.QueryRowContext(
		ctx, query,
		normalized.Email, normalized.PasswordHash,
	).Scan(
		&out.Id,
		&out.Email,
		&out.PasswordHash,
		&out.CreatedAt,
		&out.UpdatedAt,
	)

	if err != nil {
		return models.User{}, classifySQLError(op, "user", err, true)
	}

	return out, nil
}

func (r *PostgresUserRepo) GetByEmail(ctx context.Context, email string) (models.User, error) {

	const op = "user.get_by_email"

	email = trimString(email)
	if email == "" {
		return models.User{}, validationError(op, "user", "email is required")
	}

	var u models.User
	query := `
		SELECT id, email, password_hash
		FROM users
		WHERE email = $1
	`

	err := r.db.QueryRowContext(
		ctx, query,
		email,
	).Scan(&u.Id, &u.Email, &u.PasswordHash)

	if err != nil {
		return models.User{}, classifySQLError(op, "user", err, true)
	}

	return u, nil
}

func (r *PostgresUserRepo) GetById(ctx context.Context, id string) (models.User, error) {

	const op = "user.get_by_id"

	id = trimString(id)
	if id == "" {
		return models.User{}, validationError(op, "user", "id is required")
	}

	query := `
		SELECT id, email, password_hash
		FROM users
		WHERE id = $1	
	`
	var u models.User

	err := r.db.QueryRowContext(ctx, query, id).
		Scan(&u.Id, &u.Email, &u.PasswordHash)

	if err != nil {
		return models.User{}, classifySQLError(op, "user", err, true)
	}
	return u, nil
}
