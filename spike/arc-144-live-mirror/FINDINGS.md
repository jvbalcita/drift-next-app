# ARC-144 Part A — measured numbers and what they mean

Throwaway spike. The question it exists to answer: **does the pion/webrtc H.264 path
ARC-143 plans actually deliver, and at what latency, on this fleet?** Everything below
is measured, not asserted. Part B (device, scrcpy control socket, glass-to-glass) was
**not** run — it needs the owner at the machine.

## Method

One synthetic H.264 Annex-B source (1280x720, 30 fps, 600 frames = 20 s, x264 baseline
constrained, level 3.1, keyframe every 30, one slice per picture, ~131 kbit/s) is
generated on this machine and released by a single real-time pacer at `t0 + i/30 s`.
In the same browser page, at the same instants, it is delivered twice:

1. **RTP** — pion/webrtc v4.2.20 `TrackLocalStaticSample` H.264 track (RFC 6184,
   packetization-mode 1) → `<video srcObject>`.
2. **MSE** — the *same access units* muxed to fragmented MP4 with `ffmpeg -c copy`
   (no re-encode) → `SourceBuffer`, appended fragment by fragment as they are pushed.

Every frame carries its own index burned into its pixels as a 20-cell barcode
(16 index bits + 4 sync bits). The browser reads the presented frame's index back out
of the decoded pixels in a `requestVideoFrameCallback` and records the browser's own
`expectedDisplayTime` for that frame. The sender records the time each access unit was
handed to the RTP stack. The two logs join **on the frame index**, so:

> latency = (the browser's expected display time for source frame *i*) − (the moment the
> Go hop released source frame *i*)

Both processes read the same system clock; the measured offset between them was
**0.02 ms** (lowest-RTT of 25 NTP-style samples, best RTT 0.30 ms), so no clock
correction is applied.

**The instrument is verified before any number is taken** (`results/verification.log`):
all 600 generated frames decode back to their own index after x264 encode and ffmpeg
decode (600/600), the access-unit splitter's count matches `ffprobe -count_frames`
(600), and the fragmented MP4 covers exactly the 600 access units of the same file.

## Results

600 frames @ 30 fps, 1280x720, loopback, one page rendering both transports at once.
`p50` is the median per-frame latency in ms; `delta` is MSE p50 − RTP p50 for the same run.

| run | browser | fragments | RTP p50 | RTP p95 | MSE p50 | MSE p95 | delta | RTP frames dropped / decoded | frames presented (RTP / MSE) |
|---|---|---|---|---|---|---|---|---|---|
| `on` | headless | 100 ms | 20.7 | 21.4 | 120.6 | 121.3 | **99.9** | 4 / 600 | 556 / 557 |
| `on2` | headless | 100 ms | 18.3 | 19.2 | 118.2 | 119.1 | **99.9** | 51 / 600 | 448 / 480 |
| `on3` | headless | 100 ms | 28.2 | 29.1 | 128.0 | 129.1 | **99.8** | 3 / 600 | 500 / 490 |
| `on4` | headless | 100 ms | 40.1 | 42.6 | 106.7 | 109.3 | 66.6 | 15 / 600 | 510 / 511 |
| `off` | headless, **pixel probe disabled** | 100 ms | — | — | — | — | — | 3 / 600 | 507 / 462 |
| `hw` | headed | 100 ms | 49.2 | 111.0 | 76.9 | 88.6 | 27.7 | 13 / 600 | 460 / 451 |
| `hw2` | headed | 100 ms | 46.6 | 57.8 | 80.9 | 91.5 | 34.3 | 3 / 600 | 403 / 408 |
| `f33` | headless | 33 ms | 36.8 | 38.4 | 104.1 | 105.1 | 67.3 | 72 / 600 | 447 / 484 |

Machine: macOS 26.6.2 / arm64, Chrome 152.0.7977.84, Go 1.27.1, ffmpeg 9.0.1. Load
average was ~10-11 throughout from unrelated processes (a node service, Codex,
WindowServer), which is why the RTP p50 moves between runs; the runs with the least
disturbance are `on`, `on2`, `on3`.

### What the browser actually decoded (run `on`)

- negotiated codec (browser `codec` stats): **`video/H264`, clockRate 90000, payload type 102**;
  inbound codec id `CIT01_102_level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f`.
  The answer's `m=video` line offers H.264 payload types only (`102 104 108 114 116 39 41 43 118 120`),
  so H.264 was forced — see surprise 3 for what that fmtp is *not*.
