# Drift Next Local-First Storage and Plug-and-Play Amendment

> **For Hermes:** Treat this amendment as higher priority than conflicting PostgreSQL/S3 assumptions in prior Drift Next plans. Use subagent-driven-development only after the local-first foundation decisions are approved.

**Goal:** Make Drift Next a self-contained, plug-and-play macOS and Windows application that works locally without PostgreSQL, S3, Docker, a hosted control plane, or manual dependency installation for the core product.

**Architecture:** Each installation runs an owned local Drift service and UI. The bundled Go service owns SQLite, local workflows/skills, local artifact storage, device adapters, leases, audit, and local API contracts; the React/Tauri UI calls that service over a local authenticated loopback boundary. SQLite is the system of record for a single local installation. Any future centralized coordination is an explicit optional product mode using an API/sync boundary—not a requirement and never a shared SQLite file.

**Tech Stack:** Tauri 2 + React/Vite UI; bundled Go local service/sidecar; pure-Go SQLite driver preferred to avoid CGO cross-platform packaging complexity; SQLite WAL; embedded SQL migrations; local content-addressed filesystem artifact store; signed/versioned workflow and skill packages; optional future PostgreSQL/object-storage/remote-control services only after demonstrated multi-host requirements.

**Status:** This amendment supersedes the assumption that PostgreSQL and S3 are foundation prerequisites. It preserves the normalized domain model, safety invariants, typed API boundary, and future migration path from the prior implementation plan.

---

## 1. CTO decision

**Recommendation: remove PostgreSQL and S3 from the required local installation.**

For the product described—installed locally on macOS or Windows, immediately usable, with locally installed workflows/skills and no current production deployment—PostgreSQL adds installation, service lifecycle, upgrade, backup, permissions, and support burden without delivering corresponding value.

Use:

```text
Tauri desktop application
        │
        │ authenticated loopback API
        ▼
Bundled Go local service
  ├─ SQLite WAL database
  ├─ local workflow/skill registry
  ├─ local artifact content store
  ├─ local device/edge runtime
  └─ future optional sync adapter
```

Do **not** use:

- PostgreSQL as a mandatory desktop dependency;
- Docker as a runtime requirement;
- S3/MinIO as a local-first artifact prerequisite;
- a shared SQLite file over SMB, cloud-drive sync, network mounts, or multiple independent hosts;
- browser-side direct SQLite, filesystem, shell, or device access;
- a “plugin” system that silently runs arbitrary downloaded code or invokes `npm install`/shell scripts without a trust boundary.

PostgreSQL remains a legitimate future option when actual requirements prove the need for shared multi-operator state, centralized remote fleet coordination, high-availability service operation, cross-host scheduling, long-retention centralized audit, or hosted deployment. It is not required now.

---

## 2. Product operating model

### 2.1 Local installation is the unit of ownership

A Drift installation is a local workspace that owns:

- its SQLite database;
- its workflows, skill packages, and configuration;
- local artifact metadata and content;
- its local history, audit records, and backup/export archive;
- device adapters and their local device/host state;
- its operator session and local access controls.

A person can install Drift on another macOS or Windows computer and immediately obtain a new independent local workspace. That workspace does not require a central server to start, configure a database server, or provision S3.

### 2.2 Local service remains the authority boundary

Do not collapse all logic into the React renderer or expose privileged Tauri IPC broadly.

The application should ship a bundled Go service that:

- binds only to loopback or uses a local IPC mechanism;
- owns SQLite connections and migration execution;
- executes typed workflow/device intents;
- maintains leases, fencing, idempotency, policy decisions, audit events, and cleanup;
- manages plug-in/workflow lifecycle;
- exposes a narrow typed API to the UI;
- supervises local adapters when those are later authorized.

Tauri is a packaging and local UI host. The Go service is still the local control plane and should remain independently testable.

### 2.3 Cross-computer use is later explicit pairing, not accidental sharing

The first local release supports a local operator and local device/host resources. If future requirements need one operator to view/control resources attached to another host, add an explicit paired edge/remote mode with authenticated APIs and synchronization. Do not try to make a SQLite file act as a network database.

---

## 3. SQLite design rules

SQLite is appropriate for the current local-first product if we enforce its actual operating constraints.

### 3.1 Required SQLite configuration

