# Discovery Simplification Implementation Plan

> **For Hermes:** Use subagent-driven-development to implement this plan phase-by-phase. Do not start a phase before the previous phase's exit criteria are met and reviewed.

**Goal:** Replace the two parallel discovery/registration stacks with one simple flow — **launch → auto-scan the default Network Profile → devices appear** — by deleting pending candidates, registration, and provisioning.

**Architecture:** Network Profiles stay and remain the saved scan policy (they exist in Plufina and old Drift, and are the mechanism for auto-scan). What goes is everything between "scan found a device" and "device is visible": the candidate queue, its approval step, the registration service, the provisioning prerequisites, and the entire second lab discovery stack. Discovery becomes an observation that upserts device + endpoint by serial; it is no longer a lifecycle requiring approval.

**Tech Stack:** Go, Connect/protobuf, SQLite (WAL, forward-only migrations), React/TypeScript console, Tauri shell.

**Owner decisions this plan implements (2026-09-16):**
- Keep Network Profiles; they scan saved ranges and auto-scan when set as default.
- Remove pending candidates.
- Remove registration.
- Remove provisioning.
- Later (not this plan): enumerate any ADB port / OTG, not just 5555.

---

## 1. Current context

### Two stacks exist for one outcome

**Stack A — `DiscoveryService`** (5 RPCs): `CreateNetworkProfile` (lands `draft`) → `UpdateNetworkProfile` (must set `active`, or scan is refused) → `StartScan` → `ScanRun` (`requested→running→completed`) → `ScanCandidate` (`discovered→pending_approval`) → `DecideScanCandidate` → `RegisterScanCandidate` → real `devices` + `device_endpoints` rows.

**Stack B — `LabAdapterService` (6 RPCs) + `LabRegistrationService` (4 RPCs)**: `DiscoverLabDevices` → `ConfirmLabTarget` → `VerifyLabProvisioning` (5 probe booleans + `operatorAuthorized`) → `ApproveLabProvisioning` → `RegisterLabDevice` (in-memory register, then durable register, with `RevertRegister`/`ReplaceRegistered` compensation).

Both end at the same place: one managed device. Total surface: **15 RPCs, 3 enums, 2 state machines, 2 approval models.**

### Evidence of the cost

- `discovery/service.go:57` and `discovery/lab_scanner.go:66` both hard-reject a profile that is not `active`; `CreateNetworkProfile` defaults to `draft`. Creating a usable profile therefore always takes two calls.
- `network_profiles` has 13 columns and `CHECK (state = 'active' OR is_default = 0)`; old Drift's equivalent has 7 columns and no state at all.
- `discovery/lab_scanner.go:96-101` writes `port = 1` as a sentinel for USB candidates purely to satisfy `ObservedCandidate.Valid()`.
- `edge/registration/service.go` holds `verified`/`approvals`/`registered` maps in memory (18 references) and reconciles them against SQLite by hand.
- `cmd/control-plane/main.go:117` sets `MaxRegisteredDevices: 0`, which per `registration/service.go:258` means **unlimited** — the one-device gate is silently off.
- `db/migrations/0019_lab_multi_device_registrations.sql` already exists to lift the cap further. That entire line of work disappears with registration.

### The reference behaviour

Old Drift (`src/registry/routes.ts:134`) models a scan result as:

```ts
type NetworkScanCandidate = { host, serial, model, status: "device"|"unauthorized"|"offline", registered: boolean }
```

`sendSuccess(res, { profile, candidates: await scanProfile(profile) })` — **one call in, device list out.** `registered` is a flag, not a queue. That is the target shape.

### Root cause

P6 built discovery against fake devices. P13/P14 needed a real device and built a second stack beside `discovery.Scanner` instead of implementing that seam — which `AuthorizedLabScanner` already does. A gate that was correct for P13's authorization scope became permanent architecture.

### Safety argument for this change

The approval ceremony was guarding a boundary it does not actually protect. What protects a device is the **P7 safety kernel** — lease, fencing token, policy decision, control session. A device row that exists confers no action authority: without a lease and a control session, nothing can touch it. Removing registration therefore does not weaken action safety, while removing a large amount of failure surface.

---

## 2. Non-goals

- Do not change the P7 safety kernel (leases, fencing, policy, control sessions, action catalog).
- Do not remove Network Profiles.
- Do not remove `CaptureLabObservation`'s read-only guarantees; only its container changes.
- Do not implement any-port / OTG enumeration (separate later feature; see §8).
- Do not touch workflow/run, recording/skill, or artifact domains.
- No production/multi-device rollout authorization is granted by this plan.