- `framesReceived 600, framesDecoded 600, framesDropped 0, packetsLost 0, jitter 0`,
  20 keyframes decoded, `videoWidth/Height 1280x720`.
- `MediaSource.isTypeSupported("video/mp4; codecs=\"avc1.42c01f\"") = true`;
  `MediaCapabilities.decodingInfo` reports `supported/smooth/powerEfficient = true` for
  both `webrtc` and `media-source`; `VideoDecoder.isConfigSupported('avc1.42c01f')` true,
  including with `hardwareAcceleration: 'prefer-hardware'`.
- The MSE consumer accepted the fragments only under that contentType, and the barcode
  read back from its pixels proves those exact decoded pictures are the source frames.

### Where the 20 ms goes (run `on`, from the browser's own stats + the sender log)

| component | per frame |
|---|---|
| Go hop: packetise + hand to the RTP stack (`WriteSample`) | **0.18 ms mean, 0.11 ms p50**, max 5.0 ms |
| pacing lateness of the source itself | 0.98 ms mean, max 5.4 ms |
| browser receive jitter buffer (`jitterBufferDelay / emitted`) | **7.65 ms** |
| H.264 decode (`totalDecodeTime / framesDecoded`) | **1.48 ms** |
| browser processing delay (`totalProcessingDelay / framesDecoded`) | **9.31 ms** |
| **measured end to end (barcode join)** | **20.7 ms p50** |

7.65 + 1.48 + 9.31 = 18.4 ms of the 20.7 ms measured; the residual ~2 ms is the
packetisation/loopback hop and the compositor's display estimate. 689 RTP packets
carried 600 access units — 552 of 600 access units fit in a single packet at the
1200-byte MTU pion uses, 48 were fragmented (mean 2.85 packets, largest access unit
3875 B). MSE-side, fragment push → browser arrival was 0.19-2.32 ms p50 across runs.

## Verdict on Part A

1. **The pipeline is sound with no device and no media server.** pion/webrtc serves a
   real H.264 track, Chrome decodes it as H.264 at 1280x720 with 600/600 frames decoded
   and zero packets lost, and the *same bytes* reach a second consumer over MSE (the
   fragmented MP4 is `-c copy` of the same access units, verified to cover exactly them).
   Nothing here suggests pion is a poor fit, and mediamtx/TURN are not needed for this hop.
2. **The added latency of the Go hop is ~20 ms** (18.3-28.2 ms p50 in the three least
   disturbed headless runs; 40.1 ms in a loaded one; 46.6-49.2 ms when the browser is a
   real window on a real display).
3. **This is not glass-to-glass.** Encode and capture are excluded: the source is a
   pre-encoded file. What is included is file read → H.264 parse → RTP packetisation →
   UDP/loopback → receive jitter buffer → H.264 decode → compositor scheduling, measured
   as the time until the browser expects that exact frame to be displayed. Input
   injection, device capture, hardware encode, USB/Wi-Fi transport and the console's own
   webview are all outside this number.
4. **MSE on identical bytes costs ~100 ms more** at 100 ms fragments (99.9 ms delta in
   three separate runs) and ~67 ms more at 33 ms fragments. As a low-latency transport
   it cannot match RTP.

## The single biggest latency contributor

The browser's receive-side queueing, not the Go code and not the decode: **jitter buffer
(7.65 ms/frame) + processing delay (9.31 ms/frame) ≈ 17 ms of the 20.7 ms**, against
**1.48 ms of actual H.264 decode** and **~0.2 ms of Go-side packetisation and send**.
For the MSE path the dominant term is different in kind — fragment granularity plus the
player's buffer policy — but the same order (~100 ms).

## Surprises that bear on ARC-143

1. **The instrument can dominate the measurement.** The first version read the barcode
   back 1:1 (20k pixels/frame); that readback stalled the pipeline, dropped 12% of frames
   and inflated the measured "latency" from ~20 ms to ~224 ms. Drawing the strip into a
   20x1 canvas (80 bytes) fixed it, and an instrument-off control run then showed the
   same drop rate as the probe-on run (3 vs 4 dropped). Anyone repeating this measurement
   must keep the probe under ~0.2 ms/frame; the number it reports otherwise is its own cost.
2. **Fragment size is not the MSE lever the plan might assume.** Going from 3-frame
   (100 ms) to 1-frame (33 ms) fragments moved MSE p50 from ~120 ms to ~104 ms: with
   1-frame fragments the player simply buffered ~2 of them. The chunking cost is a
   property of the player's buffer policy as much as of the fragment duration.
