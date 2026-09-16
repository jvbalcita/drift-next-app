# ADR-0014: An append-only observation/evidence record per dispatched device action

- Status: Accepted — Wave 1a (ARC-64)
- Date: 2026-09-16
- Depends on: ADR-0002 (current projections separate from append-only history), ADR-0003 (local storage, forward-only migrations), ADR-0010 (device input dispatch, the readiness refusal vocabulary and the postcondition evaluation), ADR-0012 (the device input transport surface, which records that the composition root that constructs a dispatcher is ARC-89)
- Does not lift: ADR-0004 and ADR-0005 (no raw ADB shell, no arbitrary command text), ADR-0008 (a typed-text value is referenced by an opaque handle and never carried)

## Context

By ARC-62 / ARC-66 a device input was authorized, dispatched, evaluated against its declared postcondition and recorded — but only as **state**. `control_action_attempts` is the attempt state machine: it is updated as the attempt moves, and its terminal row says what an attempt *ended as*, not what *happened*. Two things followed:

1. **Nothing referenced what happened.** A recording — the artefact this product exists to produce and replay — had no history to point at. It could reference the intent, the lease tuple and the idempotency key, which is a statement of what was intended, or it could reference the current attempt row, which the next transition overwrites. The observation the postcondition was evaluated against was discarded the moment the attempt completed: it was taken, answered, and dropped.
2. **A refusal left no trace.** ADR-0010 recorded that refusals are not audited: the kernel writes no audit record for a refusal it makes before dispatch, and the dispatch boundary returns a typed `Refusal` to its caller and nothing else. An operator who was told "the lease had expired" four hours ago could not say afterwards that it had happened, or how often, or to which device.

## Decision

**Emit exactly one append-only observation/evidence record per dispatch call through `InputDispatcher.Run`, carrying the action identity, the target device, the outcome and the resulting observation, with the values admitted by shape so that no typed content and no credential can be carried.**

### 1. One record per dispatch call, appended after the outcome is known

`InputDispatcher.Run` splits into `dispatch`, which runs the attempt and reports everything the record needs, and one `recordEvidence` call. The record is therefore appended exactly once per `Run`, on every path — including the paths that never reached a device — and no path can append twice or not at all. Appending after the attempt completes is deliberate: the record carries the **recorded** outcome, not the adapter's provisional one.

The record is built from facts the boundary already holds (`evidenceFor`). It carries:

| Fact | Field |
|---|---|
| action identity | `attempt_id`, `action_kind`, `invocation_surface` |
| target device | `device_id` (stable identity), `serial` (the transport it was addressed to) |
| outcome | `disposition`, `outcome`, `postcondition_state`, `refusal_reason`, `failure_class` |
| resulting observation | `observation_token`, `observation_foreground_package`, `observation_field_length`, `observation_partial`, `observation_failure_class` |

`disposition` is the course the action took, and it is the fact that separates a record of something that happened from a record of something that did not:

| Disposition | Meaning |
|---|---|
| `dispatched` | the typed input reached the device transport for this attempt |
| `refused` | no typed input was sent |
| `replayed` | a duplicate delivery returned the recorded result instead of acting again |

`dispatched` is derived from what the **executing adapter** reports, not from the kernel's dispatch transition. The kernel records a dispatch before the boundary decides whether the argument array may be built, so a render-space refusal (ADR-0011) is a kernel-dispatched attempt that nevertheless sent nothing; the record says `refused`, which is the truth about the input. `attemptReportSink` is the per-attempt mailbox that carries this back: the adapter publishes whether the transport was invoked and what observation it took, and the dispatcher reads it after the actor has finished.

### 2. A refusal records evidence, and a request that names no action records none

**A refusal records evidence.** This is the decision this record exists to make explicit, because the alternative is defensible and was rejected only for a stated reason: an evidence record for a refused action is as valuable as one for a dispatched action, and a refusal that is recorded only in a returned typed error is a refusal an operator cannot corroborate afterwards. The record carries the boundary's own `RefusalReason` (the thirteen-reason vocabulary of ADR-0010), the refusal's failure class and its empty outcome. It invents no terminal outcome and carries no observation, because nothing was observed.

**A request that names no typed action records nothing.** A request whose payload is absent, doubled or incomplete builds no action identity and reaches no device; an action identity is what evidence is evidence of, and a record naming no kind would be a record of nothing. This is the boundary of the claim above, and it is asserted rather than left implicit.

