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

---

# ARC-145 — the device encoder half, the shipping webview, and ARC-131's frame engine

ARC-144 localised the cost to the device half and said so in one sentence: *~93-131 ms of
a 96-134 ms budget is the device's own capture and encode, and the fix is not in the
browser.* This section is the follow-up the brief asked for: vary the encoder's own
levers against a fixed pipeline, measure the engine the console actually ships in rather
than a proxy for it, and check whether ARC-131's frame engine shares the trap ARC-144
found. Same rig, same module, same device class; everything below is measured, and every
number names the load it was measured under, because on this fleet that turned out to be
the largest single factor.

## C1. The device half, one encoder lever at a time

### What changed in the rig

The rig could already vary exactly one lever (`max_size`), through one flag. It now varies
four, and — more importantly — it *measures what the device did with them* rather than
recording what it was asked for:

* `cmd/live` gained `-bitrate` (bits/s), `-max-fps`, and `-codec-options` (raw MediaCodec
  options in scrcpy's `key[:type]=value` syntax, appended verbatim after the IDR option, so
  the caller's spelling is what reaches the encoder).
* `internal/scrcpy.Options` gained `BitRate`/`MaxFPS`, which become `video_bit_rate=` and
  `max_fps=` in the server's argument list. The server's option names were checked against
  the 4.1 dex rather than assumed.
* Every report now carries an `encoder` section with the request beside the measurement:
  `requested` (max_size, max_fps, bit_rate, idr interval, codec options) and `measured`
  (the encoded size from the session packet, the measured kbit/s and capture fps, the key
  frame count and the IDR interval it implies, **the profile-level-id out of the device's
  own SPS**, and the arrival and PTS spans). A knob the hardware ignores therefore shows up
  as a request with no matching measurement.
* `tools/run-arc145-c1.sh` runs one cell of the matrix per invocation, and
  `tools/summarise-arc145.py` renders the table below from the committed reports.

Three instrument defects were found and fixed on the way, and they are part of the record:

1. **Rates taken over the encoder's PTS are wrong when its PTS jumps.** One run's PTS
   carried three gaps (595 ms, 3.4 s, 11.5 s) and read as a 37.1 s stream with a 30.3 fps
   cadence when the run was 20.0 s at ~56 fps. The rates are now taken over the arrival
   window (wall clock) and the PTS span is reported beside them.
2. **A NaN silently produced a zero-byte report.** `json.MarshalIndent` refuses NaN, its
   error was discarded, and one cell wrote a 0-byte report that read exactly like a run
   that measured nothing. Non-finite floats are now null in the report and a marshal
   failure is fatal.
3. **A downscaled stream needs the strip's cell in the VIDEO's pixels, not the screen's.**
   At `max_size=720` the device's 32 px cell is 10.07 px wide in the encoded frame; a probe
   told 32 read the wrong cell on every column and refused every frame (377/377 `sync`
   failures). The host now restates the cell in the video frame (`video_cell`) and the same
   cell then decoded 78/78 presented frames. Without this the resolution lever cannot be
   measured at all.

### The matrix

One device (SM-G9750 at `192.168.1.104:5555`), one geometry, 20 s per run, 4 taps, Chrome
152 headless on this host. `g2g` is glass-to-glass p50 in ms: the browser's expected display
time for a frame minus the device clock read out of that frame's own pixels, skew-corrected
by the device page's own NTP-style estimate (reported per run). "clock read" is the fraction
of presented frames whose pixels yielded a decodable clock — 1.000 in every reported run
below unless stated.

#### The matrix, as one table

| run | requested | encoded | forwarded / decoded / presented | g2g p50 | p95 | p99 | clock read | probe cost mean/max | skew used | browser |
|---|---|---|---|---|---|---|---|---|---|---|
| `c1-base` | device, device fps, 8M (default), idr 2s idr2 | 1080x2280, 1414 kbit/s, 56.1 fps, 10 IDR (every 2.00s), SPS 420032 | 1123 / 934 / 476 | **203.4** | 260.7 | 282.5 | 476/476 | 7.1/30.6 | +22.9 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-base2` | device, device fps, 8M (default), idr 2s idr2 | 1080x2280, 1630 kbit/s, 57.9 fps, 10 IDR (every 2.00s), SPS 420032 | 1158 / 771 / 341 | **120.0** | 191.3 | 264.4 | 341/341 | 6.4/12.8 | +36.5 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-base3` | device, device fps, 8M (default), idr 2s idr2 | 1080x2280, 1569 kbit/s, 56.6 fps, 10 IDR (every 2.00s), SPS 420032 | 1132 / 989 / 488 | **102.2** | 189.6 | 250.3 | 488/488 | 7.0/18.9 | +53.3 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-base4` | device, device fps, 8M (default), idr 2s idr2 | 1080x2280, 1769 kbit/s, 64.6 fps, 11 IDR (every 1.82s), SPS 420032 | 1293 / 1173 / 516 | **95.5** | 130.8 | 163.8 | 516/516 | 7.0/63.7 | +58.2 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-base5` | device, device fps, 8M (default), idr 2s idr2 | 1080x2280, 1646 kbit/s, 62.9 fps, 11 IDR (every 1.82s), SPS 420032 | 1258 / 1138 / 520 | **83.2** | 132.3 | 133.6 | 520/520 | 6.6/50.5 | +59.4 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-idr10` | device, device fps, 8M (default), idr 10s idr10 | 1080x2280, 1563 kbit/s, 55.8 fps, 2 IDR (every 10.00s), SPS 420032 | 1116 / 515 / 202 | **112.9** | 142.4 | 177.6 | 202/202 | 6.8/18.4 | +52.2 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-idr0` | device, device fps, 8M (default), idr 0s — | 1080x2280, 1583 kbit/s, 60.2 fps, 3 IDR (every 6.67s), SPS 420032 | 1203 / 473 / 142 | **85.6** | 119.3 | 352.2 | 142/142 | 6.3/19.0 | +53.5 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-idr0b` | device, device fps, 8M (default), idr 0s — | 1080x2280, 1519 kbit/s, 62.0 fps, 3 IDR (every 6.67s), SPS 420032 | 1241 / 478 / 139 | **83.0** | 132.8 | 365.7 | 139/139 | 6.5/21.8 | +56.6 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-idr0c` | device, device fps, 8M (default), idr 0s — | 1080x2280, 1604 kbit/s, 62.5 fps, 3 IDR (every 6.67s), SPS 420032 | 1250 / 650 / 230 | **95.4** | 129.0 | 146.1 | 230/230 | 6.6/14.5 | +59.6 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-a720i0` | 720, device fps, 8M (default), idr 0s — | 340x720, 414 kbit/s, 90.2 fps, 4 IDR (every 5.00s), SPS 42001f | 1805 / 605 / 78 | **82.0** | 115.1 | 148.8 | 78/78 | 2.2/16.8 | +56.9 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-br2m` | device, device fps, 2000000, idr 0s — | 1080x2280, 1192 kbit/s, 67.6 fps, 3 IDR (every 6.67s), SPS 420032 | 1354 / 652 / 176 | **81.5** | 99.1 | 181.8 | 176/176 | 6.9/17.6 | +54.3 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |
| `c1-main2` | device, device fps, 8M (default), idr 0s profile=int=2,level=int=256 | 1080x2280, 1436 kbit/s, 61.5 fps, 3 IDR (every 6.67s), SPS 4d0032 | 1232 / 556 / 176 | **95.9** | 113.1 | 145.4 | 176/176 | 6.4/21.1 | +57.9 (device_ntp_round_trip) | Macintosh; Intel Mac OS X 10_15_7 |

## Load at the time of each run

| run | host loadavg | device loadavg | mean | negative samples (join noise) | run error |
|---|---|---|---|---|---|
| `c1-base` | { 14.08 10.96 9.59 } | 15.92 14.74 15.12 9/3153 10309 | 190.0 | 9 | — |
| `c1-base2` | { 8.43 9.66 9.28 } | 16.74 15.50 15.34 6/3159 11841 | 118.2 | 28 | — |
| `c1-base3` | { 6.03 7.59 8.55 } | 17.12 16.82 16.22 3/3165 19790 | 105.7 | 22 | — |
| `c1-base4` | { 5.68 7.23 8.26 } | 16.27 16.65 16.26 9/3124 21896 | 87.7 | 38 | — |
| `c1-base5` | { 10.52 10.35 9.70 } | 16.78 16.86 16.68 4/3122 32017 | 85.1 | 24 | — |
| `c1-idr10` | { 7.32 7.71 8.54 } | 16.97 16.84 16.26 3/3168 20538 | 101.3 | 8 | — |
| `c1-idr0` | { 8.55 7.87 8.55 } | 16.92 16.85 16.29 6/3123 21265 | 66.3 | 36 | — |
| `c1-idr0b` | { 11.30 11.17 9.83 } | 16.86 16.89 16.65 3/3120 30255 | 72.3 | 32 | — |
| `c1-idr0c` | { 9.69 10.26 9.70 } | 16.61 16.81 16.67 3/3119 32559 | 79.7 | 39 | — |
| `c1-a720i0` | { 10.66 11.00 9.69 } | 16.39 16.82 16.61 1/3116 29720 | 85.0 | 0 | — |
| `c1-br2m` | { 13.38 10.32 9.07 } | 17.18 16.90 16.54 9/3128 27711 | 56.4 | 39 | — |
| `c1-main2` | { 7.43 10.18 9.57 } | 17.24 16.92 16.67 10/3128 30885 | 69.2 | 40 | — |

wrote /Users/artisanclaw/multica_workspaces/artisan-cla-bcf9a4adc3f9/arc-145-5305845209ee/workdir/drift-next-app/spike/arc-144-live-mirror/tools/../results/arc145-c1-table.json

Read the requested/encoded columns carefully, because two of them are not what the words
suggest:

* **`max_size` bounds scrcpy's LONGER side, not a width.** `max_size=720` on this device is
  **340x720** (0.24 MP) against the device's own 1080x2280 (2.46 MP) — a 10x pixel
  reduction, not "720p". ARC-144 never used a downscale: its Part B streamed the device's
  own 1080x2280 (`max_size=0`) throughout, so the 720-class cell here is a new condition, not
  a repeat of one.
* **scrcpy's own default bit rate is 8 Mbit/s**, which this content never needed: the
  measured rate is 1414-1769 kbit/s at 1080x2280 and 414 kbit/s at 340x720. The `bit_rate`
  column states the request; the `kbps` column is what came out of the socket.

### What the matrix says

**1. The observation host's own load dominates the measurement, and by more than any knob.**
The five baseline runs — identical settings, one session, ~55 minutes apart end to end —
measured **203.4, 120.0, 102.2, 95.5, 83.2 ms p50** while this host's load average fell from
14.1 to 5.7. The last two baselines are as fast as the best knob cell in the whole matrix.
Single-run comparisons on this fleet therefore measure the host's mood: the drift band is
~2.4x, wider than any encoder lever's effect. Every cell's load is in the table above for
exactly this reason, and the C1 conclusions below are drawn only from cells whose loads
overlap.

**2. Resolution does nothing. Bit rate does nothing. Profile does nothing.** All three were
measured against the same reference (native 1080x2280, 8 Mbit/s, encoder's own IDR setting)
inside one overlapping load window:

| lever varied | cell | g2g p50 / p95 | host load | what the device actually did |
|---|---|---|---|---|
| none (reference) | `c1-idr0`, `c1-idr0b`, `c1-idr0c` | 85.6 / 119.3, 83.0 / 132.8, 95.4 / 129.0 | 8.6, 11.3, 9.7 | 1080x2280, 1583-1604 kbit/s, 60-63 fps |
| resolution 1080-class → 720-class | `c1-a720i0` | 82.0 / 115.1 | 10.7 | 340x720, 414 kbit/s, and **90.2 fps** |
| bit rate 8 Mbit/s → 2 Mbit/s | `c1-br2m` | 81.5 / 99.1 | 13.4 | 1192 kbit/s measured |
| profile Baseline → Main | `c1-main2` | 95.9 / 113.1 | 7.4 | SPS `420032` → **`4d0032`** (profile took effect, level did not: Main level 5.0, not the level 4.0 asked for) |

A **10x reduction in encoded pixels** moved glass-to-glass by nothing measurable. Neither
did a 5x reduction in bit rate, nor a different H.264 profile. The device's ~85 ms is not
its encoder working hard at pixels or bits: it is the render → capture → encode → LAN path
being what it is, and cutting the pixels does not cut it. (The one interesting side effect:
with fewer pixels the device's encoder ran *faster* — 90 fps against 56-62 — and still did
not get quicker end to end.)

**3. The IDR interval is not what moves it either, but it is not optional.** Against the same
reference, `idr 2 s` measured 203.4/120.0/102.2/95.5/83.2 and `idr 10 s` measured 112.9; the
2 s band overlaps the reference band completely once the host is quiet (83.2 vs 83.0). What
the IDR interval decides is not the latency but whether a late-attaching viewer can decode
at all — the two `max_fps=30` runs below are the evidence.

**4. `max_fps=30` could not be measured, twice, and how it failed is the finding.** Both runs
forwarded 638-642 frames, decoded 38-42, presented 7-8, and produced 7-8 clock reads whose
p50 came out *negative* (the join is one frame out, which is what a handful of samples can
do). This is not a fast stream: it is a stream the late-attaching browser mostly fails to
decode. Both runs' encoder emitted **2 IDRs in 20 s** (10.01 s apart). Reported as a gap,
not as a latency: **`max_fps` is not measured by this spike.**

**5. The encoder's levers cannot be combined on this fleet in this rig — the device-side
server aborts.** This is the sharpest new result, and it bounds everything above. With
`video_codec_options=i-frame-interval:int=2` in place, adding any of `max_size=720` (5
attempts), `video_bit_rate=2000000` (2), or `max_fps=30` (1) made the scrcpy 4.1 server
abort with **`stack corruption detected (-fstack-protector)`** before it wrote a single byte
of stream metadata. Two entries in `video_codec_options` (`i-frame-interval` + `profile`)
abort it too (2 attempts). Alone, each of `max_size`, `max_fps`, `video_bit_rate`,
`profile:int=2` and `i-frame-interval:int=2` starts a session normally, and the server-level
options combine with each other (`max_size` + `max_fps`, `max_size` + `video_bit_rate` both
fine). One exception was observed: `max_size=720` + `profile:int=2` started three times.

The same encoder settings through the **stock scrcpy 4.1 client**
(`--max-size=720 --video-codec-options=i-frame-interval:int=2 --record`) completed a 4 s
recording on the same device immediately afterwards. So the trigger is not the settings
alone: this rig's server-option set (it drives the server directly and sets
`send_frame_meta`/`send_stream_meta`/`send_dummy_byte` itself) is the difference. It is
recorded here as **a limit of this rig on this fleet and a lead, not as a product defect** —
no claim is made that the shipping client would hit it, and the combination rule above is
empirical, not explained. Raw attempts: `results/lever-combos.md`.

The practical consequence for the matrix: the four levers could not be crossed with each
other, so each is measured alone (which is why the reference rows carry the encoder's own
IDR default).

**6. The instrument's own cost, and its one-frame noise.** The strip readback cost 6.3-7.1 ms
mean per presented frame (max 13-64 ms) — 30x Part A's 0.2 ms budget, and it is reported per
run for that reason. It does not enter the latency number (the join uses the browser's
expected display time, recorded before the read), but it can cost presented frames: 1-3% of
Chrome's samples land below zero in every run, which bounds the join at about one frame
interval (~18 ms), and the frame counts per run are in the table so a thin run is visible as
thin. `p95`/`p99` are in the table beside every `p50` for the same reason.

## C2. The webview the console actually ships in — measured, not extrapolated

ARC-144 measured Safari 26.6.2 and called it the engine's number (235.6 ms), not the
console's, and said the console's own shell was not exercised. It is now exercised.

**Method.** The console's own Tauri application (`apps/console`, `tauri 2.11.5` /
`wry 0.55.1`, WKWebView on macOS) is built with the repository's own command
(`pnpm exec tauri build --debug --no-bundle`) and then started as its dev shell with a
`--config` override that sets `build.devUrl` to this rig's page and empties
`beforeDevCommand`. What is the console's: the shell binary, the Tauri/wry versions, the
window configuration, the CSP and the engine. What is the rig's: the URL — the console's own
UI has no clock probe in it. Run:

