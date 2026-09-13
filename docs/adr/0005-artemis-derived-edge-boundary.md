# ADR-0005: Selective ARTEMIS-derived edge and perception boundary

- Status: Accepted — owner-approved 2026-09-14
- Date: 2026-09-14

## Context

ARTEMIS contains useful Android observation, targeting, readiness, and helper-protocol ideas, but its Python runtime, database, daemon, scheduler, model/provider stack, and UI would widen Drift Next's trust boundary before the local control plane is proven. Drift Next needs a clear replacement path so useful evidence can be adopted without importing a second authority.

## Decision

Adopt only reviewed concepts as Drift-owned contracts: observation snapshots with explicit coordinate space and provenance, typed action/capability/result envelopes, XML/accessibility-first target resolution, serialized per-device actors, atomic-capture metadata, indeterminate-completion handling, and non-mutating readiness diagnostics. Drift Next remains authoritative for persistence, policy, leases/fencing, idempotency, workflows, audit, outbox, artifacts, and operator control.

Do not add ARTEMIS, Python, `artemis-client`, an MCP server, a model provider, or a sidecar as a foundation dependency. Do not treat ARTEMIS files, locks, traces, database rows, model output, or helper tokens as Drift authority. Raw ADB shell, arbitrary coordinates, clipboard/global commands, automatic package changes, and fail-open uncertainty are outside this boundary.

Any future ARTEMIS-derived helper is optional and subordinate. It may be considered only after the native Go/fake path demonstrates a measured capability gap, the source revision and provenance are pinned, the helper protocol and token/consent/signing/rollback threat model are reviewed, and the P13/P14 one-device gate receives separate Sentinel and owner authorization. The helper token authenticates helper traffic; it never replaces a Drift lease or fencing token.

## Alternatives considered

- Import the full ARTEMIS runtime. Rejected because it introduces a second persistence and orchestration authority, mandatory runtime dependencies, and a wider device/model trust boundary.
- Ignore ARTEMIS entirely. Rejected because its observation and targeting concepts can improve the native contracts without accepting its application architecture.
- Make an ARTEMIS-derived helper primary immediately. Rejected until a native baseline, measured gap, provenance, and one-device security gate exist.

## Consequences

- P2 through P10 may model and test the adopted contracts with sanitized fake sources only.
- P13/P14 must compare the built-in adapter path with any optional helper using the same typed Drift contracts and explicit rollback evidence.
- Helper installation, repair, enablement, device consent, and network exposure are separate approved actions; diagnostics remain read-only suggestions.

## Validation

- Buf/API compatibility tests cover observation provenance, coordinate space, capability negotiation, stable failures, and indeterminate outcomes before helper work.
- Fake actor tests prove stale/ambiguous targets, partial capture, and uncertain transport fail closed.
- P13/P14 acceptance requires a pinned-source comparison, consent/token/transport/lifecycle tests, measured capability gain, security review, and explicit owner authorization.
