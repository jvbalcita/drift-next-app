# ADR-0004: Real device adapter for the Phase 13 one-lab-device vertical slice

- Status: Accepted — owner authorized controlled P13 one-lab-device work 2026-09-15
- Date: 2026-09-15

## Context

Phase 13 is the first Drift Next work permitted to touch a real Android device. Every prior phase used fakes, so the observation, evidence, failure, and indeterminate contracts described in ADR-0005 have never been exercised against a real transport. P13 must prove the narrowest possible slice: read-only observation of one confirmed lab device, through a Drift-owned adapter, with the same typed contracts the fake path already satisfies.

ADR-0005 left one question deliberately open: whether an optional ARTEMIS-derived helper (Python, `uiautomator2`, or an Accessibility Helper APK) is needed on the device side, and whether the native Go/ADB path is sufficient on its own. That question must be answered before P13 chooses an adapter, because a helper widens the trust boundary with an installable, privileged, token-authenticated on-device component.

This ADR records the scope of the authorization, the adapter recommendation, the security boundary, and the go/no-go position for expanding beyond one device.

## Decision

Implement the P13 observation vertical slice with **native ADB plus the built-in UIAutomator dump**, owned entirely by the Go service. **Do not adopt the optional helper.**

The slice lives in `internal/edge/adb` (transport and process safety), `internal/edge/uiautomator` (hierarchy capture and bounds), `internal/edge/lab` (application service, authorization, confirmation, classification, evidence), `proto/drift/v1/lab_adapter.proto` (typed contract), and the console lab-adapter UI.

### Scope and authorization

The owner authorized, on 2026-09-15, a controlled Phase 13 test against **one confirmed lab device**. Implementation lives on branch `artisan/phase-13-real-device-vertical-slice`.

This P13 authorization covered one confirmed lab device under attended read-only observation only. It did **not** cover production devices, production accounts, a device fleet, multi-device or mirrored execution, or unattended operation.

**P14 follow-on (2026-09-15):** Boss separately authorized controlled registration and optional edge-runtime spool for one confirmed lab device, including the reviewed USB/wireless provisioning path, on branch `artisan/phase-14-registration-runtime-spool`. Fleet rollout, multi-device/mirrored execution, production devices/accounts, and unattended execution remain out of scope and **NO-GO** without further authorization.

### One-device boundary

A lab session holds at most one confirmed target, in service memory, for the life of the session. Discovery is separate from approval: `LabAdapterService.DiscoverLabDevices` enumerates candidates and creates nothing. `ConfirmLabTarget` requires an explicit serial that was actually enumerated and a matching confirmation text. The generic literal `CONFIRM` is accepted only when exactly one candidate was discovered; with more than one candidate the operator must type the serial itself, so a target can never be inferred from list order, row position, display name, model, or address.

Confirming a target does not register a canonical device, does not write the control-plane registry, does not grant a lease, and does not confer approval authority.

### Non-goals

- P13 slice itself: no registration flow, runtime spool, onboarding, or port provisioning (those moved to the separately authorized P14 branch).
- No arbitrary shell. There is no `adb shell <caller text>` path and no string interpolation anywhere in the adapter.
- No credentials, tokens, secrets, or account material of any kind flow through this slice.
- No production accounts and no production devices.
- No unattended or fleet operation. Every capture is operator-attributed.
- No helper by default. No Python runtime, no `uiautomator2`, no Accessibility Helper APK, no sidecar, no MCP server, no model provider.
- No device input. The slice issues no taps, swipes, text entry, key events, clipboard access, or package changes.

### Adapter ownership

The Go service owns ADB exclusively. The console never calls ADB, never shells out, never sees a device path, and never receives raw command output. The browser's only device-facing surface is the typed `LabAdapterService` RPCs. Tauri gains no shell, filesystem, or process capability for this slice.

The adb executable is explicit configuration: an absolute path supplied through `DRIFT_P13_ADB_PATH`, validated by `validateExecutable`, and empty until an operator sets it. There is no PATH search and no discovery of an interpreter. Real-device mode additionally requires the explicit opt-in `DRIFT_P13_LAB_MODE=1`; either one missing yields a deterministic mock-mode service, so a misconfigured host never silently reaches a device.

### Stable identity versus transport identity

