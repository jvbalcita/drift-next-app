# Artifacts, Retention, and Local Media

Operational guide for Drift Next Phase 15 local artifact storage and interactive media. This document describes the local-first design only. Remote object storage, SFU, TURN, hosted media, and multi-device streaming remain deferred.

## Ownership and security boundaries

- The Go control plane owns artifact authorization, persistence, retention, deletion, and audit.
- SQLite stores **metadata only**. Artifact bytes live in a private application-data content-addressed filesystem (CAS).
- The console, Tauri renderer, and packages must never open SQLite or resolve arbitrary filesystem paths.
- Stored metadata and UI input never authorize an arbitrary path. Reads and deletes use typed artifact IDs scoped to a workspace after authorization.
- Artifact failure must not corrupt workflow state, mutate leases/fencing tokens, or bypass device-control safety. Capture failures stay isolated from device-command and run outcomes.
- Indeterminate action semantics are preserved: artifact availability never proves command completion.

## Artifact lifecycle

Explicit states:

| State | Meaning |
| --- | --- |
| `pending` | Admission/staging started; bytes not yet verified in CAS |
| `stored` | Bytes verified and metadata committed |
| `referenced` | At least one active owner reference exists |
| `retained` | Retention policy or protected class keeps the artifact |
| `eligible_for_deletion` | No protecting references; cleanup may proceed |
| `deleted` | Bytes removed and metadata records deletion outcome |
| `cleanup_failed` | Cleanup attempted and failed; metadata remains visible |

Transitions are validated by the artifact domain state machine. Failed cleanup never silently drops metadata.

## Content-addressed storage layout

Default root (under application data, never a caller-supplied path):

```text
<app-data>/artifacts/cas/
  objects/<workspace-id>/<hash-prefix>/<content-hash>
  tmp/<write-id>
```

Control-plane wiring:

- When `DRIFT_CONTROL_PLANE_DB` is set, the control plane mounts Artifact Connect RPCs.
- CAS root defaults to `<dir(DRIFT_CONTROL_PLANE_DB)>/artifacts/cas`.
- Override with absolute (or resolvable) `DRIFT_ARTIFACT_CAS_ROOT`.
- Without a durable DB path, artifact routes are not mounted (metadata and CAS stay unavailable).

- Object names are content hashes. Unsafe filenames, `..`, absolute paths, and symlink escapes are rejected.
- Layout is private to the control plane process user. Operators should keep the directory mode restrictive (owner read/write only).

## Atomic write and hash verification

1. Write bytes to a temporary file under `tmp/`.
2. Verify declared size and compute content hash.
3. Atomically rename into the content-addressed object path.
4. Persist SQLite metadata only after successful verification.
5. Duplicate content for the same workspace + content hash is idempotent: reuse the existing object and metadata when hashes match.

## Authorization and audit

Authorized local-service operations only:

- list / filter metadata
- get metadata
- read authorized bytes or sanitized previews
- request deletion / cleanup
- inspect storage health

Audited events include: artifact reads, rejected admissions, retention decisions, deletes, cleanup failures, and authorization failures. Audit records stay bounded and redacted.

## Redaction and admission

Before CAS admission, reject screenshots, UI trees, OCR results, annotations, traces, and recordings that contain sensitive or **uncertain-sensitive** content.

- Binary media (`image/*`, `video/*`, `audio/*`, `application/octet-stream`) **fails closed** as uncertain-sensitive by default. String scanning cannot prove a screenshot or recording is safe.
- Callers may admit binary bytes only with an explicit `SensitivitySafe` classification from a trusted sanitization boundary (`AdmitWithClassification` / `PersistSanitizedScreenshot`).
- Raw capture paths (`PersistScreenshot`) omit bytes and retain omission metadata when sanitization has not completed.
- Do not persist unsafe bytes merely because metadata is available.
- Store bounded omission/error metadata explaining rejection.
- Redact sensitive text before persistence, audit, logs, previews, and API responses.
- Keep raw secrets, credentials, tokens, device keys, and sensitive screen content out of fixtures, screenshots, artifacts, logs, and documentation.
- Preserve distinctions: sanitized, redacted, omitted, partial, failed, indeterminate.
- Absence of a detected secret is **not** proof of safety when the sanitizer reports uncertainty.

