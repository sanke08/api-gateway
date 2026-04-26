-- This migration enables PostgreSQL extensions required by the schema.
-- Why this is needed:
-- We use UUID generation at the database layer for stable primary keys.
-- PostgreSQL's gen_random_uuid() comes from pgcrypto.
CREATE EXTENSION IF NOT EXISTS "pgcrypto";


-- The users table stores global identity only.
-- A user is a person, not a tenant-scoped membership.
-- This is the correct model when one person can belong to multiple businesses.
CREATE TABLE IF NOT EXISTS users (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(100) NOT NULL UNIQUE,
	password VARCHAR(100) NOT NULL,

	created_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP
);

-- Email lookup is critical for login.
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);


DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_users_updated_at') THEN
        CREATE TRIGGER update_users_updated_at
            BEFORE UPDATE ON users
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;
END
$$;