A lab session identity is prefixed `lab:` and stays stable for the session. The adb transport identifier, the connection state, the connection type, the serial's network address, and the UI row are mutable transport facts. `Adapter.TransportIdentity` returns transport identity deliberately separately from stable identity, and neither the proto nor the service ever uses a transport fact as a primary key. `LabDiscoveredDevice.transport_id` is documented in the contract as a transport fact that must not be used as device identity.

### Native ADB/UIAutomator baseline

Hierarchy capture uses `adb shell uiautomator dump --compressed`. The preferred path streams to stdout through `exec-out ... /dev/tty` so no device-side file is created. When streaming is unavailable the adapter falls back to a temporary file inside its own namespace (`/sdcard/drift-<correlation-id>.xml`, matched by an anchored pattern) and always removes it — after success, after a failed dump, and after cancellation. A cleanup that fails is reported as `cleanup_failed` rather than presented as clean teardown.

Health uses `adb get-state` plus a typed allow-list of read-only build properties (`ro.build.version.release`, `ro.build.version.sdk`, `ro.build.version.security_patch`, `ro.product.model`, `ro.product.manufacturer`, `ro.product.device`, `ro.product.cpu.abi`). No caller can widen that list at runtime. Screenshots use `adb exec-out screencap -p`, bounded and validated as PNG.

### Optional helper evaluation

The native path was measured sufficient for the P13 observation vertical slice, and the helper is **not adopted**.

The justification is the absence of a measured capability gap. The P13 slice needs exactly four capabilities — enumerate, health, screenshot, hierarchy — and the native ADB/UIAutomator path provides all four under the typed Drift contracts, with bounds, classification, cancellation, cleanup, and redaction proven by unit and integration tests in `internal/edge/adb`, `internal/edge/uiautomator`, and `internal/edge/lab`. ADR-0005 requires a demonstrated gap before a helper may be considered; no such gap exists for observation.

The helper question is deferred, not closed. Revisit it only if semantic-targeting or postcondition evidence shows a gap the native path cannot close — for example, if resolving targets by semantic role, or verifying an action postcondition, provably requires accessibility-event streams or on-device state the UIAutomator dump cannot express. Any revisit still requires pinned source and provenance, a reviewed helper protocol and token/consent/signing/rollback threat model, measured capability gain, Sentinel review, and separate owner authorization.

### Security and threat model

The adversary model for this slice is a hostile or compromised device, a hostile transport on the lab network, untrusted command output, and **a hostile or compromised process running as the operator on the same host**. The slice is read-only, so the primary risks are command injection into the host, data exfiltration through captured evidence, targeting the wrong device, treating an uncertain outcome as success, and a local process driving the lab surface without the operator's knowledge.

Each is addressed structurally: argv-only execution removes host injection; hashes, bounds, and redaction limit evidence exposure; explicit confirmation removes inference of a target; and indeterminate outcomes stay indeterminate rather than resolving to success.

The local-caller risk is addressed in two layers, because loopback alone does not answer it. First, the listen address is validated rather than trusted: `DRIFT_CONTROL_PLANE_ADDR` must resolve to a loopback literal (`127.0.0.0/8`, `::1`, or `localhost`), and anything else — including a bare `:port`, which binds every interface — aborts startup with a fatal log instead of exposing the lab surface to the network. Second, the mounted lab route is guarded by a shared secret: a caller must present `X-Drift-Lab-Token` matching `DRIFT_P13_LAB_TOKEN`, compared in constant time, or every lab RPC is refused with `401` and a generic body that leaks nothing about the configured token, the mode, or the confirmed target.

Real-device mode makes that token mandatory: when `DRIFT_P13_LAB_MODE=1`, an empty or absent `DRIFT_P13_LAB_TOKEN` aborts startup. Mock mode may omit the token, because no device is reachable and no observation is real; running an unguarded loopback surface is therefore a mock-only posture, not a production-like one. The console sends the token through `VITE_DRIFT_LAB_TOKEN`. The token authenticates a local caller to the lab route only. It is not a device credential, grants no lease, carries no fencing authority, and never appears in a log, an audit summary, or an error body.

### Process and command safety

Every invocation is an argument array. There is no shell, no `sh -c`, no string interpolation, and no caller-supplied command text at any point. Serials are validated against an anchored pattern before every device operation and are always passed as their own `-s SERIAL` token. Argument tokens reject non-printable bytes and every shell metacharacter as defense in depth even though no builder can emit them. Device-scoped argument arrays must match a builder-produced allow-list (`matchesAllowlist`) or execution is refused.

