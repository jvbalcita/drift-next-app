# ARC-145 — which scrcpy server option sets start a session, and which abort

Evidence for the C1 finding that the encoder's levers cannot be combined on this fleet in
this rig. Every line below is one attempt of

```bash
./results/bin/live -serial 192.168.1.104:5555 -max-size {0|540|720} \
  -keyframe-interval {0|2} [-bitrate N] [-max-fps N] [-codec-options ...] \
  -wait-probe 2s -allow-no-probe -duration 2s
```

i.e. the rig's own session start, not a synthetic probe. "session up" means the device
reported its encoded size and the stream was readable; `stack corruption detected
(-fstack-protector)` is the device-side server's own abort message, seen on stderr as the
server died before writing any stream metadata.

| options passed to the device server | attempts | outcome |
|---|---|---|
| `video_codec_options=i-frame-interval:int=2` (the rig's baseline) | 8 in this session (+ ARC-144's live5-8) | session up |
| *(no codec options at all)* | 6 | session up |
| `max_size=720` | 3 | session up (340x720) |
| `max_size=540` | 1 | session up (254x540) |
| `max_fps=30` | 2 | session up |
| `video_bit_rate=2000000` | 1 | session up |
| `video_codec_options=profile:int=2` | 3 | session up |
| `max_size=720` + `max_fps=30` | 1 | session up |
| `max_size=720` + `video_bit_rate=2000000` | 1 | session up |
| `max_size=720` + `video_codec_options=profile:int=2` | 2 | session up |
| `max_size=720` + `video_codec_options=i-frame-interval:int=2` | 5 | **abort** |
| `max_size=540` + `video_codec_options=i-frame-interval:int=2` | 1 | **abort** |
| `video_bit_rate=2000000` + `video_codec_options=i-frame-interval:int=2` | 1 | **abort** |
| `max_fps=30` + `video_codec_options=i-frame-interval:int=2` | 1 | **abort** |
| `video_bit_rate=2000000` + `video_codec_options=profile:int=2` | 1 | **abort** |
| `max_fps=30` + `video_codec_options=profile:int=2` | 1 | **abort** |
| `video_codec_options=i-frame-interval:int=2,profile:int=2` | 1 | **abort** |
| `video_codec_options=i-frame-interval:int=2,profile:int=2,level:int=256` | 1 | **abort** |
| `max_size=540` + `video_bit_rate=2000000` + `video_codec_options=i-frame-interval:int=2` | 1 | **abort** |

Pattern: each option is fine alone, the server-level options are fine together, and adding a
*second* encoder configuration (a second codec option, or a size/bit-rate/frame-rate override
together with a codec option) aborts the server — with one exception observed
(`max_size=720` + `profile:int=2` started three times). The rule is empirical; the mechanism
was not root-caused, and the abort's message names a stack canary rather than an option.

**Control — the same encoder settings through the stock client:**

```bash
scrcpy -s 192.168.1.104:5555 --no-window --no-audio --max-size=720 \
  --video-codec-options=i-frame-interval:int=2 --record=/tmp/cli720.mkv \
  --record-format=mkv --time-limit=4
# INFO: Recording started to matroska file: /tmp/cli720.mkv
# INFO: Time limit reached
# INFO: Recording complete to matroska file: /tmp/cli720.mkv
```

It completed on the same device immediately after the rig's attempts had aborted. So the
trigger is not the encoder settings alone: the difference is this rig's own server-option set
(it drives `com.genymobile.scrcpy.Server` directly and sets
`send_device_meta`/`send_stream_meta`/`send_frame_meta`/`send_dummy_byte` itself). Recorded as
a limit of this rig on this fleet and as a lead — **not** as a product defect.

## Raw transcript of the logged attempts

```
=== -keyframe-interval 0 -bitrate 2000000
2026/09/18 03:35:11 live: scrcpy session up in 1.102s, streaming 1080x2280
=== -keyframe-interval 0 -bitrate 2000000 -codec-options profile:int=2
2026/09/18 03:36:16 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
=== -max-fps 30 -codec-options profile:int=2
2026/09/18 03:36:20 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
=== -max-fps 30 -keyframe-interval 2
2026/09/18 03:36:26 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
=== -keyframe-interval 2 -bitrate 2000000 -max-size 540
2026/09/18 03:36:30 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
=== -keyframe-interval 0 -max-size 720 -max-fps 30
2026/09/18 03:36:45 live: scrcpy session up in 1.419s, streaming 340x720
=== -keyframe-interval 0 -max-size 720 -bitrate 2000000
2026/09/18 03:37:50 live: scrcpy session up in 989ms, streaming 340x720
=== -keyframe-interval 0 -codec-options profile:int=2
2026/09/18 03:38:54 live: scrcpy session up in 1.001s, streaming 1080x2280
=== -keyframe-interval 0 -max-size 720 -codec-options profile:int=2
2026/09/18 03:40:01 live: scrcpy session up in 919ms, streaming 340x720
=== rig repeat 1: max_size=720 + idr2
2026/09/18 03:41:45 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
=== rig repeat 2: max_size=720 + idr2
2026/09/18 03:41:50 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
=== idr2 + profile (two codec options, no server option)
2026/09/18 03:42:05 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
=== idr2 + profile + level
2026/09/18 03:42:09 live: scrcpy session: scrcpy: reading stream metadata: unexpected EOF (stack corruption detected (-fstack-protector))
```