- One local service owns write access; UI processes do not open the database.
- Enable foreign-key enforcement on **every** connection.
- Use WAL journal mode for responsive concurrent reads with a serialized writer.
- Configure a bounded busy timeout and surface lock/contention errors as typed errors.
- Use short transactions; never hold a transaction while waiting for device/network/UI work.
- Use explicit transaction boundaries for every state mutation, lease, policy decision, audit event, and outbox/event insertion.
- Use `BEGIN IMMEDIATE` or the selected driver’s equivalent for lease/fencing transitions that need a write reservation.
- Store timestamps consistently as UTC integer epoch milliseconds or RFC 3339 UTC text; choose one convention and enforce it.
- Store internal IDs as text UUID/ULID values; never use mutable ADB serial, host, sheet row, or display name as a primary key.
- Store bounded, schema-versioned payloads as JSON text; do not turn SQLite into an unbounded blob/config store.
- Run `PRAGMA quick_check` in backup/recovery validation, not on every normal startup.

### 3.2 Relationship model remains normalized

The move from PostgreSQL to SQLite does **not** reduce the relationship model. Retain the reviewed domain design:

- organizations/local workspaces and operators;
- edge/local runtime identities;
- stable devices, endpoint history, bindings, inventory, health, events, and observations;
- Network Profiles, scan runs, scan candidates, approvals, and registration events;
- groups, membership history, order, automation agents, and assignments;
- control sessions, leases, fencing, idempotency, policies, audit, and outbox;
- workflows, immutable versions, typed steps, runs, run steps, action attempts, and replay evidence;
- account sources, non-secret accounts, service states, runs, and explicit device assignments;
- typed settings and local artifact metadata.

SQLite supports foreign keys, check constraints, unique constraints, indexes, partial unique indexes, transactions, and triggers where they are justified. Application/domain services still own cross-resource invariants and user-facing conflict behavior.

### 3.3 SQLite-specific schema adjustments

| Earlier PostgreSQL assumption | Local-first SQLite decision |
|---|---|
| `uuid` columns | Store canonical UUID/ULID as `TEXT` with validation at the service boundary. |
| `timestamptz` | Use one canonical UTC representation, documented and tested. |
| `inet` | Persist canonicalized address/range fields as text/integer representation after strict service validation; never compare IP strings lexically. |
| advisory/row locks | Use short write transactions plus lease/fencing tokens; never rely on a long-running DB lock. |
| JSONB | Use bounded versioned JSON text only where relational columns are not appropriate. |
| PostgreSQL outbox publisher | Keep a local append-only outbox/event queue; it can later feed a sync adapter. |
| S3 blobs | Store artifacts in a local content-addressed directory, with metadata/hash/retention in SQLite. |
| multi-writer central DB | One local service is the writer; remote synchronization is a future API problem. |

### 3.4 Migrations and backups

- Embed ordered SQL migrations in the Go service binary.
- Track applied migration versions/checksums in a local `schema_migrations` table.
- Apply migrations before opening the normal UI workspace.
- Before a destructive or non-reversible migration, create a timestamped local backup/export and verify the backup is readable.
- Migrations are forward-only once shipped. Repairs use a new migration; do not mutate historical migration files.
- Test new install, upgrade from every supported prior local schema, interrupted migration recovery, and backup restore.
- Provide an explicit user-visible export/import/restore mechanism. Do not silently overwrite a workspace.

The migration implementation may use a small embedded runner or a pinned SQLite-compatible migration library. Selection must favor deterministic embedded operation, checksum verification, locking/dirty-state handling, and cross-platform packaging—not framework popularity.

---

## 4. Local artifact storage instead of S3

### 4.1 Initial artifact store

Use an application-data location selected through the operating system’s standard per-user application-data APIs. The storage layout should be private to Drift, not tied to the repository or arbitrary user paths.

Example logical structure:

```text
Drift application data/
  workspace/
    drift.sqlite
    artifacts/
      sha256/<content-hash>
    backups/
    workflows/
    skills/
    logs/
```

The exact physical path differs by OS and must be obtained through platform APIs, not hard-coded strings.

### 4.2 Artifact rules

- SQLite stores metadata only: artifact ID, hash, MIME/type, size, owner, retention, source run/device, and lifecycle.
- Artifact content is written atomically: temporary file → hash/verify → atomic rename into the content-addressed store.
- Never let an artifact record authorize arbitrary filesystem paths.
- Enforce size, retention, deletion, and redaction policy before write where possible.
- Keep screenshots/UI trees/recordings optional; the first local foundation needs metadata and sanitized fixtures, not real media capture.
- Use local export packages for backup/transfer; do not add cloud synchronization by default.

### 4.3 Future object-storage adapter

If a later hosted/central mode needs remote artifacts, implement an `ArtifactStore` interface at the service boundary. The initial filesystem store and a future S3-compatible store must implement the same retention, authorization, hash, and deletion contract. Do not require an S3 emulator locally.