The child process does not inherit the host environment. Only `HOME`, `TMPDIR`, `ANDROID_ADB_SERVER_PORT`, and `ANDROID_ADB_SERVER_SOCKET` are forwarded. `ANDROID_SERIAL` is deliberately excluded so transport selection stays explicit in argv. Stdout and stderr are bounded per stream and truncation is reported rather than hidden. Screenshot payloads are bounded and PNG-validated; hierarchy payloads are bounded by node count, depth, and byte size.

### Timeouts and cancellation

Every operation carries a context deadline. A killed process gets a bounded wait delay before its pipes are abandoned, so a hung child cannot hold the service. Operator cancellation and deadline expiry are classified distinctly: cancellation yields `operator_cancelled`, while a deadline yields `timeout` followed by `indeterminate`, because the deadline may have expired after the command was already dispatched to the device. An already-canceled context is refused before any process is spawned.

### Observation/evidence model

One capture produces one `LabObservationBundle`: health state, a screenshot referenced by content hash and byte count, a bounded hierarchy summary (`"<n> nodes, depth <d>, <completeness>"`), node count, max depth, a completeness flag, a freshness token, latency, failure class, indeterminate flag, postcondition result, and the sanitized audit events for that capture.

Raw view-hierarchy XML never crosses the contract. Screen bytes never cross the contract; a bounded base64 preview appears only when previews are explicitly enabled and the complete PNG fits the configured cap, and an oversized capture sets `preview_truncated` with an empty preview rather than returning a partial image. Completeness is reported honestly: truncated, partial, and incomplete are distinct from complete.

A bundle is marked verified only when the postcondition holds — the target is healthy and usable, the screenshot has a hash and non-zero bytes, and the hierarchy is complete with a non-zero node count. Absence of an error is never treated as success.

### Failure classification

Failures are classified into three families and never collapsed:

- **Infrastructure** — the adapter, executable, configuration, or host process is at fault. Unclassified failures default here so they fail closed.
- **Observation** — the adapter worked and the device answered, but the observation is unusable: a device attached yet offline or unauthorized, a missing property, a malformed or empty dump, a non-PNG or oversized screenshot. A health report with a nil error is never implicitly healthy; callers must check the failure class.
- **Indeterminate** — the outcome is unknown. A deadline that may have expired after dispatch produces this.

Transport, device-offline, postcondition, cleanup-failed, and operator-cancelled classes carry through the same typed vocabulary into the proto and the UI.

### Indeterminate action policy

There is no blind replay. An idempotency key whose outcome is unknown is never retried automatically. The session readiness stays `indeterminate` until an operator clears the target or a later capture with a **new** idempotency key **verifies its postcondition**. A later determinate failure does not resolve it: a failed capture says nothing about whether the earlier command reached the device. A completed key is deduplicated and returns its recorded outcome rather than re-running the observation.

### Reconnect behavior

When a transport identity changes mid-session, the lab service calls the adapter's `ReattachReadOnly`, which performs **at most one read-only reattach per observed change**: it re-runs `adb devices -l` and nothing else. The service does not re-enumerate on its own, so the single-use cap cannot be bypassed from the application layer. It issues no `connect`, no `reconnect`, and no other mutating transport command. A second reattach for the same previous transport identity fails with `ErrReattachExhausted`; a genuinely different later change permits one more. If the serial reappears in an unusable state the capture fails `transport`; if it does not reappear it fails `device_offline`. The stable session identity is unchanged by a transport change.

### USB and wireless-debugging boundaries

Drift never enables wireless debugging, never runs `adb pair`, never runs `adb connect`, and never opens a device-side port. Discovery is not enablement: enumerating a transport that an operator already established says nothing about Drift authorizing it.

A wireless target requires explicit operator confirmation of the exact `host:port` serial. This is not theoretical for the current lab: a read-only `adb devices -l` on the P13 host enumerated **20 attached transports, all TCP `host:port` wireless transports and zero USB**. In that environment nothing may be inferred, which is precisely why the generic `CONFIRM` literal is rejected whenever more than one candidate exists.

### Port exposure and firewall policy

The Drift service listens on loopback, and that is enforced at startup rather than assumed: a non-loopback `DRIFT_CONTROL_PLANE_ADDR` fails closed. Drift exposes no ADB port, forwards no port, and requests no inbound firewall rule. Port 5555 and any other ADB TCP port on a lab device is operator-established infrastructure outside Drift's control and outside its authorization. Phase 14 port provisioning is explicitly out of scope here.

### Token and lease separation

