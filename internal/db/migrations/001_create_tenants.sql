-- This migration enables PostgreSQL extensions required by the schema.
-- Why this is needed:
-- We use UUID generation at the database layer for stable primary keys.
-- PostgreSQL's gen_random_uuid() comes from pgcrypto.
CREATE EXTENSION IF NOT EXISTS "pgcrypto";


-- Table for storing tenant organizations.
--
-- Why this table exists:
-- Tenants are the primary organizational boundary in the system. Each tenant represents a customer
-- or organization that owns resources (like API keys, webhooks, rate limits, etc.) and has its own
-- users (members) with tenant-specific roles.
--
-- Key Fields:
--   - id: A globally unique identifier for the tenant.
--   - name: Human-readable name of the tenant.
--   - Slug: A unique Slug associated with the tenant, used for routing/identification.
--   - status: The current status of the tenant (active/suspended).
--
-- Relationships:
--   - One-to-Many with TenantMembership: A tenant has many members (users).
--   - One-to-Many with APIKey: A tenant can create multiple API keys.
--   - One-to-Many with Webhook: A tenant can configure multiple webhooks.

CREATE TABLE IF NOT EXISTS tenants (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	name VARCHAR(60) NOT NULL,
	Slug VARCHAR(100) NOT NULL UNIQUE,
	status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'suspended')),

	created_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP
);


-- Fast tenant lookup by Slug is important for admin flows and future routing.
CREATE INDEX IF NOT EXISTS idx_tenants_Slug ON tenants(Slug);


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