# ARC-144 Part A — measured numbers and what they mean

Throwaway spike. The question it exists to answer: **does the pion/webrtc H.264 path
ARC-143 plans actually deliver, and at what latency, on this fleet?** Everything below
is measured, not asserted.

Part A (the media path, no device) is the first section. **Part B — a live device,
scrcpy's own video socket, scrcpy's control socket for input, glass-to-glass and input
round-trip — is measured in the second section, and it changes the plan**: the browser
hop is cheap (~1-3 ms), the device half is the whole cost, and the device's encoder
emits one IDR per session, which makes a naively forwarded stream undecodable.

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


---

# ARC-144 Part B — the live measurement (device, scrcpy, glass-to-glass, input)

Part A measured the media path with no device and answered the browser half of the
question. Part B answers the half Part A could not, on a real lab unit, with scrcpy's
own video socket and scrcpy's own control socket.

## B0. What was run

One device: **SM-G9750 at `192.168.1.104:5555`**, Android 12 (SDK 31), render size
1080x2280, a lab unit from ARC-67's authorized set. All seven named units sit at load
13-18 from unrelated lab work; `.104` was used for every run and its load is reported
with each one. Bounds observed: lab units only, no production data, no credentials,
`DRIFT_SYNC_LIVE` unset, nothing powered off, `tcpip` never used.

The rig (`cmd/live`) does the whole session itself — it pushes `scrcpy-server` (4.1),
opens the reverse tunnel, launches `app_process`, and owns both sockets:

* **video**: `send_stream_meta=true send_frame_meta=true`, i.e. the encoded frame size
  arrives in the session packet and every packet is one encoded frame with its own
  PTS. (Part A's rig had to split a file into access units; here the device already
  delimits them, so a frame is forwarded the instant it arrives and nothing waits for
  the next one.)
* **control**: input as a 32-byte `INJECT_TOUCH_EVENT` with `action_button`/`buttons`
  set. `adb shell input tap` was never used — it spawns a process on the device and
  costs 100-300 ms, which is larger than the budget being measured.

The device displays a page served by this rig over the LAN (`cmd/live` binds the LAN
address). That page paints the millisecond wall clock as a 60-cell black/white strip at
a fixed place in its viewport, flips a bit when a touch arrives, and reports its own
clock to the host. The host browser runs the same probe style as Part A: it reads that
strip **out of the decoded video** and records, per presented frame, the device clock
carried in that frame's pixels and the browser's own expected display time.

```
glass-to-glass = expectedDisplayTime(frame N) − device_clock_in_pixels(frame N) − skew
input (device) = device_clock_at_touch_handler − host_clock_at_the_control_write − skew
input (video)  = expectedDisplayTime(first frame carrying that tap) − the same write
```

The device-half instrument is validated without any video at all: `cmd/clockprobe`
takes a screenshot, finds the strip's top edge (the one place a dark pixel sits above a
full white cell), and decodes the clock from it. It located the strip at (0, 569) in
every run, one cell = 32 device pixels, a 32-row white run above it, monotonic clock
reads, ~550 ms per screencap.

## B1. Clock skew — three estimators, and why only one is trustworthy

| estimator | live5 | live6 | live7 | live8 |
|---|---|---|---|---|
| device page, NTP-style round trip (25 exchanges, best RTT) | −46.62 | −51.33 | −51.67 | −52.57 |
| host, min one-way delay over ~200 clock posts | (~−46, see note) | −48.00 | — | — |
| `adb shell date +%s%N`, midpoint of 25 round trips | −71.22 | −70.97 | −70.4 | — |

(`live5` and the runs before it had a field-naming bug that zeroed the min-delay
estimator in the *analysis*, so its reported value is withheld here; the raw heartbeat
posts from that run give −46.1 ms, which agrees with its NTP estimate within 0.5 ms.)