**A duplicate delivery records the replay.** The replay is a fact about what happened, so it is recorded — as `replayed`, with the recorded outcome, and with **no observation**. Copying the first record's observation into the replay would claim the duplicate delivery took an observation it never took; the first record already holds it.

### 3. Append-only is a property of the schema, not of a convention

- The recorder port has exactly one operation, `Append`. There is no update, replace, upsert or delete operation on the interface or on the store behind it.
- The service assigns the record's identity and appends; it refuses a record that carries a caller-chosen identity, so two dispatches of the same action cannot be collapsed into one by an identity collision. A second dispatch appends a second record and leaves the first byte-for-byte as it was.
- The table itself refuses mutation: `action_evidence_is_append_only_update` and `action_evidence_is_append_only_delete` raise `ABORT` on every `UPDATE` and `DELETE`, so a caller that reaches the database without going through the repository still cannot rewrite or erase evidence. A test asserts both refusals against raw SQL.
- **Append order is the insertion order and not the recorded timestamp.** Two appends can share one timestamp — a frozen clock, a coarse clock, two devices dispatched in the same millisecond — and a history whose order ties is a history that cannot be read in order. The repository reads `ORDER BY rowid`. This was found by a test rather than assumed: the integration fixture's clock is deliberately frozen, and the first version of this slice returned the retry before the original.

The table is history, and `control_action_attempts` stays the projection. Neither reads the other, and the migration adds no column to the projection.

### 4. Redaction is a fail-closed admission, and the record has nowhere to carry content

The record type has no field for typed content and none for a handle to it. A typed-text action records the **addressed field's length**, which is a count; the value and the handle that names it have no path into the record, and a test walks the record's fields by reflection and fails if one is ever added whose name could carry content.

`RedactActionEvidence` admits every value a caller could populate by shape and refuses the record otherwise:

- identifiers (`workspace`, `device_id`, `attempt_id`), the transport `serial` and the observation handle are opaque, bounded tokens — no whitespace, no content punctuation, no shell metacharacter, no path separator, no flag, no glob;
- the foreground package is a package name;
- the refusal reason is a member of this boundary's closed refusal vocabulary;
- the failure classes, outcome and postcondition state are members of their shared vocabularies;
- the addressed field's length is a bounded count.

It **fails closed rather than blanking**. Blanking an inadmissible value would persist a record that looks as though nothing was observed when the observer in fact returned something this boundary will not store, and an evidence record that misreports an observation is worse than no record. The store re-derives its own admission independently (AGENTS.md §9, and the same "a second gate re-derives its own rule" property the ADB allow-list has), so a caller that reaches the repository without the boundary's redaction is still refused.

**An outcome that cannot be recorded is reported.** When the dispatch succeeded and the record could not be written, `Run` returns the result *and* a typed `EvidenceRecordError` naming the operation and a fixed reason; the device action has already happened, so this is never a report that the action failed — it is a report that the outcome is not explainable from stored evidence. The dispatch is not repeated. When the dispatch itself failed, that failure is the reported one and stays the primary classification.

### 5. What was added to the observation shape, and what remains ARC-89

Nothing was added to `PostconditionObservation` and nothing to the wire contract. The evidence record carries the four facts that port already exposes — the freshness token, the foreground package, the addressed field's length, the partial flag — plus the observation's own failure class. What ARC-64 adds is the **carrier**: before this slice the foreground package and the addressed field length were taken inside the executing adapter, used to answer the postcondition, and dropped; there was no path by which they could leave the actor.

What remains ARC-89's work, stated plainly: the **composition root** itself — ARC-89 owns it (ADR-0012 §6) — and, with it, the **production** `PostconditionObserver` and the observation-bundle change ADR-0010 deferred. The existing bundle carries neither the foreground package nor the addressed field length, so no production observer can fill them today. Until then the port fails closed as it does now (no observer, no dispatch) and the evidence record's observation is only as complete as the observer that filled it.

## Alternatives considered

