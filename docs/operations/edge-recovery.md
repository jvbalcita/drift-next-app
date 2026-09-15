# Edge Recovery — Registration, Reconnect, and Runtime Spool

This document describes operator recovery for Drift Next Phase 14: controlled
one-device lab registration and the optional edge-runtime spool. It does not
authorize fleet rollout, unattended execution, production devices, or a second
mandatory database.

## Scope

- One explicitly authorized lab device
- Attended operator sessions only
- Canonical state stays in the bundled local service SQLite database
- Optional bounded spool exists only for a separate edge runtime process

## Explicit Transitions

Keep these stages separate. Never collapse them into one button.

1. **Discovery** — Network Profile scan through an authorized local runtime in lab mode
2. **Approval** — Operator decision on a candidate (approve / reject / expire)
3. **Provisioning** — Verify pairing, ADB ownership, platform-tools, transport and endpoint identity, port policy, and rollback readiness
4. **Registration** — Explicit operator confirmation creates the canonical device/endpoint

Silent registration of a discovered or provisioned endpoint is refused.

## Pre-Registration Checks

Before registration, record evidence for:

| Check | Failure behavior |
| --- | --- |
| Device pairing / authorization | Refuse; report clearly; do not repair the device |
| ADB server ownership | Refuse; do not claim foreign ADB servers |
| Platform-tools compatibility | Refuse; never implicitly install software |
| Transport identity | Refuse empty or ambiguous transport IDs |
| Endpoint identity | Refuse empty serial / host identity |
| Port policy | Refuse ports outside the Network Profile allow-list |
| Rollback readiness | Refuse when rollback evidence is missing |

Drift never enables accessibility, removes packages, broadens permissions, or
opens firewall exposure as part of recovery.

## Runtime Connection States

| State | Meaning | Operator action |
| --- | --- | --- |
| Connected | Runtime link is healthy | Normal low- and authorized mutating work |
| Reconnecting | Link recovery in progress | Wait; high-risk actions remain refused |
| Disconnected | Link lost (network loss, host restart, device disappearance, process restart) | Refresh observation; resolve indeterminate items |

Helper attach/detach, protocol/version, token rotation/revocation, and transport
IDs are **runtime observations**. They are never control-plane leases or fencing
tokens.

## Optional Runtime Spool

When the edge runtime is a separate process, it may keep a bounded local spool for:

- reconnect cursors
- low-risk outbox messages
- observations waiting for upload

Spool rules:

- Bounded queue size; enqueue fails closed when exhausted
- Retention expiry drops aged items
- Sequence ordering is preserved
- Duplicate idempotency keys are rejected
- Local fence tokens are runtime observations only
- High-risk or stale-policy actions are refused while disconnected
- Dispatched-before-disconnect items become **indeterminate**

### Indeterminate Outcomes

If an action may have been dispatched before disconnect or timeout:

1. Classify it as indeterminate
2. Do **not** blindly retry or replay from the spool
3. Require a fresh observation **or** explicit operator confirmation

Confirm replay and drop are separate, confirmable controls.

## Recovery Playbooks

### Missing tools / incompatible platform-tools

1. Read the error detail in the console
2. Install or update Platform-Tools yourself
3. Re-run verification; Drift will not install tools

### Unauthorized or unpaired device

1. Complete pairing on the device under attendance
2. Re-run provisioning verification
3. Approve, then register only after verification succeeds

### Network loss / device disappearance / transport ID change

1. Mark runtime disconnected
2. Review blocked / indeterminate spool items
3. Reconnect with fresh transport evidence
4. Resolve indeterminate items with observation or confirmation

### Process or host restart

1. Runtime starts disconnected until heartbeat succeeds
2. Spool pending low-risk items may remain; dispatched items stay indeterminate
3. Re-authorize high-risk work through the control plane after reconnect

### Queue exhaustion

1. Inspect spool health (pending, blocked, max size, retention)
2. Confirm or drop blocked items
3. Allow retention expiry or free capacity before new enqueues

## Rollback

Registration and provisioning keep rollback readiness as a precondition.
Rollback does not imply automatic device repair. Prefer:

1. Clear the lab target / session
2. Leave canonical registry untouched until a new approved registration
3. Preserve audit and registration events in SQLite

## Safety Invariants

- Loopback-only control-plane service boundary
- Existing token / consent protections remain mandatory
- One-device lab scope for P14
- No second mandatory database for a normal local install
- No autonomous offline fleet behavior