```bash
cd apps/console && pnpm exec tauri build --debug --no-bundle   # once, outside the run
SERIAL=192.168.1.104:5555 DURATION=20s TAPS=4 WPROBE=120s \
  tools/run-part-b.sh c2-tauri tauri
```

The probe's own user agent confirms which engine it was, and it is not Safari:
`Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)` —
WKWebView's UA, with no `Version/… Safari/…` token.

**Result (native 1080x2280, same settings as the C1 reference, one window on a real display):**

| run | engine | frames with a clock | g2g p50 | p95 | p99 | negative samples | host load |
|---|---|---|---|---|---|---|---|
| `c2-tauri` | console Tauri shell (WKWebView) | 877 (decode rate 1.000) | **93.7** | 180.7 | 200.7 | 1 | 17.9 |
| `c2-tauri2` | console Tauri shell (WKWebView) | 880 (decode rate 1.000) | **84.0** | 150.0 | 169.0 | 0 | 21.0 |

Its browser-side components are the same order as Chrome's: jitter buffer 1.97 ms, H.264
decode 0.44 ms, processing delay 2.41 ms — so ~80-90 ms of the 84-94 ms is again the device
half, not the webview. Both runs decoded **every frame they received** (1236/1236 and
1186/1186) and lost 0 and 2981 packets respectively: 0.1-0.2s of RTP loss on a heavily
loaded host, which did not show up as decode failure.

