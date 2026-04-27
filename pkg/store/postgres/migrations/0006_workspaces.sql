-- Copyright 2026 Shankar Reddy. All Rights Reserved.
--
-- Licensed under the Business Source License 1.1 (the "License").
-- You may not use this file except in compliance with the License.
-- A copy of the License is bundled with this distribution at ./LICENSE
-- in the repository root, or available at https://mariadb.com/bsl11/.
--
-- Licensed Work:  NetSite
-- Change Date:    2125-01-01
-- Change License: Apache License, Version 2.0
--
-- On the Change Date, the rights granted in this License terminate and
-- you are granted rights under the Change License instead.
--
-- 0006_workspaces.sql — Saved-view bundles per Task 0.23.
--
-- What:
--   A workspace is an operator-saved set of "pinned views" — each
--   view is a netql query (or a deep-link to a dashboard route) plus
--   a name. An operator opens a workspace and gets back exactly the
--   page they last saved: same charts, same filters, same time
--   range. URL deep-links to a workspace also reproduce its state
--   in a fresh tab.
--
-- How:
--   - `id` is a prefixed-TEXT primary key (`wks-<short>`), chosen by
--     the handler at create time. ON DELETE CASCADE on the tenant
--     reference removes orphans cleanly.
--   - `views` is JSONB so we can extend the per-view schema without
--     a migration each time.
--   - `share_slug` is a separate column, UNIQUE so two workspaces
--     can never collide on the same slug. NULL when the workspace
--     is tenant-internal (the default).
--   - `share_expires_at` enables a 7-day default for signed share
--     links per the design note. NULL means "no expiry".
--
-- Why this shape rather than a separate share-link table:
--   At v0.0.11 there is one slug per workspace; introducing a
--   workspace_share_links table would invent extra surface area
--   that v0.0.11 does not need. When we add multi-recipient
--   sharing (Phase 2), promoting `share_slug`/`share_expires_at`
--   into a join table is a one-migration step.
--
-- Idempotency: every CREATE is guarded by IF NOT EXISTS; rerunning
-- this migration on a populated DB is a no-op.

CREATE TABLE IF NOT EXISTS workspaces (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    owner_user_id    TEXT NOT NULL REFERENCES users(id),
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    views            JSONB NOT NULL DEFAULT '[]'::jsonb,
    share_slug       TEXT UNIQUE,
    share_expires_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS workspaces_tenant_id_idx
    ON workspaces (tenant_id);

CREATE INDEX IF NOT EXISTS workspaces_owner_user_id_idx
    ON workspaces (owner_user_id);

-- Partial index: only shared workspaces have a non-NULL slug.
-- Queries that resolve a slug to a workspace hit this much smaller
-- index rather than scanning the full table.
CREATE INDEX IF NOT EXISTS workspaces_share_slug_idx
    ON workspaces (share_slug)
    WHERE share_slug IS NOT NULL;