3. **Registering H.264 does not pin the fmtp the answer advertises.** `pion/webrtc`
   v4.2.20 answers with the *remote's* H.264 entries (this answer carried ten payload
   types, all H.264, which is what registering only H.264 buys: no VP8/VP9 can be
   negotiated). The payload type actually used, 102, advertises
   `profile-level-id=42001f` — Chrome's own offer entry — while this stream's SPS says
   `42c01f` (constrained baseline, level 3.1, what x264 `-profile:v baseline` produced).
   **Chrome decoded it correctly anyway**: 600/600 frames decoded, and the barcode read
   out of the decoded pixels proves the pictures are the source frames. Two consequences
   for ARC-143: (a) the fmtp's profile-level-id is not a signal the browser enforces, so
   do not rely on it to describe the stream; and (b) a real scrcpy stream arrives with
   whatever profile the *device's* hardware encoder chose (often Main or High), so the
   answer's fmtp will not describe it either — if the plan ever depends on profile-level-id
   (hardware decode eligibility, capabilities checks), read the SPS, as this spike does,
   rather than the negotiated fmtp line.
   One related trap: `RTCRtpReceiver.getParameters().codecs` lists every codec on the
   m-line (VP8 and VP9 appear first here) and is not the negotiated codec; the browser's
   `codec` stats entry is.
4. **A real window costs real milliseconds.** Headed Chrome measured roughly 2.3x the
   headless hop (46.6-49.2 vs 18.3-28.2 ms p50) with a much longer tail (p95 up to
   111 ms). The console is a real window; treat the headed figure as the console's
   condition and the headless one as the pipeline floor.
5. **Two 720p streams in one page is already at the compositor's limit.** In both
   transports ~7% of decoded frames never reached the compositor (the browser decoded
   600/600 and dropped 0-4 at the video element). That is a harness artifact of measuring
   both transports in one page, but it means per-frame numbers exist only for presented
   frames, and it is a reminder that this is a rendering-budget question as much as a
   transport question.
6. **A second MSE consumer perturbs the RTP path.** In the `f33` run, appending a
   fragment every 33 ms in the same page raised RTP drops to 72/600 and its p50 to
   36.8 ms. Not a defect in either transport, but it is why only one transport should be
   the measured one in any confirmation run.

## What is deliberately not done

- **Part B in full.** No device was touched: no scrcpy session, no control-socket tap, no
  `adb input`, no glass-to-glass, no input round-trip. The owner-gate in the brief was
  respected; the phones visible on adb were not used.
- No encode or capture latency (file source), so the glass-to-glass verdict for the
  ARC-143 bar of <100 ms is **not** established here — only the browser-hop half of it.
- No mediamtx, no TURN, no external media server (loopback only).
- The console's webview (Tauri/WebKit) was not exercised; this is Chrome 152.
- Records, not tuning: nothing in this spike is wired into `cmd/control-plane` or the
  console, and no dependency was added to the product module graph.

## What this gives the ARC-143 decision

- The collapse-the-middle plan is viable for the hop measured: ~20 ms on the pipeline,
  ~50 ms in a real window, with no media server and no loss on loopback.
- Against a <100 ms glass-to-glass bar, that leaves roughly 50-80 ms for device capture,
  hardware encode and USB/Wi-Fi transport — plausible for scrcpy's encoder, but
  **unproven**, and it is the whole of Part B's job to measure it.
- Against a <50 ms input round-trip bar, nothing here is evidence: input was not touched.
- MSE should not be the low-latency transport (≈ +100 ms on identical bytes); if it is
  kept at all it is for a fallback/replay path, not for "feels physically present".

## Reproducing

```bash
cd spike/arc-144-live-mirror
tools/run-part-a.sh on   1 headless 100000   # primary condition
tools/run-part-a.sh off  0 headless 100000   # instrument-off control
tools/run-part-a.sh hw   1 headed   100000   # real-window condition
tools/run-part-a.sh f33  1 headless  33000   # fragment-size sensitivity
```

Committed evidence: `results/verification.log` (instrument validation + environment),
`results/report-*.json` (per-run aggregates, per-frame series omitted for size),
`results/summary.md` (the table above). The raw per-frame logs
(`results/send-*.json`, `results/client-*.json`) are not committed — they are ~1 MB and
regenerated by the commands above; `cmd/analyze -per-frame` prints the series.
