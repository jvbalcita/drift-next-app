# Phase 13 ADB/UIAutomator evaluation matrix

Template and running record for the one-lab-device vertical slice
([ADR-0004](../../../docs/adr/0004-real-device-adapter.md)).

**Status:** scaffolding complete; no live-device measurements recorded.
**Host environment observed 2026-09-15:** a read-only `adb devices -l` enumerated
**20 attached transports, all TCP wireless (`host:port`), zero USB**. Phase 13 forbids
inferring a target, so every live cell below is blocked on an operator typing a serial.

## How to read this

| Column | Meaning |
| --- | --- |
| Measurement | The property being evaluated. |
| Proven by existing tests | What the current unit/integration suite already establishes, with the test names. Deterministic fakes, no device. |
| Requires live device | Whether a real confirmed target is needed to close the row. |
| Live result | Measured outcome. Empty until measured. |
| Evidence | Where the supporting output lives. |

Rules: never write an estimate into **Live result**. Never mark a row measured without the
command output behind it. A row may be simultaneously well covered by fakes and unproven
against real hardware — that is the normal state of this table today.

## 1. Transport and discovery

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| `adb devices -l` parsing across every transport state | Yes — `TestEnumerateParsesEveryTransportState`, `TestEnumerateHandlesAnEmptyDeviceList` | No for parsing; yes for real-world field ordering | — | — |
| Unsafe serials in output are rejected | Yes — `TestEnumerateRejectsUnsafeSerialsInOutput` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| Unusable transports classified (offline / unauthorized / no permissions) | Yes — `TestValidateDeviceClassifiesUnusableTransports` | Yes, to confirm real devices report the states we classify | pending operator serial confirmation | — |
| Transport identity is separate from device identity | Yes — `TestTransportIdentityIsSeparateFromDeviceIdentity`, `TestTransportIdentityReportsAMissingIdentifier`, `TestConfirmTargetBindsASessionIdentityThatIsNotTheTransportIdentity` | Yes, to observe a real transport-id change | pending operator serial confirmation | — |
| USB vs TCP connection-type derivation | Partial — derived from serial shape in `adb.ConnectionUSB` / `ConnectionTCP` | Yes; the current lab has **zero USB transports**, so the USB path is unexercised against hardware | pending operator serial confirmation (no USB device available) | — |
| Discovery creates/registers nothing | Yes — `TestDiscoverEnumeratesWithoutConfirmingOrRegisteringAnything` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Unavailable adapter reports honestly, invents no candidates | Yes — `TestDiscoverReportsAnUnavailableAdapterWithoutInventingCandidates` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |

## 2. Target confirmation

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Generic `CONFIRM` rejected with several candidates | Yes — `TestConfirmTargetRefusesGenericConfirmationWhenSeveralCandidatesAreAttached` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Generic `CONFIRM` accepted only for a single candidate | Yes — `TestConfirmTargetAcceptsTheGenericLiteralOnlyForASingleCandidate` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Never-enumerated serial rejected | Yes — `TestConfirmTargetRejectsASerialThatWasNeverEnumerated` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Unusable transport state rejected at confirmation | Yes — `TestConfirmTargetRejectsAnUnusableTransportState` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Confirmation UX usable with 20 attached transports | No | Yes — this is an operations question, not a unit-test question | pending operator serial confirmation | — |
| Capture blocked before confirmation | Yes — `TestCaptureObservationIsBlockedBeforeConfirmation`, `TestCaptureObservationRequiresTheConfirmedSerial` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |

## 3. Health and device properties

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| `adb get-state` read and classified | Yes — `TestHealthReportsAnObservableButUnusableDevice` | Yes | pending operator serial confirmation | — |
| Allow-listed `getprop` keys collected | Yes — `TestHealthCollectsAllowlistedProperties`, `TestGetPropArgvHonorsTypedAllowlist` | Yes, to confirm real devices populate them | pending operator serial confirmation | — |
| Android version / API level observed | Fake-provable only | Yes | pending operator serial confirmation | — |
| Platform-tools version observed | Yes — `TestParseVersionFallsBackToTheLegacyLine` | Yes, for the actual installed toolchain | pending operator serial confirmation | — |
| Nil error is not implicitly healthy | Yes — `HealthReport.Healthy()` + `TestHealthSurfacesInfrastructureFailure` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| API-level range supported by `uiautomator dump --compressed` | No | Yes, per device/API level | pending operator serial confirmation | — |

