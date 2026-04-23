-- Version: 1.01
-- Description: Create table users
CREATE TABLE users (
    id UUID NOT NULL,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    roles TEXT [] NOT NULL,
    password_hash TEXT NOT NULL,
    organizations TEXT [] NULL,
    enabled BOOLEAN NOT NULL,
    date_created TIMESTAMP NOT NULL,
    date_updated TIMESTAMP NOT NULL,
    PRIMARY KEY (id)
);
CREATE INDEX users_email_idx ON users (email);
-- Version: 1.02
-- Description: Create table products
CREATE TABLE organizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9-]+$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- Version: 1.04
-- Description: Create table audit
CREATE TABLE audit (
    id UUID NOT NULL,
    obj_id UUID NOT NULL,
    obj_domain TEXT NOT NULL,
    obj_name TEXT NOT NULL,
    actor_id UUID NOT NULL,
    action TEXT NOT NULL,
    data JSONB NULL,
    message TEXT NULL,
    timestamp TIMESTAMP NOT NULL,
    PRIMARY KEY (id)
);
-- =========================================================
-- Version: 1.05
-- Description: Add missing columns to organizations
-- =========================================================
ALTER TABLE organizations
ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS date_created TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS date_updated TIMESTAMPTZ;
-- Back-fill from existing columns so NOT NULL is safe to add
UPDATE organizations
SET date_created = created_at,
    date_updated = updated_at
WHERE date_created IS NULL;
ALTER TABLE organizations
ALTER COLUMN date_created
SET NOT NULL,
    ALTER COLUMN date_updated
