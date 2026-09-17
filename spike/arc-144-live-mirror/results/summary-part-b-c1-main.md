# ARC-144 Part B — c1-main

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | -15.4 ms | 58 frames with a clock read |
| glass-to-glass p95 | 118.2 ms | |
| glass-to-glass mean | 11.1 ms | 33 samples below zero |
| glass-to-glass + page render bound | -15.4 ms | |
| input, device handler (excludes return video) p50 | 12 ms | 4 taps |
| input, video-observed p50 | 35.1 ms | 2 taps |
| Go hop forward p50 | 0.2 ms | 1239 frames |
| browser jitter buffer | 0.11035000000000002 ms | |
| browser decode | 0.49120791208791204 ms | |
| browser processing delay | 0.5769852747252747 ms | |

## Clock

- skew used: 53.906005859375 (device_ntp_round_trip)
- ntp round trip (device side) +53.906 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +58.000 ms over 157 posts (spread 227.000 ms); adb midpoint +34.741 ms (min RTT 75.4 ms, spread 86.382 ms)
- device clock in pixels: {58 0 150 3233 57 0 10400}

## Decode

- 58 frames presented, 58 carried a decodable clock, 0 did not
- browser: received 1239, decoded 455, dropped 31, lost 0
- probe cost: mean 13.384482762936887 ms, max 210.5 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -34.741455078125,
    "spread_ms": 86.382080078125,
    "adb_rtt_min_ms": 75.384,
    "adb_rtt_p50_ms": 99.429
  },
  "page": {
    "host_minus_device_ms_min_delay": 58,
    "samples": 157,
    "spread_ms": 227
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.012,
    "capture_fps": 61.912,
    "encoder_pts_span_s": 37.398,
    "frames": 1239,
    "height": 2280,
    "idr_interval_s": 6.671,
    "kbps": 1489.377,
    "key_frames": 3,
    "sps_profile_level_id": "4d0032",
    "width": 1080
  },
  "requested": {
    "bit_rate": 0,
    "codec_options": [
      "profile:int=2",
      "level:int=256"
    ],
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
    "host_down_ms": 1789674160401.755,
    "write_down_up_ms": 0.032,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674185265.494,
    "write_down_up_ms": 0.037,
    "handler_ms": 24.412109375,
    "video_seen_ms": 103.006103515625,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674187766.697,
    "write_down_up_ms": 0.013,
    "handler_ms": 12.208984375,
    "video_seen_ms": 35.10302734375,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674190267.921,
    "write_down_up_ms": 0.051,
    "handler_ms": 11.985107421875,
    "video_seen_ms": null,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674192769.045,
    "write_down_up_ms": 0.038,
    "handler_ms": 11.861083984375,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
