-- This table stores one usage record for every request that passes through
-- the gateway.
--
-- Why this exists:
-- It supports:
-- - usage analytics
-- - future billing
-- - request auditing
-- - tenant reporting
-- - API usage monitoring

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS usage (
    -- Unique identifier for this usage record.
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Tenant that made the request.
    tenant_id UUID NOT NULL
        REFERENCES tenants(id) ON DELETE CASCADE,

    -- API key used for the request.
    -- Nullable because JWT-authenticated requests may not use an API key.
    api_key_id UUID NULL
        REFERENCES api_keys(id) ON DELETE CASCADE,

    -- Authenticated user, if available.
    user_id UUID NULL
        REFERENCES users(id) ON DELETE SET NULL,

    -- Tenant membership used for authorization.
    membership_id UUID NULL
        REFERENCES tenant_memberships(id) ON DELETE SET NULL,

    -- Unique request identifier.
    request_id TEXT,

    -- Endpoint that was accessed.
    endpoint TEXT NOT NULL,

    -- HTTP method (GET, POST, etc.).
    method TEXT NOT NULL,

    -- Final response status code.
    status_code INTEGER NOT NULL DEFAULT 0,

    -- Total request duration in milliseconds.
    duration_ms BIGINT NOT NULL DEFAULT 0,

    -- Bytes received from the client.
    bytes_in BIGINT NOT NULL DEFAULT 0,

    -- Bytes returned to the client.
    bytes_out BIGINT NOT NULL DEFAULT 0,

    -- Whether the response came from cache.
    cached BOOLEAN NOT NULL DEFAULT FALSE,

    -- Whether the gateway retried the upstream request.
    retried BOOLEAN NOT NULL DEFAULT FALSE,

    -- Creation timestamp.
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Last update timestamp.
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Common billing and analytics queries.
CREATE INDEX IF NOT EXISTS idx_usage_tenant_api_time
    ON usage (tenant_id, api_key_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_tenant_time
    ON usage (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_tenant_timestamp
    ON usage (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_request_id
    ON usage (request_id);

CREATE INDEX IF NOT EXISTS idx_usage_user_id
    ON usage (user_id);

CREATE INDEX IF NOT EXISTS idx_usage_membership_id
    ON usage (membership_id);

-- Automatically update updated_at whenever a row changes.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_trigger
        WHERE tgname = 'update_usage_updated_at'
    ) THEN
        CREATE TRIGGER update_usage_updated_at
            BEFORE UPDATE ON usage
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column();
    END IF;
END
$$;