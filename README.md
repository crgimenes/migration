# Migration Tool

[![MIT Licensed](https://img.shields.io/badge/license-MIT-green.svg)](https://tldrlegal.com/license/mit-license)
[![Go Version](https://img.shields.io/badge/go-1.27+-blue.svg)](https://golang.org)

Database migration tool for PostgreSQL and SQLite with transaction
support, schema drift detection, and schema analytics. Pure Go, no cgo.

Beyond running SQL migration files, it can introspect a live PostgreSQL
database, detect changes people made directly on it, and turn that drift
into a reviewable up/down migration pair. It can also answer "what
changed between version X and Y" from versioned schema snapshots.

## Features

- **Transactions**: each batch of migrations commits or rolls back as one
- **Multi-database**: PostgreSQL and SQLite, detected from the URL
- **Drift detection** (PostgreSQL): compare the live schema against the
  last known state and generate the migration that captures the changes
- **Schema analytics** (PostgreSQL): diff any two schema versions,
  snapshots, or live databases
- **AI/script friendly**: `-json` on every command, meaningful exit codes
- **Safe by construction**: generated destructive statements come out
  commented; renames are flagged, never guessed

## Installation

```bash
go install github.com/crgimenes/migration@latest
```

Or compile from source:

```bash
git clone https://github.com/crgimenes/migration.git
cd migration
go build -o migration
```

## Usage

```text
migration [options] <command> [args]

Commands:
  up [n]              run all (or n) pending migrations
  down [n]            revert all (or n) applied migrations
  status              list pending migrations
  snapshot            record the live schema (PostgreSQL)
  diff                show drift vs the last snapshot (exit 2 when found)
  capture [name]      turn drift into an up/down migration pair
  report <from> <to>  diff two points: snapshot version, `live`, or URL

Options:
  -url     database URL (or DATABASE_URL)
  -dir     migrations directory (or MIGRATIONS)
  -json    machine-readable JSON output
  -no-snapshot   skip the automatic snapshot after up/down
  -action  legacy action flag (or ACTION); prefer the positional form
```

### Running migrations

```bash
export DATABASE_URL="postgres://user:password@localhost:5432/dbname?sslmode=disable"
export MIGRATIONS="./migrations"

migration status
migration up        # run everything pending
migration up 1      # run only one
migration down 1    # revert one
```

SQLite works the same way:

```bash
migration -url "sqlite:///path/to/database.db" -dir ./migrations up
migration -url "sqlite::memory:" -dir ./migrations status
```

### Detecting manual changes (PostgreSQL)

People change databases directly. This tool turns those changes into
migrations instead of letting them drift:

```bash
# After every successful `up`/`down` on PostgreSQL a schema snapshot is
# written to <migrations dir>/snapshots/NNN.json automatically. To
# record a baseline explicitly:
migration snapshot

# Someone runs DDL directly on the database. Later:
migration diff
# ● Drift detected: 2 changes
#   public.users
#     ● column_added public.users.email: text
#     ● index_added public.users.idx_users_email: CREATE INDEX ...
#   Σ 1 column added, 1 index added

# Turn the drift into a reviewable migration pair:
migration capture add_email
# writes 004_add_email.up.sql and 004_add_email.down.sql, registers
# version 4 as applied (the database already has these changes), and
# snapshots the new state.
```

Review the generated SQL before committing it. Statements that would
destroy data (DROP TABLE, DROP COLUMN, DROP TYPE) are generated
commented out with a warning; a drop+add pair that looks like a rename
is flagged so you can rewrite it as `ALTER ... RENAME` yourself.

`migration diff` exits with code 2 when drift exists (0 = clean,
1 = error), so CI can fail a pipeline on unexpected manual changes:

```bash
migration -json diff | jq .changes
```

### Schema analytics

```bash
migration report 3 7      # what changed between snapshot 3 and 7 (offline)
migration report 7 live   # what changed since snapshot 7 (-url database)
migration report "postgres://a/db1" "postgres://b/db2"   # two live databases
```

### JSON output

Every command accepts `-json`. Examples:

```bash
migration -json status
# {"action":"status","count":2,"files":["..."],"ok":true}

migration -json diff
# {"action":"diff","snapshot":3,"drift":true,"changes":[{"kind":"column_added",...}],"ok":true}
```

## Migration files

Files follow the naming convention:

```text
001_create_users_table.up.sql
001_create_users_table.down.sql
002_add_email_index.up.sql
002_add_email_index.down.sql
```

The number prefix is the version. Each `.up.sql` needs a matching
`.down.sql` that reverts it.

## The schema_migrations table

The tool creates and manages a `schema_migrations (version INTEGER
PRIMARY KEY)` table to track applied versions. It only ever touches this
one table; it is excluded from snapshots, diffs, and reports.

## Snapshots

`<migrations dir>/snapshots/NNN.json` holds the introspected schema as
of migration version NNN: tables, columns, constraints and indexes (as
canonical `pg_get_constraintdef`/`pg_get_indexdef` text), enums, and
standalone sequences. Snapshots are plain JSON meant to be committed
alongside the SQL files. Views, functions, and triggers are not modeled.

## Exit codes

| code | meaning                        |
|------|--------------------------------|
| 0    | success / no drift             |
| 1    | error                          |
| 2    | drift found (`diff` only)      |

## Development

```bash
go test -timeout 30s -count 1 ./...
```

PostgreSQL integration tests (introspection, capture round-trip) need a
live server and skip when `DATABASE_URL` is not set:

```bash
docker run -d --rm --name migration-test-pg \
  -e POSTGRES_PASSWORD=migration -e POSTGRES_DB=migration_test \
  -p 55433:5432 postgres:15-alpine

DATABASE_URL='postgres://postgres:migration@localhost:55433/migration_test?sslmode=disable' \
  go test -timeout 120s -count 1 ./...
```

## License

This project is licensed under the MIT License - see the
[LICENSE](LICENSE) file for details.