All values are host − device, so **the device's clock runs ~50 ms ahead of the host's**.
The two page-based estimators agree within 4 ms. The adb one is 20 ms further out and is
the one to distrust: a shell command's own spawn and execution time sits *inside* the
round trip (57-101 ms round trips on these loaded units, spread 14-30 ms), so the
midpoint correction places the device's timestamp later than it was. A run that had used
the adb estimator alone would have over-corrected every latency by ~20 ms and moved the
verdict below.

## B2. Glass-to-glass, per frame

Four runs, 30 s each, 8 taps, the same device and geometry. `frames` are presented
frames whose pixels yielded a decodable clock — **the strip decoded on every single
presented frame in every run (decode rate 1.000, 0 unreadable)**.

| run | engine | frames | p50 | p95 | p99 | mean | min | max | frames < 0 |
|---|---|---|---|---|---|---|---|---|---|
| live5 | Chrome 152, headless | 892 | **127.9** | 172.6 | 212.9 | 98.9 | −92.4 | 338.0 | 36 |
| live6 | Chrome 152, headless | 835 | **96.0** | 134.6 | 209.6 | 98.9 | −137.9 | 490.4 | 29 |
| live7 | Chrome 152, real window | 737 | **134.3** | 260.1 | 313.9 | 217.8 | −92.1 | 912.9 | 22 |
| live8 | **Safari / WebKit 26.6.2** | 1396 | **235.6** | 275.6 | — | 217.8 | 80.6 | 467.6 | 0 |

(An earlier run, `live4`, measured 43.1 ms p50 *raw, uncorrected* — it predates the
skew fix below and is not comparable; it is listed here only so the record is complete.)

**The frames below zero are the instrument's own noise and are not hidden.** They mean
the pixel readback caught the frame *after* the one the callback reported; that biases
those samples low by about one encoder interval (18 ms). It bounds this join at roughly
one frame and it is why the p95/p99 above are the numbers to plan against. The WebKit
run had none — with a real window and a slower jitter buffer the readback and the
callback stay aligned — and it is the cleanest distribution of the four
(clock deltas 5-133 ms, 1396 distinct values, none non-monotonic).

## B3. Where the time goes

Same runs, decomposed. p50 in ms.

| component | live5 | live6 | live7 | live8 | how measured |
|---|---|---|---|---|---|
| Go hop (receive → handed to the RTP stack) | 0.1 | 0.1 | 0.1 | 0.2 | per frame, in the hop (max 3.3 / 9.7 / 7.1 / 8.8) |
| browser jitter buffer | 2.12 | 0.26 | 2.28 | 9.73 | `jitterBufferDelay / emitted` |
| H.264 decode | 0.26 | 0.26 | 0.24 | 0.44 | `totalDecodeTime / framesDecoded` |
| browser processing delay | 2.43 | 0.51 | 2.62 | 10.19 | `totalProcessingDelay / framesDecoded` |
| **everything else (device half)** | **~123** | **~95** | **~129** | **~215** | residual |

The hop emits 4577-4816 RTP packets per run, the browser receives exactly that many,
`packetsLost` is 0 and the sender's sequence has no gaps: **pion and the loopback
transport cost nothing measurable** (the same thing Part A found from the other side).

So the device half — its own render, capture, hardware encode and the LAN hop, which
this rig cannot separate further — is **93-131 ms of a 96-134 ms budget in Chrome and
~215 ms of 236 ms in WebKit**. The encoder's own PTS advances every 18.1-18.4 ms
(≈54 fps), and the strip's clock advances in the same 18-19 ms steps, so the device is
sampling at ~54 fps and each frame's pixels are already an appreciable fraction of the
budget old when they leave the phone.

## B4. Input round-trip

8 taps injected per run over the control socket, all 8 acknowledged by the device in
every run.

| run | engine | p50 | p95 | per tap |
|---|---|---|---|---|
| live5 | Chrome headless | 15.7 | 23.8 | 23.8 13.1 21.9 15.7 13.7 17.2 12.1 16.4 |
| live6 | Chrome headless | 10.4 | 36.6 | 20.6 10.4 11.2 9.8 36.6 12.6 9.3 7.9 |
| live7 | Chrome real window | 10.7 | 24.5 | 24.5 11.4 12.9 7.7 9.1 16.3 10.7 9.4 |
| live8 | Safari / WebKit | 12.7 | 29.7 | — |