## 4. Screenshot capture

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| `exec-out screencap -p` returns bounded PNG | Yes — `TestScreenshotHashesBoundedPNGBytes` | Yes | pending operator serial confirmation | — |
| Oversized and non-PNG payloads rejected | Yes — `TestScreenshotRejectsOversizedAndNonPNGPayloads` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| Content hash is stable and is the only byte reference exposed | Yes — `TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode` | Yes, against real image bytes | pending operator serial confirmation | — |
| Real screenshot byte size vs the configured bound | No | Yes | pending operator serial confirmation | — |
| Bounded preview emitted only when enabled; oversized capture truncates rather than returning a partial image | Yes — `TestCaptureObservationEmitsABoundedPreviewOnlyWhenEnabled`, `TestCaptureObservationTruncatesRatherThanReturningAPartialImage` | Yes, to see whether real PNGs fit the cap | pending operator serial confirmation | — |

## 5. View-hierarchy capture

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| stdout streaming path avoids a device-side temporary file | Yes — `TestCaptureStreamsTheHierarchyWithoutATemporaryFile` | Yes — whether `exec-out ... /dev/tty` works on the real target is device/API dependent | pending operator serial confirmation | — |
| Temporary-file fallback used and always removed | Yes — `TestCaptureFallsBackToATemporaryFileAndAlwaysRemovesIt` | Yes | pending operator serial confirmation | — |
| Temporary file removed after a failed dump and after cancellation | Yes — `TestCaptureRemovesTheTemporaryFileAfterAFailedDump`, `TestCaptureRemovesTheTemporaryFileAfterCancellation` | Yes | pending operator serial confirmation | — |
| Cleanup failure reported, not hidden | Yes — `TestCaptureReportsCleanupFailureWithoutClaimingCleanTeardown` | Yes | pending operator serial confirmation | — |
| Malformed and empty dumps rejected distinctly | Yes — `TestCaptureRejectsMalformedHierarchies`, `TestCaptureReportsAMissingHierarchyRatherThanAnEmptyOne` | No | n/a (fake-provable) | `internal/edge/uiautomator/adapter_test.go` |
| Node, depth, and payload bounds enforced | Yes — `TestCaptureTruncatesAtTheNodeBound`, `TestCaptureTruncatesAtTheDepthBound`, `TestCaptureTruncatesAtThePayloadBound`, `TestCaptureScalesToALargeHierarchyWithinItsBounds` | Yes, to learn whether real screens approach the bounds | pending operator serial confirmation | — |
| Real hierarchy node count and depth on the target's home screen | No | Yes | pending operator serial confirmation | — |
| Truncated output marked partial | Yes — `TestCaptureMarksRunnerTruncatedOutputAsPartial` | No | n/a (fake-provable) | `internal/edge/uiautomator/adapter_test.go` |
| Observed text sanitized | Yes — `TestCaptureMapsNodesAndSanitizesObservedText` | Yes, against real on-screen text | pending operator serial confirmation | — |