SET NOT NULL;
-- Optional: drop the old columns if you want to standardise naming
-- ALTER TABLE organizations DROP COLUMN created_at, DROP COLUMN updated_at;
-- =========================================================
-- Version: 1.06
-- Description: Create org_role enum
-- =========================================================
DO $$ BEGIN CREATE TYPE org_role AS ENUM (
    'SUPER ADMIN',
    'ORG ADMIN',
    'PROJECT MANAGER',
    'VIEWER'
);
EXCEPTION
WHEN duplicate_object THEN NULL;
END $$;
-- =========================================================
-- Version: 1.07
-- Description: Create org_members table
-- =========================================================
CREATE TABLE IF NOT EXISTS org_members (
    member_id UUID NOT NULL DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    user_id UUID NOT NULL,
    role org_role NOT NULL DEFAULT 'VIEWER',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT org_members_pkey PRIMARY KEY (member_id),
    CONSTRAINT org_members_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT org_members_user_fk FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT org_members_org_user_unique UNIQUE (org_id, user_id)
);
CREATE INDEX IF NOT EXISTS org_members_org_idx ON org_members (org_id);
CREATE INDEX IF NOT EXISTS org_members_user_idx ON org_members (user_id);
-- =========================================================
-- Version: 1.08
-- Description: Create subscription enums + subscriptions table
-- =========================================================
DO $$ BEGIN CREATE TYPE subscription_plan AS ENUM ('free', 'pro', 'enterprise');
EXCEPTION
WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN CREATE TYPE subscription_status AS ENUM ('trialing', 'active', 'past_due', 'cancelled');
EXCEPTION
WHEN duplicate_object THEN NULL;
END $$;
CREATE TABLE IF NOT EXISTS subscriptions (
    subscription_id UUID NOT NULL DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    plan subscription_plan NOT NULL DEFAULT 'free',
    status subscription_status NOT NULL DEFAULT 'trialing',
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT subscriptions_pkey PRIMARY KEY (subscription_id),
    CONSTRAINT subscriptions_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT subscriptions_one_active UNIQUE (org_id, status) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX IF NOT EXISTS subscriptions_org_idx ON subscriptions (org_id);
-- =========================================================
-- Version: 1.09
-- Description: Drop organizations column from users
--              (membership is now tracked in org_members)
-- =========================================================
ALTER TABLE users DROP COLUMN IF EXISTS organizations;
-- =========================================================
-- Version: 1.10
-- Description: Create projects and user_project_access tables
-- =========================================================
CREATE TABLE IF NOT EXISTS projects (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#60a5fa' CHECK (color ~ '^#[0-9a-fA-F]{6}$'),
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT projects_pkey PRIMARY KEY (id),
    CONSTRAINT projects_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT projects_org_name_unique UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS projects_org_idx ON projects (org_id);
-- user_project_access scopes project_manager and viewer roles to
-- specific projects within an org. org_admin and super_admin skip
-- this table entirely — they see all projects via role check.
CREATE TABLE IF NOT EXISTS user_project_access (
    user_id UUID NOT NULL,
    project_id UUID NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT upa_pkey PRIMARY KEY (user_id, project_id),
    CONSTRAINT upa_user_fk FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT upa_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS upa_project_idx ON user_project_access (project_id);
-- =========================================================
-- Version: 1.11
-- Description: Create org_invitations table
-- =========================================================
CREATE TABLE IF NOT EXISTS org_invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    email TEXT NOT NULL,
    role org_role NOT NULL DEFAULT 'VIEWER',
    token TEXT NOT NULL UNIQUE,
    invited_by UUID NOT NULL,
    project_ids TEXT [] NOT NULL DEFAULT '{}',
    accepted_at TIMESTAMPTZ NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT org_invitations_org_fk FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT org_invitations_user_fk FOREIGN KEY (invited_by) REFERENCES users(id)
);
CREATE INDEX IF NOT EXISTS org_invitations_org_idx ON org_invitations(org_id);
CREATE INDEX IF NOT EXISTS org_invitations_email_idx ON org_invitations(email);
-- =========================================================
-- Version: 1.12
-- Description: Create verification_tokens table
-- =========================================================
CREATE TABLE IF NOT EXISTS verification_tokens (
    token TEXT PRIMARY KEY,
    user_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT vt_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS verification_tokens_user_idx ON verification_tokens(user_id);
-- =========================================================
-- Version: 1.13
-- Description: Create logs table
-- =========================================================
CREATE TABLE IF NOT EXISTS logs (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL,
    level TEXT NOT NULL CHECK (level IN ('DEBUG', 'INFO', 'WARN', 'ERROR')),
    message TEXT NOT NULL,
    source TEXT NOT NULL,
    ts TIMESTAMPTZ NOT NULL DEFAULT now(),
    tags TEXT [] NOT NULL DEFAULT '{}',
    meta JSONB NOT NULL DEFAULT '{}',
    CONSTRAINT logs_pkey PRIMARY KEY (id),
    CONSTRAINT logs_project_fk FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS logs_project_ts_idx ON logs (project_id, ts DESC);
CREATE INDEX IF NOT EXISTS logs_level_idx ON logs (level);
-- Version: 1.14
-- Description: Create integration_providers table (seeded, static catalog)
CREATE TABLE IF NOT EXISTS integration_providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    icon TEXT NOT NULL,
    type TEXT NOT NULL,
    description TEXT NOT NULL,
    fields JSONB NOT NULL DEFAULT '[]',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INT NOT NULL DEFAULT 0
);
-- Version: 1.15
-- Description: Create integrations table (per-org configured integrations)
CREATE TABLE IF NOT EXISTS integrations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    provider_id TEXT NOT NULL,
    name TEXT NOT NULL,
    credentials_enc BYTEA NOT NULL,
    credentials_iv BYTEA NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT integrations_org_fk FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT integrations_provider_fk FOREIGN KEY (provider_id) REFERENCES integration_providers(id),
    CONSTRAINT integrations_org_provider_name_uq UNIQUE (org_id, provider_id, name)
);
CREATE INDEX IF NOT EXISTS integrations_org_idx ON integrations(org_id);
-- Version: 1.16
-- Description: Create alert_rules table
CREATE TABLE IF NOT EXISTS alert_rules (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL,
    connection_id UUID NOT NULL,
    project_id    UUID,
    name          TEXT NOT NULL,
    level         TEXT NOT NULL,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT alert_rules_org_fk        FOREIGN KEY (org_id)        REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT alert_rules_connection_fk FOREIGN KEY (connection_id) REFERENCES integrations(id)  ON DELETE CASCADE,
    CONSTRAINT alert_rules_project_fk    FOREIGN KEY (project_id)    REFERENCES projects(id)      ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS alert_rules_org_idx        ON alert_rules(org_id);
CREATE INDEX IF NOT EXISTS alert_rules_connection_idx ON alert_rules(connection_id);