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

-- Version: 1.35
-- Description: Indexes backing full-range log search
-- These were built against production with CREATE INDEX CONCURRENTLY, which
-- cannot run here: darwin executes each migration inside a transaction and
-- Postgres rejects CONCURRENTLY in one. So this migration is a no-op on any
-- environment that already has them, and a plain (locking) build elsewhere —
-- acceptable on a fresh or small database, not on a large live one. Build those
-- by hand, concurrently.
--
-- Measured over the full retained range with values that match nothing:
--   message/source trigram BitmapOr   12.9 ms   (was a 10.9 s sequential scan)
--   source equality                    0.1 ms
--   meta containment                   2.2 ms
-- The trigram pair is what lifts the previous two-hour search window.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS logs_message_trgm_idx ON logs USING gin (message gin_trgm_ops);
CREATE INDEX IF NOT EXISTS logs_source_trgm_idx ON logs USING gin (source gin_trgm_ops);
CREATE INDEX IF NOT EXISTS logs_project_source_ts_idx ON logs (project_id, source, ts DESC);
CREATE INDEX IF NOT EXISTS logs_meta_gin_idx ON logs USING gin (meta jsonb_path_ops);
-- Kept small and deliberately not relied upon: Postgres mis-estimates array
-- containment badly enough that it prefers the ordered ts walk, so tag filters
-- stay window-bound in the application. See QueryFilter.scanFilters.
CREATE INDEX IF NOT EXISTS logs_tags_gin_idx ON logs USING gin (tags);

-- Version: 1.36
-- Description: Saved views and dashboards
-- Both are a named, owned, org-scoped JSON document, so they share one shape and
-- one visibility rule. The definition itself is opaque to the backend: it is the
-- frontend's query and layout state, round-tripped verbatim. Only its size is
-- policed, since nothing here can validate its meaning.
--
-- visibility is 'private' (creator only) or 'org' (every member). project_id is
-- optional: a view pinned to a project is only offered to callers who can see
-- that project, while a null project_id spans the org.
DO $$ BEGIN CREATE TYPE view_visibility AS ENUM ('private', 'org');
EXCEPTION
WHEN duplicate_object THEN NULL;
END $$;
CREATE TABLE IF NOT EXISTS saved_views (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    query JSONB NOT NULL DEFAULT '{}'::jsonb,
    visibility view_visibility NOT NULL DEFAULT 'org',
    created_by UUID NOT NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT saved_views_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT saved_views_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    CONSTRAINT saved_views_owner_fk FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT saved_views_name_not_blank CHECK (length(btrim(name)) > 0)
);
CREATE INDEX IF NOT EXISTS saved_views_org_idx ON saved_views (org_id, name);
CREATE INDEX IF NOT EXISTS saved_views_owner_idx ON saved_views (created_by);
CREATE TABLE IF NOT EXISTS dashboards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    panels JSONB NOT NULL DEFAULT '[]'::jsonb,
    visibility view_visibility NOT NULL DEFAULT 'org',
    created_by UUID NOT NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT dashboards_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT dashboards_owner_fk FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT dashboards_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT dashboards_panels_is_array CHECK (jsonb_typeof(panels) = 'array')
);
CREATE INDEX IF NOT EXISTS dashboards_org_idx ON dashboards (org_id, name);
CREATE INDEX IF NOT EXISTS dashboards_owner_idx ON dashboards (created_by);

-- Version: 1.37
-- Description: Per-source hourly ingest counters backing source health
-- The Sources UI needs "events and errors in the last 24 hours" per source.
-- Counting that from logs costs 735ms for a single source at 700k events/day
-- (bitmap scan plus a 7,600 block heap fetch for the level), and grows with
-- ingest, so it is rolled up at write time instead — the same trade the
-- log_stats_hourly rollup makes for /logs/stats.
--
-- The counters come from the ingest path's existing async usage recorder rather
-- than the logs transaction, so they describe what was accepted, not what a
-- later purge left behind. That is the right basis for a health signal and it
-- keeps this table out of retention's rollup-repair contract; rows are simply
-- pruned once they age past the health window.
CREATE TABLE IF NOT EXISTS ingest_stats_hourly (
    source_id     UUID NOT NULL,
    hour          TIMESTAMPTZ NOT NULL,
    event_count   BIGINT NOT NULL DEFAULT 0,
    error_count   BIGINT NOT NULL DEFAULT 0,
    dropped_count BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT ingest_stats_hourly_pkey PRIMARY KEY (source_id, hour),
    CONSTRAINT ingest_stats_hourly_source_fk FOREIGN KEY (source_id) REFERENCES sources (id) ON DELETE CASCADE
);
-- Pruning deletes by age across every source, which the primary key cannot serve.
CREATE INDEX IF NOT EXISTS ingest_stats_hourly_hour_idx ON ingest_stats_hourly (hour);