These are write → the device page's touch handler, skew-corrected: the tunnel, the
scrcpy controller thread, Android's input dispatch and the page's own handler, and
**nothing of the return video path**. The same taps seen the other way round — the first
video frame that carries the tap's counter — are p50 85.5 / 92.1 / 127.4 / 203.8 ms,
i.e. the input plus a full return trip.

Write cost per tap: 0.02-0.31 ms for the down/up pair.

## B5. Verdict against ARC-143's two claims

**Claim 1 — glass-to-glass under 100 ms: NOT met as written.**
The pion hop is not the problem (0.1 ms), the browser's own receive path is not the
problem (1-3 ms in Chrome), and the packet path loses nothing. The measured
glass-to-glass is 96-134 ms in Chrome across three runs and **235.6 ms in WebKit, which
is the engine the console actually ships in**. Best case 96.0 ms is at the bar; the
console's condition is 1.4-2.5x over it. The numbers are not close enough to the bar for
a small tuning pass to fix, and the fix is not in the browser half.

**Claim 2 — input round-trip under 50 ms: MET, comfortably.**
p50 10.4-15.7 ms, p95 ≤ 36.6 ms, all taps, every run, through scrcpy's control socket.
The control socket is a good path and it is fast. Including the return video path the
observed round trip is 85-128 ms in Chrome, which is the number an operator's own eyes
would experience and which is dominated by the video half, not by input.

## B6. The single biggest latency contributor

**The device's own capture-and-encode pipeline, as an inseparable block with its render
and the LAN hop.** With the Go hop at 0.1 ms and Chrome's receive bar at ~1-3 ms,
~93 ms of live6's 96 ms and ~123 ms of live5's 128 ms are device-side. Nothing in this
rig can split that further; what it can say is that it is not transport, not pion, not
the decoder and not the browser's queueing, and that the device was carrying load 17-18
from unrelated work throughout.

## B7. Surprises that change ARC-143's plan

1. **The device's screen encoder emits exactly ONE IDR per session, and SPS/PPS exactly
   once.** This is the finding that broke every early run: the browser received all 1397
   frames of a 25 s stream, decoded **none**, and stayed at `readyState 0` — a black
   mirror that looks like a working connection in every counter. The same bytes
   re-encoded with a keyframe every second decoded 330/330. Three consequences for the
   plan: (a) ask the device's encoder for periodic IDRs — scrcpy's
   `video_codec_options=i-frame-interval:int=2` was verified working on this fleet
   (5 IDRs in 10 s instead of 1); (b) the hop must **re-attach SPS/PPS to every IDR it
   forwards**, because the device never repeats them and pion's payloader emits its
   one STAP-A only once; (c) a real implementation should also cache the last IDR and
   send it to a viewer that attaches late, instead of making it wait for the next one.
   This is the "device half is the risk" that Part A predicted, in its sharpest form.
2. **A failed decode is silent.** No error, no exception, no console message: a live
   stream that can never decode is indistinguishable from a working one until you read
   the pixels. Any product wiring must assert on decoded frames, not on connection state.
3. **The device clock is ~50 ms ahead of the host, and the obvious measurement of that
   is wrong.** The adb round trip reads 20 ms further out than two independent
   page-based estimators. Anyone repeating this must measure the skew from inside the
   page (or accept a 20 ms systematic error in every number).
4. **The instrument has a one-frame join uncertainty.** 3-4% of Chrome samples landed
   below zero; a pixel readback can catch the next frame. Report p95/p99 alongside p50,
   or the tail disappears.
5. **Chrome on Android paints its own toolbar over the top of the layout viewport**
   (~257 device px of status bar plus toolbar), so a marker at the top of the page has
   its first rows covered and cannot be located exactly; the page was moved down 120 CSS
   px. Fullscreen from an injected touch never engages (`Permissions check failed`), so
   that escape route does not exist.
