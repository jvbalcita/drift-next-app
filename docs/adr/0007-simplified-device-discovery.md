# ADR-0007: Simplified device discovery — a scan observes and upserts

- Status: Accepted — records the simplification merged in PR #28, PR #34, and PR #37; reverses the candidate/approval/registration model of ADR-0002
- Date: 2026-09-16

## Context

ADR-0002 modelled discovery as a chain of separate persisted transitions: a Network Profile authorized a scan, a Scan Run recorded the request, Scan Candidates retained observed endpoints without creating devices, an operator approval/rejection/expiry decision resolved a candidate, and only an approved candidate could create a Device with endpoint history. Phase 14 then added a second, independent lifecycle on the lab adapter — `DiscoverLabDevices` enumerated candidates, `ConfirmLabTarget` bound one of them to session state, and `ClearLabTarget` cleared it — backed by its own registration, provisioning-check, and approval tables.

Both stacks existed to answer one question — which devices are attached to this workspace — and both encoded the same assumption: that observing a device and treating it as a device must be different events, mediated by an operator decision. The cost was concrete. The discovery surface had grown to 15 RPCs, 3 enums, 2 state machines, and 2 approval models, plus six tables (`scan_candidates`, `approval_decisions`, `registration_events`, `lab_device_registrations`, `lab_provisioning_checks`, `lab_registration_approvals`) that had to be migrated, tested, and kept consistent with each other.

The duplication also produced a structural defect rather than a cosmetic one. On the lab adapter, `authorizeCapture` hard-required `state.ConfirmedSerial`, and only `ConfirmTarget` ever wrote it, so capture was unreachable without the confirmation step; the second lifecycle could not simply be deleted. The same coupling showed up in migration 0020: `scan_candidates` referenced `scan_runs` with `ON DELETE RESTRICT`, so a database that had ever produced a candidate blocked the `scan_runs` rebuild outright. Approval state was, in practice, load-bearing for code that had nothing to do with approval.

## Decision

One path. A scan is an observation that upserts canonical devices, and it is the only way discovery creates anything.

- `DiscoveryService.StartScan` runs one bounded scan of a saved Network Profile, idempotent on its idempotency key. `FinishScan` upserts each observed device into `devices`/`endpoints` matched on `(workspace_id, serial)`: a device seen for the first time is inserted directly in the `active` state, and a device seen again keeps its stable `device_id` and refreshes `last_seen_at`. The `scan_runs` row is history, not a gate.
- There is no candidate queue, no `pending_approval` state, no approval RPC, no registration RPC, and no provisioning flow. The removed proto names, field numbers, and enum numbers stay reserved in `proto/drift/v1/discovery.proto` rather than reused.
- A Network Profile is saved scan policy, not a resource with a lifecycle: create, update, or delete, with no state column and no `row_version`, and at most one default per workspace. Migration 0020 drops the candidate, approval, registration, and provisioning tables and rebuilds `scan_runs` and `network_profiles` to this shape.
- The lab adapter keeps enumeration as a scan-only primitive. PR #37 (Phase E) removed its duplicate lifecycle and replaced confirm-then-capture with a device-scoped capture that names its own target on every call. PR #34 (Phase D) added one bounded, non-fatal startup auto-scan of the default profile, reusing the same scan machinery rather than adding a second implementation.

### The argument that decided it

The approval ceremony guarded a boundary it did not actually protect. What makes a device action safe in Drift Next is the P7 kernel: a valid per-device lease with a fencing token, a policy decision, and an explicit control session, checked before dispatch. A device row confers none of that — it is identity and observation history, not permission. Nothing in the kernel consults whether a device was approved, registered, or provisioned, and every mutating path is refused on lease, fencing, policy, or session grounds regardless of how the device row came to exist. Removing the approval step therefore does not weaken action safety: it removes a step that re-verified a distinction the safety boundary never relied on.

