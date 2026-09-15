# ADB compatibility evidence (Phase 13)

This directory holds the opt-in real-device evaluation for the Phase 13 one-lab-device
vertical slice described in [ADR-0004](../../../docs/adr/0004-real-device-adapter.md).

Everything here is **read-only observation evidence**. No test, fixture, or procedure in
this directory issues device input, installs anything, enables wireless debugging, pairs a
device, or opens a port.

## Scope

The owner authorized controlled Phase 13 work against **one confirmed lab device** on
2026-09-15. That authorization does not extend to production devices, production accounts,
a fleet, multi-device execution, unattended runs, or any Phase 14 work.

## Prerequisites

1. Official Android Platform-Tools installed, with an absolute path to the `adb` binary.
   Drift does not search `PATH` and does not download or install a toolchain.
2. Exactly one lab device that the operator personally intends to observe, already
   attached by the operator. Drift does not establish transports.
3. The device serial, known and typed by the operator. It is never inferred.
4. An attended session. These tests are not for CI and not for unattended execution.

CI sets none of the environment variables below, so every real-device test skips there.

## Environment variables

| Variable | Required | Purpose |
| --- | --- | --- |
| `DRIFT_P13_LAB_MODE` | yes, must equal `1` | Explicit operator opt-in to real-device lab mode. Any other value keeps the service in deterministic mock mode. |
| `DRIFT_P13_ADB_PATH` | yes | Absolute path to the official `adb` executable. Relative paths, bare names, and unset values are rejected. |
| `DRIFT_P13_LAB_SERIAL` | yes for the real-device test | The exact serial the operator confirms as the target. Must appear in discovery output or the test fails closed. |

`DRIFT_P13_LAB_MODE` and `DRIFT_P13_ADB_PATH` are the dual gate enforced by
`lab.LabModeRequested`; either one missing yields a mock-mode service. `DRIFT_P13_LAB_SERIAL`
is read only by the opt-in test and is what makes the target explicit.

## Running the opt-in real-device test

```bash
DRIFT_P13_LAB_MODE=1 \
DRIFT_P13_ADB_PATH=/absolute/path/to/platform-tools/adb \
DRIFT_P13_LAB_SERIAL=<the serial you are confirming> \
go test ./internal/edge/lab -run TestRealDevice -v
```

Without all three variables the test skips with a message naming what is missing. The
normal suite is unaffected:

```bash
go test ./internal/edge/...
```

## Safety rules

These are not suggestions. They are the conditions under which the authorization holds.

1. **Confirm the serial explicitly.** Type it. Never accept a default, a first row, a
   model name, a display name, or an address as the target.
2. **Never auto-target in a multi-device environment.** A read-only `adb devices -l` on the
   current P13 lab host enumerated **20 attached transports, all TCP wireless and zero USB**.
   Any heuristic selection in that environment is a wrong-device incident waiting to happen.
   The generic `CONFIRM` literal is rejected whenever more than one candidate is discovered.
3. **Read-only only.** Permitted operations are enumerate, validate, health, screenshot, and
   hierarchy dump. No taps, swipes, text entry, key events, clipboard access, package
   changes, settings changes, or reboots.
4. **Do not enable wireless debugging and do not pair.** Drift never runs `adb connect`,
   `adb pair`, `adb tcpip`, or `adb forward`. Discovery is not enablement.
5. **Do not install a helper.** No Python runtime, no `uiautomator2`, no Accessibility
   Helper APK. ADR-0004 records the native path as sufficient for this slice.
6. **Sanitize everything you record.** Real serials, wireless addresses, account names,
   on-screen text, screenshots, and raw hierarchy XML do not go into this directory, into
   commits, into issues, or into review comments. Use the fixtures in `fixtures/` for
   documentation, and `[REDACTED]` where a placeholder is needed.
7. **Stop on anything ambiguous.** An indeterminate outcome stays indeterminate. Do not
   replay an idempotency key whose outcome is unknown; clear the target and start a new
   capture with a new key.
8. **Attended sessions only.** Do not leave a confirmed target active when you step away.

## Fixtures

- `fixtures/sample-devices-l.txt` — sanitized sample `adb devices -l` output using fake
  serials (`LABSERIAL001`, `192.0.2.10:5555`). For documentation and parser illustration
  only; not a test golden file.
- `fixtures/sample-hierarchy-small.xml` — minimal valid UIAutomator hierarchy XML with
  non-sensitive content.

The `192.0.2.0/24` range is the IETF TEST-NET-1 documentation block and is not routable.

## Filling in the matrix

`matrix.md` separates what the existing test suite already proves from what requires a
live device. Do not estimate a live cell, do not copy a number from another project, and
do not mark a row measured without the command output to support it. Live cells stay
`pending operator serial confirmation` until an operator confirms a serial and the
measurement is actually taken.

2026-09-15: operator confirmed `192.168.1.109:5555`. Measured results and the opt-in
PASS are recorded in `matrix.md` and `evidence-2026-09-15.md`. Remaining gaps are listed
in the matrix footer and must not be filled by estimation.