-- Version: 1.38
-- Description: Permalinks, log annotations and read-only API keys
-- Three small tables behind the sharing and programmatic-access surface.
--
-- A permalink is a short slug standing for either one log line or a frozen
-- query, so a link pasted into a chat still opens the same view a week later.
-- The slug is the lookup key rather than the id because it travels in URLs.
CREATE TABLE IF NOT EXISTS log_permalinks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NULL,
    slug TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('log', 'query')),
    log_id UUID NULL,
    query JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID NOT NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT log_permalinks_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT log_permalinks_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    CONSTRAINT log_permalinks_owner_fk FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE CASCADE,
    -- A log permalink names a log in a project; a query permalink carries a query.
    CONSTRAINT log_permalinks_target CHECK (
        (kind = 'log' AND log_id IS NOT NULL AND project_id IS NOT NULL) OR
        (kind = 'query' AND log_id IS NULL)
    )
);
CREATE INDEX IF NOT EXISTS log_permalinks_org_idx ON log_permalinks (org_id, date_created DESC);

-- There is deliberately no foreign key from log_id to logs: retention deletes
-- logs, and a permalink to a purged log should report that the log is gone
-- rather than vanish and turn a shared link into a 404 with no explanation.

-- An annotation is a note anchored in time, either to one log line or to a
-- moment ("deployed 4.12 here"). Both carry a ts so one index serves the log
-- view and the chart overlay.
CREATE TABLE IF NOT EXISTS log_annotations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    log_id UUID NULL,
    ts TIMESTAMPTZ NOT NULL,
    body TEXT NOT NULL,
    created_by UUID NOT NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    date_updated TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT log_annotations_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT log_annotations_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    CONSTRAINT log_annotations_owner_fk FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT log_annotations_body_not_blank CHECK (length(btrim(body)) > 0)
);
CREATE INDEX IF NOT EXISTS log_annotations_project_ts_idx ON log_annotations (project_id, ts DESC);
CREATE INDEX IF NOT EXISTS log_annotations_log_idx ON log_annotations (log_id) WHERE log_id IS NOT NULL;

-- API keys authenticate the read-only query API, which scripts and CI use in
-- place of a user session. They are read-only by construction: ingest keys live
-- in sources and are a separate scheme, so a leaked query key cannot write.
CREATE TABLE IF NOT EXISTS api_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NULL,
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by UUID NULL,
    last_used_at TIMESTAMPTZ NULL,
    expires_at TIMESTAMPTZ NULL,
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT api_keys_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT api_keys_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE,
    CONSTRAINT api_keys_owner_fk FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT api_keys_name_not_blank CHECK (length(btrim(name)) > 0)
);
-- Authentication looks a key up by hash, so that lookup must be unique and fast.
CREATE UNIQUE INDEX IF NOT EXISTS api_keys_key_hash_idx ON api_keys (key_hash);
CREATE INDEX IF NOT EXISTS api_keys_org_idx ON api_keys (org_id, date_created DESC);

