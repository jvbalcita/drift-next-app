# 0016 — Catalogued device settings (rotation lock and autofill off)

Status: accepted (2026-09-17)

## Context

The product this one replaces prepares its fleet the same way every session: it
locks screen rotation and turns autofill off. Neither is cosmetic, and both were
read out of the old product rather than ported by reflex:

- **Rotation lock.** The recorded skills are COORDINATE-based. The old
  repository's own guide states the rule outright: rotation lock is enforced by
  the runner, and a coordinate-recorded skill is invalid in any other
  orientation. A device free to rotate is a device on which every coordinate
  recording is invalid.
- **Autofill off.** The Android autofill popup lands on top of the login forms
  this fleet drives. A popup over a form is a tap that lands on the popup.

The old product applied both through two endpoints (`POST /registry/settings/rotation`
and `POST /registry/settings/autofill`), each of which issued fixed `adb shell`
commands and returned `{ serial, success, error? }` per device plus
`{ total, applied, failed }`. Autofill read its own setting back and failed when
autofill was still enabled.

Two rules in AGENTS.md constrain how that is rebuilt here:

- Section 3 permits a general device command only in one of two forms, and the
  blanket ban on such a command was retired on the owner's instruction
  (2026-09-17). The CATALOGUED form is "a named operation with a bounded, typed
  parameter set, appearing as an ordinary first-class action with its own audit
  record".
- Section 3 also requires each allow-list admission to be its own recogniser,
  disjoint from every other admission.

## Decision

Both settings are first-class action-catalog kinds — `rotation_lock` and
`autofill_off` — in the CATALOGUED form of the general-command rule.

1. **Kinds and capability.** Each kind declares its own risk, retry class,
   mutation reason, declared postcondition and allowed invocation surface
   (`manual` only). Both require a new capability, `device.settings`, rather than
   reusing `device.input.system`: authority to inject input is not authority to
   rewrite a device's settings. Autofill off is `high` risk, so the policy
   evaluator requires explicit operator approval before the kernel authorizes it.

2. **Fixed admissions, one recogniser.** The nine argument arrays — five writes
   and four read-backs — are admitted by `matchesDeviceSettingsAllowlist`, a
   recogniser of its own, with every token spelled out literally. No admission
   has a variable position at all: no decimal, name, flag, path or second
   command, so no admitted array can express command text. The recogniser is a
   third family beside the typed device inputs and the read-only builders, and
   the disjointness of the three is asserted in a test rather than left to the
   order they happen to be asked in.

3. **The read-back is part of the operation.** A settings change is not reported
   as applied because a command exited zero. The device is read back — rotation
   as `accelerometer_rotation=0` and `user_rotation=0`, autofill as no selected
   service and the augmented service disabled — and a read-back that does not
   satisfy the catalog's declared postcondition fails the attempt. A read the
   device refuses leaves its setting unconfirmed, and an unconfirmed setting
   never satisfies a postcondition.

4. **Fleet-wide apply through the kernel.** One control session is opened for the
   caller; each device gets its OWN lease, fencing token, attempt and idempotency
   key. Devices run one at a time. A device whose lease cannot be taken, whose
   transport is gone, or whose setting does not read back as required is recorded
   against its own device id and the run continues; every lease is released on
   the way out. The report carries one row per device per setting plus device
   counts, and no aggregate verdict replaces a row.

## Consequences

- The wire contract (`proto/drift/v1/device_settings.proto`) exposes one RPC whose
  request names a closed enum of settings and no device list, so a caller cannot
  assert which devices are attached or compose a command.
- A setting that was written and not confirmed is reported as not applied, so an
  operator never reads success for a device that is still rotating.
- Adding a third setting is a deliberate change: a new enum member, a new catalog
  entry, a new fixed admission, and its own read-back.
- The `Lifted` field on both catalog entries cites this record, so a reviewer can
  tell a kind the retired ban previously refused from one that was never
  deferred.
