# ADR-0003: Local storage and forward-only migration discipline

- Status: Accepted — owner-approved 2026-09-14
- Date: 2026-09-13; approved 2026-09-14

## Context

The bootstrap repository names PostgreSQL and opt-in object services, while the reviewed Drift Next plan adopts one local-first installation with SQLite WAL as the system of record and a private local artifact store. The foundation needs one clear persistence, migration, recovery, and retention model before code creates additional schema or startup DDL.

This ADR supersedes ADR-0001 only for the local-first storage decision. ADR-0001 remains the record for the safe bootstrap's console, Tauri, Go, protobuf, mock-data, and loopback boundaries.

## Decision

### Canonical local storage

Each Drift Next installation uses one SQLite database, owned exclusively by the bundled Go control plane, as its authoritative local system of record. The database runs with foreign keys enabled on every connection, WAL journal mode, a bounded busy timeout, UTC timestamps in one documented representation, and short explicit transactions. Browser, Tauri, packages, and edge/runtime processes do not open or write the database directly.

Remote PostgreSQL, object storage, NATS, and other distributed services are not foundation dependencies. They remain opt-in future possibilities only when a measured multi-host or centralized requirement has an approved migration and rollback path.

Artifacts are stored as bytes in a private content-addressed filesystem store. SQLite records only bounded metadata: content hash, size, media/type classification, schema version, retention class, ownership/reference links, access state, and deletion outcome. Artifact writes are atomic: temporary file, verification and hash, then atomic rename. Stored metadata and UI input never authorize an arbitrary filesystem path.

### Migration contract

Schema changes use one pinned, SQL-first migration runner selected in Phase 1. Migrations are ordered, forward-only, checksummed, and recorded in a migration ledger. A shipped migration is immutable; a defect is corrected with a new forward repair migration. Startup and application code may not introduce ad hoc DDL.

A migration runner must:

- serialize migration attempts with a lock appropriate to the local driver;
- refuse a dirty or interrupted migration state until explicit recovery is completed;
- execute transactionally where SQLite support permits it;
- record version, checksum, applied timestamp, and dirty/recovery state;
- support fresh installation, incremental upgrade, interruption simulation, backup/restore, and controlled repair tests;
- use expand, backfill, verify, and contract sequencing for destructive changes.

The bootstrap `0001_initial.sql` is a historical baseline. It is not silently edited to absorb the Phase 3 domain model; later migrations evolve the schema forward.

### Lifecycle state and retention storage

Current projections and append-only history are stored separately. Audit records, policy decisions, lease events, action attempts, run events, mirror events, observations, health samples, inventory snapshots, and artifact metadata retain correlation, causation, actor/source, schema-version, and workspace context required to explain a result. Retention processing is itself observable and must never erase records needed by an active run, legal hold, audit requirement, or referenced artifact without a recorded decision.

Retention classes are:

| Class | Records | Storage behavior |
| --- | --- | --- |
| Current | Current device, endpoint, binding, health, inventory, assignment, setting, and policy projections | Retain the latest valid projection plus provenance; supersession is explicit. |
| Operational history | Health samples, observations, inventory snapshots, low-risk device events, and mirror/run telemetry | Append first; compact or expire only through a documented policy and recorded job. |
| Execution evidence | Run targets, action attempts, postconditions, cleanup outcomes, mirror target results, and referenced artifacts | Retain until the owning run is eligible for a reviewed retention action; never remove target-level failure evidence early. |
| Audit and security | Audit events, approval decisions, lease/fencing events, policy decisions, registration/retirement events, and retention decisions | Append-only and protected from ordinary cleanup; no routine expiry policy is assumed. |
| Disposable | Derived caches, temporary artifact-write files, local previews, and rebuildable projections | Bounded and recoverable; cleanup must not change canonical state. |

The exact time-to-live values, artifact size quotas, backup cadence, and formal legal-hold requirements are intentionally not invented by this ADR. They require an approved operational policy before retention jobs or destructive cleanup are implemented.

### Failure and recovery boundaries

Database transactions do not wait for device, network, filesystem, or user-interface work. A state transition that creates audit and outbox records commits those records atomically with the authoritative change; delivery happens after commit and is idempotent. Artifact failure, cleanup failure, migration interruption, contention, and restore failure are classified outcomes, not silent repair paths.

Backups and restores include the SQLite database and referenced artifact store as one documented recoverable unit. Recovery exercises must validate foreign keys, migration ledger state, artifact references, and that stale leases or fencing tokens cannot resume unsafe work.

## Alternatives considered

- Make PostgreSQL the required foundation database. Rejected because the initial product is one local workspace and no centralized/multi-host requirement has been proven.
- Let every local process open SQLite. Rejected because it weakens ownership, transaction, migration, and audit boundaries.
- Store artifact bytes, screenshots, or unbounded JSON in SQLite. Rejected because it complicates database lifecycle, backup, and contention without a demonstrated benefit.
- Permit startup DDL or mutable migration files. Rejected because upgrades become unreproducible and partial failures cannot be audited safely.
- Assign retention durations now. Rejected because durations, quotas, and legal obligations are product/operational decisions not supported by evidence in the foundation plan.

## Consequences

- Phase 1 must choose and test one migration runner before persistence implementation begins.
- Phase 3 must use SQLite-oriented constraints, foreign keys, WAL/concurrency reasoning, and forward-only migrations.
- Future centralized storage requires a separate ADR with data migration, dual-read/write or cutover plan, verification, rollback, and retention reconciliation.
- The control plane owns database access and artifact authorization, preserving a narrow trust boundary for the console, Tauri shell, packages, and future edge adapters.

## Remaining decisions requiring later approval

- Artifact size quotas, storage-location policy, encryption-at-rest requirements, and backup/restore cadence.
- Legal-hold, export, and deletion obligations for audit records.
- Exact operational-history and execution-evidence retention durations.

## Validation

- Phase 1 must test clean bootstrap, migration ledger checksums, lock behavior, dirty-state refusal, and explicit repair handling.
- Phase 3 must test fresh install, the explicit historical `0001` PostgreSQL boundary, incremental SQLite upgrade from `0002`, interruption/recovery, foreign-key enforcement, workspace isolation, and concurrent conditional updates.
- Artifact tests must prove atomic write/rename, hash verification, bounded metadata, path authorization, retention classification, and cleanup failure visibility.
- Backup/restore verification must prove database/artifact consistency and reject unsafe resumption from stale control state.