**So the shipping webview is not the 2.4x-penalty engine ARC-144 had to assume.** 84.0 and
93.7 ms p50 — against Chrome headless at 83-102 ms in the same session, at *higher* host
load than most of those Chrome runs. ARC-144's 235.6 ms is Safari's own number (its window,
its playback path), and treating it as the console's would have overstated the console's
cost by ~2.5x. The gap it was meant to bound is closed: **the console's engine is in the same
band as the browser measured headless.**

Two caveats stated plainly: the shell was run in dev mode with the URL overridden (the
engine and the window configuration are the app's; the frontend bundle and the Connect
surface are not exercised), and both runs are windows on a display of a host under load 18-21.

## C3. Does ARC-131's frame engine share ARC-144's trap?

**Verdict: no — it does not share the trap, and the reason is structural.** The trap is a
property of a persistent H.264 session with one IDR and one SPS/PPS: a receiver that misses
frame 0 stays black forever with every counter looking healthy. ARC-131's engine has no
session, no decoder and no bitstream. Below, each of the four questions with the code path
that answers it.

**(a) Does it request periodic IDRs?** Not applicable, and that is the answer: there is no
encoder session to configure. Each frame is one complete PNG produced by
`exec-out screencap -p` through the adapter's allow-listed fixed builder shape
(`internal/edge/adb/command.go:222`, `internal/edge/adb/adapter.go:459`), driven from the
engine's `FrameCapturer` seam (`internal/media/frames.go:51`) by the tick loop
(`internal/media/frames.go:445`). Every frame is intra-coded by construction; there is no
GOP, so there is no frame 0 to miss.