---

## 3. Target flow

```
Control plane starts
  └─ a Network Profile is marked default?
       └─ scan it (bounded to its range + ports)
            └─ for each observed transport:
                 serial already known? → update its endpoint + last-seen, mark known
                 serial new?           → create device + endpoint, mark new
       └─ operator opens console → devices are already listed

Console
  └─ "Scan now" re-runs the default profile (or a chosen one)
  └─ device rows show: online / offline / unauthorized / new / known
```

**No candidate queue. No approval. No registration. No provisioning.**

---

## 4. Removal inventory

### 4.1 Go packages

| Path | Action |
|---|---|
| `internal/edge/registration/` (`service.go`, `lab_probe.go`, + tests) | **Delete** (399 + 66 lines) |
| `internal/edge/lab/service.go` — `Discover`, `ConfirmTarget`, `ClearTarget` | **Remove methods**; keep `CaptureObservation`, `Status`, `Events` |
| `internal/edge/registration/lab_probe.go` `PrerequisiteProbe` | **Delete** (the 9 booleans die with it) |
| `internal/product/enumerator.go` | **Keep, fix** — stop discarding `Host`/`Port`/`Fingerprint` |

### 4.2 Connect handlers

| Path | Action |
|---|---|
| `internal/transport/connect/lab_registration.go` (+ test) | **Delete** |
| `internal/transport/connect/discovery.go` | **Rewrite** — drop `DecideScanCandidate`, `RegisterScanCandidate` |
| `internal/transport/connect/lab_adapter.go` | **Remove** `DiscoverLabDevices`, `ConfirmLabTarget`, `ClearLabTarget` handlers |
| `internal/transport/connect/product.go` | Remove `LabRegistration` from `ProductHandlers` if referenced |

### 4.3 Service route mounting

- `internal/service/server.go`: delete `LabRegistrationRoute`, `LabRegistrationRouteWithStore`.
- `cmd/control-plane/main.go:119-121`: delete `allowedPorts`, `registrationService`.
- `cmd/control-plane/main.go:117`: `MaxRegisteredDevices` disappears with the package.

### 4.4 Protobuf

`proto/drift/v1/discovery.proto`:
- Remove RPCs `DecideScanCandidate`, `RegisterScanCandidate`.
- Remove messages `DecideScanCandidateRequest/Response`, `RegisterScanCandidateRequest/Response`.
- Remove enum `ScanCandidateState` and messages `ScanRun`, `ScanCandidate` **if** replaced by the new shape (§5).
- **Add `reserved` for every removed field number and name** so numbers are never reused.

`proto/drift/v1/lab_registration.proto`: **delete the file**; reserve its package/service names in a comment in `discovery.proto` or a `DEPRECATED.md`.

`proto/drift/v1/lab_adapter.proto`: remove `DiscoverLabDevices*`, `ConfirmLabTarget*`, `ClearLabTarget*`; reserve their numbers.

**Compatibility strategy (required by AGENTS.md §6):** `drift.v1` has exactly one consumer, the console, which ships from this repository and is built/deployed in lockstep with the service. Removal is therefore performed in a single coordinated change, with `reserved` statements preventing number reuse. No external client exists; if that changes, this decision must be revisited.

### 4.5 SQLite migrations

New forward-only migration `db/migrations/0020_discovery_simplification.sql`. Do **not** edit shipped migrations.

| Table | Action | Note |
|---|---|---|
| `scan_candidates` | **DROP** | concept removed |
| `approval_decisions` | **DROP** | concept removed |
| `registration_events` | **DROP** | superseded by `operational_events` |
| `lab_provisioning_checks` | **DROP** | concept removed |
| `lab_registration_approvals` | **DROP** | concept removed |
| `lab_device_registrations` | **DROP** | concept removed |
| `scan_runs` | **KEEP, slim** | useful history: when did we last scan, and did it fail |
| `network_profiles` | **KEEP, simplify** | see §4.6 |

**Data-loss decision (needs explicit owner ack before executing):** these tables hold lab/test data only (P6 fake registry, P13/P14 one-device lab runs). Dropping them is the point of the change. If any row must be preserved, the migration must first export to `operational_events` before the DROP.

### 4.6 `network_profiles` simplification

Rebuild the table (SQLite 12-step) in `0020` to match the old Drift shape:

