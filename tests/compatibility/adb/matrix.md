# Phase 13 ADB/UIAutomator evaluation matrix

Template and running record for the one-lab-device vertical slice
([ADR-0004](../../../docs/adr/0004-real-device-adapter.md)).

**Status:** live one-device evidence recorded 2026-09-15 against operator-named
serial `192.168.1.109:5555` (SM-G9750, Android 12 / API 31, TCP wireless).
Evidence log: [`evidence-2026-09-15.md`](./evidence-2026-09-15.md).
Opt-in test: `TestRealDeviceReadOnlyObservation` **PASS**.
**Host environment:** 20 attached TCP transports, zero USB; the capture named its own
target and never inferred one.

## How to read this

| Column | Meaning |
| --- | --- |
| Measurement | The property being evaluated. |
| Proven by existing tests | What the current unit/integration suite already establishes, with the test names. Deterministic fakes, no device. |
| Requires live device | Whether a real attached target is needed to close the row. |
| Live result | Measured outcome. Empty until measured. |
| Evidence | Where the supporting output lives. |

Rules: never write an estimate into **Live result**. Never mark a row measured without the
command output behind it. A row may be simultaneously well covered by fakes and unproven
against real hardware — that is the normal state of this table today.

## 1. Transport and discovery

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| `adb devices -l` parsing across every transport state | Yes — `TestEnumerateParsesEveryTransportState`, `TestEnumerateHandlesAnEmptyDeviceList` | No for parsing; yes for real-world field ordering | Live enumerate returned 20 TCP `device` rows including the named serial with `transport_id` | `evidence-2026-09-15.md`; test log |
| Unsafe serials in output are rejected | Yes — `TestEnumerateRejectsUnsafeSerialsInOutput` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| Unusable transports classified (offline / unauthorized / no permissions) | Yes — `TestValidateDeviceClassifiesUnusableTransports` | Yes, to confirm real devices report the states we classify | not exercised this run (all observed transports were `device`) | — |
| Transport identity is separate from device identity | Yes — `TestTransportIdentityIsSeparateFromDeviceIdentity`, `TestTransportIdentityReportsAMissingIdentifier`, `TestCaptureObservationBindsAStableIdentityThatIsNotTheTransportIdentity` | Yes, to observe a real transport-id change | Stable lab identity ≠ transport id on capture; live transport-id *change* not observed | `TestRealDeviceReadOnlyObservation` |
| USB vs TCP connection-type derivation | Partial — derived from serial shape in `adb.ConnectionUSB` / `ConnectionTCP` | Yes; the current lab has **zero USB transports**, so the USB path is unexercised against hardware | TCP derived and reported for the named serial; USB still unexercised | test log `connection=tcp` |
| Discovery creates/registers nothing | Yes — `TestDiscoverEnumeratesWithoutConfirmingOrRegisteringAnything` | No | n/a (fake-provable); live enumeration logged 20 candidates with none registered | `TestRealDeviceReadOnlyObservation` |
| Unavailable adapter reports honestly, invents no candidates | Yes — `TestCaptureObservationReportsAnUnavailableAdapterWithoutInventingCandidates` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |

## 2. Target selection (device-scoped capture)

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Generic `CONFIRM` rejected with several candidates | No — the confirm lifecycle is retired; a capture now names its own device and no confirmation text exists | No | n/a | `docs/adr/0004-real-device-adapter.md` |
| A capture that names no device is refused without touching the adapter | Yes — `TestCaptureObservationRefusesAnUnnamedTargetWithoutTouchingTheAdapter` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| A serial that is not attached is refused, never inferred | Yes — `TestCaptureObservationRefusesASerialThatIsNotAttached` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| An ambiguous serial is refused rather than resolved by order | Yes — `TestCaptureObservationRefusesAnAmbiguousTarget` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Unusable transport state refused before any observation | Yes — `TestCaptureObservationRefusesAnUnusableTarget` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Naming one device among 20 attached transports | No | Yes — an operations question, not a unit-test question | Operator named the exact serial among 20; the capture resolved it and refused nothing else | operator transcript + PASS |
| Capture is authorized per call from the explicit target | Yes — `TestCaptureObservationAuthorizesEveryCallByOperator` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |

## 3. Health and device properties

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| `adb get-state` read and classified | Yes — `TestHealthReportsAnObservableButUnusableDevice` | Yes | `device` | test bundle `health=device`; host probe |
| Allow-listed `getprop` keys collected | Yes — `TestHealthCollectsAllowlistedProperties`, `TestGetPropArgvHonorsTypedAllowlist` | Yes, to confirm real devices populate them | release/sdk/model/manufacturer populated on target | `evidence-2026-09-15.md` |
| Android version / API level observed | Fake-provable only | Yes | Android 12 / API 31 | `evidence-2026-09-15.md` |
| Platform-tools version observed | Yes — `TestParseVersionFallsBackToTheLegacyLine` | Yes, for the actual installed toolchain | `37.0.1-15733141` | test log |
| Nil error is not implicitly healthy | Yes — `HealthReport.Healthy()` + `TestHealthSurfacesInfrastructureFailure` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| API-level range supported by `uiautomator dump --compressed` | No | Yes, per device/API level | API 31: dump succeeded (stdout path) | evidence + PASS |

## 4. Screenshot capture

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| `exec-out screencap -p` returns bounded PNG | Yes — `TestScreenshotHashesBoundedPNGBytes` | Yes | PNG 1080×2280; 3 543 647 bytes; within bound | evidence; test hash |
| Oversized and non-PNG payloads rejected | Yes — `TestScreenshotRejectsOversizedAndNonPNGPayloads` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| Content hash is stable and is the only byte reference exposed | Yes — `TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode` | Yes, against real image bytes | `sha256:871cf07f647a86f66bb737a4756425a905fb160e06c2416d34a0fe6a405137d1`; no raw bytes in contract log | test log |
| Real screenshot byte size vs the configured bound | No | Yes | 3 543 647 bytes; `preview_truncated=false` | test log |
| Bounded preview emitted only when enabled; oversized capture truncates rather than returning a partial image | Yes — `TestCaptureObservationEmitsABoundedPreviewOnlyWhenEnabled`, `TestCaptureObservationTruncatesRatherThanReturningAPartialImage` | Yes, to see whether real PNGs fit the cap | Real capture fit; preview not truncated | test log |

## 5. View-hierarchy capture

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| stdout streaming path avoids a device-side temporary file | Yes — `TestCaptureStreamsTheHierarchyWithoutATemporaryFile` | Yes — whether `exec-out ... /dev/tty` works on the real target is device/API dependent | Succeeded on API 31 target (2 736-byte stdout dump) | host probe in evidence |
| Temporary-file fallback used and always removed | Yes — `TestCaptureFallsBackToATemporaryFileAndAlwaysRemovesIt` | Yes | not exercised (stdout path succeeded) | — |
| Temporary file removed after a failed dump and after cancellation | Yes — `TestCaptureRemovesTheTemporaryFileAfterAFailedDump`, `TestCaptureRemovesTheTemporaryFileAfterCancellation` | Yes | not exercised this run | — |
| Cleanup failure reported, not hidden | Yes — `TestCaptureReportsCleanupFailureWithoutClaimingCleanTeardown` | Yes | not exercised this run | — |
| Malformed and empty dumps rejected distinctly | Yes — `TestCaptureRejectsMalformedHierarchies`, `TestCaptureReportsAMissingHierarchyRatherThanAnEmptyOne` | No | n/a (fake-provable) | `internal/edge/uiautomator/adapter_test.go` |
| Node, depth, and payload bounds enforced | Yes — `TestCaptureTruncatesAtTheNodeBound`, `TestCaptureTruncatesAtTheDepthBound`, `TestCaptureTruncatesAtThePayloadBound`, `TestCaptureScalesToALargeHierarchyWithinItsBounds` | Yes, to learn whether real screens approach the bounds | Live tree well under bounds (7 nodes / depth 2) | test log |
| Real hierarchy node count and depth on the target's home screen | No | Yes | 7 nodes, max depth 2, complete | test log |
| Truncated output marked partial | Yes — `TestCaptureMarksRunnerTruncatedOutputAsPartial` | No | n/a (fake-provable) | `internal/edge/uiautomator/adapter_test.go` |
| Observed text sanitized | Yes — `TestCaptureMapsNodesAndSanitizesObservedText` | Yes, against real on-screen text | Bundle exposes summary/counts only; no raw XML in contract | test log |

