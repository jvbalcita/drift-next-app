# ARC-252 launch command-line bound evidence — 2026-09-21

The live mirror opened nothing on the lab fleet: `StartMirrorStream` answered a stream in `STARTING`,
and seconds later the stream ended as `transport_unavailable`, with the plane's own log showing

```text
scrcpy: reading the stream header: unexpected EOF:
  the device server said: stack corruption detected (-fstack-protector)
```

and the device's crash log naming `android::ACodec::reconfigEncoder4OtherApps` (`SIGABRT`,
`__stack_chk_fail`) while the device-side scrcpy server was bringing its encoder up. scrcpy's own
client streamed the same devices at the same bounds at the same time, so the device and the bounds
were not the fault. This file records what the difference turned out to be, and the measurement that
bounds it.

## What it is

The device's codec stack copies the **calling process's command line** into a fixed **264-byte
buffer** while it names the app whose encoder it is configuring — the string its own crash log prints
as `Cmdline:`, and the same string Samsung's ACodec logs as its `app-name`. A server launched with a
command line that reaches the end of that buffer aborts inside the codec stack **before its first
frame**, so the host sees only a stream header that never arrives.

The device-side server is started as

```text
app_process / com.genymobile.scrcpy.Server 4.1 scid=<8 hex> <options...>
```

so the whole line is the fixed head **plus every option**. The plane's launch was 307 bytes long
(every option the server has a default for was stated explicitly); scrcpy's own client's launch is
179 bytes. That 128-byte difference is the entire difference between the two clients.

## Method

One lab device, one server build, one variable at a time. Each launch below pushes the server, opens
the reverse tunnel to a loopback listener, starts the server with a named token set, holds it 6 s, and
then reads the device's own `logcat` for the crash signature. The device is hard-reset (and the reset
verified) before every launch, and `logcat` is cleared, so a leftover capture cannot be mistaken for
the token set's own effect.

```text
adb -s <serial> shell CLASSPATH=/data/local/tmp/scrcpy-server.jar app_process / \
  com.genymobile.scrcpy.Server 4.1 scid=<8 hex> <tokens...>
adb -s <serial> logcat -d | grep -c "stack corruption detected"
```

98 launches were recorded, in three families:

1. **Token-set bisect** — every pair, triple and quad of the extra options (`control`, `tunnel_forward`,
   `send_device_meta`, `send_stream_meta`, `send_frame_meta`, `cleanup`), plus reversals and repeats.
   No proper subset reproduces the abort; only the full set does.
2. **Length ladder** — the same token sets padded with options that cannot reach the encoder
   (`clipboard_autosync`, `power_off_on_close`, `audio_dup`, `downsize_on_error`, repeated
   `audio=false`), and shortened by dropping semantically unrelated options.
3. **Environment control** — the same launch with the device-side environment padded by 100 and 200
   bytes, to separate "the command line" from "the process's environment".

## Results

**The trigger is the length of the command line, and nothing else.**

| Command line (bytes) | Measured | Launches |
| --- | --- | --- |
| ≤ 263 | streams | 41 |
| ≥ 264 | aborts inside `reconfigEncoder4OtherApps` | 57 |

Every one of the 98 launches matches the model "the space-joined command line, `app_process` through
the last option, is safe at 263 bytes and aborts at 264" — including the padding ladder (a key that
cannot reach the encoder flips the result when it makes the line long enough), the shortening
experiments (dropping one option from the crashing set makes it stream), and the environment control
(argv counted, environment not counted). The two boundary runs were repeated and were stable
(263 clean twice, 264 aborting twice), and the result is order-independent (`cli plane plane cli`).

Directly measured examples:

| Launch | Command line | Result |
| --- | --- | --- |
| scrcpy's own client (`--no-audio --no-window --record`, 1080p, 24 fps, 2.5 Mbps) | 179 | 142 KB–532 KB recorded, 0 crash lines |
| the plane's launch as it was | 307 | aborts; `Cmdline:` in the crash log is that launch |
| the plane's launch with one option removed | 263 | streams |
| the plane's launch as it is now | 252 (255 worst case) | streams |

## Conditions