**(b) Does it re-attach parameter sets to every IDR it forwards?** There are no parameter
sets and nothing is forwarded. The invariant that takes its place is the one the engine
actually has to keep — *never deliver part of an image* — and it is kept in two places: a
capture larger than the 32 KiB preview bound is reported as truncated with **no** preview
rather than as a prefix (`internal/media/frames.go:476-481` with the single bound in
`internal/media/preview.go:81-89`), and a capture that is not a PNG fails loudly with
`FailureObservation`/`ErrScreenshotInvalid` rather than being stored
(`internal/edge/adb/adapter.go:732` `isPNG`, checked before the hash is taken).

**(c) Does it tolerate a late join or a mid-stream attach?** Yes, and there is no stream to
attach to: a subscription is what starts the work (`FrameEngine.Subscribe`,
`internal/media/frames.go:305-324`), each tick reads the subscriber set fresh
(`internal/media/frames.go:425-437`), and the first capture for a new subscriber lands within
one interval (2 s default) plus one bounded capture (5 s default) — not "forever". The two
ways a reader could be misled are closed explicitly: `Frame(serial)` reports `ok=false` for a
device nothing is subscribed to, so a stopped-capturing device cannot be read as one being
watched (`internal/media/frames.go:345-353`), and `Unsubscribe` drops the frame rather than
leaving it behind (`internal/media/frames.go:329-340`).

