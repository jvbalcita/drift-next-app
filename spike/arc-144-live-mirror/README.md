# ARC-144 Part A — live-mirror media path, measured (throwaway spike)

This is a measurement rig, not a feature. It is not wired into `cmd/control-plane`,
it is not in the console, and it lives in its own Go module (`spikelocal/arc144`) so
the shipped module graph is untouched.

It answers one question, with numbers instead of an assertion: **what does the
pion/webrtc H.264 path that ARC-143 plans actually cost on this machine, and what
does the same video cost over MSE?**

The measurements, their methods, and the exclusions are in [FINDINGS.md](FINDINGS.md).
Raw per-run aggregates are in `results/report-*.json` and the table in `results/summary.md`.

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
| `tools/run-part-a.sh` | one reproducible run end to end |

## The one dependency

`github.com/pion/webrtc/v4 v4.2.20` (with `github.com/pion/rtp v1.10.5` and
`github.com/pion/ice/v4 v4.4.2`) — required to serve the H.264 track the ARC-143 plan
is built on. It is required only by this nested module, so `drift.local/drift-next`'s
`go.mod`/`go.sum` are unchanged. The pion set is pinned to the versions webrtc v4.2.20
declares: `go mod tidy` alone resolves `pion/ice` to v4.4.3, which pulls
`pion/transport/v5` and does not compile against webrtc v4.2.20.

Part B (device, scrcpy control socket, glass-to-glass) is deliberately not
implemented — it needs the owner at the machine.
