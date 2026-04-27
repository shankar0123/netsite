# Postgres migrations

> The migration runner lives in [`../migrate.go`](../migrate.go). The
> integration test that proves idempotency lives in
> [`../migrate_test.go`](../migrate_test.go).

## Rules

1. **Forward-only.** No down migrations. To revert a change, write a new
   forward migration that undoes it. Down migrations encourage
   environment drift between dev and prod.
2. **Idempotent.** Every statement uses `IF NOT EXISTS`, `IF EXISTS`,
   `ON CONFLICT DO NOTHING`, or equivalent. Re-running an applied
   migration must be a no-op even if the runner's tracking row is
   missing. Two reasons:
   - Defense in depth against tracking-table loss or hand-edits.
   - Lets the same SQL serve as a "schema-from-scratch bootstrap" that
     can be applied to a fresh database without going through the runner.
3. **Never edit a file once it has been applied to any environment.**
   Editing a recorded migration makes the schema's history unverifiable.
   To change behaviour, write a new migration with a higher number.
4. **One logical change per file.** A single `0007_add_users_email.sql`
   that adds a column, backfills it, and adds a NOT NULL constraint is
   fine; a `0007_misc.sql` that bundles unrelated changes is not.
5. **Naming: `NNNN_short_description.sql`.** Four-digit zero-padded
   prefix (so `0001` … `9999`), then `_`, then a snake_case description.
   The runner sorts files lexicographically, which matches the prefix
   numerically as long as you stay zero-padded.
6. **One transaction per file.** The runner wraps each file in a single
   transaction. If you need DDL that cannot run inside a transaction
   (`CREATE INDEX CONCURRENTLY`, `ALTER TYPE … ADD VALUE` for an enum),
   put that statement in its own file and do not mix it with other DDL.
7. **No application-level data in migrations.** Reference data (lookup
   tables, default tenant rows) is loaded by the application or by a
   seed command, not by a migration. Migrations are for schema only,
   with the smallest possible exception for sentinel/ops rows.

## Tracking table

The runner maintains `_schema_migrations(name TEXT PRIMARY KEY,
applied_at TIMESTAMPTZ)`. Inspect it directly to audit what has been
applied:

```sql
SELECT name, applied_at FROM _schema_migrations ORDER BY name;
```

A `name` column entry is the file name (e.g. `0001_init.sql`), so the
audit log lines up one-to-one with the files in this directory.

## Adding a migration

1. Pick the next prefix. Check the current highest with
   `ls migrations/*.sql | tail -1`.
2. Create the new file. Begin with a 5–15 line comment block under the
   project documentation standard (CLAUDE.md → Documentation Discipline):
   what the change does, how it does it, and why now.
3. Make every statement idempotent.
4. Add or update the integration-test assertion in `../migrate_test.go`
   that proves the new migration is idempotent and produces the expected
   end state.
5. Run `make test-integration` locally (requires Docker for
   testcontainers).

## Why no `golang-migrate` / `goose` / `sqlc`

NetSite's migration runner is ~150 lines of code with no external
dependencies beyond `pgx` and `embed`. Pulling in `golang-migrate`
or `goose` would add a CLI, configuration file format, version-locking
model, and dependency tree that we do not need for forward-only
idempotent migrations. `sqlc` is a query-generation tool and is
orthogonal to the migration question; it is not used in NetSite either,
because we prefer hand-written SQL with raw `pgx` per CLAUDE.md → Code
Standards.