---

## 5. Plug-and-play installer and dependency model

### 5.1 Installer outcome

A fresh macOS or Windows installation should:

1. Install the signed Drift desktop application and bundled local service.
2. Create the user-scoped application-data directory on first launch.
3. Create/open the local SQLite workspace and run embedded migrations.
4. Initialize a local workspace, local operator context, and safe default settings.
5. Start the local service under application supervision and establish the local UI/service session.
6. Verify bundled component versions and integrity before enabling privileged features.
7. Provide an explicit readiness screen showing what is installed, what is unavailable, and which optional capabilities need consent.

The installer should bundle the known core runtime. Core use must not require a user to install PostgreSQL, Docker, Node.js, Go, Python, or S3 tooling.

### 5.2 Optional platform/device dependencies

Some future device capabilities can legitimately need OS-specific packages, device drivers, Android Platform-Tools, elevation, reboot, firewall permission, or a physical cable. “Automatic setup” must mean managed and transparent—not silent or arbitrary.

For each optional dependency:

- ship it in the signed application bundle when licensing/size/support permit; otherwise fetch only from a pinned allow-listed source;
- verify checksum/signature and exact version before use;
- show the package, version, purpose, required privileges, and rollback behavior;
- obtain user confirmation before elevation or system-level changes;
- install using argument arrays and platform APIs, never shell interpolation;
- retain an installation receipt/version record;
- expose repair/uninstall/diagnostic actions;
- keep the application usable when the optional dependency is unavailable.

Do not make the initial installer invoke ADB, attach to devices, or change Android state automatically.

### 5.3 macOS and Windows packaging

- Build separate native application bundles/installers for macOS and Windows from the same source/versioned release manifest.
- Bundle the Go service as a Tauri sidecar or managed local companion process with a narrow local IPC/loopback contract.
- Sign/notarize macOS releases and sign Windows installers/binaries before broad distribution.
- Keep OS-specific adapters behind a small interface and test the same contract with fake implementations.
- Avoid CGO-only local database dependencies if they complicate reproducible cross-platform packaging; prefer a pure-Go SQLite path unless an evidence-backed performance/capability reason requires otherwise.

---

## 6. Workflows and skills as installable packages

### 6.1 Separate declarative workflows from executable plug-ins

**Workflow package:** declarative, typed, versioned state-machine definition plus fixtures, metadata, policy requirements, and signatures/hashes. A workflow cannot contain credentials, raw arbitrary shell commands, or hidden executable callbacks.

**Skill package:** a versioned package that can provide reusable typed workflow templates, schemas, UI metadata, validators, fixtures, and capability declarations. It may use a reviewed runtime adapter only through explicit host-provided capabilities.

Do not call arbitrary source code “a skill” and execute it with full local process authority.

### 6.2 Package manifest requirements

Every package must declare:

- package ID, display name, semantic version, publisher, and content hash;
- minimum Drift application/service version;
- supported operating systems and architecture constraints;
- required host capabilities;
- declared data classes and artifact behavior;
- requested permissions/risk level;
- migrations/config schema if applicable;
- test fixtures and validation status;
- installation, enablement, disablement, and uninstall behavior.

### 6.3 Capability and trust model

The local service, not the UI, evaluates a package manifest.

Initial package trust tiers:

1. **Built-in:** shipped and signed with Drift.
2. **Locally installed trusted:** explicitly approved by the operator after manifest/capability review.
3. **Untrusted:** inspectable but not executable/enabled until approved.

Capabilities must be explicit and narrow, for example:

- read local device inventory;
- create a workflow run request;
- observe a sanctioned device state;
- read/write package-owned non-secret local settings;
- emit structured events;
- request artifact capture through the host.

A package must not receive direct database handles, raw filesystem scope, raw device protocol access, privileged shell access, credentials, arbitrary network access, or authority to bypass leases/policy/audit.

### 6.4 Package installation flow

```text
select local package file or approved catalog package
  → validate manifest, version compatibility, signature/hash
  → show requested capabilities and data handling
  → operator approves installation
  → stage package in temporary directory
  → validate fixtures/schema
  → atomically activate or reject
  → write audit record and local package event
```

Package updates use the same staged/rollback-capable flow. A failed update leaves the last known-good package active.

---

## 6.5 Multi-device control and automation

The local-first model supports both direct control and controlled fan-out.

### Interactive source-and-followers mirroring

An operator may:

