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
-- Version: 1.17
-- Description: Add org_id to audit table for org-scoped audit log queries
ALTER TABLE audit
ADD COLUMN IF NOT EXISTS org_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';
CREATE INDEX IF NOT EXISTS idx_audit_org_ts ON audit (org_id, timestamp DESC);
-- Version: 1.18
-- Description: Add retention_days to projects table
ALTER TABLE projects
ADD COLUMN IF NOT EXISTS retention_days INT NULL;
-- Version: 1.19
-- Description: Add billing plans table and extend subscriptions for Stripe
CREATE TABLE IF NOT EXISTS plans (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            TEXT UNIQUE NOT NULL,
    name            TEXT NOT NULL,
    price_cents     INT  NOT NULL DEFAULT 0,
    interval        TEXT NOT NULL DEFAULT 'month',
    stripe_price_id TEXT,
    features        JSONB NOT NULL DEFAULT '{}',
    is_active       BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO plans (slug, name, price_cents, stripe_price_id, features) VALUES
    ('free', 'Free', 0, NULL, '{"log_retention_days":7,"max_projects":3,"max_members":5}'),
    ('pro', 'Pro', 500, NULL, '{"log_retention_days":90,"max_projects":-1,"max_members":-1}'),
    ('enterprise', 'Enterprise', -1, NULL, '{"log_retention_days":-1,"max_projects":-1,"max_members":-1}')
ON CONFLICT (slug) DO NOTHING;
ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS plan_id                UUID REFERENCES plans(id),
    ADD COLUMN IF NOT EXISTS stripe_customer_id     TEXT,
    ADD COLUMN IF NOT EXISTS stripe_subscription_id TEXT,
    ADD COLUMN IF NOT EXISTS cancel_at_period_end   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS cancelled_at           TIMESTAMPTZ;
ALTER TABLE subscriptions
    ALTER COLUMN period_start DROP NOT NULL,
    ALTER COLUMN period_end   DROP NOT NULL;
UPDATE subscriptions s
SET plan_id = p.id
FROM plans p
WHERE p.slug = s.plan::text
  AND s.plan_id IS NULL;
ALTER TABLE subscriptions ALTER COLUMN plan_id SET NOT NULL;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_one_active;
DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'subscriptions_org_unique'
    ) THEN
        ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_org_unique UNIQUE (org_id);
    END IF;
END $$;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS plan;
INSERT INTO subscriptions (org_id, plan_id, status, cancel_at_period_end)
SELECT o.id, p.id, 'active', false
FROM organizations o
CROSS JOIN plans p
WHERE p.slug = 'free'
  AND NOT EXISTS (
      SELECT 1 FROM subscriptions s WHERE s.org_id = o.id
  );
-- Version: 1.20
-- Description: Create sources table (tenant infrastructure-log ingestion)
CREATE TABLE IF NOT EXISTS sources (
    id                 UUID NOT NULL DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL,
    project_id         UUID NOT NULL,
    kind               TEXT NOT NULL CHECK (kind IN ('otel','syslog','fluentbit','vector','k8s','http')),
    name               TEXT NOT NULL,
    key_prefix         TEXT NOT NULL,
    key_hash           TEXT NOT NULL,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen_at       TIMESTAMPTZ,
    rate_limit_per_sec INT NOT NULL DEFAULT 500,
    rate_limit_burst   INT NOT NULL DEFAULT 1000,
    sample_debug_info  NUMERIC NOT NULL DEFAULT 1.0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT sources_pkey PRIMARY KEY (id),
    CONSTRAINT sources_org_fk FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT sources_project_fk FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT sources_org_name_unique UNIQUE (org_id, name)
);
CREATE UNIQUE INDEX IF NOT EXISTS sources_key_hash_idx ON sources (key_hash);
CREATE INDEX IF NOT EXISTS sources_org_idx ON sources (org_id);
-- Version: 1.21
-- Description: Add infrastructure-log dimensions to logs table
ALTER TABLE logs ADD COLUMN IF NOT EXISTS source_type TEXT NOT NULL DEFAULT 'app' CHECK (source_type IN ('app','infra'));
ALTER TABLE logs ADD COLUMN IF NOT EXISTS source_id UUID;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS host TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS container TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS pod TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS namespace TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS cluster TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS unit TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS facility TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS region TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS cloud_resource_id TEXT;
ALTER TABLE logs ADD COLUMN IF NOT EXISTS attributes JSONB NOT NULL DEFAULT '{}';
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'logs_source_fk') THEN
        ALTER TABLE logs ADD CONSTRAINT logs_source_fk FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE SET NULL;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS logs_project_sourcetype_ts_idx ON logs (project_id, source_type, ts DESC);
