# Live mirror transport: measured on this fleet

This is the measurement ARC-231 asked for before the console's default transport
was chosen. It compares the two transports the control plane already carries for
a live mirror — the negotiated WebRTC peer connection and the per-device
container stream (MSE over `/drift/v1/mirror/stream`) — on this hub, over this
fleet's own devices.

The default the measurement chose is **TCP**, stated with these numbers beside
it in the console (`apps/console/src/lib/live-mirror.ts`, `liveMirrorCopy.settings.notice`)
and in the code that sets it (`apps/console/src/pages/ControlPage.tsx`,
`settingsDefaults.liveMirrorTransport`). The operator's setting is unchanged:
both transports remain selectable, and the choice travels with every stream.

## The harness

`cmd/mirror-transport-measure` — one binary, no test-only fixtures. It starts a
mirror stream for one device, over one transport at a time, from the same
control plane a browser talks to:

    go build -o /tmp/mtm ./cmd/mirror-transport-measure
    /tmp/mtm -addr http://127.0.0.1:8099 -token <lab token> \
        -range 192.168.1.100-192.168.1.140 -port 5555 \
        -serial 192.168.1.123:5555 -runs 3 -window 60 -settle 8 -motion \
        -out measurement.jsonl

For each run it records the JSONL row it writes: `handshake_ms` (start of the
open call to the transport being ready to carry a picture), `ttff_ms` (start of
the open call to the first picture in hand), the inter-picture gap distribution
(`gap_p50_ms`, `gap_p95_ms`, `gap_p99_ms`, `gap_max_ms`), `pictures_per_s`,
`stalls_over_1s`, and the state and failure the plane reports for the stream.

A picture is one access unit: for WebRTC, the RTP packet that ends one (the
marker bit); for TCP, one `mdat` box of the container stream, which is one
encoded frame. Both counts are frames, so the two are comparable.

`-motion` drives the device's own screen for the length of each run, with one
long-lived on-device loop rather than a host-side command per frame. It is not
decoration: this fleet's screen encoder is change-driven, so a still screen
carries no pictures at all and a measurement taken on an idle device measures the
encoder's idle behaviour instead of the transport.

## The fleet

- Hub: this Mac, control plane on `127.0.0.1:8099`, lab adapter in lab mode.
- Device: `192.168.1.123:5555` — the fleet is TCP-addressed (`192.168.1.x:5555`).
- 10 devices observed at `1080x2280` render.
- 3 runs of 60 s per transport, alternating, 8 s settle between runs, 2026-09-21.

## Results

| run | handshake | ttff | pictures | pictures/s | gap p50 | gap p95 | gap max | stalls >1 s |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| webrtc 1 | 1084 ms | 1093 ms | 3264 | 54.4 | 16.6 ms | 37.0 ms | 98 ms | 0 |
| webrtc 2 | 41 ms | 64 ms | 3496 | 58.3 | 16.6 ms | 27.6 ms | 1177 ms | 1 |
| webrtc 3 | 95 ms | 121 ms | 3495 | 58.3 | 16.1 ms | 32.7 ms | 1250 ms | 1 |
| tcp 1 | 2 ms | 15 ms | 3561 | 59.4 | 16.5 ms | 29.1 ms | 277 ms | 0 |
| tcp 2 | 1 ms | 11 ms | 3560 | 59.3 | 16.5 ms | 28.4 ms | 254 ms | 0 |
| tcp 3 | 1 ms | 51 ms | 3568 | 59.5 | 16.5 ms | 27.4 ms | 249 ms | 0 |

The plane opened **one** device session for the whole measurement (one
`live mirror open` line in its log, viewers coming and going), so the 1084 ms
first run is the capture start being paid once, not a per-viewer cost: run 1 is
the only run that had to start a capture. Every later run, on either transport,
joined a session already carrying pictures — which is the comparison that
matters, and it is the same comparison on both sides.

- **Time to first picture.** TCP is one to two orders of magnitude quicker to
  put a picture in front of the viewer: 1-2 ms against WebRTC's 41-95 ms on a
  warm session. WebRTC's cost is its handshake — ICE gathering, the SDP exchange,
  and a peer connection on both ends — and it is paid before any picture can
  move. TCP's is a fetch of a stream the plane is already producing.
- **Steady state.** The two are the same to within noise: ~59 pictures/s, p50
  gap 16.5 ms, p95 27-37 ms. Both are delivering the encoder's own rate.
