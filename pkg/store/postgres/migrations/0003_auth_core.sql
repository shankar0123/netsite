-- 0003_auth_core.sql — tenants, users, sessions.
--
-- What: the three tables that back NetSite's authentication and
--       multi-tenancy boundary in Phase 0.
-- How:  CREATE TABLE IF NOT EXISTS + CREATE INDEX IF NOT EXISTS.
--       Foreign keys are declared with ON DELETE behaviour; no
--       cascading on tenant or user removal — auditors expect
--       deactivation rather than deletion for compliance.
-- Why these three:
--       - tenants is the multi-tenancy root. Even single-tenant
--         deployments have exactly one row here so the FK chain
--         stays consistent for an acquirer-operated multi-tenant
--         hosted offering later.
--       - users carries password_hash + role + tenant FK. Phase 0
--         auth is local bcrypt; OIDC ships in Phase 1 (D11) on this
--         same table.
--       - sessions backs cookie-based browser sessions; opaque
--         session IDs are stored here, not in cookie payloads, so
--         server-side revocation is one DELETE.
--
-- Why prefixed-TEXT IDs (CLAUDE.md):
--       tenants:  tnt-<slug>
--       users:    usr-<short-uuid-without-dashes>
--       sessions: ses-<short-uuid-without-dashes>
--       Human-greppable in logs, no UUID parsing in URL routes, and
--       backward-compatible if we ever switch to a different ID
--       scheme since callers treat them as opaque TEXT.
--
-- Why role is a CHECK constraint, not an enum type:
--       Postgres ENUM types require a separate migration to add a
--       value, and ALTER TYPE … ADD VALUE cannot run inside a
--       transaction. CHECK with a TEXT column is dumber, more
--       portable, and lets us extend the role set in a single
--       migration.

CREATE TABLE IF NOT EXISTS tenants (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    email           CITEXT NOT NULL,
    password_hash   TEXT NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- email is unique within a tenant (citext makes the comparison
-- case-insensitive). Two tenants may have a user with the same
-- email — they are separate principals with separate password
-- hashes and roles.
CREATE UNIQUE INDEX IF NOT EXISTS users_tenant_email_idx
    ON users (tenant_id, email);

CREATE INDEX IF NOT EXISTS users_tenant_idx
    ON users (tenant_id);

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id),
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sessions_user_idx
    ON sessions (user_id);

-- expires_at index supports the cleanup query that periodically
-- prunes expired rows (Phase 0 ships this as a manual SQL script;
-- a session-cleanup background task lands in Task 0.13).
CREATE INDEX IF NOT EXISTS sessions_expires_idx
    ON sessions (expires_at);
