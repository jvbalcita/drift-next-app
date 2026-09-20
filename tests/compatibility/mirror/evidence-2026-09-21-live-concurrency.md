# ARC-228 live-stream concurrency evidence — 2026-09-21

The plane's device-session capacity is a number a deployment states. Before this card it was a
silent default (4 sessions, 1 reserved for the operator's own frame, i.e. 3 live tiles) whose only
supporting measurement was a **bulk-transfer** figure: four concurrent `adb pull` streams moved
78 Mbps and eight collapsed. Bulk transfer is not the demand a live tile makes, so this card
measures the thing the number actually bounds — how many live mirror sessions this host and this
fleet's transport carry at once — with the product's own scrcpy client.

## Method

Opt-in Go test: `internal/edge/scrcpy/live_concurrency_probe_test.go`. It opens N real sessions
through the product's own client, holds them, and records per-stream frame counts, frame rate,
encoded throughput and presentation-timestamp deltas. The first 5 s of every stream (encoder
start-up, first IDR, handshake burst) are excluded from steady-state figures.

```text
DRIFT_MIRROR_PROBE=1 \
DRIFT_MIRROR_PROBE_ADB=/opt/homebrew/bin/adb \
DRIFT_MIRROR_PROBE_SERVER=/opt/homebrew/share/scrcpy/scrcpy-server \
DRIFT_MIRROR_PROBE_COUNTS=4,8 \
DRIFT_MIRROR_PROBE_HOLD_SECONDS=30 \
DRIFT_MIRROR_PROBE_OUT=/abs/report.json \
go test ./internal/edge/scrcpy -run TestLiveConcurrencyProbe -v -count=1
```

Every gate must be set or the test skips; CI sets none, so no device work happens in CI.

## Conditions

| Fact | Value |
| --- | --- |
| Host | `ArtisanClaws-MacBook-Pro.local` (macOS, arm64) |
| Hub | this host's wireless path to the device fleet (`en0`) |
| Host load at the clean 4-session point | `43.16 56.25 55.27` (1/5/15 min) |
| Device-side server | `/opt/homebrew/share/scrcpy/scrcpy-server` |
| Host `adb` | Android Debug Bridge `1.0.41` |
| Devices online | 10 (wireless, TCP `host:port`) |
| Serial handling | masked as `device-sha256:<5 chars>`; no raw serial is committed |
| **Profile measured** | **the plane's own default: native 1080×2280 at 30 fps ≈ 8.0 Mbps per stream** |

The profile matters: 8 Mbps is the plane's *unbounded native* stream — the operator's own frame.
A grid tile is carried at a bounded preview level (Medium is capped at 1.2 Mbps), so every figure
below is a **conservative upper bound** on what the tile grid costs the same path.

## Results

Raw reports are committed beside this file in `probe-reports/`. Steady figures exclude the first
5 s; a 30 s hold leaves a 25 s steady window, i.e. 751 frames at 30.0 fps.

### 4 concurrent sessions — clean (`probe-n4.json`)

| Serial | Frame | Frames | Steady frames | fps | Mbps | Stalled | PTS delta (median) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `device-sha256:0d658` | 1080×2280 | 992 | 751 | 30.04 | 7.997 | 0 s | 33 386 µs |
| `device-sha256:1c546` | 1080×2280 | 964 | 752 | 30.08 | 7.996 | 0 s | 33 340 µs |
| `device-sha256:bbcd4` | 1080×2280 | 931 | 751 | 30.04 | 8.009 | 0 s | 33 376 µs |
| `device-sha256:a52cc` | 1080×2280 | 901 | 751 | 30.04 | 8.001 | 0 s | 33 390 µs |

**Aggregate 32.00 Mbps. 4 requested, 4 opened, 0 stalled streams, not degraded.** Every stream held
30 fps for the whole 30 s window at a 33.3 ms presentation cadence (30 fps exactly).

### 8 concurrent sessions — only four could be opened (`probe-n8.json`)

| Serial | Frames | Steady frames | fps | Mbps | Stalled |
| --- | --- | --- | --- | --- | --- |
| `device-sha256:0d658` | 4590 | 751 | 30.04 | 8.020 | 0 s |
| `device-sha256:1c546` | 4561 | 750 | 30.00 | 7.983 | 0 s |
| `device-sha256:bbcd4` | 4532 | 751 | 30.04 | 8.013 | 0 s |
| `device-sha256:a52cc` | 4502 | 751 | 30.04 | 8.000 | 0 s |

**Aggregate 32.02 Mbps for the four that opened. 8 requested, 4 opened, 4 refused to open.**
Each refusal failed after three attempts with the same named cause:

```text
scrcpy: pushing the server to <host:port>: adb mirror-push-server failed (timeout)
cause=adb invocation canceled: context deadline exceeded
```

The four sessions that *did* open were not degraded at all — the plane was carrying 32 Mbps at
30 fps on every stream while four further sessions were being refused. What ran out is not
bandwidth or encoder capacity; it is the number of sessions this hub can **start**.

### Rerun at 4 and 5 requested (`probe-committed.json`)

| Requested | Opened | Aggregate | Steady fps (per opened stream) | Stalled | Refusal |
| --- | --- | --- | --- | --- | --- |
| 4 | 3 | 23.96 Mbps | 30.00 / 30.04 / 30.04 | 0 | 1 × `mirror-push-server` timeout |
| 5 | 4 | 32.03 Mbps | 30.04 / 30.00 / 30.08 / 30.00 | 0 | 1 × `mirror-push-server` timeout |

**The first point at which a session could not be opened at all is 4 requested**, and five
requested opens at most four. No opened stream stalled at any measured point.

### Single-session baselines

| Report | Hold | Opened | fps | Mbps | Stalled |
| --- | --- | --- | --- | --- | --- |
| `probe-video.json` | 20 s | 1/1 | 30.0 | 7.998 | 0 |
| `probe-long-video.json` | 20 s | 1/1 | 30.07 | 7.994 | 0 |
| `probe-sustain.json` | 70 s | 1/1 | 30.0 | 8.008 | 0 |

### The first attempt, kept because it is the awkward one (`probe-smoke.json`)

The very first probe run (18:30, 4 requested, 20 s hold) **degraded**: 3 streams opened, all three
stalled for the whole window, aggregate 0.006 Mbps, zero steady frames, PTS deltas pinned at the
100 ms cap. Every later run at the same and larger sizes was clean, so this is recorded as a
first-run condition rather than as the fleet's limit. It was taken before the probe excluded a
warm-up window and before the earlier sessions on those devices were torn down; the reports above
are the runs that followed it. It is committed unchanged rather than dropped — a measurement set
that only contains its good runs is not evidence.

## What the measurement says

1. **Four concurrent live sessions at the plane's own default profile are clean**: 30 fps and
   ~8 Mbps each, 32 Mbps aggregate, zero stalls over a 30 s hold, on a host already at load 43.
2. **A fifth session cannot be opened**: at five requested, four open and one is refused with a
   named `mirror-push-server` timeout after three attempts. At eight requested, still exactly four
   open.
3. **The bound is session admission, not streaming capacity**: the sessions that did open carried
   full frame rate and full bit rate while further sessions were being refused, so what the fleet
   runs out of first is the number of captures it can start, which is what a session capacity is.
4. **The default is therefore 4 sessions with 1 kept for the operator's own frame — 3 live
   tiles — and this card confirms that number rather than changing it.** It was previously a
   default nobody had measured; it is now a measured one, and a deployment that wants a different
   number states it and can read back what it resolved to.
5. **The tile grid is not what this bound is about.** 3 tiles at Medium cost ~3.6 Mbps of the
   32 000 kbps default transport budget, so the grid is bounded by session admission here; the
   transport bound is what stops a *more expensive* preview level from oversubscribing the same
   path, and that is a separate, stated budget.

## Not exercised this run

- N = 10 and N = 16: the fleet lists 10 online devices, five more sessions than the fleet would
  open, so no clean 10- or 16-point exists on this hub to measure. The 8-point already fails to
  open more than 4.
- Holds longer than 70 s (the sustained point is 1 session).
- Bounded preview levels (Low 0.5 / Medium 1.2 / High 2.5 / Extra 6 Mbps): the probe opens the
  plane's default native stream, so the measured cost per session is its upper bound.
- Any host or path other than this Mac and this wireless hub; a deployment on a different host
  must measure its own fleet rather than read this file's number as universal.
- Device-side encoder starvation as distinct from session admission: a refused session is refused
  before any encoder runs, so this run cannot separate the two.
- USB transports and devices other than the ten online here.

## Reproducing

```text
DRIFT_MIRROR_PROBE=1 \
DRIFT_MIRROR_PROBE_ADB=/opt/homebrew/bin/adb \
DRIFT_MIRROR_PROBE_SERVER=/opt/homebrew/share/scrcpy/scrcpy-server \
DRIFT_MIRROR_PROBE_SERIALS=<ten host:port serials> \
DRIFT_MIRROR_PROBE_COUNTS=1,4,5,8 \
DRIFT_MIRROR_PROBE_HOLD_SECONDS=30 \
DRIFT_MIRROR_PROBE_OUT=/abs/report.json \
go test ./internal/edge/scrcpy -run TestLiveStreamConcurrencyProbe -v -count=1
```

The reports in `probe-reports/` are the exact output of that command for the runs above. The probe
masks every serial as a SHA-256 prefix itself; hub addresses that appear inside refusal reasons
were masked to `<hub-address-masked>` when the reports were committed (the refusal still names the
device it belongs to, by its masked serial). Nothing else in the reports was edited.