-- Version: 1.39
-- Description: Integration provider catalog — seven new providers, and the
-- original ten moved under migration control
-- The catalog is the API's own list of what can be configured, and a row is only
-- reachable if a Caller is registered for the same id — Create refuses an
-- unknown provider — so these land together with their implementations.
--
-- The first ten rows are not new. They were only ever inserted by seed.sql,
-- which also inserts sample users, so a deployment that ran migrations without
-- seeding got an empty catalog and no integrations at all. They are product
-- data, not sample data, so they belong here. ON CONFLICT makes this a no-op
-- wherever they already exist, including production.
--
-- The seven that follow were chosen for coverage rather than count: Teams and
-- Google Chat are the two chat destinations a business customer is most likely
-- to already live in, Mattermost is the self-hosted answer to the same
-- question, WhatsApp reaches a phone in the markets where SMS is the fallback
-- rather than the norm, and GitHub and Linear put an alert where the person who
-- has to fix it already works.
INSERT INTO integration_providers (id, name, icon, type, description, fields, sort_order)
VALUES
    (
        'slack',
        'Slack',
        '💬',
        'Messaging',
        'Send alerts to Slack channels via webhook.',
        '[{"k": "webhookUrl", "ph": "https://hooks.slack.com/services/...", "label": "Webhook URL"}]',
        1
    ),
    (
        'discord',
        'Discord',
        '🎮',
        'Messaging',
        'Forward log alerts to Discord via webhook.',
        '[{"k": "webhookUrl", "ph": "https://discord.com/api/webhooks/...", "label": "Webhook URL"}]',
        2
    ),
    (
        'telegram',
        'Telegram',
        '✈️',
        'Messaging',
        'Receive alerts as Telegram bot messages.',
        '[{"k": "botToken", "ph": "123456:ABC...", "label": "Bot Token"}, {"k": "chatId", "ph": "-100123", "label": "Chat ID"}]',
        3
    ),
    (
        'pagerduty',
        'PagerDuty',
        '🚨',
        'Incident',
        'Auto-create PagerDuty incidents on critical errors.',
        '[{"k": "apiKey", "ph": "u+xxxxxxxx", "label": "API Key"}, {"k": "serviceId", "ph": "P1234AB", "label": "Service ID"}]',
        4
    ),
    (
        'webhook',
        'Webhook',
        '🔗',
        'Custom',
        'POST structured JSON to any HTTP endpoint.',
        '[{"k": "url", "ph": "https://yourapp.com/hook", "label": "Target URL"}, {"k": "secret", "ph": "optional HMAC secret", "label": "Secret"}]',
        5
    ),
    (
        'email',
        'Email',
        '📧',
        'Notify',
        'Send email alerts when log events trigger.',
        '[{"k": "to", "ph": "team@co.com", "label": "To Address"}]',
        6
    ),
    (
        'opsgenie',
        'OpsGenie',
        '🔔',
        'Incident',
        'Create OpsGenie alerts for on-call escalation.',
        '[{"k": "apiKey", "ph": "xxxx-xxxx-xxxx", "label": "API Key"}]',
        7
    ),
    (
        'jira',
        'Jira',
        '🧩',
        'Ticketing',
        'Open Jira issues automatically on ERROR logs.',
        '[{"k": "domain", "ph": "org.atlassian.net", "label": "Domain"}, {"k": "email", "ph": "you@org.com", "label": "Account Email"}, {"k": "token", "ph": "ATATT...", "label": "API Token"}, {"k": "project", "ph": "ENG", "label": "Project Key"}]',
        8
    ),
    (
        'twilio',
        'Twilio',
        '📱',
        'SMS',
        'Send SMS alerts to a phone number via Twilio.',
        '[{"k": "accountSid", "ph": "ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "label": "Account SID"}, {"k": "authToken", "ph": "", "label": "Auth Token"}, {"k": "from", "ph": "+12345678900", "label": "From Number"}, {"k": "to", "ph": "+12345678900", "label": "To Number"}]',
        9
    ),
    (
        'beemsms',
        'Beem Africa',
        '📲',
        'SMS',
        'Send SMS alerts via Beem Africa.',
        '[{"k": "apiKey", "ph": "", "label": "API Key"}, {"k": "secretKey", "ph": "", "label": "Secret Key"}, {"k": "senderId", "ph": "MYAPP", "label": "Sender ID"}, {"k": "to", "ph": "+255700000000", "label": "To Number"}]',
        10
    ),
    (
        'msteams',
        'Microsoft Teams',
        '🟣',
        'Messaging',
        'Post alerts to a Teams channel via a Workflows webhook.',
        '[{"k":"webhookUrl","label":"Workflow URL","ph":"https://prod-00.westeurope.logic.azure.com:443/workflows/..."}]',
        11
    ),
    (
        'googlechat',
        'Google Chat',
        '💠',
        'Messaging',
        'Post alerts to a Google Chat space via webhook.',
        '[{"k":"webhookUrl","label":"Webhook URL","ph":"https://chat.googleapis.com/v1/spaces/.../messages?key=..."}]',
        12
    ),
    (
        'mattermost',
        'Mattermost',
        '🔷',
        'Messaging',
        'Post alerts to a Mattermost channel via an incoming webhook.',
        '[{"k":"webhookUrl","label":"Webhook URL","ph":"https://mattermost.example.com/hooks/xxxxxxxx"},{"k":"channel","label":"Channel override (optional)","ph":"alerts"}]',
        13
    ),
    (
        'whatsapp',
        'WhatsApp',
        '🟢',
        'Messaging',
        'Send alerts to WhatsApp via the Business Cloud API. Set a template name — free-form text is only delivered within 24 hours of the recipient messaging you.',
        '[{"k":"phoneNumberId","label":"Phone Number ID","ph":"123456789012345"},{"k":"accessToken","label":"Access Token","ph":""},{"k":"to","label":"To Number","ph":"+255700000000"},{"k":"templateName","label":"Template Name (recommended)","ph":"streamlogia_alert"},{"k":"templateLanguage","label":"Template Language (optional)","ph":"en"}]',
        14
    ),
    (
        'github',
        'GitHub',
        '🐙',
        'Ticketing',
        'Open a GitHub issue when an alert fires.',
        '[{"k":"owner","label":"Owner","ph":"my-org"},{"k":"repo","label":"Repository","ph":"my-service"},{"k":"token","label":"Access Token","ph":"github_pat_..."},{"k":"labels","label":"Labels (optional, comma separated)","ph":"incident,logs"},{"k":"apiBaseUrl","label":"API Base URL (optional, Enterprise)","ph":"https://github.example.com/api/v3"}]',
        15
    ),
    (
        'linear',
        'Linear',
        '📐',
        'Ticketing',
        'Create a Linear issue when an alert fires.',
        '[{"k":"apiKey","label":"API Key","ph":"lin_api_..."},{"k":"teamId","label":"Team ID","ph":"a1b2c3d4-..."},{"k":"priority","label":"Priority 0-4 (optional)","ph":"2"}]',
        16
    ),
    (
        'africastalking',
        'Africa''s Talking',
        '📶',
        'SMS',
        'Send SMS alerts via Africa''s Talking.',
        '[{"k":"username","label":"Username","ph":"myapp"},{"k":"apiKey","label":"API Key","ph":""},{"k":"to","label":"To Number","ph":"+254700000000"},{"k":"from","label":"Sender ID (optional)","ph":"MYAPP"},{"k":"sandbox","label":"Sandbox? true/false (optional)","ph":"false"}]',
        17
    )