**(d) Does it surface a decode failure rather than a healthy-looking connection with nothing
decoded?** There is no decode to fail, and the engine's answer to the question behind the
question — *can a device's media read as fine while showing nothing?* — is `Frame.Current()`:
it requires a capture to have succeeded **and** no later attempt to have failed
(`internal/media/frames.go:112`), a failed capture is classified and recorded against the
device while the last frame is left explicitly non-current (`internal/media/frames.go:513-527`),
`Failures` is a counter that is never reset (`internal/media/frames.go:87-92`), and the
engine's closing record names the last failure class and detail
(`internal/media/frames.go:165-186`). Truncated and shutdown-cancelled captures are counted
separately from failures, so neither is blamed on the device.

Two honest qualifications:

* **The engine asserts on the capture operation, not on the pixels.** A screencap that
  returns a valid PNG of a black or otherwise empty screen is stored and reads as `Current`.
  That is the same *shape* as the trap at a different level — a successful observation that
  shows nothing — but it is a black image rather than a false "connected", and nothing
  downstream decodes it.
* **At runtime today nothing subscribes.** `Subscribe` is called only from
  `internal/media/frames_test.go`; the composition root constructs the engine and runs it
  (`cmd/control-plane/main.go:211-218`, awaited at `:280`), and no route serves `Frame` or
  `Frames`. So the shipping process currently holds no frames at all, and there is no living
  instance of the defect class either way. That is an observation about the consumer surface,
  not a defect found here — and it is the reason the four answers above are code paths plus
  tests (`internal/media/frames_test.go`, 14 tests including
  `TestFrameEngineClassifiesFailuresWithoutStoppingTheLoop`,
  `TestFrameEngineReportsTruncationRatherThanAPartialFrame`) rather than a measurement of a
  running engine.

