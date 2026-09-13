# Drift Next domain language

Drift Next is a local-first, supervised control platform. These terms describe the concepts that cross its domain boundaries.

## Workspace and device identity

**Workspace**:
A single installation's isolated authority scope and ownership boundary.
_Avoid_: Organization when referring to the local authority scope.

**Device**:
A stable, registered Android or fake-device identity managed by the workspace.
_Avoid_: Serial, endpoint, handset name

**Endpoint**:
A mutable transport observation, such as a serial or host/port, associated with a device over time.
_Avoid_: Device identity, serial as device ID

**Edge agent**:
A local runtime that reports capabilities and hosts device actors; it is not the logical automation personality assigned to a device.
_Avoid_: Automation agent, device

**Scan candidate**:
Non-authoritative evidence discovered by a bounded scan and awaiting an approval decision.
_Avoid_: Device, discovered device

**Ungrouped**:
The computed view of devices without an active group placement.
_Avoid_: Ungrouped group

## Control and automation

**Automation agent**:
A logical, versioned automation profile that may be assigned to many devices.
_Avoid_: Edge agent, worker process

**Control session**:
An explicit supervised operator context that permits control authorization.
_Avoid_: Login session when discussing device control

**Lease**:
A time-bounded per-device control claim carrying a monotonically increasing fencing token.
_Avoid_: Lock when durable ownership and fencing are intended

**Observation snapshot**:
An immutable, provenance-labeled capture of a device state and its available evidence at a point in time.
_Avoid_: Current status, screenshot-only

**Workflow run**:
An execution record with a target snapshot and independent per-device target outcomes.
_Avoid_: Batch result when target-level outcomes matter

**Skill**:
A reviewed, immutable, versioned set of typed deterministic steps that can be replayed under current observation and policy.
_Avoid_: Script, arbitrary automation

## External references and evidence

**Account reference**:
Non-secret metadata that identifies an external account without storing credentials, sessions, or verification data.
_Avoid_: Credential, account session

**Evidence artifact**:
An application-managed, content-addressed record of bytes or metadata used to explain an observation or execution outcome.
_Avoid_: Arbitrary file path

**Logical interaction event**:
One recorded user-intent transition represented as BEFORE state, ACTION, and AFTER state rather than one raw input primitive.
_Avoid_: Raw event when the normalized event is intended