Helper tokens are not applicable: no helper is adopted, so no helper token exists in this slice. This is unchanged from ADR-0005's rule that a helper token would authenticate helper traffic only and would never substitute for Drift authority. The local lab token described above is the same kind of narrow credential: it authenticates a local caller to the lab route and is never Drift authority.

Control-plane leases and fencing tokens remain authoritative and are untouched by this slice. The lab service holds no lease, issues no fencing token, and grants no mutating authority. Its session confirmation is an observation scope, not ownership of a device.

### Provenance and signing

The adapter runs the operator's official Android Platform-Tools `adb` binary at an explicitly configured absolute path. Drift ships, downloads, installs, and signs nothing on the device or the host. There is no helper artifact, so there is no helper artifact to pin, attribute, sign, or roll back. The observed platform-tools version is recorded in the status and in every observation bundle so evidence is attributable to a specific host toolchain.

### Compatibility matrix

The evaluation matrix and the opt-in real-device procedure live in `tests/compatibility/adb/`. That directory records which measurements the existing unit and integration tests already prove and which require a live device. Live cells remain unfilled pending operator serial confirmation; they are not estimated.

### Sanitization and redaction

Captured process output is redacted before it reaches an error, a log, an audit record, or the contract. Redaction removes credential material, adb key paths (`adbkey`, `adbkey.pub`), and vendor-key locations, then bounds the result and marks truncation. Observed UI text from the hierarchy is sanitized and the capture records that it was. Audit event summaries are bounded redacted prose and never carry raw command output, raw XML, or credentials.

### UI integration

There is no dedicated routed lab page. The lab surfaces are the Control, Devices, and Events pages, which compose the lab components exported from `apps/console/src/pages/lab-adapter.tsx`: Control renders `LabStatusStrip` and `LabObservationFrame`, Devices renders `LabAdapterStatusPanel`, and Events renders the lab audit records alongside every other event.

The default data path is the mock control plane, so the console ships with typed intents and no network dependency. The optional Connect client in `apps/console/src/lib/api/lab-adapter-client.ts` is used only when `VITE_DRIFT_LAB_ADAPTER_URL` is set. When it is, `useControlPlane` routes exactly the four lab intents — `discoverLabDevices`, `confirmLabTarget`, `clearLabTarget`, `captureLabObservation` — through `applyLabIntent` in `lab-control-plane.ts`, which projects the returned `LabStatus` onto `snapshot.labAdapter` via `toLabAdapterView`. Every non-lab intent stays on the mock client, and a failed lab call leaves the previous projection untouched while reporting the failure in the lab strip's live region, so an unreachable adapter is never rendered as a fresh observation. The mock-only `simulateLabCaptureFailure` intent exercises the indeterminate surface without a device and is never routed to the adapter.

The existing Control page design is preserved: the lab slice adds status, confirmation, and evidence surfaces within the established visual system rather than restyling the page. The UI renders sanitized state and requests typed intents; it implements no authorization, no confirmation policy, and no device protocol. Status must not be conveyed by color alone, and indeterminate must be visibly distinct from both success and failure. Mock observations report zero latency and an explicitly unmeasured hierarchy summary, so a fixture is never read as a device measurement.

### Operational limitations

- Multi-device environments require explicit serial confirmation. The lab host had 20 attached transports; capture proceeded only after the operator typed `192.168.1.109:5555`.
- CI skips real-device tests. They are opt-in, gated on `DRIFT_P13_LAB_MODE`, `DRIFT_P13_ADB_PATH`, and `DRIFT_P13_LAB_SERIAL`, and CI sets none of them.
- The wireless fleet attached in the lab must not be auto-targeted. Enumeration is not selection, and no heuristic may promote a candidate.
- Evidence persistence is not part of this slice: `artifact_id` stays empty until the artifact store is wired in, so bundles reference captures by content hash only.
- Session state is in-memory and does not survive a service restart.

### Recommendation and expansion decision

**Adapter recommendation: use native ADB + built-in UIAutomator dump for P13; do not adopt the optional helper.**

**Go/no-go for expanding beyond one lab device: NO-GO.** Expansion stays blocked until all three conditions hold: additional explicit owner authorization; single-target confirmation UX proven in operations against the real multi-transport lab environment; and Sentinel review of the specific expansion.

## Alternatives considered