- **Emit from the kernel (`ActionService.Complete`) instead of the dispatch boundary.** Rejected: the kernel never sees the observation, and widening `Completion` to carry it would put the observation port inside the kernel that ADR-0010 kept unchanged.
- **Emit from the executing adapter.** Rejected: the adapter knows the observation but not the recorded outcome, and it runs inside the actor. The record would then have to be re-read or patched to state the outcome — which is the mutation this slice exists to prevent.
- **Derive `dispatched` from `action_attempts.dispatched_at`.** Rejected: the kernel records a dispatch before the boundary decides whether it may build an argument array, so a refused coordinate would be recorded as a dispatch. That is a record of what was intended, which is the thing this slice replaces.
- **Copy the recorded attempt's observation token into the replay record.** Rejected: it would claim the replay observed something, and the first record already holds the observation.
- **Blank an inadmissible observation value instead of refusing the record.** Rejected as above: a record that misreports an observation is worse than a refused one, and the refusal is visible rather than silent.
- **Order the history by `recorded_at`.** Rejected: ties make the order non-deterministic, and this slice's integration proof has a frozen clock, which is exactly the case a timestamp ordering gets wrong.
- **Write evidence inside the same transaction as the completion transition.** Rejected for this slice: it would require the kernel to own the evidence write and to see the observation, and the two are deliberately separate histories.

## Consequences

- A recording can reference evidence — the action identity, the target device, the outcome and the observation — instead of the intent it was built from, and that reference survives the projection being updated.
- A refusal is now a recorded fact with its own reason and failure class, so "how often was this device refused, and why" is answerable from stored history.
- The evidence history grows by one row (plus its audit and outbox rows) per dispatch call, including refusals. Retention is `execution_evidence`; the table is not cleaned by anything yet.
- The record type is the store's row type (`store.ActionEvidence`), the same way `AuditEntry` and `OutboxEntry` are, because `internal/edge/execution` already depends on `internal/store/sqlite` and the dependency must not run the other way.
- The recorder is a construction option (`WithEvidenceRecorder`). A dispatcher built without one appends nothing: evidence is not a safety gate on the dispatch, so absence loses the record rather than refusing the action. **The composition root that will construct a dispatcher is ARC-89 and not this card's surface** (ADR-0012 §6), so nothing in production binds a recorder yet and this slice's records are, on today's `main`, produced only by tests. When ARC-89 constructs the dispatcher it must pass `WithEvidenceRecorder(store.NewActionEvidenceService(db))`, or the surface this card delivers records nothing in production.

## Validation

- `internal/store/sqlite/action_evidence_test.go`: two dispatches of the same action producing two records with the first unchanged (compared field by field); a raw `UPDATE` and a raw `DELETE` each refused by the database with the history intact afterwards; append order per device; and seventeen inadmissible records refused (`invalid_input`) with nothing persisted — missing identity, a kind that is not a catalog entry, unknown disposition, outcome and postcondition state, a failure class outside the vocabulary, a refusal reason that is free text, content-bearing and unbounded observation handles, a foreground package that is not a package name, negative and over-bound field lengths, a serial that could express a command, and a caller-chosen record identity.
- `internal/edge/execution/evidence_test.go`: the reflection walk asserting no field of the record could carry content, the walk asserting **every** string field refuses a content-shaped value, an admissible record handed on unchanged, and sixteen inadmissible records refused.
- `internal/edge/execution/input_dispatch_evidence_test.go`: one record per dispatched action for each of the five typed inputs, each carrying identity, target, disposition, outcome and observation; three refusal cases recorded with their own reason, failure class and empty observation at zero device calls; a request naming no typed action recording nothing; a replay recorded without an observation; the typed-text record carrying neither the handle nor the value in any rendering, at the addressed field's length; and an unrecordable outcome reported as a typed `EvidenceRecordError` with the dispatch not repeated.
- `internal/edge/execution/input_dispatch_evidence_sqlite_test.go`: the same properties against a real SQLite database through the real append-only service — a dispatch, a retry and a duplicate delivery producing three records in append order with the first unchanged and the duplicate delivery not acting again; and the stored rows of a typed-text action carrying neither the handle nor the value.
- `gofmt -l` on the changed files, `go vet ./...`, `go build ./...`, `go test ./...`, `go test -race ./internal/edge/... ./internal/store/sqlite/... ./internal/transport/connect/...`, `bash scripts/secret-scan.sh` and `git diff --check` are the gates for this change; `go.mod`/`go.sum` are unchanged and no dependency was added.