## 6. Command and process safety

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| argv arrays only; no shell metacharacters in any built command | Yes — `TestBuiltArgvContainsNoShellMetacharacters`, `TestValidateArgvRejectsInjectedTokens` | No | n/a (fake-provable) | `internal/edge/adb/command_test.go` |
| Serial validated before every operation and passed as its own token | Yes — `TestEveryDeviceOperationValidatesTheSerialFirst`, `TestRunAllowlistedPlacesTheSerialInItsOwnToken`, `TestValidateSerialRejectsUnsafeValues`, `TestValidateSerialAcceptsRealShapes` | No | n/a (fake-provable) | `internal/edge/adb/*_test.go` |
| Executable must be an explicit absolute path | Yes — `TestValidateExecutableRequiresExplicitAbsolutePath`, `TestNewAdapterRequiresExplicitConfiguration` | No | n/a (fake-provable) | `internal/edge/adb/*_test.go` |
| Arbitrary shell and blind replay refused by the allow-list | Yes — `TestAllowlistRejectsBlindReplayAndArbitraryShell`, `TestRunAllowlistedRefusesUnapprovedArgv`, `TestCaptureOnlyIssuesAllowlistedCommands` | No | n/a (fake-provable) | `internal/edge/adb/*_test.go`, `internal/edge/uiautomator/adapter_test.go` |
| Device-side writes confined to the adapter namespace | Yes — `TestDevicePathStaysInsideTheAdapterNamespace` | Yes, to confirm `/sdcard` is writable on the target | pending operator serial confirmation | — |
| Child process does not inherit the host environment | Yes — `TestProcessRunnerDoesNotInheritTheHostEnvironment` | No | n/a (fake-provable) | `internal/edge/adb/process_test.go` |
| stdout/stderr bounded; pipes keep draining | Yes — `TestProcessRunnerBoundsOutput`, `TestBoundedBufferReportsFullWritesSoPipesKeepDraining` | No | n/a (fake-provable) | `internal/edge/adb/process_test.go` |

## 7. Timeouts, cancellation, and indeterminate outcomes

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Timeout kills the invocation | Yes — `TestOperationTimeoutKillsTheInvocation`, `TestProcessRunnerKillsChildOnTimeout` | Yes, against a real slow/hung transport | pending operator serial confirmation | — |
| Operator cancellation distinct from timeout | Yes — `TestOperatorCancellationIsDistinctFromTimeout`, `TestProcessRunnerKillsChildOnCancellation`, `TestCaptureObservationClassifiesOperatorCancellation` | No | n/a (fake-provable) | `internal/edge/adb/*_test.go`, `internal/edge/lab/service_test.go` |
| Already-canceled context refused before spawn | Yes — `TestProcessRunnerRefusesAnAlreadyCanceledContext` | No | n/a (fake-provable) | `internal/edge/adb/process_test.go` |
| Timeout becomes indeterminate and is never replayed | Yes — `TestCaptureObservationTimeoutIsIndeterminateAndIsNeverReplayed` | Yes, to confirm real timeouts land in this path | pending operator serial confirmation | — |
| Completed idempotency key deduplicated | Yes — `TestCaptureObservationDeduplicatesACompletedIdempotencyKey` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Indeterminate cleared only by operator or a new key | Yes — `TestClearTargetReleasesTheSessionAndResolvesIndeterminateReadiness` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |

## 8. Reconnect and transport change

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| At most one read-only reattach per observed change | Yes — `TestReattachIsReadOnlyAndUsedAtMostOncePerChange`, `TestReattachRequiresAPreviousTransportIdentity` | Yes, against a real wireless reconnect | pending operator serial confirmation | — |
| Transport change reconciled read-only; no connect/reconnect issued | Yes — `TestCaptureObservationReconcilesATransportChangeReadOnly` | Yes | pending operator serial confirmation | — |
| Stable session identity survives a transport change | Yes — same test + `TestConfirmTargetBindsASessionIdentityThatIsNotTheTransportIdentity` | Yes | pending operator serial confirmation | — |
| Wireless transport stability over a session | No | Yes | pending operator serial confirmation | — |