```sql
CREATE TABLE network_profiles_next (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT NOT NULL,
    name           TEXT NOT NULL CHECK (length(trim(name)) > 0 AND length(name) <= 128),
    address_policy TEXT NOT NULL CHECK (length(trim(address_policy)) > 0),
    ports_json     TEXT NOT NULL CHECK (length(ports_json) <= 8192),
    is_default     INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0,1)),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX network_profiles_one_default_idx
    ON network_profiles (workspace_id) WHERE is_default = 1;
```

Changes: **drop `state`**, **drop `row_version`**, drop `CHECK (state='active' OR is_default=0)`. A profile is saved configuration; `is_default` marks the one used for auto-scan. Removal of a profile is deletion.

`ports_json` stays an array (a low-cost superset of old Drift's single `port`; not a complexity driver).

### 4.7 Console

| Path | Action |
|---|---|
| `apps/console/src/pages/NetworkProfilesPage.tsx` | Rewrite: remove the Candidates / Provisioning views; keep Profiles + Scans; add a "Scan now" action |
| `apps/console/src/pages/lab-adapter.tsx` | Delete target-confirmation UI; retain observation capture against a selected device |
| `apps/console/src/lib/api/lab-registration-client.ts` | **Delete** |
| `apps/console/src/lib/api/lab-control-plane.ts` | Remove registration + target-confirm intents |
| `apps/console/src/lib/domain/control-plane.ts` | Remove `ScanCandidate*`, `LabRegistration*`, provisioning intents/view types |
| `apps/console/src/lib/api/real-control-plane.ts` | Remove calls to deleted RPCs |
| `apps/console/src/lib/api/control-plane-clients.ts` | Remove `decideScanCandidate`, `registerScanCandidate`, `LabRegistrationService` |

---

## 5. New scan contract

Replace the candidate lifecycle with a direct result.

```proto
// discovery.proto

message ObservedDevice {
  string host = 1;              // empty for USB
  uint32 port = 2;              // 0 = no TCP port (USB / OTG)
  string serial = 3;            // ADB transport identity
  string model = 4;
  DeviceLinkState state = 5;    // ONLINE | OFFLINE | UNAUTHORIZED
  bool known = 6;               // already present in this workspace
  string device_id = 7;         // set when known
  string endpoint_id = 8;       // set when known
  int64 last_seen_at_ms = 9;
}

enum DeviceLinkState {
  DEVICE_LINK_STATE_UNSPECIFIED = 0;
  DEVICE_LINK_STATE_ONLINE = 1;
  DEVICE_LINK_STATE_OFFLINE = 2;
  DEVICE_LINK_STATE_UNAUTHORIZED = 3;
}

message ScanRequest {
  RequestContext context = 1;
  WorkspaceRef workspace = 2;
  string network_profile_id = 3;   // empty = use the default profile
}
message ScanResponse {
  string scan_run_id = 1;
  string network_profile_id = 2;
  repeated ObservedDevice devices = 3;
}

service DiscoveryService {
  rpc Scan(ScanRequest) returns (ScanResponse);
  rpc ListScanRuns(...) returns (...);   // keep for history
}
```

**Behaviour:** `Scan` with an empty `network_profile_id` uses the profile where `is_default = 1`. It upserts device + endpoint by serial, writes a `scan_runs` history row and `operational_events`, and returns the observed list. No intermediate state.

---

## 6. Phased execution

Each phase: TDD, its own branch, its own PR, Sentinel review before merge, `main` must stay green.

### Phase A — Network Profile simplification

**Files:** `db/migrations/0020_discovery_simplification.sql`, `internal/networkprofiles/model.go`, `internal/store/sqlite/registry.go`, `internal/transport/connect/network_profile.go`, console Network Profiles page.

**Tests:** `internal/networkprofiles/model_test.go`, `db/migrations/integration_test.go`, `apps/console/src/pages/NetworkProfilesPage.test.tsx`.

**Tasks:**
1. Write failing migration test: profile row has no `state`/`row_version`; two defaults in one workspace violate the unique index.
2. Write migration `0020` (profiles rebuild only, in this phase).
3. Update `networkprofiles.NetworkProfile`: drop `State`, `RowVersion`; `Validate()` no longer requires `Active`; keep name length, policy CIDR/range, non-empty ports ≤64, no duplicates.
4. Update `Create`/`Update` store paths and the Connect handler.
5. Update the console page to create/edit/delete profiles with a "set as default" toggle.

**Exit criteria:** create → set default → scan-eligible in **one** call each; `go test ./...` green; `buf lint && buf build` green; console typecheck/lint/test/build green.

### Phase B — Scan returns devices directly

**Files:** `internal/discovery/service.go`, `internal/discovery/model.go`, `internal/discovery/lab_scanner.go`, `internal/store/sqlite/registry.go`, `internal/transport/connect/discovery.go`, `proto/drift/v1/discovery.proto`.

**Tests:** `internal/discovery/service_test.go`, `internal/discovery/lab_scanner_test.go`, `internal/store/sqlite/registry_test.go`, `internal/transport/connect/discovery_test.go`.

**Tasks:**
1. Write failing tests: `Scan` on an empty profile id uses the default; scanning twice with the same device yields `known: true` the second time and does not duplicate the device row; an unauthorized device appears with `UNAUTHORIZED` and is not actionable.
2. Add `ObservedDevice`/`ScanRequest`/`ScanResponse` to `discovery.proto`; `reserved` the removed numbers.
3. Implement `Service.Scan` (profile resolve → enumerate → upsert → events → response).
4. Implement upsert: match existing endpoint by `(workspace_id, serial)` → reuse `device_id`; else create device (new stable id) + endpoint. **This preserves the `Device` (stable) vs `Endpoint` (mutable) distinction** that old Drift collapsed — keep it.
5. Delete `DecideCandidate`, `RegisterCandidate`, `ExpireCandidate`, `CandidateState`, and the candidate store methods.
6. Fix `LabRuntimeEnumerator` to carry `Host`/`Port`/`Fingerprint` through; replace the `port = 1` sentinel with `port = 0`.
7. Drop `scan_candidates`, `approval_decisions`, `registration_events` in `0020`.

**Exit criteria:** scan → device visible with no intermediate call; `known` correct on re-scan; no duplicate rows under `-race` with concurrent scans; P7 safety kernel untouched and its tests green.

### Phase C — Remove registration and provisioning

**Files:** delete `internal/edge/registration/`, `internal/transport/connect/lab_registration.go`, `lab_registration.proto`, console `lab-registration-client.ts`; edit `internal/service/server.go`, `cmd/control-plane/main.go`, `internal/service/security_test.go`.

**Tests:** delete the corresponding test files; add an assertion in `internal/service/server_test.go` that no registration route is mounted.

**Tasks:**
1. Delete `internal/edge/registration/` and its tests.
2. Delete `lab_registration.proto` and regenerate.
3. Delete `LabRegistrationRoute`/`LabRegistrationRouteWithStore`; remove from `main.go`.
4. Drop `lab_provisioning_checks`, `lab_registration_approvals`, `lab_device_registrations` in `0020`.
5. Remove registration intents from the console domain types and mock.

**Exit criteria:** `go build ./...` clean with no `registration` references; `rg -n "MaxRegisteredDevices|VerifyProvisioning|ApproveLabProvisioning"` returns nothing; console builds; five CI checks green.

### Phase D — Auto-scan on launch

**Files:** `cmd/control-plane/main.go`, `internal/product/` (new `autoscan.go`), `internal/service/server.go`.

**Tests:** `internal/product/autoscan_test.go`.

**Tasks:**
1. Write failing test: on startup with a default profile and an enumerator returning two devices, both appear as `devices` rows and a `scan_runs` row exists.
2. Implement `autoscan.Runner`: on start, if a default profile exists, run one scan. Non-blocking; failures are recorded as events and never fatal to startup.
3. Wire it in `main.go` with a bounded context and cancellation on shutdown.
4. Console: device list refreshes on load and offers "Scan now".

**Exit criteria:** start the service against a temp DB with a seeded default profile → `ListDevices` returns the enumerated devices with no console interaction.

### Phase E — Collapse the second discovery stack

**Files:** `internal/edge/lab/service.go`, `internal/transport/connect/lab_adapter.go`, `proto/drift/v1/lab_adapter.proto`, `internal/edge/lab/observation_adapter.go`.

**Tasks:**
1. Remove `DiscoverLabDevices`, `ConfirmLabTarget`, `ClearLabTarget` from proto, service, and handlers (reserve numbers).
2. Move observation capture to a device-scoped RPC (`CaptureObservation(device_id)`), preserving operator attribution per call — the explicit intent is kept, the confirm lifecycle is not.
3. Delete `ConfirmationRejection` and its classification paths.

**Exit criteria:** one discovery path exists in the codebase; capture still requires an explicit device selection and remains read-only; ADR-0004 updated to record the new boundary.

### Phase F — Cleanup and documentation

**Tasks:**
1. `rg` for stragglers: `CandidateState`, `pending_approval`, `ProvisionReady`, `labTarget`, `registered device capacity`.
2. Update `README.md`, `CONTEXT.md` (remove *Scan candidate* and registration vocabulary), `docs/domain/resource-lifecycle.md`.
3. Write `docs/adr/0007-simplified-device-discovery.md` recording the deliberate reversal of "discovery must be distinct from approval and canonical device creation", with the safety argument from §1.
4. Update `AGENTS.md` §2 domain rules to match.
5. Regenerate protobuf/TS artifacts; run `scripts/check-generated.sh`.

**Exit criteria:** no references to removed concepts; generated artifacts reproducible; all gates green.

---

## 7. Verification gates (every phase)

```bash
go vet ./... && go build ./... && go test ./...
go test -race -count=1 ./internal/store/sqlite/... ./internal/discovery/...
pnpm typecheck && pnpm lint && pnpm test && pnpm build
buf lint && buf build
bash scripts/check-generated.sh && bash scripts/secret-scan.sh
git diff --check
```

Plus a live check per phase: run the control plane against a throwaway DB, call the changed RPCs, and read the rows back with `sqlite3` to confirm persistence.

**Review rule:** each phase gets an exact-SHA review before merge. Do not advance a phase on an unverified head.

---

## 8. Deferred (explicitly not in this plan)

- **Any-port / OTG enumeration.** `adb devices -l` already lists devices on any port; the filter is ours (`allowedPorts := []uint16{5555}` in `cmd/control-plane/main.go:119`, `lab_registration.go:58`, and the host/port filter in `lab_scanner.go:84-92`). Once listing no longer goes through registration, this becomes a small filter change — apply port policy at *connect* time, not at *list* time. Owner has scoped this as a later feature.
- Real multi-device fleet rollout authorization.
- Any change to the safety kernel, workflows, recordings, or artifacts.

---

## 9. Risks and tradeoffs

| Risk | Assessment | Mitigation |
|---|---|---|
| Dropping tables loses data | Low — P6/P13/P14 lab data only | Owner ack before executing `0020`; optional export to `operational_events` first |
| Removing the approval gate weakens safety | **None to action safety** — leases + policy + control session are unchanged | Kernel untouched; scan is bounded by profile range + ports; device rows are inert |
| Proto removal breaks a client | Low — single in-repo consumer | Coordinated single change; `reserved` numbers; revisit if an external client appears |
| Auto-scan on startup slows launch | Low with a bounded range | Bounded context + non-blocking; failures recorded, never fatal |
| Reversal of a documented domain rule | Real — AGENTS.md says discovery must be distinct from creation | ADR-0007 records the decision and reasoning explicitly |
| Losing the Device/Endpoint distinction | Medium if rushed | Upsert keyed on `(workspace, serial)` **reuses** the existing `device_id`; stable identity is preserved |
| Phase C is a large deletion | Medium | Delete-only phases first (C), additive contract changes first (B) |

---

## 10. Owner decisions (approved 2026-09-16)

All four blocking questions are settled. These are now plan constraints, not options.

1. **`0020` data retention — DROP OUTRIGHT.** `scan_candidates`, `approval_decisions`, `registration_events`, `lab_provisioning_checks`, `lab_registration_approvals`, and `lab_device_registrations` are dropped with no export step. No migration to `operational_events`. Removal is the point; the data is lab/test only. **Consequence:** the DROP is irreversible — no rollback path for these rows. Do not soften this into a soft-delete or a retained archive table.
2. **Auto-scan — STARTUP ONLY.** One scan when the control plane starts, if a default profile exists. Plus the manual "Scan now" action. **No periodic timer.** Do not add a scheduler, ticker, or cron for this.
3. **Profiles are DELETE-ONLY.** `state` and `disable` are removed. Deletion is the only lifecycle transition. No retire/disable/archive states, no soft-delete column.
4. **`scan_runs` — KEEP INDEFINITELY.** No retention cap, no pruning job. One row per scan is cheap and it is useful history.

---

## 11. Definition of done

- One discovery path in the codebase; one scan RPC; no candidate, registration, or provisioning concepts anywhere in proto, Go, SQL, or console.
- Launching the control plane with a default profile lists devices with no operator interaction.
- A device is usable by being assigned and acted on under the existing lease/policy/control-session kernel — not by being registered.
- All verification gates green; ADR-0007 and updated `AGENTS.md`/`CONTEXT.md` committed.
- Net reduction: ~15 RPCs → 3; 2 state machines → 0; 2 discovery stacks → 1.