## 6. Command and process safety

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| argv arrays only; no shell metacharacters in any built command | Yes — `TestBuiltArgvContainsNoShellMetacharacters`, `TestValidateArgvRejectsInjectedTokens` | No | n/a (fake-provable) | `internal/edge/adb/command_test.go` |
| Serial validated before every operation and passed as its own token | Yes — `TestEveryDeviceOperationValidatesTheSerialFirst`, `TestRunAllowlistedPlacesTheSerialInItsOwnToken`, `TestValidateSerialRejectsUnsafeValues`, `TestValidateSerialAcceptsRealShapes` | No | n/a (fake-provable); live path used validated serial | PASS |
| Executable must be an explicit absolute path | Yes — `TestValidateExecutableRequiresExplicitAbsolutePath`, `TestNewAdapterRequiresExplicitConfiguration` | No | n/a (fake-provable) | `internal/edge/adb/*_test.go` |
| Arbitrary shell and blind replay refused by the allow-list | Yes — `TestAllowlistRejectsBlindReplayAndArbitraryShell`, `TestRunAllowlistedRefusesUnapprovedArgv`, `TestCaptureOnlyIssuesAllowlistedCommands` | No | n/a (fake-provable) | `internal/edge/adb/*_test.go`, `internal/edge/uiautomator/adapter_test.go` |
| Device-side writes confined to the adapter namespace | Yes — `TestDevicePathStaysInsideTheAdapterNamespace` | Yes, to confirm `/sdcard` is writable on the target | `/sdcard/Android/data` touch+rm succeeded (`sdcard_ok`) | evidence |
| Child process does not inherit the host environment | Yes — `TestProcessRunnerDoesNotInheritTheHostEnvironment` | No | n/a (fake-provable) | `internal/edge/adb/process_test.go` |
| stdout/stderr bounded; pipes keep draining | Yes — `TestProcessRunnerBoundsOutput`, `TestBoundedBufferReportsFullWritesSoPipesKeepDraining` | No | n/a (fake-provable) | `internal/edge/adb/process_test.go` |

## 7. Timeouts, cancellation, and indeterminate outcomes

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Timeout kills the invocation | Yes — `TestOperationTimeoutKillsTheInvocation`, `TestProcessRunnerKillsChildOnTimeout` | Yes, against a real slow/hung transport | not exercised this run | — |
| Operator cancellation distinct from timeout | Yes — `TestOperatorCancellationIsDistinctFromTimeout`, `TestProcessRunnerKillsChildOnCancellation`, `TestCaptureObservationClassifiesOperatorCancellation` | No | n/a (fake-provable) | `internal/edge/adb/*_test.go`, `internal/edge/lab/service_test.go` |
| Already-canceled context refused before spawn | Yes — `TestProcessRunnerRefusesAnAlreadyCanceledContext` | No | n/a (fake-provable) | `internal/edge/adb/process_test.go` |
| Timeout becomes indeterminate and is never replayed | Yes — `TestCaptureObservationTimeoutIsIndeterminateAndIsNeverReplayed` | Yes, to confirm real timeouts land in this path | not exercised this run | — |
| Completed idempotency key deduplicated | Yes — `TestCaptureObservationDeduplicatesACompletedIdempotencyKey` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Indeterminate resolved only by a new key that verifies its postcondition | Yes — `TestIndeterminateReadinessSurvivesALaterDeterminateFailure` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |

## 8. Reconnect and transport change

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| At most one read-only reattach per observed change | Yes — `TestReattachIsReadOnlyAndUsedAtMostOncePerChange`, `TestReattachRequiresAPreviousTransportIdentity` | Yes, against a real wireless reconnect | not exercised this run | — |
| Transport change reconciled read-only; no connect/reconnect issued | Yes — `TestCaptureObservationReconcilesATransportChangeReadOnly` | Yes | not exercised this run | — |
| Stable session identity survives a transport change | Yes — `TestCaptureObservationReconcilesATransportChangeReadOnly`, `TestCaptureObservationBindsAStableIdentityThatIsNotTheTransportIdentity` | Yes | Stable ≠ transport on live capture; change survival not exercised | PASS |
| Wireless transport stability over a session | No | Yes | Single capture session completed without transport loss (~12s) | PASS |