ON CONFLICT (id) DO NOTHING;

-- Version: 1.40
-- Description: Client error monitoring — raw events, grouped issues, and facets
-- Browser crash reports. These are the application's own errors, not customer
-- log data, and they are deliberately kept in their own tables: the volume,
-- retention and privacy rules are all different from logs.
--
-- The events table doubles as the ingest queue. There is no broker here, and
-- adding one to serve a few thousand browser errors a day would be a second
-- system to operate for no gain; a claim with FOR UPDATE SKIP LOCKED is the
-- standard Postgres pattern and it is durable, which an in-memory channel is
-- not — returning 202 for an event that a restart then loses would be a lie.
CREATE TABLE IF NOT EXISTS client_error_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Client-generated, and the idempotency key: sendBeacon and a retry after a
    -- failed flush both resend the same event.
    event_id UUID NOT NULL,

    -- Nullable because errors happen before login, on the landing and auth
    -- pages, which is exactly where they matter most. An anonymous event has no
    -- org and is visible only to a super admin.
    org_id UUID NULL,
    user_id UUID NULL,
    role TEXT NULL,

    level TEXT NOT NULL CHECK (level IN ('fatal', 'error', 'warning')),
    kind TEXT NOT NULL CHECK (kind IN ('unhandled', 'unhandledrejection', 'react', 'api', 'manual')),
    name TEXT NOT NULL,
    message TEXT NOT NULL,
    stack TEXT NOT NULL DEFAULT '',
    component_stack TEXT NOT NULL DEFAULT '',

    release TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',

    api JSONB NULL,
    breadcrumbs JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- occurred_at is the browser's clock and cannot be trusted; received_at is
    -- ours. Both are kept because the gap is itself a signal (a beacon flushed
    -- after a long unload, a device with a wrong clock).
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Grouping is done by the worker, not at ingest, so a poisoned event cannot
    -- fail the request that delivered it.
    fingerprint TEXT NULL,
    fingerprint_version INT NOT NULL DEFAULT 0,
    issue_id UUID NULL,
    processed_at TIMESTAMPTZ NULL,
    process_attempts INT NOT NULL DEFAULT 0,
    process_error TEXT NULL,

    -- How many identical events this row stands for when the client sampled.
    -- Totals stay honest without storing every occurrence.
    sampled_count INT NOT NULL DEFAULT 1 CHECK (sampled_count >= 1),

    CONSTRAINT client_error_events_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT client_error_events_user_fk FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL
);

