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
-- 0007_annotations.sql — operator-pinned notes per Task 0.24.
--
-- What:
--   An annotation is a tiny markdown note attached to a (scope,
--   scope_id, timestamp) tuple. Operators write things like
--   "rolled forward 12:30 UTC" pinned to a canary's failure
--   timestamp; the canary detail view surfaces the annotation as a
--   marker on the timeline. Annotations are immutable; correcting
--   a typo means delete + recreate so the audit trail stays clean.
--
-- How:
--   - `id` is `ann-<short>`, prefixed-TEXT per CLAUDE.md.
--   - `scope` is constrained via CHECK to the v0.0.12 set:
--     'canary', 'pop', 'test'. Future scopes (prefix, asn, route)
--     are added by ALTER TABLE … DROP CONSTRAINT + ADD CONSTRAINT
--     in a single forward migration. We deliberately do not use a
--     Postgres ENUM type because ALTER TYPE … ADD VALUE cannot run
--     inside a transaction, which breaks our migration runner's
--     one-transaction-per-file rule.
--   - `scope_id` is the prefixed ID of the scoped object
--     (`tst-foo`, `pop-lhr-01`, etc). We don't FK it because the
--     scope set is intentionally polymorphic; making the FK
--     conditional on scope adds complexity without adding safety
--     (the list query is the consumer; missing scope_id rows fall
--     out as no-results naturally).
--   - `at` is the event timestamp the operator is annotating —
--     NOT created_at. The two often differ: an operator writes a
--     postmortem note on Tuesday for an outage that happened
--     Monday at 03:14 UTC.
--   - `body_md` is markdown. We render server-side only on share
--     paths; the React shell renders client-side so trusted-HTML
--     concerns stay scoped.
--
-- Why a single composite index on (tenant_id, scope, scope_id, at):
--   Every list query is "annotations for tenant X, scope Y, scope_id
--   Z, ordered by at" — this single index covers it. Adding more
--   indexes would slow inserts without speeding any query we
--   actually run.

CREATE TABLE IF NOT EXISTS annotations (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scope       TEXT NOT NULL CHECK (scope IN ('canary', 'pop', 'test')),
    scope_id    TEXT NOT NULL,
    at          TIMESTAMPTZ NOT NULL,
    body_md     TEXT NOT NULL DEFAULT '',
    author_id   TEXT NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS annotations_scope_idx
    ON annotations (tenant_id, scope, scope_id, at);

CREATE INDEX IF NOT EXISTS annotations_author_idx
    ON annotations (author_id);
