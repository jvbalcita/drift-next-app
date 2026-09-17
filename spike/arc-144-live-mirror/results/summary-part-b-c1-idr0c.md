# ARC-144 Part B — c1-idr0c

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 95.4 ms | 230 frames with a clock read |
| glass-to-glass p95 | 129 ms | |
| glass-to-glass mean | 79.7 ms | 39 samples below zero |
| glass-to-glass + page render bound | 95.4 ms | |
| input, device handler (excludes return video) p50 | 18.3 ms | 4 taps |
| input, video-observed p50 | 17.4 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1250 frames |
| browser jitter buffer | 0.17146400602409637 ms | |
| browser decode | 0.28100246153846153 ms | |
| browser processing delay | 0.44663415384615385 ms | |

## Clock

- skew used: 59.614990234375 (device_ntp_round_trip)
- ntp round trip (device side) +59.615 ms at 10.000 ms best RTT over 25 exchanges; min-delay estimator +47.000 ms over 157 posts (spread 273.000 ms); adb midpoint +35.201 ms (min RTT 57.8 ms, spread 23.593 ms)
- device clock in pixels: {230 0 32 217 228 0 10517}

## Decode

- 230 frames presented, 230 carried a decodable clock, 0 did not
- browser: received 1250, decoded 650, dropped 14, lost 0
- probe cost: mean 6.583478261336037 ms, max 14.5 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -35.20068359375,
    "spread_ms": 23.5927734375,
    "adb_rtt_min_ms": 57.77,
    "adb_rtt_p50_ms": 76.204
  },
  "page": {
    "host_minus_device_ms_min_delay": 47,
    "samples": 157,
    "spread_ms": 273
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 19.999,
    "capture_fps": 62.502,
    "encoder_pts_span_s": 37.298,
    "frames": 1250,
    "height": 2280,
    "idr_interval_s": 6.666,
    "kbps": 1604.133,
    "key_frames": 3,
    "sps_profile_level_id": "420032",
    "width": 1080
  },
  "requested": {
    "bit_rate": 0,
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
    "host_down_ms": 1789674734118.6208,
    "write_down_up_ms": 0.021,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674758850.335,
    "write_down_up_ms": 0.04,
    "handler_ms": 18.280029296875,
    "video_seen_ms": 17.364990234375,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674761346.27,
    "write_down_up_ms": 0.039,
    "handler_ms": 18.344970703125,
    "video_seen_ms": -28.670166015625,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674763842.8318,
    "write_down_up_ms": 0.032,
    "handler_ms": 17.783203125,
    "video_seen_ms": 124.568115234375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674766342.1492,
    "write_down_up_ms": 0.017,
    "handler_ms": 25.4658203125,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