1. select one device as the **source/controller**;
2. acquire an interactive control lease for that source;
3. select multiple devices or all eligible devices as **followers**;
4. start an explicit mirror session;
5. control the source while Drift translates each approved source action into a typed action intent for the follower set.

Selecting a device for viewing or selecting it as a possible follower does not acquire a lease. Once mirroring starts, the mirror session acquires and tracks an independent lease/fencing token for every device that will receive state-changing actions. The source lease is not reused as a shared target lease.

Every follower has its own device actor, fresh observation, semantic target resolution, policy check, action attempt, postcondition, and result. Drift must not blindly replay source coordinates, stale UI nodes, or raw transport commands across devices. A follower may be offline, incompatible, policy-denied, or unable to resolve the target even when the source succeeds.

The mirror session must show per-device state and never report “all mirrored successfully” from the source result alone. The initial safe behavior is independent follower failure with an explicit session policy for continue, pause, or stop-all. Irreversible or high-risk actions require per-follower policy approval and are not automatically broadcast merely because the source performed them.

### Multi-device workflow and skill execution

A workflow or skill is not limited to one device. A run has a target set resolved from one of:

- explicitly selected devices;
- a device group;
- devices assigned to a logical automation agent;
- all devices matching an approved capability/policy selector.

The run stores a target-set snapshot and creates independent per-device target executions. Each target obtains its own lease, actor queue, observation history, action attempts, failure state, retry budget, and cleanup. A bounded concurrency policy controls how many assigned devices execute simultaneously; one device’s failure must not corrupt the others or be hidden by a single aggregate status.

A logical automation agent may be assigned to many devices. The safe default is at most one active logical automation-agent assignment per device, while one automation agent may have many active device assignments. Assignment changes are historical and do not silently add devices to a run that has already started unless an explicit watch/dynamic-target mode is later approved.

A workflow/skill may therefore be installed once and applied to every eligible assigned device without duplicating workflow definitions or bypassing per-device leases.

---

## 7. Revised implementation order

This amendment changes the earlier implementation phases as follows.

### Revised Phase 0 — Confirm local-first product boundary

Approve:

- one independent local workspace per installation;
- SQLite WAL as the local system of record;
- local filesystem artifact store;
- no mandatory hosted service, PostgreSQL, S3, Docker, or cloud account;
- local service + UI process boundary;
- package trust/capability model;
- no shared SQLite/network-drive database;
- explicit future remote/pairing mode only when required.

### Revised Phase 1 — Cross-platform bootstrap and SQLite migration harness

Replace the PostgreSQL migration assumption with:

- pure-Go SQLite driver evaluation;
- embedded migration runner/ledger;
- foreign-key/WAL/busy-timeout configuration;
- local application-data path abstraction;
- backup-before-destructive-migration path;
- empty/upgrade/interrupted-migration/restore tests;
- bundled-service startup and local session lifecycle tests.

Likely files:

- `go.mod`, `go.sum`
- `internal/store/sqlite/connection.go`
- `internal/store/sqlite/migrations.go`
- `internal/platform/appdata/path.go`
- `internal/platform/backup/*`
- `db/migrations/*.sql` (SQLite dialect)
- `cmd/control-plane/main.go` or a renamed local-service entry point
- `apps/console/src-tauri/` sidecar/package configuration
- CI packaging/migration checks

### Revised Phase 2 — Preserve normalized schema, translate it to SQLite

Retain the full domain model and constraints from the structured rebuild plan, but implement them in SQLite-compatible SQL and local service transactions. Do not remove Network Profile scan/approval, groups, assignments, events, account references, settings, policy, lease, workflow, and audit domains merely because the database is embedded.

### Revised Phase 3 — Local control-plane API and fake device runtime

Implement the local Go service, typed local contracts, lease/fencing/idempotency, fake edge/device actors, and deterministic registry/scan workflow. Include direct operator control of a selected source device, explicit source-to-multiple/all-follower mirror sessions, independent per-device target leases/results, and parent workflow runs that can execute across assigned devices. This remains mock-only and has no Android dependency.

### Revised Phase 4 — Local console CRUD and package manager UX

Connect the existing console to the local service. Add resource-specific CRUD screens, Network Profile scan-candidate approval, lease/run timelines, source-device controls, multiple/all-follower selection, per-target mirror status and stop controls, workflow target-set selection, package install/enable/status, settings, and local backup/export controls. The UI never manipulates SQLite or filesystem state directly.

### Revised Phase 5 — Local workflow/skill package runtime