- **Adopt the ARTEMIS-derived helper (Python / `uiautomator2` / Accessibility Helper APK) now.** Rejected. It adds an installable privileged on-device component, a token lifecycle, a consent flow, a signing owner, and a Python runtime to satisfy capabilities the native path already satisfies. ADR-0005 requires a measured gap first, and observation shows none.
- **Use raw `adb shell` with composed command strings.** Rejected. It reintroduces host-side injection surface and makes the allow-list unenforceable.
- **Auto-select the target when exactly one device looks plausible.** Rejected. With 20 attached wireless transports in the lab, inference is the most likely way to observe the wrong device, and P13 forbids inferring a target.
- **Keep the slice fake-only and defer real devices again.** Rejected. The typed contracts have never met a real transport, and deferring leaves P14 planning on unvalidated assumptions.
- **Persist lab evidence to the artifact store in this slice.** Rejected for scope. Content-hash references are sufficient to prove the observation contract; persistence carries retention and redaction decisions that belong with the artifact work.

## Consequences

- P13 ships with no new runtime dependency, no on-device install, and no new network exposure.
- The console gains lab surfaces that are strictly read-only plus confirmation, so the Control page's existing behavior and design are unaffected.
- Every capture costs an explicit operator confirmation in the current lab. That is deliberate friction and will be felt.
- An indeterminate outcome requires operator action to clear. There is no automatic recovery path, by design.
- Semantic targeting and action postconditions remain unproven against a real device; if they later demand accessibility-event data, the helper question reopens under ADR-0005's gate.
- P14 registration/runtime spool was blocked by this P13 ADR until separate owner authorization; that authorization was granted 2026-09-15 for one-device attended lab work only. This P13 slice still produces adapter evidence, not P14 completion evidence.
- Live one-device measurements for the operator-confirmed serial `192.168.1.109:5555` are recorded in `tests/compatibility/adb/matrix.md` and `tests/compatibility/adb/evidence-2026-09-15.md`. Remaining gaps (USB path, forced timeout, transport-id change, dump file-fallback) are listed explicitly and do not reopen helper adoption.

## Validation

- Unit and integration tests in `internal/edge/adb` cover serial validation, argv construction without shell metacharacters, the getprop allow-list, device-path namespacing, rejection of blind replay and arbitrary shell, redaction of adb keys and vendor keys, bounded redaction, transport-state parsing, transport identity separation, bounded PNG screenshots, single-use read-only reattach, exit-status classification, timeout kill, cancellation as distinct from timeout, refusal of an already-canceled context, and non-inheritance of the host environment.
- Tests in `internal/edge/uiautomator` cover stdout streaming without a temporary file, temporary-file fallback with removal after success, failure, and cancellation, honest reporting of cleanup failure, malformed and empty dumps, node/depth/payload bounds, truncation marking, serial validation before any command, allow-listed commands only, and text sanitization.
- Tests in `internal/edge/lab` cover mock-mode default with no device work, discovery that confirms and registers nothing, operator attribution, capture blocked before confirmation, rejection of the generic literal with several candidates, rejection of a never-enumerated serial and an unusable transport, stable identity distinct from transport identity, sanitized verified bundles, bounded preview and truncation, idempotency-key deduplication, timeout as indeterminate and never replayed, operator cancellation, postcondition failure on an incomplete hierarchy, read-only transport-change reconciliation, target clearing, bounded redacted event summaries, and the dual-gate lab-mode requirement.
- Tests in `internal/edge/lab` additionally cover the required confirmation reason, the single-use `ReattachReadOnly` call on the transport-change path, and an indeterminate readiness that survives a later determinate failure and is resolved only by a verified capture or an operator clear.
- Tests in `internal/service` cover loopback address validation — including rejection of a bare `:port`, `0.0.0.0`, and a non-loopback hostname — and the constant-time lab token guard for missing, wrong, and correct tokens.
- `buf lint` and `buf build` validate the `LabAdapterService` contract; console tests cover the typed client, the lab token header, the mock/lab intent routing split, the refusal-versus-unreachable distinction, and the mock indeterminate simulation.
- An opt-in real-device test in `internal/edge/lab/real_device_test.go` exercises the read-only path against a confirmed lab serial. It skips unless `DRIFT_P13_LAB_MODE=1`, `DRIFT_P13_ADB_PATH`, and `DRIFT_P13_LAB_SERIAL` are all set, and it fails closed when the serial is not present in discovery.
- Real-device acceptance requires filling `tests/compatibility/adb/matrix.md` with measured results from a confirmed serial, plus Sentinel and owner review, before any expansion is reconsidered.