## 9. Failure classification and postconditions

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Exit statuses classified into infrastructure / observation / indeterminate | Yes — `TestExitStatusClassification`, `TestHealthSurfacesInfrastructureFailure` | Yes, against real adb exit codes | Successful path only this run; no failure-class on verified bundle | PASS |
| Unclassified failures default closed to infrastructure | Yes — `adb.FailureClassOf` + `TestExitStatusClassification` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| Postcondition fails for an incomplete hierarchy | Yes — `TestCaptureObservationFailsThePostconditionForAnIncompleteHierarchy` | Yes | not exercised on hardware (fake-proven) | — |
| Verified bundle requires health + hash + complete tree | Yes — `TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode` | Yes | `postcondition_verified=true` with health, hash, complete hierarchy | test log |

## 10. Redaction, evidence, and audit

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| adb key paths and vendor keys redacted from output | Yes — `TestRedactOutputRemovesSensitiveMaterial` | Yes, against real stderr | not specifically observed on stderr this run | — |
| Redacted detail is bounded | Yes — `TestRedactOutputIsBounded` | No | n/a (fake-provable) | `internal/edge/adb/command_test.go` |
| Event summaries bounded and redacted | Yes — `TestEventSummariesAreBoundedAndRedacted` | Yes, against real observations | Events logged with masked serial; no raw XML/PNG | test log |
| No raw XML or screen bytes cross the contract | Yes — proto shape + `TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode` | Yes | Hash + counts only in bundle log | test log |

## 11. Mode gating and composition

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Lab mode requires both the opt-in and the executable path | Yes — `TestLabModeRequiresBothAnOptInAndAnExecutablePath`, `TestLabModeRequestedAcceptsAnExplicitOptIn` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Default is mock mode with no device work | Yes — `TestNewServiceDefaultsToMockModeWithoutAnyDeviceWork` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Operator attribution required for discovery and capture | Yes — `TestCaptureObservationRequiresAnAttributableOperator`, `TestCaptureObservationAuthorizesEveryCallByOperator` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Capture issues only read-only adapter calls | Yes — `TestCaptureObservationIssuesOnlyReadOnlyAdapterCalls` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| End-to-end read-only slice against an explicitly named serial | No | Yes | **PASS** against `192.168.1.109:5555` | `internal/edge/lab/real_device_test.go`; evidence |

## 12. Latency

Measured 2026-09-15 on the confirmed serial. Host `/usr/bin/time -p` figures are wall-clock for the named adb invocation; `LatencyMs` is the adapter-reported bundle total.

| Measurement | Requires live device | Live result | Evidence |
| --- | --- | --- | --- |
| `adb get-state` round trip | Yes | ≈ 0.01s real (n=3) | evidence |
| Allow-listed `getprop` round trip | Yes | ≈ 0.04–0.06s real for `ro.build.version.sdk` (n=3) | evidence |
| `screencap -p` capture | Yes | 9.80s real; 3 543 647-byte PNG | evidence |
| `uiautomator dump --compressed` (stdout path) | Yes | 2.47s real; 2 736-byte XML | evidence |
| `uiautomator dump --compressed` (file fallback + cat + rm) | Yes | not exercised (stdout succeeded) | — |
| Full observation bundle end to end | Yes | `LatencyMs=12061` | test log |

## 13. Optional helper comparison

Not adopted for P13. ADR-0004 records the native path as sufficient for the observation
slice, with no measured capability gap for enumerate, health, screenshot, or hierarchy.
Live evidence on API 31 reinforced that conclusion: native dump and screencap completed.

| Measurement | Status |
| --- | --- |
| Helper installed | No — and not authorized for this slice |
| Helper capability gain over native | Not measured; no gap identified for observation |
| Helper token / consent / signing / rollback evaluation | Deferred to ADR-0005's gate |
| Trigger to reopen | Semantic-targeting or postcondition evidence showing a gap the native path cannot close |

## Remaining evidence gaps (honest)

1. Unusable transport states (offline / unauthorized / no permissions) not seen live.
2. No USB transport available — USB connection-type path still unexercised on hardware.
3. Forced timeout / hung-transport indeterminate path not exercised on hardware.
4. Live transport-id change → single read-only reattach not exercised.
5. UIAutomator temporary-file fallback not used (stdout path worked).
6. Evidence persistence to the artifact store remains out of scope (`artifact_id` empty).