CREATE INDEX IF NOT EXISTS logs_source_ts_idx ON logs (source_id, ts DESC);
CREATE INDEX IF NOT EXISTS logs_host_idx ON logs (host);
CREATE INDEX IF NOT EXISTS logs_unit_idx ON logs (unit);
CREATE INDEX IF NOT EXISTS logs_namespace_idx ON logs (namespace);
-- Version: 1.22
-- Description: Create ingest_usage table (daily per-source counters for quota + billing)
CREATE TABLE IF NOT EXISTS ingest_usage (
    source_id     UUID NOT NULL,
    org_id        UUID NOT NULL,
    project_id    UUID NOT NULL,
    day           DATE NOT NULL,
    event_count   BIGINT NOT NULL DEFAULT 0,
    byte_count    BIGINT NOT NULL DEFAULT 0,
    dropped_count BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT ingest_usage_pkey PRIMARY KEY (source_id, day),
    CONSTRAINT ingest_usage_source_fk FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS ingest_usage_org_day_idx ON ingest_usage (org_id, day);
-- Version: 1.23
-- Description: Add infra retention + daily quota limits to plan features
UPDATE plans SET features = features || '{"infra_retention_days":7,"infra_daily_event_quota":1000000}'::jsonb WHERE slug = 'free';
UPDATE plans SET features = features || '{"infra_retention_days":14,"infra_daily_event_quota":50000000}'::jsonb WHERE slug = 'pro';
UPDATE plans SET features = features || '{"infra_retention_days":-1,"infra_daily_event_quota":-1}'::jsonb WHERE slug = 'enterprise';
-- Version: 1.24
-- Description: Project-scope integration connections + add rule owner
-- Connections move from org-scoped to project-scoped. project_id is nullable at
-- the DB level ONLY to preserve legacy org connections that cannot be re-homed
-- (see v1.25); every connection created through the API sets it.
ALTER TABLE integrations ADD COLUMN IF NOT EXISTS project_id UUID;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'integrations_project_fk') THEN
        ALTER TABLE integrations ADD CONSTRAINT integrations_project_fk FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
    END IF;
END $$;
-- Uniqueness is now per-project, not per-org. Drop the old org-level constraint
-- so a connection can be cloned into a project without colliding.
ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_org_provider_name_uq;
CREATE UNIQUE INDEX IF NOT EXISTS integrations_project_provider_name_uq ON integrations (project_id, provider_id, name);
CREATE INDEX IF NOT EXISTS integrations_project_active_idx ON integrations (project_id, enabled);
-- Alert rules gain a display owner (creator). Nullable for legacy rows.
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS user_id UUID;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'alert_rules_user_fk') THEN
        ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS alert_rules_project_idx ON alert_rules (project_id);
-- Version: 1.25
-- Description: Data migration — re-home org connections/rules to projects
-- Deterministic, idempotent (guarded by the mapping table + ON CONFLICT), and
-- reversible via integration_migration_map (old->new connection id per project).
-- Strategy: rules with a project keep it (cloning their connection into that
-- project if it is still org-level); org-wide rules (project_id IS NULL) fan out
-- to every project in the org that already has logs. Original org connections
-- that end up unreferenced are disabled (never dropped) so no credentials leak
-- or vanish.
CREATE TABLE IF NOT EXISTS integration_migration_map (
    old_connection_id UUID NOT NULL,
    new_connection_id UUID NOT NULL,
    project_id        UUID NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT integration_migration_map_pkey PRIMARY KEY (old_connection_id, project_id)
);
DO $$
DECLARE
    r        RECORD;
    p        RECORD;
    new_conn UUID;
    src_prov TEXT;
    src_name TEXT;