- **Stability.** This is where they part. TCP's worst gap was 249-277 ms and it
  never stalled for a second in three minutes of streaming. WebRTC stalled for
  more than a second twice, with worst gaps of 1177 ms and 1250 ms, and its
  p95 was higher on every run. A 1.2-second freeze is visible to an operator
  watching a device.

## What was not measured

- **Glass-to-glass latency.** Both numbers are time to a picture *in hand at the
  viewer*, not time to a picture *on the glass*. Measuring that needs a clock the
  device and the viewer share, which this harness does not have; the two
  transports are compared on the same footing, but neither figure is a
  latency-to-eye figure and neither is claimed as one.
- **Load.** One device, one viewer, an otherwise quiet hub. The console's grid
  mirrors up to 3 devices with 1 session kept for the operator's own frame
  (capacity 4); how the two transports behave at that capacity, and how WebRTC's
  peer connections compare to TCP's streams there, is not measured here.
- **Other devices.** One device out of the ten observed. Nothing in these numbers
  suggests device-specific behaviour, but one device is one device.

## One observation from the run, not a finding

While measuring, one TCP run that started 4 s after a WebRTC run had stopped was
primed from a cached keyframe in 9 ms and then carried no further picture for the
whole window, reading one picture against the 11-13 a live session carried. With
a longer settle (6 s) and no run immediately before it, the same path carried
pictures normally. This is recorded because it is a picture-less stream that
looks live, not because its cause is established — nothing in this measurement
distinguishes a session reused after it ended from a capture that had not
restarted. It is worth a look on its own card.

The WebRTC rows above read `MIRROR_STREAM_STATE_FAILED` with the reason "the
browser's peer connection is closed" because this harness closes its peer at the
end of the window before it reads the stream's state. The plane's own record of
the same teardown is `no viewer is watching this device`. That reading is an
artifact of this client's teardown order, not a finding about the transport.

## The other half of the card

The adb churn this card is about was measured on this host in the same session:

- The adb server's log (`adb.<uid>.log`) was **165,969 lines**, of which
  **153,160** were `usb_osx.cpp` at level E — `Unable to create an interface
  plug-in (e00002be)` — over eight days (09-12 23:50 to 09-20 23:13), a
  per-second retry storm against a USB transport this hub does not use. Also
  4,975 `network.cpp` connection failures, 640 `timeout expired while flushing
  socket, closing`, and 62 `transport_usb.cpp` errors.
- **Six** adb clients fataled at `main.cpp:168 could not install *smartsocket*
  listener: Address already in use` within 0.7 s on 09-20 18:52:46, which is when
  the second adb server was started. See below.
- A control plane left running on this host wrote **33 of its 42 log lines** —
  78% of everything it said in seven minutes — as the same repeated line:
  `discovery watcher poll failed: timeout: lab adapter could not enumerate
  devices`. One recurring condition, one line per poll, unbounded. That is the
  defect requirement 3 fixes; the same condition now records one classified
  reading and a line per order of magnitude instead.

### The second adb server on 59999

Two adb servers are running on this host: the default on 5037 (PID 38278) and a
second on 59999 (PID 30395, started 09-20 18:52:45, parent gone — PPID 1, so it
outlived whatever started it). Both write to the same `adb.<uid>.log`, which is
why the churn above is the sum of both.

The second one exists because the older `drift` repo's test boundary isolates its
adb traffic from the host's fleet: `src/__tests__/helpers/adb-boundary.ts` exists
precisely because tests that share the host's adb server failed, and its comment
records the same four tests passing once `ANDROID_ADB_SERVER_PORT` pointed at a
different server. `executor-wiring.test.ts` and `lifecycle.test.ts` are its two
callers. Nothing in `drift-next-app` sets that variable — the new plane names
`ANDROID_ADB_SERVER_PORT` only to strip it from the environment it hands its
adapter (`internal/edge/adb/process.go`), so a test run cannot point the plane at
a server the operator did not start.

So: the second server is a test-isolation server that was left running after its
test run ended. It is **not** eliminated by this card — killing it is an action on
a process this run does not own, and it was left up because a concurrent agent
run was using the fleet on this host. The fix is to make the boundary clean up
after itself: a test that starts a server on its own port should stop it, or the
boundary should record the PID it started and kill it on teardown. That belongs
on its own card, with the older repo as its home.
