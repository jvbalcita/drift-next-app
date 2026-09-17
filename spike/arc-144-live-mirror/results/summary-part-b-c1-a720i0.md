# ARC-144 Part B — c1-a720i0

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 82 ms | 78 frames with a clock read |
| glass-to-glass p95 | 115.1 ms | |
| glass-to-glass mean | 85.0 ms | 0 samples below zero |
| glass-to-glass + page render bound | 82 ms | |
| input, device handler (excludes return video) p50 | 10.6 ms | 4 taps |
| input, video-observed p50 | 280.4 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1805 frames |
| browser jitter buffer | 0.048061746031746025 ms | |
| browser decode | 0.19584247933884297 ms | |
| browser processing delay | 0.23692115702479338 ms | |

## Clock

- skew used: 56.85595703125 (device_ntp_round_trip)
- ntp round trip (device side) +56.856 ms at 6.000 ms best RTT over 25 exchanges; min-delay estimator +62.000 ms over 155 posts (spread 57.000 ms); adb midpoint +29.173 ms (min RTT 70.6 ms, spread 20.936 ms)
- device clock in pixels: {78 16 165 364 78 0 9935}

## Decode

- 78 frames presented, 78 carried a decodable clock, 0 did not
- browser: received 1805, decoded 605, dropped 25, lost 0
- probe cost: mean 2.246153846765176 ms, max 16.80000001192093 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -29.1728515625,
    "spread_ms": 20.935546875,
    "adb_rtt_min_ms": 70.555,
    "adb_rtt_p50_ms": 98.013
  },
  "page": {
    "host_minus_device_ms_min_delay": 62,
    "samples": 155,
    "spread_ms": 57
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.007,
    "capture_fps": 90.22,
    "encoder_pts_span_s": 36.722,
    "frames": 1805,
    "height": 720,
    "idr_interval_s": 5.002,
    "kbps": 414.338,
    "key_frames": 4,
    "sps_profile_level_id": "42001f",
    "width": 340
  },
  "requested": {
    "bit_rate": 0,
    "codec_options": [],
    "idr_interval_s": 0,
    "max_fps": 0,
    "max_size": 720
  }
}
```

## Taps

```json
[
  {
    "seq": 1,
    "kind": "setup",
    "host_down_ms": 1789674449905.088,
    "write_down_up_ms": 0.042,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674474140.76,
    "write_down_up_ms": 0.012,
    "handler_ms": 22.095947265625,
    "video_seen_ms": 280.43994140625,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674476645.891,
    "write_down_up_ms": 0.043,
    "handler_ms": 7.96484375,
    "video_seen_ms": 125.208740234375,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674479147.2798,
    "write_down_up_ms": 0.049,
    "handler_ms": 10.576171875,
    "video_seen_ms": 323.6201171875,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674481647.716,
    "write_down_up_ms": 0.029,
    "handler_ms": 13.139892578125,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
