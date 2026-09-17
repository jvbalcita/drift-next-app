# ARC-144 Part B — c1-fps302

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | -16 ms | 7 frames with a clock read |
| glass-to-glass p95 | 34.7 ms | |
| glass-to-glass mean | -23.9 ms | 6 samples below zero |
| glass-to-glass + page render bound | -16 ms | |
| input, device handler (excludes return video) p50 | 10.6 ms | 4 taps |
| input, video-observed p50 | 806.5 ms | 1 taps |
| Go hop forward p50 | 0.2 ms | 638 frames |
| browser jitter buffer | 0.9953942307692308 ms | |
| browser decode | 0.33958947368421055 ms | |
| browser processing delay | 1.312684210526316 ms | |

## Clock

- skew used: 58.89990234375 (device_ntp_round_trip)
- ntp round trip (device side) +58.900 ms at 6.000 ms best RTT over 25 exchanges; min-delay estimator +63.000 ms over 178 posts (spread 238.000 ms); adb midpoint +38.660 ms (min RTT 60.4 ms, spread 18.740 ms)
- device clock in pixels: {7 100 167 183 7 0 933}

## Decode

- 7 frames presented, 7 carried a decodable clock, 0 did not
- browser: received 638, decoded 38, dropped 14, lost 0
- probe cost: mean 5.7428571581840515 ms, max 9.5 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -38.659912109375,
    "spread_ms": 18.740478515625,
    "adb_rtt_min_ms": 60.401,
    "adb_rtt_p50_ms": 99.918
  },
  "page": {
    "host_minus_device_ms_min_delay": 63,
    "samples": 178,
    "spread_ms": 238
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.011,
    "capture_fps": 31.883,
    "encoder_pts_span_s": 36.981,
    "frames": 638,
    "height": 2280,
    "idr_interval_s": 10.005,
    "kbps": 1341.993,
    "key_frames": 2,
    "sps_profile_level_id": "420032",
    "width": 1080
  },
  "requested": {
    "bit_rate": 0,
    "codec_options": [],
    "idr_interval_s": 0,
    "max_fps": 30,
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
    "host_down_ms": 1789674615333.558,
    "write_down_up_ms": 0.212,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674646887.689,
    "write_down_up_ms": 0.016,
    "handler_ms": 19.2109375,
    "video_seen_ms": 806.510986328125,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674649388.3252,
    "write_down_up_ms": 0.024,
    "handler_ms": 10.57470703125,
    "video_seen_ms": null,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674651889.093,
    "write_down_up_ms": 0.454,
    "handler_ms": 11.806884765625,
    "video_seen_ms": null,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674654390.2458,
    "write_down_up_ms": 0.086,
    "handler_ms": 9.654052734375,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