## 9. Failure classification and postconditions

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Exit statuses classified into infrastructure / observation / indeterminate | Yes — `TestExitStatusClassification`, `TestHealthSurfacesInfrastructureFailure` | Yes, against real adb exit codes | pending operator serial confirmation | — |
| Unclassified failures default closed to infrastructure | Yes — `adb.FailureClassOf` + `TestExitStatusClassification` | No | n/a (fake-provable) | `internal/edge/adb/adapter_test.go` |
| Postcondition fails for an incomplete hierarchy | Yes — `TestCaptureObservationFailsThePostconditionForAnIncompleteHierarchy` | Yes | pending operator serial confirmation | — |
| Verified bundle requires health + hash + complete tree | Yes — `TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode` | Yes | pending operator serial confirmation | — |

## 10. Redaction, evidence, and audit

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| adb key paths and vendor keys redacted from output | Yes — `TestRedactOutputRemovesSensitiveMaterial` | Yes, against real stderr | pending operator serial confirmation | — |
| Redacted detail is bounded | Yes — `TestRedactOutputIsBounded` | No | n/a (fake-provable) | `internal/edge/adb/command_test.go` |
| Event summaries bounded and redacted | Yes — `TestEventSummariesAreBoundedAndRedacted` | Yes, against real observations | pending operator serial confirmation | — |
| No raw XML or screen bytes cross the contract | Yes — proto shape + `TestCaptureObservationReturnsASanitizedVerifiedBundleInMockMode` | Yes | pending operator serial confirmation | — |

## 11. Mode gating and composition

| Measurement | Proven by existing tests | Requires live device | Live result | Evidence |
| --- | --- | --- | --- | --- |
| Lab mode requires both the opt-in and the executable path | Yes — `TestLabModeRequiresBothAnOptInAndAnExecutablePath`, `TestLabModeRequestedAcceptsAnExplicitOptIn` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Default is mock mode with no device work | Yes — `TestNewServiceDefaultsToMockModeWithoutAnyDeviceWork` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| Operator attribution required for discovery and capture | Yes — `TestDiscoverAndCaptureRequireAnAttributableOperator` | No | n/a (fake-provable) | `internal/edge/lab/service_test.go` |
| End-to-end read-only slice against a confirmed serial | No | Yes | pending operator serial confirmation | `internal/edge/lab/real_device_test.go` (opt-in, currently skipped) |

## 12. Latency

**No latency numbers are recorded and none may be estimated.** The adapter measures and
reports latency per operation (`HealthReport.Latency`, `ScreenshotResult.Latency`,
`HierarchyCapture.Latency`, `ObservationBundle.LatencyMs`), but every value observed so far
comes from deterministic fakes and is meaningless as a performance figure.

| Measurement | Requires live device | Live result | Evidence |
| --- | --- | --- | --- |
| `adb get-state` round trip | Yes | pending operator serial confirmation | — |
| Allow-listed `getprop` round trip | Yes | pending operator serial confirmation | — |
| `screencap -p` capture | Yes | pending operator serial confirmation | — |
| `uiautomator dump --compressed` (stdout path) | Yes | pending operator serial confirmation | — |
| `uiautomator dump --compressed` (file fallback + cat + rm) | Yes | pending operator serial confirmation | — |
| Full observation bundle end to end | Yes | pending operator serial confirmation | — |

## 13. Optional helper comparison

Not adopted for P13. ADR-0004 records the native path as sufficient for the observation
slice, with no measured capability gap for enumerate, health, screenshot, or hierarchy.

| Measurement | Status |
| --- | --- |
| Helper installed | No — and not authorized for this slice |
| Helper capability gain over native | Not measured; no gap identified for observation |
| Helper token / consent / signing / rollback evaluation | Deferred to ADR-0005's gate |
| Trigger to reopen | Semantic-targeting or postcondition evidence showing a gap the native path cannot close |

## Open evidence gaps

1. Every live cell above, blocked on an operator confirming a serial among 20 attached transports.
2. No USB transport is available in the current lab, so the USB connection-type path is unexercised against hardware.
3. Real-device latency is entirely unmeasured.
4. Wireless transport stability across a session is unobserved.
5. API-level coverage for `uiautomator dump --compressed` is a single unconfirmed target at best.
6. Evidence persistence to the artifact store is out of scope for this slice, so `artifact_id` stays empty.