**What ARC-143 should take from this:** the trap is real, it is in the *stream*, and the two
things it demands — ask the encoder for periodic IDRs, and re-attach SPS/PPS to every IDR
your hop forwards — are not things an intra-only snapshot path gives you for free, because
the snapshot path never has the problem. Check the hop before shipping: this spike's
`internal/livepeer` already re-attaches parameter sets (`internal/livepeer/peer.go:220`), and
the product has no H.264 code of its own yet.

## The bar: can ARC-143's <100 ms be reached on this fleet?

Best measured conditions, on this fleet, one device, host as quiet as it got:

| path | g2g p50 | p95 | p99 |
|---|---|---|---|
| device + pion hop + Chrome headless (native 1080x2280, 8 Mbit/s) | 83.0-95.4 (5 runs) | 119.3-191.3 | 146.1-365.7 |
| device + pion hop + the console's own Tauri/WKWebView shell | 84.0-93.7 (2 runs) | 150.0-180.7 | 169.0-200.7 |
| the same, host loaded (the first baseline of the session) | 203.4 | 260.7 | 282.5 |

* **p50 reaches the bar; it does not hold it.** 83-96 ms p50 is *at* <100 ms, on a quiet
  host, with the device carrying unrelated load (16-17 throughout). Under a loaded host the
  identical configuration measured 203.4 ms.
