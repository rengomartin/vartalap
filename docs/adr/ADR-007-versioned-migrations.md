# ADR-007: Versioned embedded migrations

- **Status:** Accepted
- **Date:** 2026-11-23

## Context

The binary should be self-contained and deployable without a separate migration
tool. The schema changed significantly between the public-chat release and the
private messenger release, and existing databases must upgrade without losing
unrelated data.

## Decision

Embed numbered SQL migrations in the Go binary and apply them automatically.

- Files live in `migrations/` and are embedded with `go:embed *.sql`.
- Filenames start with a zero-padded version and an underscore, e.g.
  `003_messenger.sql`.
- `migrations.All()` sorts filenames and parses the version prefix.
- `Store.Migrate` creates `schema_migrations(version, applied_at)`, applies each
  unseen version in a transaction, and records it.
- Migrations are written with `CREATE TABLE IF NOT EXISTS` /
  `CREATE INDEX IF NOT EXISTS` where practical so databases created before the
  runner existed upgrade safely.
- **Applied migrations are immutable.** Changes require a new numbered file.
- Legacy tables from `001_init.sql` are preserved and never modified or read.

## Consequences

- A single binary upgrades the database on startup.
- Deployments are simple and reproducible.
- Schema history is explicit and auditable in version control.
- A failed migration rolls back its transaction and aborts startup, leaving the
  previous schema intact.
- Editing an applied migration causes deployed databases to diverge from fresh
  ones; the immutability rule is enforced by convention and review, not by the
  database.
- There is no down migration. Rollbacks require restoring a backup or writing a
  forward fix.
- Concurrent instances running migrations are not supported (single-instance
  design).

## Alternatives considered

- **External migration tool (golang-migrate, goose).** Rejected: an extra binary
  or dependency and no clear benefit for one embedded schema.
- **Auto-create tables at runtime from structs (ORM).** Rejected: less explicit,
  harder to audit, and poor fit for the hand-written SQL and constraints.
- **One big schema file executed on every start.** Rejected: no version history
  and unsafe for evolving deployments.
- **Destructive rebuild on upgrade.** Rejected: would lose identity and
  conversation data.
