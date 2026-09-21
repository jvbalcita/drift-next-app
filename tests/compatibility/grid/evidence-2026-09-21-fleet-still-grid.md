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

## Reports

| File | SHA-256 |
| --- | --- |
| `probe-reports/probe-still-fleet.json` | `847e62a54cceb4debd5de3d95d166d7ff2375854ddd3b4a3715ab1b78c10ba45` |
| `probe-reports/probe-operator-baseline.json` | `c04934b2cff6202d3e22eac9dc732c012c8c0bc4e6a5e608854cc799bca59a2b` |

The still-fleet report carries the per-device table (state, bytes, delivered
size, measured cadence), the per-level table, both operator arms second by
second, and the bound the run implies.
