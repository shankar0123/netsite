-- 0002_extensions.sql — required Postgres extensions.
--
-- What: enables `citext` for case-insensitive text columns.
-- How:  CREATE EXTENSION IF NOT EXISTS makes this idempotent.
-- Why now: `users.email` is a citext column in 0003_auth_core.sql.
--          Extensions must exist before tables that reference them.
-- Why citext at all: email lookups ("login as ALICE@example.com" vs
--          stored "alice@example.com") would otherwise need
--          LOWER(email) = LOWER($1) at every comparison site, which
--          defeats the indexed lookup. citext stores case as-given
--          but compares case-insensitively, which is exactly the
--          contract email addresses follow per RFC 5321.

CREATE EXTENSION IF NOT EXISTS citext;