* **p95 does not reach the bar in any condition measured** (119-191 ms). If ARC-143's bar is
  a single number, p50 83-96 / p95 ~150 is the honest shape of it, and a target of "p50 under
  100 ms, p95 under 200 ms" is what this evidence supports.
* **Tuning the encoder is not the way to find the remaining margin.** A 10x pixel reduction,
  a 5x bit-rate reduction and a profile change each moved the number by nothing measurable;
  the only lever that moved it was the host's own load, and that is not a product lever.
* **What is left is inside the device's own render → capture → encode path**, which this rig
  still cannot split from the LAN hop, and the *device's* load (16-17 on this unit from
  unrelated work for the whole session). Those two are where the next measurement should
  point, and neither is reachable from the encoder.

## Not done, and bounds

* **No root cause for the scrcpy server abort**, and no claim that it is a product defect —
  the stock client completed the same encoder settings on the same device. It is a rig-level
  limit with a lead attached.
* **`max_fps=30` is a gap**, not a number: two runs, both undecodable to the instrument (7-8
  clock reads, negative p50). A 480-class cell is likewise unmeasured: it needs the IDR
  option, and that combination aborts the server.
* **The device half is still one residual** (render + capture + hardware encode + LAN hop).
  Nothing here splits capture from encode, and the device's own rAF→glass delay is
  *included*, so every number above is conservative in that direction.
* **One device, one geometry, one direction.** `.104` only, portrait only, no audio, no
  multi-device, no MSE. Two unrelated scrcpy 2.4 sessions (pids 6530 and 15695, 480p at 1-2
  fps) were running on that device throughout from work that is not this card's; they were
  not touched.
* **The observation host is a participant, not a neutral instrument.** It carried load 5.7-21
  from other work for the whole session, and that is what the drift in the baseline rows is.
  The same matrix on an idle host would give a narrower band; the ordering of the levers
  would not change, because none of them moved the number at all.
* **Bounds observed.** Lab SM-G9750 units only (`.104`, plus `adb`-visible units for
  inventory); no production data; no credentials; `DRIFT_SYNC_LIVE` unset; nothing powered
  off; `tcpip` never used; the owner was at the machine. The Tauri window was opened on the
  owner's display for the C2 runs and closed by the script afterwards.
* The spike is unchanged in kind: nothing here is wired into `cmd/control-plane` or the
  console, and no dependency was added to the product module graph.

## Reproducing

```bash
cd spike/arc-144-live-mirror

# C1: the encoder matrix, one cell at a time (20 s each, serial/device via env)
tools/run-arc145-c1.sh base a720i0 br2m main idr10 idr0      # see the script for the cells
python3 tools/summarise-arc145.py c1-base c1-idr0 c1-a720i0 c1-br2m c1-main2

# C2: the console's own Tauri shell at this rig's page (build once, outside the run)
(cd apps/console && pnpm exec tauri build --debug --no-bundle)
SERIAL=192.168.1.104:5555 DURATION=20s TAPS=4 WPROBE=120s tools/run-part-b.sh c2-tauri tauri

# the server-abort lead: which option combinations start a session and which abort
cat results/lever-combos.md
```

Committed evidence: `results/report-live-c1-*.json` and `results/report-live-c2-tauri*.json`
(per-run aggregates: every percentile, the encoder request beside the measured stream, the
skew reports, the per-tap table, the probe's cost), `results/summary-arc145-c1-table.txt` and
`results/arc145-c1-table.json` (the table above, machine-readable),
`results/summary-part-b-c1-*.md` and `-c2-*.md` (one per run), and the abort attempts in
`results/lever-combos.md`. The raw per-frame run logs (`results/live-*.json`, ~7 MB
each) and the screenshots are not committed; the commands above regenerate them.
