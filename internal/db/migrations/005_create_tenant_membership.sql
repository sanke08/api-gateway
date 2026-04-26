CREATE EXTENSION IF NOT EXISTS "pgcrypto";


-- The tenant_memberships table connects users to tenants.
-- This is what enables one user to belong to many businesses.

CREATE TABLE tenant_memberships (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    role TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    
    created_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP  DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT tenant_memberships_role_check
        CHECK (role IN ('owner', 'admin', 'member')),
    CONSTRAINT tenant_memberships_status_check
        CHECK (status IN ('active', 'invited', 'suspended')),
    CONSTRAINT tenant_memberships_unique_user_tenant
        UNIQUE (user_id, tenant_id)
);

-- Fast lookups are needed in both directions:
-- 1) "which tenants does this user belong to?"
-- 2) "which users belong to this tenant?"
CREATE INDEX IF NOT EXISTS idx_tenant_memberships_user_id ON tenant_memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_tenant_memberships_tenant_id ON tenant_memberships(tenant_id);