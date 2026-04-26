CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS usage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL,
    method TEXT NOT NULL,

    bytes_in BIGINT NOT NULL DEFAULT 0,
    bytes_out BIGINT NOT NULL DEFAULT 0,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);



CREATE INDEX IF NOT EXISTS idx_usage_tenant_api_time ON "usage"(tenant_id, api_key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_usage_tenant_time ON "usage"(tenant_id, created_at DESC);


-- trigger to auto update updated_at
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_usage_updated_at') THEN
        CREATE TRIGGER update_usage_updated_at
            BEFORE UPDATE ON usage
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;
END
$$;