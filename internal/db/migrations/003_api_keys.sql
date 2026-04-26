CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- The api_keys table stores machine credentials scoped to a tenant.
-- API keys must remain tenant-specific because machines act on behalf of a business,
-- not on behalf of a global user identity.

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL UNIQUE,
    description TEXT,
    active BOOLEAN NOT NULL DEFAULT TRUE,

    created_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_api_keys_tenant_id ON api_keys(tenant_id);
CREATE UNIQUE INDEX idx_api_keys_key_hash ON api_keys(key_hash);

-- trigger to auto update updated_at
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_api_keys_updated_at') THEN
        CREATE TRIGGER update_api_keys_updated_at
            BEFORE UPDATE ON api_keys
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;
END
$$;