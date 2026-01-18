CREATE EXTENSION IF NOT EXISTS "pgcrypto";


CREATE TABLE IF NOT EXISTS tenants (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	name VARCHAR(60) NOT NULL,
	domain VARCHAR(100) NOT NULL UNIQUE,
	status VARCHAR(20) NOT NULL CHECK(status IN ('active', 'inactive')),
	
	created_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP
);


-- function to auto update updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- trigger to auto update updated_at
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_tenants_updated_at') THEN
        CREATE TRIGGER update_tenants_updated_at
            BEFORE UPDATE ON tenants
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;
END
$$;