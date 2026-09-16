# Edge Recovery — Reconnect and Runtime Spool

This document describes operator recovery for the local control plane and an
optional separate edge runtime: reconnect behavior, the optional bounded spool,
and indeterminate outcomes. It does not authorize fleet rollout, unattended
execution, production devices, or a second mandatory database.

Discovery no longer has an approval, provisioning, or registration stage. A scan
observes attached devices and upserts them directly; there is no candidate queue
and no registration decision to recover. See ADR-0007.

## Scope

- One explicitly named lab device per capture
- Attended operator sessions only
- Canonical state stays in the bundled local service SQLite database
- Optional bounded spool exists only for a separate edge runtime process

## Explicit Transitions

Keep these stages separate. Never collapse them into one button.

1. **Discovery** — Network Profile scan through an authorized local runtime in lab mode; the scan observes devices and upserts them as canonical devices
2. **Connection** — Runtime link and transport state, reported as observations
3. **Capture** — An explicitly named device is observed read-only; naming a device registers nothing and grants no lease

Stages are still distinct kinds of work, but observing a device is itself the act that
makes it a device. There is no intermediate approval or registration step between
discovering an endpoint and it being visible.

## Pre-Connection Checks

Before a scan or capture, Drift records evidence for:

| Check | Failure behavior |
| --- | --- |
| Device pairing / authorization | Refuse; report clearly; do not repair the device |
| ADB server ownership | Refuse; do not claim foreign ADB servers |
| Platform-tools compatibility | Refuse; never implicitly install software |
| Transport identity | Refuse empty or ambiguous transport IDs |
| Endpoint identity | Refuse empty serial / host identity |
| Port policy | Refuse or filter out ports outside the Network Profile allow-list |

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
2. Re-run the scan
3. Confirm the device reports a usable transport; an unauthorized or offline
   device is reported as such and is not actionable

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

Rollback does not imply automatic device repair or a registration decision. Prefer:

1. Release any held lease and close the control session
2. Leave the canonical registry untouched; a device row changes only when it is observed again
3. Preserve audit, scan history, and observation events in SQLite

## Safety Invariants

- Loopback-only control-plane service boundary
- The lab route token and its constant-time check remain mandatory in real-device mode
- One explicitly named lab device per capture; nothing is inferred from list order or a display name
- No second mandatory database for a normal local install
- No autonomous offline fleet behavior