| Fact | Value |
| --- | --- |
| Host | `ArtisanClaws-MacBook-Pro.local` (macOS, arm64) |
| Device | one lab SM-G9750, Android 12, firmware `G9750ZHU8HXE1` |
| Device-side server | `/opt/homebrew/share/scrcpy/scrcpy-server`, version 4.1 |
| Host `adb` | Android Debug Bridge `1.0.41` |
| Serial handling | masked; no raw serial is committed |
| Bounds at the time | 1080p, 24 fps, 2.5 Mbps, `i-frame-interval:int=2` (the operator's own frame) |

The bound is a property of **this fleet's firmware**, measured rather than assumed: another encoder
implementation may size its buffer differently. What the product controls is its own line, and it
keeps that line short.

## Device evidence after the change

**Four concurrent sessions through the product's own client** (`probe-reports/probe-arc252-4-after-launch-bound.json`,
the repo's own probe with the devices' screens being driven so a stall means the stream and not a
still screen):

```text
N=4   opened 4/4, aggregate 8.88 Mbps, stalled 0 stream(s)
      device-sha256:0d658 510x1080: 8.27 fps, 2.30 Mbps, 188 frame(s), 2 key frame(s), stalled 0 s
      device-sha256:1c546 510x1080: 8.07 fps, 2.12 Mbps, 181 frame(s), 2 key frame(s), stalled 0 s
      device-sha256:bbcd4 510x1080: 8.07 fps, 2.30 Mbps, 175 frame(s), 2 key frame(s), stalled 0 s
      device-sha256:a52cc 510x1080: 5.93 fps, 2.15 Mbps, 132 frame(s), 2 key frame(s), stalled 0 s
      conclusion: no stream stalled at any measured point; 4 concurrent live session(s) were carried cleanly
```

Every device was left with 0 `app_process` and no leftover jar.

**One session through the plane's own client**, reading the stream directly: dialed in 697 ms,
`frame 510x1080`, 207 pictures, 2 key frames, 4 292 142 bytes in 15.7 s, and no abort line on the
device.

**Through the plane's own API** — our own control plane on `127.0.0.1:8099` against a copy of the lab
database, `StartMirrorStream` then `GetMirrorStream`, workspace `workspace-lab-local`, an operator
viewer:

```text
{"stream":{"transport":"MIRROR_TRANSPORT_WEBRTC","state":"MIRROR_STREAM_STATE_STARTING"}}
2026/09/21 07:44:34 live mirror open for <device> at 510x1080, encoded for the operator viewer
2026/09/21 07:44:49 live mirror ended for <device> (engine_stopped): media: the mirror engine was stopped
```

The device session is opened and carried; the stream ends only because the plane is stopped. The
`transport_unavailable` end that this card was opened for does not occur. The stream stays `STARTING`
while no browser has negotiated the media leg (the state is derived from frames delivered to the
viewer, and a stream with no viewer has delivered none), which is why the WebRTC leg itself is not
evidence here: only the device leg is.

## What the launch leaves out, and why that is safe

The launch now states only the options the admitted server does **not** default to. Each omission is
the 4.1 build's own default, measured on the device rather than read from source:

| Left out | Default, measured how |
| --- | --- |
| `tunnel_forward=false` | the server connected out to the reverse tunnel with no token |
| `send_stream_meta=true` | the codec id and the session packet arrived with no token |
| `cleanup=true` | the pushed jar was gone after the server exited with no token |

`control=true`, `send_frame_meta=true` and `send_device_meta=false` stay stated: this client opens
the control socket, reads the 12-byte packet header, and requires device meta to be absent, so the
framing it depends on is on the launch rather than left to a default. What makes the omissions safe is
that the server version is admitted as a **literal** by the allow-list, and that each omission fails
loudly at the handshake rather than silently — the client refuses a stream whose codec id, session
packet or framing is not what it parses.

## Reproducing

```text
go test ./internal/edge/adb -run 'TestMirrorLaunchFitsTheDeviceCommandLineBudget|TestTheBuilderRefusesALaunchTheDeviceCannotRead' -v

DRIFT_MIRROR_PROBE=1 \
DRIFT_MIRROR_PROBE_ADB=/opt/homebrew/bin/adb \
DRIFT_MIRROR_PROBE_SERVER=/opt/homebrew/share/scrcpy/scrcpy-server \
DRIFT_MIRROR_PROBE_SERIALS=<4 lab serials> \
DRIFT_MIRROR_PROBE_COUNTS=4 \
DRIFT_MIRROR_PROBE_HOLD_SECONDS=20 \
DRIFT_MIRROR_PROBE_OUT=/abs/report.json \
go test ./internal/edge/scrcpy -run TestLiveStreamConcurrencyProbe -v -count=1
```

Both gates must be set or the test skips; CI sets none.

## Harness note

The concurrency probe carried no encode profile, which the client requires, so it answered
`a capture rate must be in 1..24, got 0` for every device and measured 0 streams opened for every
point. It now passes the operator's own encode profile, which is what the report above was measured
with.