Implement manifests, staging, compatibility checks, trust/capability prompts, version pinning, rollback, and audit. Start with built-in/example declarative packages and fake adapters. A package may run across an explicit target set, including every eligible device assigned to an automation agent, with independent per-device execution and bounded concurrency.

### Revised Phase 6 — Optional real adapter and managed dependency installer

Only after foundation gates pass, implement lab-only Platform-Tools/ADB integration and a managed, signed/verified optional-dependency flow. Evaluate `uiautomator2` only against measured requirements. No automatic device state changes during application install.

### Revised Phase 7 — Optional remote mode and hosted services

Only when product evidence requires multiple hosts/operators or centralized coordination, add a separately enabled remote mode:

- paired remote edge agents;
- authenticated API/sync protocol;
- conflict/ownership semantics;
- optional PostgreSQL control-plane deployment;
- optional S3-compatible artifact store;
- migration/export/import and rollback plan.

The initial local app must remain fully useful without this phase.

### Revised Phase 8 — Evidence-gated advanced capabilities

Add scheduling, NATS, WebRTC/TURN/SFU, cloud artifact sync, AI assistance, shared fleet operations, and broader native install automation only after measured need, threat-model review, and explicit rollback paths.

---

## 8. Updated verification gates

### Local installation gate

- Fresh macOS and Windows install succeeds without PostgreSQL, Docker, S3, Node, Go, or manual database setup.
- First launch creates a local workspace and upgrades it safely.
- UI can start/stop/reconnect to the bundled local service safely.
- A failed migration or corrupted workspace produces an actionable recovery path without silent data loss.
- Backup/export and restore work against a disposable local workspace.

### SQLite/data gate

- Foreign-key enforcement is verified on every database connection.
- WAL and busy timeout are configured and observable.
- Lease/fencing/idempotency tests pass under concurrent local-service calls.
- A selected source device remains directly controllable under an explicit lease.
- A mirror session can fan out typed actions to multiple/all eligible followers with an independent lease and result for each target.
- Relationship cardinality and organization/workspace isolation tests pass.
- Current projections and append histories remain consistent.
- Artifact metadata cannot reference arbitrary external paths.

### Workflow/skill package gate

- Invalid manifests/signatures/hashes are rejected.
- Capability prompts are visible and audited.
- A package cannot access undeclared host capabilities.
- Failed install/update leaves the last known-good package active.
- Packages cannot introduce credential persistence, arbitrary shell, direct DB access, or raw device access.
- Package fixtures validate before activation.
- A workflow/skill run can target every eligible device assigned to an automation agent.
- Target-set resolution is snapshotted by default and each target has independent state, lease, retry, cleanup, and failure reporting.

### Optional dependency gate

- Platform-specific package source/version/checksum is visible.
- No elevation or external download occurs without user consent.
- Install/repair/uninstall is reversible and logged.
- Optional dependency absence does not prevent non-device local use.

### Future remote-mode gate

- No SQLite file sharing is used for multi-host coordination.
- Sync is API-based, idempotent, authorized, observable, and conflict-aware.
- Local-first behavior remains available when remote mode is disabled/unreachable.

---

## 9. Decisions still requiring Boss confirmation

1. Is each installation initially a fully independent workspace, or must two local installs share one fleet immediately?
2. Do workflows/skills need third-party executable code in the first release, or are declarative packages and reviewed host adapters sufficient? Recommended: declarative first.
3. Which optional device dependencies should be bundled versus installed on-demand with consent?
4. Must the first macOS/Windows release be signed/notarized before internal use, or before external distribution only?
5. Is a local encrypted backup/export required in the first release?
6. Is remote sync/pairing a near-term product requirement or an explicitly deferred future mode?

**Safe default:** independent local workspaces, declarative packages, built-in/fake workflows first, signed optional dependencies, explicit backup/export, and no remote sync until a real multi-host workflow requires it.

---

## 10. Consequences

### Benefits now

- Single installer experience on macOS and Windows.
- No server/database/container provisioning burden.
- Fast local startup and offline-first operation.
- Lower support cost and fewer moving parts.
- Workflows/skills are available immediately on the installed machine.
- Local data ownership and portable backup/export.
- A clean future path to central services without imposing them today.

### Trade-offs accepted deliberately

- Each installation has its own local state by default.
- Cross-host shared fleet coordination is deferred and must be designed explicitly.
- SQLite has one-writer semantics; the local service must own write serialization.
- Local artifact retention and disk usage become application responsibilities.
- Native macOS/Windows packaging, signing, optional dependency lifecycle, and safe plugin packaging become first-class engineering work.

These are appropriate trade-offs for the current local, plug-and-play objective.
