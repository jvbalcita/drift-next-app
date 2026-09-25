# Live mirror quality review (branch `artisan/fast-fleet-preview-control`)

Context-mode sources: git-status-branch, git-diff-console, git-diff-media, untracked-webcodecs, webcodecs-sizing, encode-profile, control-gesture, h264-route, grid-vs-live, mirror_profile, h264_wire, mirror-playback, mirror-control-channel, live-mirror-surface.

## 1) Architecture shift

- **Playback:** TCP prefers WebCodecs H.264 WS (`browserMirrorWebCodecsPlayback`, `/drift/v1/mirror/h264` + ticket) over fMP4 MSE (`browserMirrorPlayback`). `browserAdaptiveMirrorPlayback` falls back to MSE on failure (`mirror-playback.ts`).
- **Wire:** `internal/media/h264_wire.go` — 36-byte `DRH1` packets (seq/ts/key/W/H + Annex-B AU).
- **Control:** realtime touch (`MirrorGestureSender`, `realtime_control.go`, `control_channel.go`); moves coalesced ~33ms.
- **Encode:** operator = `MirrorOperatorEncodeProfile` (MaxSize 0/native, MaxFPS 24, BitRate 2.5Mbps, IDR 2s). Ambient = preview levels (low 480/0.5M, medium 720/1.2M, high 1080/2.5M). Operator purpose re-encodes (`encodeProfileFor` / `mirror_endpoint.go` SetSize).
- **Grid:** JPEG stills via `FrameEngine` (`DRIFT_GRID_*`); not live H.264.

## 2) Aspect distortion

1. Canvas bitmap sized from **packet** W/H in `configureDecoder`; `drawImage(..., activeWidth, activeHeight)` ignores `VideoFrame.displayWidth/Height` — SAR/crop mismatch stretches inside the bitmap.
2. Canvas CSS: `absolute inset-0 size-full max-w-full object-contain` (same as video). `object-contain` helps CSS box mismatch; wrong **bitmap** aspect still looks wrong.
3. Mid-stream reconfigure on packet size/codec change resets canvas; stale stage/stream frame metadata vs new packet dims can transiently distort.
4. Swipe does not change encode size; `picture()` maps gestures from canvas buffer when visible.

## 3) Pixelation

1. Native MaxSize + only 2.5Mbps @ 24fps → motion blockiness during swipes.
2. Stuck on ambient/preview (720p/1.2M) when upscaled to big frame.
3. Operator IDR every 2s delays recovery after loss.
4. `mirrorH264MaximumDecodeQueue = 3` drops under load → artifacts during fast motion.
5. CSS upscale amplifies blocks.

## 4) Swipe → re-encode?

**No.** Touch inject only (`MirrorControlTouch*`). Observation = stream token; realtime needs `matchesFrame(w,h)`. Mismatch refuses input; does not downgrade encode. Worse look during swipe = bitrate/queue/IDR + motion complexity.

## 5) Glass-to-glass bottlenecks

Device encode (24fps/2.5M) → plane (WebRTC or H264 WS) → control coalesce (~30Hz) → VideoDecoder (+drops) → canvas drawImage. WebCodecs removes MSE remux lag; does not fix encode bitrate/fps.

## 6) Grid vs big frame

Grid = bounded JPEG stills (low memory). Big frame = one live session. Improve grid via still level/cadence; do not attach live H.264 per tile.

## 7) Verdict: **MIXED**

Latency: BETTER-capable (WebCodecs + realtime). Quality during control: can look WORSE (bitrate vs native, queue drops, packet-vs-display size). Distortion primary suspect: packet/`drawImage` sizing vs display dims, not swipe logic.
