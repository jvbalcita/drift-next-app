# SQLite migration boundary

Drift Next uses the SQL-first runner in `internal/platform/migrations` for the
local SQLite database. The runner is intentionally small and uses
`database/sql` with the pure-Go `modernc.org/sqlite` driver pinned to
`v1.58.0`. `internal/platform/migrations.Open` is the normal local service
path; it configures every modernc SQLite connection with WAL mode, foreign-key
checks, and a bounded busy timeout. `NewRunner` also accepts a supplied
`*sql.DB` and injected `fs.FS` so callers and tests can own connection setup
explicitly. It does not read environment variables or introduce an ORM.

## Files and checksums

SQLite migration files use the immutable, forward-only convention
`NNNN_name.sql`, beginning at `0002`. The four-digit version is unique and
ordered, and the name contains lowercase letters, digits, and underscores.
The runner rejects malformed filenames, duplicate versions, and any applied
name or SHA-256 checksum mismatch. Checksums are calculated over the exact
file bytes and stored as lowercase hexadecimal text.

`0001_initial.sql` is a historical PostgreSQL bootstrap. It is preserved
byte-for-byte and is not SQLite input. The runner rejects version `0001`
instead of attempting to interpret PostgreSQL DDL as SQLite. Future SQLite
migrations must be added at `0002` or later and must be reviewed as new,
immutable files.

The durable ledger is `drift_schema_migrations`:

| Column | Meaning |
| --- | --- |
| `version` | Ordered migration version, primary key |
| `name` | Filename name component |
| `checksum` | SHA-256 of exact SQL bytes |
| `applied_at` | UTC RFC3339Nano timestamp |
| `dirty` | `1` while a failed/interrupted version requires explicit repair |

The ledger table itself is the runner's fixed metadata bootstrap. Domain
schema DDL belongs in migration files; application startup and services must
not add ad-hoc tables, indexes, or columns.

## Locking and failure recovery

Every writer phase uses SQLite `BEGIN IMMEDIATE`, reserving the writer before
reading or changing migration state. Each supplied connection is configured
with a bounded busy timeout (five seconds by default, at most thirty seconds)
so cross-process contention returns a classified `migration_locked` error.

Before a migration's SQL runs, the runner commits a ledger row with `dirty=1`.
The migration SQL and the update that clears `dirty` run in one transaction.
SQLite SQL errors roll that transaction back, so migration DDL is not partially
applied while the committed dirty row remains. A process crash after the dirty
commit has the same recovery contract: the next `Apply` refuses to proceed
with `migration_dirty`.

Recovery is explicit: after the operator has addressed the external failure,
call `Repair(ctx, version)` for the exact dirty version. Repair verifies that
the current immutable file still has the recorded name and checksum, removes
the failed version from the applied set, and commits. A later `Apply` retries
the unchanged file. Repair never edits migration files, clears a clean row, or
silently rolls back an applied migration.

Transactions are deliberately narrow. They cover ledger metadata and the SQL
file only; they never wait for devices, network calls, filesystem artifacts,
UI work, or user input. Migration SQL must therefore be SQLite-compatible and
transaction-safe. There are no down migrations or startup rollback paths.
Destructive changes follow expand, backfill, verify, and contract as separate
forward migrations.

## Tests

Migration tests use temporary SQLite files and `testing/fstest.MapFS` so SQL
fixtures are injected rather than loaded from production data. They cover
fresh and incremental application, ordering/idempotence, malformed and
duplicate loading, the PostgreSQL `0001` boundary, checksum/name mutation,
transaction rollback, dirty refusal, explicit repair, bounded lock
contention, and context cancellation. Testdata is disposable and must contain
no credentials or production records; see `testdata/README.md`.

Run the focused migration gate from the repository root:

```bash
pnpm go:migrations
```

The full Go gates are:

```bash
go test ./...
go vet ./...
go build ./...
```
