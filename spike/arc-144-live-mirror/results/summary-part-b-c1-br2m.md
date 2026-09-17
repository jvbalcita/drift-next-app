# ARC-144 Part B — c1-br2m

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 81.5 ms | 176 frames with a clock read |
| glass-to-glass p95 | 99.1 ms | |
| glass-to-glass mean | 56.4 ms | 39 samples below zero |
| glass-to-glass + page render bound | 81.5 ms | |
| input, device handler (excludes return video) p50 | 11.8 ms | 4 taps |
| input, video-observed p50 | 30.2 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1354 frames |
| browser jitter buffer | 0.21991449487554907 ms | |
| browser decode | 0.314445245398773 ms | |
| browser processing delay | 0.5237230061349692 ms | |

## Clock

- skew used: 54.305908203125 (device_ntp_round_trip)
- ntp round trip (device side) +54.306 ms at 5.000 ms best RTT over 25 exchanges; min-delay estimator +59.000 ms over 157 posts (spread 345.000 ms); adb midpoint +34.483 ms (min RTT 66.7 ms, spread 138.729 ms)
- device clock in pixels: {176 12 18 383 176 0 10517}

## Decode

- 176 frames presented, 176 carried a decodable clock, 0 did not
- browser: received 1354, decoded 652, dropped 31, lost 0
- probe cost: mean 6.94204545495185 ms, max 17.600000023841858 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -34.4833984375,
    "spread_ms": 138.728515625,
    "adb_rtt_min_ms": 66.737,
    "adb_rtt_p50_ms": 100.025
  },
  "page": {
    "host_minus_device_ms_min_delay": 59,
    "samples": 157,
    "spread_ms": 345
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.024,
    "capture_fps": 67.62,
    "encoder_pts_span_s": 36.926,
    "frames": 1354,
    "height": 2280,
    "idr_interval_s": 6.675,
    "kbps": 1191.769,
    "key_frames": 3,
    "sps_profile_level_id": "420032",
    "width": 1080
  },
  "requested": {
    "bit_rate": 2000000,
    "codec_options": [],
    "idr_interval_s": 0,
    "max_fps": 0,
    "max_size": 0
  }
}
```

## Taps

```json
[
  {
    "seq": 1,
    "kind": "setup",
    "host_down_ms": 1789674209681.528,
    "write_down_up_ms": 0.07,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674232467.235,
    "write_down_up_ms": 0.051,
    "handler_ms": 25.07080078125,
    "video_seen_ms": 30.164794921875,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674234968.0808,
    "write_down_up_ms": 0.026,
    "handler_ms": 9.22509765625,
    "video_seen_ms": 45.919189453125,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674237468.499,
    "write_down_up_ms": 0.027,
    "handler_ms": 11.806884765625,
    "video_seen_ms": 28.801025390625,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674239968.982,
    "write_down_up_ms": 0.028,
    "handler_ms": 20.323974609375,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
