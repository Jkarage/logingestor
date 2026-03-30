--------------------------------------------------------------------------------
-- Version: 1.02
-- Description: Create table products
CREATE TABLE organizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9-]+$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
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
--------------------------------------------------------------------------------
-- CREATE TABLE projects (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
--     name text NOT NULL,
--     color text NOT NULL DEFAULT '#60a5fa' CHECK (color ~ '^#[0-9a-fA-F]{6}$'),
--     created_at timestamptz NOT NULL DEFAULT now(),
--     updated_at timestamptz NOT NULL DEFAULT now()
-- );
-- CREATE UNIQUE INDEX projects_org_name_idx ON projects (org_id, name);
-- CREATE TABLE user_project_access (
--     user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
--     project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
--     granted_at timestamptz NOT NULL DEFAULT now(),
--     PRIMARY KEY (user_id, project_id)
-- );
-- --------------------------------------------------------------------------------
-- CREATE INDEX upa_project_id_idx ON user_project_access (project_id);
-- -- Version: 1.03
-- -- Description: Add products view.
-- CREATE TABLE invites (
--     token text PRIMARY KEY,
--     user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
--     invited_by uuid NOT NULL REFERENCES users(id),
--     created_at timestamptz NOT NULL DEFAULT now(),
--     expires_at timestamptz NOT NULL DEFAULT now() + INTERVAL '48 hours'
-- );
-- --------------------------------------------------------------------------------
-- CREATE INDEX invites_user_id_idx ON invites (user_id);
-- CREATE INDEX invites_expires_at_idx ON invites (expires_at);
-- CREATE TABLE api_keys (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
--     created_by uuid NOT NULL REFERENCES users(id),
--     name text NOT NULL,
--     key_prefix text NOT NULL,
--     key_hash text NOT NULL UNIQUE,
--     last_used_at timestamptz,
--     revoked_at timestamptz,
--     created_at timestamptz NOT NULL DEFAULT now()
-- );
-- CREATE INDEX api_keys_project_id_idx ON api_keys (project_id);
-- CREATE INDEX api_keys_key_hash_idx ON api_keys (key_hash);
-- CREATE TABLE log_entries (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
--     level log_level NOT NULL,
--     message text NOT NULL,
--     source text NOT NULL,
--     tags text [] NOT NULL DEFAULT '{}',
--     meta jsonb NOT NULL DEFAULT '{}',
--     ts timestamptz NOT NULL,
--     ingested_at timestamptz NOT NULL DEFAULT now(),
--     api_key_id uuid REFERENCES api_keys(id) ON DELETE
--     SET NULL
-- );
-- CREATE TABLE integration_connections (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
--     provider text NOT NULL,
--     name text NOT NULL,
--     credentials bytea NOT NULL,
--     is_active boolean NOT NULL DEFAULT true,
--     created_by uuid NOT NULL REFERENCES users(id),
--     created_at timestamptz NOT NULL DEFAULT now(),
--     updated_at timestamptz NOT NULL DEFAULT now(),
--     UNIQUE (org_id, provider, name)
-- );
-- --------------------------------------------------------------------------------
-- CREATE TABLE alert_rules (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
--     connection_id uuid NOT NULL REFERENCES integration_connections(id) ON DELETE CASCADE,
--     name text NOT NULL,
--     level log_level NOT NULL,
--     project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
--     is_active boolean NOT NULL DEFAULT true,
--     created_by uuid NOT NULL REFERENCES users(id),
--     created_at timestamptz NOT NULL DEFAULT now(),
--     updated_at timestamptz NOT NULL DEFAULT now()
-- );
-- CREATE INDEX alert_rules_org_id_idx ON alert_rules (org_id);
-- CREATE INDEX alert_rules_level_project_idx ON alert_rules (level, project_id)
-- WHERE is_active = true;
-- CREATE TABLE audit_log (
--     id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
--     org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
--     actor_id uuid REFERENCES users(id) ON DELETE
--     SET NULL,
--         action text NOT NULL,
--         target_type text,
--         target_id uuid,
--         payload jsonb NOT NULL DEFAULT '{}',
--         ip_address inet,
--         ts timestamptz NOT NULL DEFAULT now()
-- );
-- CREATE INDEX audit_log_org_ts_idx ON audit_log (org_id, ts DESC);
-- CREATE INDEX audit_log_actor_id_idx ON audit_log (actor_id);
-- CREATE INDEX audit_log_target_idx ON audit_log (target_type, target_id);
-- CREATE EXTENSION IF NOT EXISTS pgcrypto;
-- CREATE EXTENSION IF NOT EXISTS pg_trgm;
-- CREATE EXTENSION IF NOT EXISTS pg_partman;
-- CREATE EXTENSION IF NOT EXISTS pg_cron;