6. **A browser tooltip swallowed the first injected tap** (Chrome's tab-group hint), and
   a tap-count join then mislabelled every remaining tap by exactly one interval
   (2.5 s, i.e. "input latency 2.5 s"). Joining by time, with each device reaction used
   once, is robust to both a swallowed and a dropped tap. Part A's lesson — that the
   instrument can dwarf the measurement — has a sibling: **the instrument can also
   mislabel the measurement, silently**.
7. **The device's stream is Baseline level 5.0 (`420032`) while the SDP still advertises
   Chrome's own entry (`42001f`, level 3.1), and Chrome decodes it anyway** — Part A's
   surprise 3 holds on a real device stream.
8. **The fleet is busy.** Every named lab unit carried load 13-18 from unrelated work
   for the whole session. These numbers are honest for a loaded fleet; an idle unit
   would likely be better, and that is itself a planning question.

## B8. Not done, and exclusions

* **The Tauri shell itself was not exercised.** B4 used Safari 26.6.2 — the same WebKit
  engine family the Tauri shell embeds on macOS, but not the console's own webview host,
  and not the console's own rendering. Treat 235.6 ms as the engine's number, not as the
  console's.
* **Capture and encode are not separated** from the device's render and the LAN hop:
  the device half is one residual. Nor is the device's own rAF→glass delay separated;
  it is *included*, so every number above is conservative in that direction.
* **One device, one geometry, one direction.** `.104` only, landscape untouched, no
  audio, no multi-device, no MSE (Part A covers MSE on identical bytes).
* No mediamtx, no TURN, no media server of any kind; the hop is pion and loopback.
* The taps are injections into a lab unit's page (authorized, owner at the machine);
  no other device command was used as input.

## B9. Reproducing

```bash
cd spike/arc-144-live-mirror

# the device-side instrument on its own: screenshot -> locate strip -> decode clock
go run ./cmd/clockprobe -serial 192.168.1.104:5555 -samples 5

# one live run (30 s, 8 taps): opens the page on the device, streams, taps, analyses
SERIAL=192.168.1.104:5555 DURATION=30s TAPS=8 tools/run-part-b.sh b1 headless
SERIAL=192.168.1.104:5555 DURATION=30s TAPS=8 tools/run-part-b.sh b2 headed
SERIAL=192.168.1.104:5555 DURATION=30s TAPS=8 tools/run-part-b.sh b3 webkit

# replay a captured stream through the same hop with no device attached
tools/run-replay.sh r1 results/media/live-<label>.h264 60 569

# all runs into one table
python3 tools/summarise-part-b.py
```

Committed evidence: `results/report-live-*.json` (per-run aggregates: every percentile,
the skew reports, the per-tap table, the browser's own decode counters),
`results/summary-part-b-*.md` (one per run), `results/summary-part-b-table.txt`. The raw
per-frame run logs (`results/live-*.json`, ~7 MB each), the captured streams
(`results/media/*.h264`) and the screenshots (`results/diag/`) are not committed — they
are regenerated by the commands above.

## B10. Tests that hold the rig together

`go test ./internal/...` (run with `ARC144_CAPTURE` set to a captured stream for the
stream-dependent ones):

* `internal/scrcpy`: the control message byte-for-byte against scrcpy's own serializer,
  and the frame-header rules including the polarity trap — **the media-packet flag is
  bit 63, and it is the *session* flag; a media packet has the top bit clear**, which is
  the opposite of what `doc/develop.md`'s prose reads like.
* `internal/clockcode`: round trip, single-cell corruption refused, strip located under
  toolbar noise, no strip in a blank screen, a half-scale strip refused, a stale clock
  refused.
* `internal/livepeer`: the payloader covers every access unit and leaves no start code in
  a payload, and **a full sender→receiver loopback in one process** — the check that
  ended the "the browser got a third of the packets" dead end by proving the hop
  delivered 2410/2410 packets and reassembled every access unit.