-- Idempotency. A duplicate delivery is a no-op insert, not a double count.
CREATE UNIQUE INDEX IF NOT EXISTS client_error_events_event_id_uq ON client_error_events (event_id);

-- The queue claim: only unprocessed rows, oldest first.
CREATE INDEX IF NOT EXISTS client_error_events_unprocessed_idx
    ON client_error_events (received_at) WHERE processed_at IS NULL;

-- Reading an issue's recent events, and pruning by age.
CREATE INDEX IF NOT EXISTS client_error_events_issue_time_idx ON client_error_events (issue_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS client_error_events_received_idx ON client_error_events (received_at);
CREATE INDEX IF NOT EXISTS client_error_events_org_time_idx ON client_error_events (org_id, occurred_at DESC);

-- An issue is one fingerprint. Scoped per org so two tenants hitting the same
-- bug triage it independently, and so a deletion for one org cannot remove the
-- other's history. Anonymous events group together under a null org.
CREATE TABLE IF NOT EXISTS client_error_issues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NULL,
    fingerprint TEXT NOT NULL,

    title TEXT NOT NULL,
    culprit TEXT NOT NULL DEFAULT '',
    level TEXT NOT NULL,
    kind TEXT NOT NULL,

    status TEXT NOT NULL DEFAULT 'unresolved' CHECK (status IN ('unresolved', 'resolved', 'ignored')),

    -- regressed marks an issue that came back after being resolved. It is
    -- separate from status because the alert is about the transition, while the
    -- status is about what a human decided.
    regressed BOOLEAN NOT NULL DEFAULT FALSE,

    event_count BIGINT NOT NULL DEFAULT 0,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ NULL,
    assignee_id UUID NULL,
    sample_event_id UUID NULL,

    CONSTRAINT client_error_issues_org_fk FOREIGN KEY (org_id) REFERENCES organizations (id) ON DELETE CASCADE,
    CONSTRAINT client_error_issues_assignee_fk FOREIGN KEY (assignee_id) REFERENCES users (id) ON DELETE SET NULL
);

-- One issue per fingerprint per org. A partial unique index handles the
-- anonymous bucket, because NULL org_id would otherwise never collide.
CREATE UNIQUE INDEX IF NOT EXISTS client_error_issues_org_fp_uq
    ON client_error_issues (org_id, fingerprint) WHERE org_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS client_error_issues_anon_fp_uq
    ON client_error_issues (fingerprint) WHERE org_id IS NULL;

CREATE INDEX IF NOT EXISTS client_error_issues_org_seen_idx ON client_error_issues (org_id, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS client_error_issues_status_idx ON client_error_issues (org_id, status, last_seen_at DESC);

-- Distinct users, orgs and releases per issue. A counter column cannot answer
-- "how many people hit this" without knowing whether it has seen this person
-- before, so the set is stored and counted; at browser-error volume these
-- tables are tiny.
CREATE TABLE IF NOT EXISTS client_error_issue_facets (
    issue_id UUID NOT NULL,
    facet TEXT NOT NULL CHECK (facet IN ('user', 'org', 'release')),
    value TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT client_error_issue_facets_pkey PRIMARY KEY (issue_id, facet, value),
    CONSTRAINT client_error_issue_facets_issue_fk FOREIGN KEY (issue_id) REFERENCES client_error_issues (id) ON DELETE CASCADE
);


-- Version: 1.41
-- Description: Scope client errors to a project
-- On the numbering: darwin parses a version as a float, so 1.40 was recorded as
-- 1.4 and this one sorts after it by value rather than by text. Keep going up
-- from here (1.42, 1.43); a bare 1.5 would also sort correctly but would make
-- the file order and the applied order diverge. And never edit an applied
-- migration, comments included — the checksum covers the whole body.
-- A crash is attributed to the project the user was working in when it
-- happened. That is what lets alerting reuse what already exists: every alert
-- rule and integration connection in the product is project-scoped, so a
-- client error with a project routes through the same rules, the same channels,
-- the same dedup window and the same maintenance windows as everything else.
--
-- Nullable, because two cases legitimately have no project: a crash on the
-- landing or login page, and a report from a signed-in user who was not looking
-- at a project. Those group and triage as before and simply cannot alert.
ALTER TABLE client_error_events ADD COLUMN IF NOT EXISTS project_id UUID NULL;
ALTER TABLE client_error_issues ADD COLUMN IF NOT EXISTS project_id UUID NULL;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'client_error_events_project_fk') THEN
        ALTER TABLE client_error_events
        ADD CONSTRAINT client_error_events_project_fk FOREIGN KEY (project_id)
            REFERENCES projects (id) ON DELETE CASCADE;
    END IF;