BEGIN
    -- Step A: rules already targeting a project. Ensure their connection is
    -- project-scoped to that same project (clone the org connection if needed).
    FOR r IN SELECT * FROM alert_rules WHERE project_id IS NOT NULL LOOP
        -- Already project-scoped to the right project? Nothing to do.
        IF EXISTS (SELECT 1 FROM integrations i WHERE i.id = r.connection_id AND i.project_id = r.project_id) THEN
            CONTINUE;
        END IF;

        SELECT new_connection_id INTO new_conn
        FROM integration_migration_map
        WHERE old_connection_id = r.connection_id AND project_id = r.project_id;

        IF new_conn IS NULL THEN
            SELECT provider_id, name INTO src_prov, src_name FROM integrations WHERE id = r.connection_id;
            IF src_prov IS NULL THEN
                CONTINUE; -- dangling connection reference; leave rule as-is
            END IF;

            new_conn := gen_random_uuid();
            INSERT INTO integrations (id, org_id, project_id, provider_id, name, credentials_enc, credentials_iv, enabled, date_created, date_updated)
            SELECT new_conn, i.org_id, r.project_id, i.provider_id, i.name, i.credentials_enc, i.credentials_iv, i.enabled, NOW(), NOW()
            FROM integrations i WHERE i.id = r.connection_id
            ON CONFLICT (project_id, provider_id, name) DO NOTHING;

            IF NOT FOUND THEN
                SELECT id INTO new_conn FROM integrations
                WHERE project_id = r.project_id AND provider_id = src_prov AND name = src_name;
            END IF;

            INSERT INTO integration_migration_map (old_connection_id, new_connection_id, project_id)
            VALUES (r.connection_id, new_conn, r.project_id)
            ON CONFLICT DO NOTHING;
        END IF;

        UPDATE alert_rules SET connection_id = new_conn WHERE id = r.id;
    END LOOP;

    -- Step B: org-wide rules (project_id IS NULL) fan out to projects with logs.
    FOR r IN SELECT * FROM alert_rules WHERE project_id IS NULL LOOP
        SELECT provider_id, name INTO src_prov, src_name FROM integrations WHERE id = r.connection_id;

        FOR p IN
            SELECT pr.id AS project_id FROM projects pr
            WHERE pr.org_id = r.org_id
              AND EXISTS (SELECT 1 FROM logs l WHERE l.project_id = pr.id)
        LOOP
            SELECT new_connection_id INTO new_conn
            FROM integration_migration_map
            WHERE old_connection_id = r.connection_id AND project_id = p.project_id;

            IF new_conn IS NULL AND src_prov IS NOT NULL THEN
                new_conn := gen_random_uuid();
                INSERT INTO integrations (id, org_id, project_id, provider_id, name, credentials_enc, credentials_iv, enabled, date_created, date_updated)
                SELECT new_conn, i.org_id, p.project_id, i.provider_id, i.name, i.credentials_enc, i.credentials_iv, i.enabled, NOW(), NOW()
                FROM integrations i WHERE i.id = r.connection_id
                ON CONFLICT (project_id, provider_id, name) DO NOTHING;

                IF NOT FOUND THEN
                    SELECT id INTO new_conn FROM integrations
                    WHERE project_id = p.project_id AND provider_id = src_prov AND name = src_name;
                END IF;

                INSERT INTO integration_migration_map (old_connection_id, new_connection_id, project_id)
                VALUES (r.connection_id, new_conn, p.project_id)
                ON CONFLICT DO NOTHING;
            END IF;

            IF new_conn IS NOT NULL THEN
                INSERT INTO alert_rules (id, org_id, connection_id, project_id, user_id, name, level, is_active, created_at, updated_at)
                VALUES (gen_random_uuid(), r.org_id, new_conn, p.project_id, r.user_id, r.name, r.level, r.is_active, NOW(), NOW());
            END IF;
        END LOOP;

        -- Retire the original org-wide rule (kept for reversibility, deactivated).
        UPDATE alert_rules SET is_active = FALSE WHERE id = r.id;
        RAISE NOTICE 'v1.25: fanned out org-wide rule % (org %) to projects with logs', r.id, r.org_id;
    END LOOP;

    -- Step C: disable org connections left unreferenced (creds preserved, flagged).
    UPDATE integrations SET enabled = FALSE
    WHERE project_id IS NULL
      AND id NOT IN (SELECT connection_id FROM alert_rules);
END $$;
-- Version: 1.26
-- Description: Track org creator (owner) for per-user org limits and self-delete
-- Nullable: legacy orgs predate ownership and stay owner-less (they simply
-- don't count against anyone's limit and can't be self-deleted).
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS created_by UUID;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'organizations_created_by_fk') THEN
        ALTER TABLE organizations ADD CONSTRAINT organizations_created_by_fk FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS organizations_created_by_idx ON organizations (created_by);
