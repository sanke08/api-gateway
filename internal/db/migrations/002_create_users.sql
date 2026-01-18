CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS users (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	tenant_id UUID NOT NULL,
	
    email VARCHAR(100) NOT NULL UNIQUE,
	password VARCHAR(100) NOT NULL,

    role VARCHAR(20) NOT NULL CHECK(role IN ('admin', 'user')),

	created_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
    
	FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
    UNIQUE(tenant_id, email)
);


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