END $$;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'client_error_issues_project_fk') THEN
        ALTER TABLE client_error_issues
        ADD CONSTRAINT client_error_issues_project_fk FOREIGN KEY (project_id)
            REFERENCES projects (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS client_error_events_project_time_idx
    ON client_error_events (project_id, occurred_at DESC) WHERE project_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS client_error_issues_project_seen_idx
    ON client_error_issues (project_id, last_seen_at DESC) WHERE project_id IS NOT NULL;

-- An issue is one fingerprint within one scope, and the scope is now the
-- project when there is one. Two teams in the same org hitting the same bug in
-- different projects triage it separately, because they will fix it separately.
--
-- The three indexes are mutually exclusive by their WHERE clauses, so exactly
-- one governs any given row. The old pair is replaced rather than kept: leaving
-- the org-wide one in place would forbid the same fingerprint in two projects
-- of one org, which is the case this migration exists to allow.
DROP INDEX IF EXISTS client_error_issues_org_fp_uq;
DROP INDEX IF EXISTS client_error_issues_anon_fp_uq;

CREATE UNIQUE INDEX IF NOT EXISTS client_error_issues_project_fp_uq
    ON client_error_issues (project_id, fingerprint) WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS client_error_issues_org_fp_uq
    ON client_error_issues (org_id, fingerprint) WHERE project_id IS NULL AND org_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS client_error_issues_anon_fp_uq
    ON client_error_issues (fingerprint) WHERE project_id IS NULL AND org_id IS NULL;

-- Version: 1.42
-- Description: Source map artifacts and resolved client error stacks
-- A production stack trace is useless on its own: the whole bundle is one line,
-- every function is renamed to a letter, and the only real information is a
-- column offset. The source map the build already emits turns
-- index-64s.js:1:24817 back into LogsView.jsx:142:8 handleFilterChange.
--
-- Maps are stored gzipped because they are megabytes of JSON that compress by
-- roughly an order of magnitude, and they are stored here rather than served
-- from the CDN because publishing them publishes the source. Nothing reads this
-- table except the grouping worker.
CREATE TABLE IF NOT EXISTS client_error_artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The release the map belongs to, matched against the release each event
    -- reports. Uploading the same file for a release again replaces it, so a
    -- re-run of a deploy job is idempotent.
    release TEXT NOT NULL,

    -- The generated file the map describes, as it appears in a stack trace —
    -- "index-64s.js", not "index-64s.js.map". Resolution looks up by this.
    file_name TEXT NOT NULL,

    content BYTEA NOT NULL,
    byte_size INT NOT NULL,
    compressed BOOLEAN NOT NULL DEFAULT TRUE,
    uploaded_by TEXT NOT NULL DEFAULT '',
    date_created TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT client_error_artifacts_uq UNIQUE (release, file_name),
    CONSTRAINT client_error_artifacts_release_not_blank CHECK (length(btrim(release)) > 0)
);
CREATE INDEX IF NOT EXISTS client_error_artifacts_release_idx ON client_error_artifacts (release);

-- The de-minified stack, kept beside the raw one rather than replacing it: the
-- raw stack is what the browser actually sent and the only thing that can be
-- re-resolved if a map is uploaded late or turns out to be wrong.
ALTER TABLE client_error_events ADD COLUMN IF NOT EXISTS resolved_stack TEXT NULL;

-- Re-grouping after a late upload needs to find the events of one release that
-- were fingerprinted without a map.
CREATE INDEX IF NOT EXISTS client_error_events_regroup_idx
    ON client_error_events (release, fingerprint_version)
    WHERE processed_at IS NOT NULL;

-- Version: 1.43
-- Description: Index the client error time range spike detection scans
-- Spike detection asks "how many events did each issue get in the last ten
-- minutes, and how many in the hour before that" — a time range across every
-- issue at once, which none of the existing indexes serve: they are keyed by
-- issue, org or project first. Partial on issue_id because an ungrouped event
-- has no rate to compare.
CREATE INDEX IF NOT EXISTS client_error_events_occurred_idx
    ON client_error_events (occurred_at DESC) WHERE issue_id IS NOT NULL;