-- Version: 1.27
-- Description: Per-project hourly level rollup backing /logs/stats, timeseries and aggregate
-- Unfiltered aggregates over logs cost ~11s on a 50M-row project. This rollup is
-- maintained incrementally on ingest, repaired by retention, and backfilled once
-- below (one full scan, ~36s over 50M rows).
--
-- The hour bucket is truncated explicitly in UTC. Plain date_trunc('hour', ts)
-- truncates in the session TimeZone, which for non-whole-hour zones (+05:45)
-- produces buckets that are not UTC hour boundaries.
--
-- source is part of the key so aggregate?groupBy=source is served from here too.
-- That relies on source being a low-cardinality service name (13 distinct across
-- the whole dataset when this was written); a high-cardinality source would
-- multiply the row count of this table.
CREATE TABLE IF NOT EXISTS log_stats_hourly (
    project_id UUID NOT NULL,
    hour TIMESTAMPTZ NOT NULL,
    source_type TEXT NOT NULL,
    source TEXT NOT NULL,
    level TEXT NOT NULL,
    count BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT log_stats_hourly_pkey PRIMARY KEY (project_id, hour, source_type, source, level),
    CONSTRAINT log_stats_hourly_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
INSERT INTO log_stats_hourly (project_id, hour, source_type, source, level, count)
SELECT
    project_id,
    (date_trunc('hour', ts AT TIME ZONE 'UTC')) AT TIME ZONE 'UTC',
    source_type,
    source,
    level,
    count(*)
FROM logs
GROUP BY 1, 2, 3, 4, 5
ON CONFLICT (project_id, hour, source_type, source, level) DO UPDATE SET count = EXCLUDED.count;

-- Version: 1.28
-- Description: Add ingest key expiry to sources for key hygiene
-- NULL means the key never expires, preserving today's behaviour for every
-- existing source. Revocation stays is_active = FALSE; expiry is the automatic
-- counterpart so a leaked key stops working without an operator acting.
ALTER TABLE sources
ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NULL;
CREATE INDEX IF NOT EXISTS sources_expires_at_idx ON sources (expires_at) WHERE expires_at IS NOT NULL;

-- Version: 1.29
-- Description: Per-org OIDC single sign-on configuration
-- One IdP per org. The client secret is AES-GCM sealed with the same key that
-- protects integration credentials, following the integrations table shape.
-- default_role is the role a just-in-time membership receives on first SSO
-- login; VIEWER keeps first login least-privileged.
CREATE TABLE IF NOT EXISTS org_sso_configs (
    org_id UUID NOT NULL,
    issuer TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_secret_enc BYTEA NOT NULL,
    client_secret_iv BYTEA NOT NULL,
    default_role org_role NOT NULL DEFAULT 'VIEWER',
    allowed_domains TEXT [] NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT org_sso_configs_pkey PRIMARY KEY (org_id),
    CONSTRAINT org_sso_configs_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE
);

-- Version: 1.30
-- Description: Audit hardening — actor IP and user agent, timezone-aware timestamps
-- A compliance log has to say where an action came from, and its timestamps have
-- to be unambiguous. The timestamp column was created without a time zone, so
-- existing rows are reinterpreted as UTC, which is what the application has
-- always written (connections are opened with timezone=utc).
ALTER TABLE audit
ADD COLUMN IF NOT EXISTS actor_ip INET NULL,
    ADD COLUMN IF NOT EXISTS actor_user_agent TEXT NULL;
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'audit' AND column_name = 'timestamp'
          AND data_type = 'timestamp without time zone'
    ) THEN
        ALTER TABLE audit
        ALTER COLUMN timestamp TYPE TIMESTAMPTZ USING timestamp AT TIME ZONE 'UTC';
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit (timestamp DESC);

-- Version: 1.31
-- Description: Cursor for audit export to an external SIEM
-- One row. The keyset cursor (timestamp, id) is persisted so a restart resumes
-- where it left off instead of replaying or skipping records. Delivery is
-- at-least-once: the cursor only advances after the destination has accepted a
-- batch, so a crash mid-batch re-sends rather than loses.
CREATE TABLE IF NOT EXISTS audit_export_cursor (
    singleton BOOLEAN NOT NULL DEFAULT TRUE,
    last_timestamp TIMESTAMPTZ NOT NULL DEFAULT to_timestamp(0),
    last_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT audit_export_cursor_pkey PRIMARY KEY (singleton),
    CONSTRAINT audit_export_cursor_singleton CHECK (singleton)
);
INSERT INTO audit_export_cursor (singleton) VALUES (TRUE) ON CONFLICT DO NOTHING;