The ceremony was not free, either. It taught operators and reviewers that a device row could mean two different things depending on provenance, and it justified the existence of a second discovery stack whose only purpose was to re-implement the first one's distinction.

## Alternatives considered

- **Keep candidates and make approval automatic.** Rejected. It keeps both state machines, the candidate table, and the approval vocabulary while removing only the operator's click, so the maintenance cost and the two-meanings ambiguity stay.
- **Keep candidates on the lab adapter only.** Rejected. Phase E showed the duplicate lifecycle diverges in ways that hide defects; two discovery paths for one outcome is the problem, not the number of approval buttons.
- **Delete discovery and rely on the lab adapter's enumeration.** Rejected. Enumeration has no policy, no bounded scan, no scan history, and no notion of a saved profile; it is a transport source, not a discovery model.
- **Model approval as a policy flag on the device row instead of a separate state machine.** Rejected as a rename. Policy decisions in this codebase are made per action against the P7 kernel, and a per-row approval flag would create a second place where action authority appears to live.

## Consequences

Easier, and preserved deliberately:

- Stable device identity stays distinct from mutable endpoint identity. A device keeps its `device_id` across rescans and endpoint changes while its endpoint row and transport facts, including the ADB transport ID and connection state, stay mutable attributes.
- Scan history survives profile deletion. `scan_runs.network_profile_id` is a nullable historical column with no foreign key, and profile deletion sets it to NULL rather than being blocked by it; the run is never removed with the profile that produced it.
- USB transports are representable without inventing a TCP port sentinel. A USB observation is a serial with an empty host and port zero, and an observation validates on a serial or a host alone.
- The console's discovery surfaces are read-only over observed devices: there is no approve, reject, register, or provision intent to route, and no provisioning wizard to keep in sync with the domain.
- ADR-0003's audit class continues to protect the events that still exist; the approval and registration events it names do not.

Deliberately not done, or left open:

- Real-device integration coverage was removed with the registration path and must be re-authored against the upsert model. `tests/integration/realdevice/realdevice_test.go` drove the removed provisioning and registration flow, could not be rewritten faithfully without a device, and was deleted rather than left broken. The tag-free selection helper in that directory is kept. This is the largest gap this ADR leaves open.
- Phase E keeps the lab enumeration as a scan-only primitive rather than folding it into the discovery service. What remains is a shared transport source, not a second lifecycle.
- The removed tables and their rows are dropped, not archived. Migration 0020 is forward-only, and candidate and registration history is not preserved.
- No fleet, multi-device, unattended, or production-device authority is granted by this ADR. The P13/P14 one-device authorization boundaries recorded in ADR-0004 are unchanged.

## Validation

- Migration and integration tests prove 0020 applies to a database migrated through 0019 and seeded with a workspace, a default profile, and a completed scan run; that `PRAGMA foreign_key_check` reports zero violations afterwards; that the scan run keeps its profile reference and idempotency key; and that profile deletion succeeds with scan history present and leaves that history intact.
- `internal/discovery` tests cover profile validation that refuses unbounded or unsafe policies, a new profile being immediately scan-eligible, a scan that upserts canonical devices and their current endpoint, an observation that reports no side effects, and a rescan that reuses the existing device row instead of duplicating it.
- `internal/discovery` lab-scanner tests cover the port and address policy filter, the mapping of a USB observation with no host or port, and a missing runtime being reported clearly rather than silently.
- `internal/product` startup-auto-scan tests cover exactly one scan per process start, device persistence, the no-default-profile and no-workspace no-op paths, a scanner or store failure that is recorded without aborting startup, the deadline bound, and cancellation on shutdown.
- The contract-level consequence is asserted in the generated proto and its reserved-name comment: the candidate and registration RPCs, messages, and enum are absent, and their names and numbers are not reused.
- `go build ./...`, `gofmt -l .`, and `git diff --check` are the standing repository gates for this change; it touches no Go behavior.