## Retention classes and policy seams

Retention classes (ADR-0003):

- `current`
- `operational_history`
- `execution_evidence`
- `audit_security`
- `disposable`

Exact TTLs, legal-hold policies, and production quotas are **not invented here**. The runtime exposes validated policy/configuration seams with safe defaults:

- default max object size and workspace byte budget (conservative local limits)
- protected classes that refuse ordinary deletion (`audit_security`, active execution evidence, active-run references)
- optional legal-hold flag only when already modeled; otherwise treated as a deferred seam

Documented defaults may be overridden by an approved operational policy later without schema rewrites that break lifecycle invariants.

## Quotas and cleanup

- Quota checks run at admission time. Over-quota admissions fail closed without writing unsafe partial objects.
- Cleanup deletes only artifacts in `eligible_for_deletion` that lack protected references.
- Protected audit records, execution evidence, active-run references, and modeled legal holds are not deleted by ordinary cleanup.
- Artifact deletion never mutates device state, leases, fencing, or run outcomes.
- `cleanup_failed` remains queryable until a successful retry or operator-reviewed resolution.

## Protected references

Active `artifact_references` and retention class rules block deletion. Ending a reference is explicit and audited. Orphaned metadata (no bytes) and orphaned bytes (no metadata) are detectable via restore verification and storage health.

## Backup, export, and restore

Backup/export treats SQLite metadata and referenced CAS bytes as **one recoverable unit**.

Restore verification must check:

- foreign-key validity
- migration ledger state
- artifact references
- content hashes vs on-disk objects
- missing or orphaned bytes
- that stale leases/fencing tokens cannot resume unsafe work

## Orphan recovery

- Orphaned metadata: mark or report; do not invent bytes.
- Orphaned bytes: report for operator review; do not auto-delete without policy.
- Never authorize recovery by pasting absolute paths from UI input.

## Media preview and recording behavior

- Observation and capture seams persist screenshots and UI-tree evidence only through the artifact service.
- Local/snapshot fallback is preferred before any interactive remote media.
- Scrcpy capture remains inside the approved one-device lab boundary; CI must not require a physical device.
- Recordings are explicit sessions with bounded lifecycle, storage, failure, and cleanup behavior.
- Grid views use low-resolution sanitized previews; full-resolution media is limited to the selected authorized source/follower device.
- WebRTC signaling, SFU, and TURN are deferred until selected-device control and mirror paths are stable and evidence requires them.

## Failure isolation

| Failure | Effect |
| --- | --- |
| Capture/admission failure | Omission or error metadata; run/command outcome unchanged |
| CAS write failure | No stored metadata promotion; temp files cleaned or classified |
| Cleanup failure | `cleanup_failed` visible; metadata retained |
| Unauthorized read/delete | Rejected and audited |

## Compose and deployment

- Primary path: local CAS under application data (always available with the control plane).
- `deploy/compose/docker-compose.artifacts.yml` remains **opt-in** (`profiles: ["artifacts"]`) for experimental object-store adjacency. It is not required for P15 and must not be treated as production remote media infrastructure.
- Preserve loopback binds and least-privilege credentials when the opt-in profile is used.

## Limitations and deferred work

Deferred beyond P15:

- remote object storage as the primary store
- SFU / TURN / hosted multi-device streaming
- production identity, mTLS, and fleet-wide media authorization
- exact retention durations, formal legal-hold product policy, and encryption-at-rest mandates pending operational approval

## Related documents

- `docs/adr/0003-local-storage-and-migration.md`
- `docs/adr/0004-real-device-adapter.md`
- `docs/adr/0006-ai-assistance-boundary.md`
- `docs/domain/resource-lifecycle.md`
- `docs/operations/edge-recovery.md`