-- Version: 1.32
-- Description: Meter app-log ingestion (JWT /v1/ingest) and give it its own quota
-- ingest_usage cannot hold these rows: its primary key is (source_id, day) with a
-- NOT NULL foreign key to sources, and app logs arrive without a source. They are
-- counted per project instead.
--
-- The quota is separate from infra rather than shared, matching how the plans
-- already separate log_retention_days from infra_retention_days.
CREATE TABLE IF NOT EXISTS app_usage (
    project_id UUID NOT NULL,
    org_id UUID NOT NULL,
    day DATE NOT NULL,
    event_count BIGINT NOT NULL DEFAULT 0,
    byte_count BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT app_usage_pkey PRIMARY KEY (project_id, day),
    CONSTRAINT app_usage_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS app_usage_org_day_idx ON app_usage (org_id, day);
UPDATE plans SET features = features || '{"app_daily_event_quota":1000000}'::jsonb WHERE slug = 'free';
UPDATE plans SET features = features || '{"app_daily_event_quota":50000000}'::jsonb WHERE slug = 'pro';
UPDATE plans SET features = features || '{"app_daily_event_quota":-1}'::jsonb WHERE slug = 'enterprise';

-- Version: 1.33
-- Description: Per-org SCIM 2.0 provisioning tokens
-- Only the hash is stored, as with ingest keys: a database dump must not yield a
-- usable provisioning credential. One active token per org keeps rotation simple
-- (issuing replaces the previous one).
CREATE TABLE IF NOT EXISTS org_scim_tokens (
    org_id UUID NOT NULL,
    token_hash TEXT NOT NULL,
    token_prefix TEXT NOT NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ NULL,
    CONSTRAINT org_scim_tokens_pkey PRIMARY KEY (org_id),
    CONSTRAINT org_scim_tokens_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS org_scim_tokens_hash_idx ON org_scim_tokens (token_hash);

-- Version: 1.34
-- Description: Alerting maturity — rule conditions, dedup/snooze/maintenance, event history
-- Rules previously carried a single `level` and fired on every matching batch,
-- with no memory: a hundred errors meant a hundred notifications. The condition
-- column generalises the trigger, and alert_events gives each alert an identity
-- so repeats collapse onto one open row.
ALTER TABLE alert_rules
ADD COLUMN IF NOT EXISTS condition JSONB,
    ADD COLUMN IF NOT EXISTS dedup_window_seconds INT NOT NULL DEFAULT 300,
    ADD COLUMN IF NOT EXISTS snooze_until TIMESTAMPTZ NULL;
-- Existing rules keep behaving identically, expressed as a level condition.
UPDATE alert_rules
SET condition = jsonb_build_object('type', 'level', 'level', level)
WHERE condition IS NULL;
ALTER TABLE alert_rules
ALTER COLUMN condition SET NOT NULL;
-- One open event per (rule, dedup_key) is what makes repeats collapse; the
-- partial unique index enforces it rather than trusting the application.
CREATE TABLE IF NOT EXISTS alert_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id UUID NOT NULL,
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    dedup_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('firing', 'acknowledged', 'resolved')),
    summary TEXT NOT NULL,
    level TEXT NOT NULL,
    match_count BIGINT NOT NULL DEFAULT 1,
    sample_log_id UUID NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_notified_at TIMESTAMPTZ NULL,
    resolved_at TIMESTAMPTZ NULL,
    acknowledged_at TIMESTAMPTZ NULL,
    acknowledged_by UUID NULL,
    CONSTRAINT alert_events_rule_fk FOREIGN KEY (rule_id) REFERENCES alert_rules (id) ON DELETE CASCADE,
    CONSTRAINT alert_events_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT alert_events_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS alert_events_open_uq ON alert_events (rule_id, dedup_key)
WHERE state <> 'resolved';
CREATE INDEX IF NOT EXISTS alert_events_org_time_idx ON alert_events (org_id, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS alert_events_project_time_idx ON alert_events (project_id, last_seen_at DESC);
-- A maintenance window suppresses delivery without disabling rules, so nothing
-- has to be remembered and switched back on afterwards.
CREATE TABLE IF NOT EXISTS alert_maintenance_windows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NULL,
    reason TEXT NOT NULL DEFAULT '',
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    created_by UUID NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT amw_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT amw_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    CONSTRAINT amw_range CHECK (ends_at > starts_at)
);
CREATE INDEX IF NOT EXISTS amw_org_window_idx ON alert_maintenance_windows (org_id, starts_at, ends_at);
