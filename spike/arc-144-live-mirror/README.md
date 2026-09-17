# ARC-144 — live-mirror media path and live-device latency, measured (throwaway spike)

This is a measurement rig, not a feature. It is not wired into `cmd/control-plane`,
it is not in the console, and it lives in its own Go module (`spikelocal/arc144`) so
the shipped module graph is untouched.

It answers two questions, with numbers instead of assertions: **what does the
pion/webrtc H.264 path that ARC-143 plans actually cost (Part A), and what does a real
device cost end to end — glass-to-glass and input round-trip through scrcpy's own
sockets (Part B)?**

The measurements, their methods, and the exclusions are in [FINDINGS.md](FINDINGS.md).
Raw per-run aggregates are in `results/report-*.json` and `results/report-live-*.json`;
the tables are `results/summary.md` and `results/summary-part-b-table.txt`.

## What it does

One real-time pacer releases the same generated H.264 access units to two consumers
at the same instants:

- **RTP** — a `TrackLocalStaticSample` H.264 track (RFC 6184) over pion/webrtc, into
  a `<video>` fed by `srcObject`. This is the ARC-143 plan.
- **MSE** — the same access units muxed to fragmented MP4 with `-c copy` (no
  re-encode) and appended to a `SourceBuffer` on 1-3 frame fragments, into a second
  `<video>`.

Every frame of the synthetic source carries its own index burned into the pixels as
a 20-cell barcode, so the browser can report which source frame it is displaying by
reading the decoded pixels — and the two logs (sender per-frame send times, browser
per-frame display times) join on that frame index. No clock is shared between the
two processes beyond the machine clock both are already reading.

## Running it

```bash
tools/run-part-a.sh <label> [readbackEvery] [headless|headed] [fragmentMicroseconds]

tools/run-part-a.sh on  1 headless 100000   # barcode probe on, 100 ms fragments
tools/run-part-a.sh off 0 headless 100000   # instrument-off control
tools/run-part-a.sh hw  1 headed   100000   # headed browser (real display compositing)
tools/run-part-a.sh f33 1 headless  33000   # 1-frame fragments
```

The script generates the source media (never committed), verifies the measurement
instrument, runs one server and one browser, and writes:

| file | contents |
|---|---|
| `results/send-<label>.json` | per-frame send record: scheduled time, hand-off time, packetisation |
| `results/client-<label>-<transport>.json` | browser results: per-frame observations, WebRTC stats, capabilities |
| `results/report-<label>.json` | the joined, aggregated measurement |
| `results/summary.md` | all runs side by side |

## Part B — the live device

Part B runs against one lab SM-G9750 and measures the thing Part A could not: the
device's own screen, encoded by scrcpy's hardware encoder, forwarded through the same
pion hop, decoded and read back in the browser — with input injected through scrcpy's
control socket.

```bash
# device-side instrument alone: screenshot -> locate the clock strip -> decode it
go run ./cmd/clockprobe -serial 192.168.1.104:5555 -samples 5

# one live run: opens the page on the device, streams, taps, analyses
SERIAL=192.168.1.104:5555 DURATION=30s TAPS=8 tools/run-part-b.sh b1 headless
SERIAL=192.168.1.104:5555 DURATION=30s TAPS=8 tools/run-part-b.sh b2 headed
SERIAL=192.168.1.104:5555 DURATION=30s TAPS=8 tools/run-part-b.sh b3 webkit

# replay one captured stream through the same hop, no device attached
tools/run-replay.sh r1 results/media/live-b1.h264 60 569

# every Part B report in one table
python3 tools/summarise-part-b.py
```

There is no camera, so the clock is one the device paints into its own pixels: a
60-cell black/white strip carrying the millisecond wall clock, plus a tap reaction bit.
The browser probe reads that strip out of the decoded video (240 bytes per frame) and
joins it to the browser's own expected display time; the host locates the strip exactly
once, from a screenshot, with `cmd/clockprobe`. Three independent clock-skew estimators
are reported with every run, and the input round-trip is measured twice: at the device's
touch handler and as seen in the returned video.

Part B adds no dependency: it speaks scrcpy's protocol itself (`internal/scrcpy`),
extends the same pion peer (`internal/livepeer`), and reuses the same nested module.

## Layout

| path | role |
|---|---|
| `cmd/genframes` | synthetic yuv420p frame source with a barcode per frame, piped into x264 |
| `cmd/verifyframes` | proves each frame's barcode survives x264 encode + ffmpeg decode |
| `cmd/verifyfmp4` | proves the fragmented MP4 covers exactly the same access units |
| `cmd/spike` | the server: pacer, pion RTP track, MSE fragment stream, results collection |
| `cmd/analyze` | joins the sender and browser logs into the latency distributions |
| `cmd/summarize` | turns per-run reports into the summary table |
| `internal/barcode` | the frame-index mark: geometry, encode, decode |
| `internal/annexb` | access-unit splitter (a new picture starts at `first_mb_in_slice == 0`) |
| `internal/fmp4` | fragmented-MP4 parser: init segment plus one record per moof+mdat |
| `tools/drive.mjs` | Chrome DevTools driver (Node, no dependencies) |
| `tools/run-part-a.sh` | one reproducible run end to end (Part A) |
| `cmd/live` | Part B rig: scrcpy session, LAN page server, pion hop, taps, run log |
| `cmd/rtpreplay` | replays a captured stream through the same hop, no device |
| `cmd/clockprobe` | locates and decodes the device's clock strip from a screenshot |
| `cmd/analyze-live` | joins the run log and the probe into Part B's numbers |
| `internal/scrcpy` | scrcpy 4.1 client protocol: server launch, video/control sockets |
| `internal/livepeer` | the pion peer for a live stream, its RTP counter, parameter sets |
| `internal/clockcode` | the device clock's codec and locator (Go half of the page's JS) |
| `internal/webui` | the two pages: the browser probe and the device's clock page |
| `internal/screencap` | screenshot → luma, for locating the strip |
| `tools/run-part-b.sh` | one live run end to end |
| `tools/run-replay.sh` | one replay run end to end |
| `tools/summarise-part-b.py` | every Part B report in one table |

## The one dependency

`github.com/pion/webrtc/v4 v4.2.20` (with `github.com/pion/rtp v1.10.5` and
`github.com/pion/ice/v4 v4.4.2`) — required to serve the H.264 track the ARC-143 plan
is built on. It is required only by this nested module, so `drift.local/drift-next`'s
`go.mod`/`go.sum` are unchanged. The pion set is pinned to the versions webrtc v4.2.20
declares: `go mod tidy` alone resolves `pion/ice` to v4.4.3, which pulls
`pion/transport/v5` and does not compile against webrtc v4.2.20.

Part B was run with the owner at the machine, on the lab fleet's authorized units.
Its headline: **input round-trip is comfortably inside the bar (p50 10-16 ms) and
glass-to-glass is not (p50 96-134 ms in Chrome, 236 ms in WebKit) — and the device half
is the whole cost.** The device's encoder emits a single IDR per session, which is the
finding that broke the first runs and the first thing ARC-143 must design around; see
FINDINGS.md, "Surprises that change ARC-143's plan".
