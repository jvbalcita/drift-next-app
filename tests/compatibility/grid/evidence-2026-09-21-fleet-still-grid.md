# Fleet grid stills: the grid on every attached device, measured

Measured 2026-09-21 on the lab fleet with the still path in this branch
(`TestGridStillFleetProbe`, `internal/media/grid_still_probe_test.go`).

What it establishes, and nothing else:

- the fleet grid carries **every attached device** as a current still, at the
  deployment's own level and cadence, and never opens a device session to do it;
- what the stills cost on the wire, and what actually bounds the cadence — which
  is **not** the transport;
- that the operator's own live frame kept carrying pictures on the same device
  while the grid refreshed every device underneath it, and that the grid opened
  **no** session of its own;
- that one still's size per preview level is what a deployment chooses the level
  and the cadence from, and that the plane's byte bound is met by spending
  quality rather than by delivering nothing.

## Fleet and toolchain

| | |
| --- | --- |
| Devices attached | 19 (Samsung SM-G9750 `beyond2qltezh`), reached over TCP (`192.168.1.x:5555`) |
| Host | `ArtisanClaws-MacBook-Pro.local`, macOS 26.6.2, host load at measurement `9.19 11.94 16.98` |
| adb | `Android Debug Bridge version 1.0.41` (`/opt/homebrew/bin/adb`) |
| Device-side server | `/opt/homebrew/share/scrcpy/scrcpy-server` (scrcpy 4.1) |
| Screen captured | 1080x2280, reported by the devices themselves |
| Level measured | `medium` (360 px wide, JPEG q65) |
| Cadence configured | 4 s |
| Capture bound | 15 s (the capture path's own `adb.DefaultOperationTimeout`) |

## Reproducing it

```sh
DRIFT_GRID_PROBE=1 \
DRIFT_GRID_PROBE_ADB=/opt/homebrew/bin/adb \
DRIFT_GRID_PROBE_SERVER=/opt/homebrew/share/scrcpy/scrcpy-server \
DRIFT_GRID_PROBE_OUT=$PWD/tests/compatibility/grid/probe-reports/probe-still-fleet.json \
DRIFT_GRID_PROBE_HOLD_SECONDS=120 \
DRIFT_GRID_PROBE_CADENCE=4s \
DRIFT_GRID_PROBE_LEVEL=medium \
go test ./internal/media/ -run TestGridStillFleetProbe -count=1 -v -timeout 1500s
```

The probe is opt-in and skips without the gates. It asserts the properties below;
the full numbers land in the report it writes and in its log.

The control arm for the operator's frame was also taken separately, with the
committed ARC-228 concurrency harness (`TestLiveStreamConcurrencyProbe`,
one stream, 60 s) so the two readings can be compared:

```sh
DRIFT_MIRROR_PROBE=1 \
DRIFT_MIRROR_PROBE_ADB=/opt/homebrew/bin/adb \
DRIFT_MIRROR_PROBE_SERVER=/opt/homebrew/share/scrcpy/scrcpy-server \
DRIFT_MIRROR_PROBE_OUT=$PWD/tests/compatibility/grid/probe-reports/probe-operator-baseline.json \
DRIFT_MIRROR_PROBE_COUNTS=1 DRIFT_MIRROR_PROBE_HOLD_SECONDS=60 \
go test ./internal/edge/scrcpy/ -run TestLiveStreamConcurrencyProbe -count=1 -v -timeout 600s
```

## 1. Every device, on the plane's own cadence, with no session spent

19 of 19 attached devices were carried as current stills, and the count never
went backwards: `[0 0 0 1 1 1 1 2 ... 18 18 18 19 19 ... 19]`, one device per
capture in the sweep's own order, all 19 held from the 79th second of a 120 s
hold. The first sweep's duration is why the ramp is that long — see §3.

| | |
| --- | --- |
| Devices carried as current stills | 19 of 19 |
| Stills delivered in full | 19 of 19 |
| Stills reported as truncated rather than delivered | 0 |
| Live sessions opened by the grid | 0 (the only sessions opened were the two operator arms) |
| Sessions the plane held while the grid swept | the operator's one |

The session figures are the point of the card: a still spends no place, so the
grid's reach is the plane's own capture work and not `sessionCapacity −
operatorReserve`, which is 4 − 1 = 3 places on this plane and therefore at most
three tiles however cheap their streams are.

## 2. What one still costs, per level

One real capture of a 1080x2280 screen, encoded at each level by the plane's own
encoder. The delivered still is what the console paints and what the transport
carries; the source capture is what the **device and the hub** pay.

| Level | Max width | JPEG quality | Delivered still | Source capture | Encode |
| --- | --- | --- | --- | --- | --- |
| low | 240 px | 60 | 9.6 KB | 3.4 MB | ~0.19 s |
| medium | 360 px | 65 | 18.7 KB | 3.4 MB | ~0.18 s |
| high | 480 px | 70 | 30.7 KB | 3.4 MB | ~0.15 s |

The card's own expectation was "a 480p JPEG should be tens of KB, not a
megabyte": measured, it is 30.7 KB at 480 px wide. The capture it came from is
3.4 MB, because `screencap` ships the screen at its native size and the level is
applied on the plane afterwards — which is the fact §3 turns on.

## 3. What bounds the cadence is the capture, not the transport

At the configured 4 s cadence the whole fleet's stills cost **0.76 Mbps**:
379 KB per sweep, 1.9% of the 40 Mbps a single operator-profile live stream was
measured at in the ARC-228 work. By bytes alone, this hub carries **~1000**
devices as stills at that still size and cadence.

The binding cost is elsewhere. One capture took a **mean of 4.79 s** per device
(3.1–4.8 s across runs), so a sequential sweep of 19 devices took **79 s**
(59–98 s across runs), and the engine reported it (`LongestSweep`) over 3 ticks
in the 120 s hold.

| | |
| --- | --- |
| Mean capture | 4793 ms |
| Longest sweep, 19 devices | 78.9 s |
| Still bytes per sweep | 379 KB (19.9 KB per device) |
| Grid cost at the 4 s cadence | 0.758 Mbps = 1.9% of the measured 40 Mbps far stream |
| Devices that fit the measured far stream by bytes | ~1000 |
| Devices that fit inside one 4 s cadence of captures | 0 |

So the cadence a deployment configures is an **aim**, and the interval a device
is actually refreshed at is `max(the cadence, the sweep)` — 79 s for this fleet,
not 4 s. That is why each frame states the interval the plane *measured* between
a device's last two captures, and why the console renders that number rather than
the configured one: a tile that said "about every 4 seconds" while the plane
refreshed it every 79 s would be a surface contradicting the measurement.

A deployment that wants N devices fresh every C seconds needs
`N × the measured capture cost ≤ C`; on this hub that is `N × 4.8 s ≤ C`. The
bound and the level are therefore deployment inputs (`DRIFT_GRID_MAX_DEVICES`,
`DRIFT_GRID_STILL_LEVEL`, `DRIFT_GRID_STILL_CADENCE`) set from these numbers
rather than from taste — see the derivation beside each constant in
`internal/media/grid_env.go`.

## 4. The byte bound is met by spending quality, not by delivering nothing

An earlier run of this probe measured one unit's medium still at **37 KB**
against the product's 32 KiB preview bound, and the plane's rule for a still over
the bound is to deliver **no picture at all** — so that device's tile would have
shown a truncation for ever, at the default level, on a screen that simply had
more detail in it.

The encoder now tries the level's quality first and steps down a bounded ladder
(65 → 48 → 36 → 30 on the medium level) until the still fits, and states the
quality it used. In this run **every** still was delivered inside the bound (19
of 19, 0 truncations), and the unit that had measured 37 KB at q65 was delivered
at 19.9 KB. Below the floor the smallest attempt is returned and the engine's own
truncation rule reports it, so a still is never delivered over the bound.

## 5. The operator's own frame, while the grid refreshes every device

The same live session, on the same device, for the same 120 s, twice: once with
nothing else running, once with the grid sweeping all 19 devices underneath it.

| | alone (control) | with the grid |
| --- | --- | --- |
| Live sessions | 1 | 1 |
| Session | 510x1080 | 510x1080 |
| Access units carried | 34 | 34 |
| Key frames | 1 | 1 |
| Bytes | 899 406 | 896 862 |
| Seconds with no access unit at all | 111 of 120 | 111 of 120 |
| Longest run of quiet seconds | 58 | 58 |
| Aggregate | 0.060 Mbps | 0.060 Mbps |

The session stayed open in both arms and carried the same number of access units,
the same bytes and the same quiet pattern, with the grid refreshing every device
between them: **the grid cost the operator's frame nothing measurable**, and it
opened no session of its own to do it.

Two honest caveats, both deliberate:

- **These counts are reported, not asserted.** An idle bench drives them: a
  device whose screen is not changing carries about 0.3 frames per second with
  nothing running at all (34 access units in 120 s, in one 58 s silence between
  status-bar ticks — the ARC-228 harness measured the same shape: 22 frames in
  60 s with one stream and nothing else on the fleet). Across the four runs taken
  for this card the two arms moved between 34 and 45 access units **in both
  directions**, so at this sample size the counts cannot order the arms. What the
  probe asserts is what the claim needs and the bench can show: the session
  opened, stayed open, stayed the same session, carried pictures for the whole
  grid arm, and the grid opened none.
- **A stronger experiment is available and is not attempted here.**
  Driving the operator's screen would make the stream carry at a known rate and
  would turn the frame counts into a comparison. That needs an authorized control
  session (input reaches a device only after the kernel authorizes it), and this
  probe holds no such authority — so it says what it measured instead.

## What this does NOT establish

- Nothing about a fleet larger than 19 devices, and nothing about USB transports:
  every device here was reached over TCP, where the capture cost measured is a
  property of the hub as much as of the device.
- Nothing about a hub under other load: the host was at load ~9-17 during these
  runs, shared with other work, and the capture cost measured is the cost under
  that load. A quieter host would capture faster; a busier one, slower.
- It does not settle whether a tile should ever escalate to live video. The
  surface as built draws stills for every device and leaves the one live session
  to the operator's own frame.

## 6. Bounded parallel capture follow-up (2026-09-24)

The still-only engine was measured again through its public grid seam
(`GridSettingsFromEnv`, `SyncSubscriptions`, `Run`, and `Frame`) with all 19
authorized Wi-Fi devices currently attached. Unlike the 2026-09-21 sweep above,
these runs explicitly set the bounded capture concurrency and stopped after one
or three attempts per subscribed device.

| | concurrency 12 | concurrency 19 |
| --- | ---: | ---: |
| Devices subscribed | 19 | 19 |
| Captures completed | 19 | 19 |
| Longest sweep | 9.431 s | 7.257 s |
| Tiles current and within 10 s at sweep completion | 19 / 19 | 19 / 19 |
| p95 tile age at sweep completion | 5.111 s | 1.486 s |
| Capture failures / cancellations | 0 / 0 | 0 / 0 |
| Go probe process sampled peak RSS | 155.8 MB | 216.2 MB |

The concurrency-19 report also records a 176.3 MB Go heap-in-use peak and a
208.3 MB Go heap-system peak. The RSS was sampled every 250 ms and covers the
opt-in Go test process only; it excludes the shared ADB server, console, and
other control-plane processes. These single-sweep measurements show that bounded
parallel ADB capture can meet the 10-second tile freshness target on this
19-device lab fleet, and materially improve on the sequential sweep above.

A sustained 19-device run then requested three captures per tile at the normal
4 s plane tick. With the configured 5 s idle interval it completed 57 captures
over six ticks with no capture failures; p95 tile age at shutdown was 5.437 s,
and measured intervals between successful captures were 9.065–10.097 s (9.513 s
mean). Two diagnostic repetitions changed only the idle interval to 3 s and 1 s:
the observed capture intervals remained 9.304–10.201 s and 9.154–10.165 s,
respectively, while sampled Go-test RSS was 217.4 MB (5 s idle), 266.6 MB (3 s
idle), and 252.2 MB (1 s idle). All three runs kept 19/19 tiles current
and under the freshness ceiling at shutdown, with no capture failures; 3, 5,
and 12 in-flight attempts were cancelled as each probe shut down. This
shows that the sweep/tick scheduling, not just the idle setting, bounds the
refresh rate in this fleet. Those reports predate the start-to-start cadence and
zero-copy ADB result path measured below. They do **not** prove total-host RSS
below 500 MB, a 25-device fleet, repeated-cadence stability over an eight-hour
soak, or independent recovery.

## 7. Start-to-start cadence and bounded ADB memory (2026-09-24)

The engine now counts capture work as start-to-start cadence: time spent waiting
for a screenshot does not get added a second time after that screenshot
completes. The ADB process runner also transfers its completed bounded output
buffer to the capture result rather than keeping a duplicate multi-megabyte PNG
copy live during each concurrent capture. The existing snapshot-copy behavior
remains available to any caller inspecting a still-writable buffer.

Two independent probes used the same 18 authorized Wi-Fi devices, 12 bounded
capture workers, four attempts per device, 1 s active / 5 s idle cadence, and a
10 s freshness ceiling. Peak tile age was sampled every 50 ms throughout each
run, not only at shutdown.

| | Run 1 | Run 2 |
| --- | ---: | ---: |
| Tiles current and within 10 s at shutdown | 18 / 18 | 18 / 18 |
| p95 / max peak tile age during run | 9.500 / 9.500 s | 9.844 / 9.844 s |
| ADB capture-call p50 / p95 | 4.726 / 5.225 s | 4.881 / 5.489 s |
| Capture failures | 0 | 0 |
| Go probe + local ADB process peak RSS | 353.8 MB | 354.4 MB |

The RSS includes the opt-in Go test process and local ADB CLI/server processes,
sampled every 250 ms. It excludes the control plane, console/browser, operating
system, and unrelated processes, so it is **not** the whole-host memory figure.
These repeated runs meet the 10 s peak freshness gate for this 18-device sample
and support the 12-worker default; they do not establish the 500 MB whole-host
limit, the optional persistent-stream configuration, a 25-device fleet, or an
eight-hour recovery soak.

Reproduce either run with the same bounded still-only path:

```sh
DRIFT_GRID_SWEEP_PROBE=1 \
DRIFT_GRID_PROBE_ADB=/opt/homebrew/bin/adb \
DRIFT_GRID_SWEEP_PROBE_OUT=$PWD/tests/compatibility/grid/probe-reports/probe-still-sweep-2026-09-24-18-device-concurrency-12-active-1s-zero-copy-repeat-four-sweeps.json \
DRIFT_GRID_SWEEP_PROBE_SWEEPS=4 \
DRIFT_GRID_CONCURRENT_CAPTURES=12 \
DRIFT_GRID_ACTIVE_CADENCE=1s \
DRIFT_GRID_IDLE_CADENCE=5s \
go test ./internal/media -run '^TestGridStillSweepProbe$' -count=1 -v -timeout 300s
```

Reproduce the concurrency-19 measurement with the opt-in, still-only probe:

```sh
DRIFT_GRID_SWEEP_PROBE=1 \
DRIFT_GRID_PROBE_ADB=/opt/homebrew/bin/adb \
DRIFT_GRID_SWEEP_PROBE_OUT=$PWD/tests/compatibility/grid/probe-reports/probe-still-sweep-2026-09-24-concurrency-19.json \
DRIFT_GRID_STREAM_PREVIEWS=false \
DRIFT_GRID_CONCURRENT_CAPTURES=19 \
go test ./internal/media -run '^TestGridStillSweepProbe$' -count=1 -v -timeout 6m
```

Add `DRIFT_GRID_SWEEP_PROBE_SWEEPS=3` to request three attempts per device, and
optionally set `DRIFT_GRID_IDLE_CADENCE=3s` or `1s` to compare the deployment's
adaptive idle profile. These are diagnostic overrides; they do not mutate the
product's defaults.

## 8. Bounded soak preflight results (2026-09-24)

The opt-in soak harness was first checked with one device for 30 seconds, then
with the full currently attached sample of 18 devices for 30 seconds. The
single-device harness run passed, but the 18-device run did **not** reach its
soak window: it spent the probe's bounded warmup attempting to bring all tiles
current, and completed neither the preflight nor a post-soak refresh. That is a
useful load/recovery failure, not evidence from a 30-second fleet soak.

| | one-device harness check | 18-device preflight attempt |
| --- | ---: | ---: |
| Requested soak duration | 30 s | 30 s (not reached) |
| Devices current / fresh at report | 1 / 1 | 17 / 11 |
| Final p95 / max tile age | 42 / 42 ms | 64.314 / 64.314 s |
| Peak p95 / max age during soak | 8.958 / 8.958 s | not measured; soak did not start |
| Captures / failures / shutdown cancellations | 6 / 0 / 1 | 117 / 9 / 12 |
| Capture-call p50 / p95 / max | 6.911 / 8.843 / 8.843 s | 10.129 / 15.001 / 15.010 s |
| Probe plus local ADB peak RSS | 297.7 MB | 520.4 MB |
| Host load at measurement | 17.94 / 14.71 / 15.84 | 12.43 / 17.41 / 17.25 |

Both RSS readings include the probe process and local ADB processes only, not
the control plane or console/browser; neither is whole-host memory acceptance.
The 18-device report's zero peak-age counters are not a pass: the harness had
not entered the interval from which those counters are sampled. That first
report predates explicit `SoakStarted`, `SoakReached`, and `ProbeCompleted`
fields; later reports carry those booleans so an aborted preflight cannot be
mistaken for a completed soak. The one-device run validates the harness and its
single-device cleanup path only. Keep the fleet freshness, recovery, memory, and
eight-hour gates open.

The harness was then corrected to persist `SoakStarted`, `SoakReached`, and
`ProbeCompleted`, and three explicitly timed 18-device trials were run. They show
the current trade-off rather than a stable pass: concurrency 12 stayed well below
the process-only memory ceiling but exceeded freshness during the timed window;
concurrency 18 left every tile fresh at shutdown but exceeded freshness during
the run and came within 11.3 MB of the 500 MB ceiling before counting the plane
or console.

| | 12 capture workers | 16 capture workers | 18 capture workers |
| --- | ---: | ---: | ---: |
| Timed window started / reached / probe completed | yes / yes / yes | yes / yes / yes | yes / yes / yes |
| Devices current / fresh at report | 18 / 16 | 18 / 18 | 18 / 18 |
| Final p95 / max tile age | 10.182 / 10.182 s | 8.533 / 8.533 s | 8.214 / 8.214 s |
| Peak p95 / max tile age | 15.530 / 15.530 s | 13.755 / 13.755 s | 12.028 / 12.028 s |
| Captures / failures | 87 / 0 | 100 / 0 | 91 / 0 |
| Capture-call p50 / p95 / max | 7.056 / 9.262 / 9.835 s | 7.991 / 10.446 / 12.143 s | 8.309 / 11.682 / 12.414 s |
| Probe plus local ADB peak RSS | 418.2 MB | 469.9 MB | 488.7 MB |
| Host load at measurement | 6.23 / 6.88 / 6.60 | 3.76 / 4.61 / 5.53 | 3.83 / 5.33 / 5.97 |

The RSS again excludes the control plane and console/browser. The 18-worker arm
is therefore not a memory pass, and all three timed arms fail the observed
peak-age criterion despite zero capture failures. These runs do not authorize changing
the product default to 18 workers; the earlier two four-sweep concurrency-12
runs remain favorable but are not enough to establish stable soak acceptance.
Do not start an eight-hour soak until freshness and whole-application memory are
stable under repeated short runs.

## Reports

| File | SHA-256 |
| --- | --- |
| `probe-reports/probe-still-fleet.json` | `847e62a54cceb4debd5de3d95d166d7ff2375854ddd3b4a3715ab1b78c10ba45` |
| `probe-reports/probe-operator-baseline.json` | `c04934b2cff6202d3e22eac9dc732c012c8c0bc4e6a5e608854cc799bca59a2b` |
| `probe-reports/probe-still-sweep-2026-09-24-concurrency-12.json` | `d0d0225137450acf51320bf9db6bd606f407f58cab343b525dd2927d56e33f2f` |
| `probe-reports/probe-still-sweep-2026-09-24-concurrency-19.json` | `8f79597dbb5ccd317e7441b5ab2bd02641bf5b04a4fac15e5faa2b387513454c` |
| `probe-reports/probe-still-cadence-2026-09-24-concurrency-19.json` | `54eaeabaf4cff0e2ecbcda2a91cc7b9497489d7b6d4bed533fe5b615d543c0b1` |
| `probe-reports/probe-still-cadence-2026-09-24-concurrency-19-idle-3s.json` | `96efee2defd8201fd131e3b91a795b893777be56b0c09f5f8b663c4a8e28129e` |
| `probe-reports/probe-still-cadence-2026-09-24-concurrency-19-idle-1s.json` | `146a6ab2e314b5bf594d6a358ff88bd4b642d6b991448ce498407641ee434860` |
| `probe-reports/probe-still-sweep-2026-09-24-18-device-concurrency-12-active-1s-zero-copy-four-sweeps.json` | `185b4f023e7fb0861cfc5a54c5db69c6b2051dcb9e15f54e538f7e6ccb24911c` |
| `probe-reports/probe-still-sweep-2026-09-24-18-device-concurrency-12-active-1s-zero-copy-repeat-four-sweeps.json` | `9e9e7156e587a7f3838afa6d2da98330c82039c2ca4d3fad75e12b121d16d03a` |
| `probe-reports/probe-still-soak-2026-09-24-1-device-30s.json` | `4896445d43872ae1dc787aefa534eadbb51efafa8e3e9d4e0b5ee175fc0e2e35` |
| `probe-reports/probe-still-soak-2026-09-24-18-device-30s.json` | `197ca0d54763412c84a83beb414956514bbe71883b4a62203253b30b5fd86c73` |
| `probe-reports/probe-still-soak-2026-09-24-18-device-30s-continuation.json` | `6f7cf58443378e1b9b9b77235281c8d6ce5d1577161b8acffc225b9170f32b97` |
| `probe-reports/probe-still-soak-2026-09-24-18-device-30s-concurrency-18.json` | `4bebf39fc7d5315e3473abe1e1abc95208787f161b21e95c86b542326b84de2a` |
| `probe-reports/probe-still-soak-2026-09-24-18-device-30s-concurrency-16.json` | `6b4d03064453c2f0e91dd00ed08ab25ba55e5110b08b6e0e1e0cd73139daba48` |

The still-fleet report carries the per-device table (state, bytes, delivered
size, measured cadence), the per-level table, both operator arms second by
second, and the bound the run implies.
