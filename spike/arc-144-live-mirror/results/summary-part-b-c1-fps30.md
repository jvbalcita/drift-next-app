# ARC-144 Part B — c1-fps30

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | -20.4 ms | 8 frames with a clock read |
| glass-to-glass p95 | 61.9 ms | |
| glass-to-glass mean | -16.2 ms | 6 samples below zero |
| glass-to-glass + page render bound | -20.4 ms | |
| input, device handler (excludes return video) p50 | 10.7 ms | 4 taps |
| input, video-observed p50 | 621.6 ms | 1 taps |
| Go hop forward p50 | 0.2 ms | 642 frames |
| browser jitter buffer | 0.35479464285714285 ms | |
| browser decode | 0.2731 ms | |
| browser processing delay | 0.29828095238095237 ms | |

## Clock

- skew used: 55.412841796875 (device_ntp_round_trip)
- ntp round trip (device side) +55.413 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +59.000 ms over 177 posts (spread 236.000 ms); adb midpoint +35.881 ms (min RTT 68.1 ms, spread 24.145 ms)
- device clock in pixels: {8 132 167 200 8 0 1134}

## Decode

- 8 frames presented, 8 carried a decodable clock, 0 did not
- browser: received 642, decoded 42, dropped 14, lost 162
- probe cost: mean 5.762500017881393 ms, max 7.800000011920929 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -35.880615234375,
    "spread_ms": 24.14501953125,
    "adb_rtt_min_ms": 68.065,
    "adb_rtt_p50_ms": 102.046
  },
  "page": {
    "host_minus_device_ms_min_delay": 59,
    "samples": 177,
    "spread_ms": 236
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.02,
    "capture_fps": 32.069,
    "encoder_pts_span_s": 36.717,
    "frames": 642,
    "height": 2280,
    "idr_interval_s": 10.01,
    "kbps": 1317.092,
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
    "host_down_ms": 1789674315551.515,
    "write_down_up_ms": 0.03,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674346844.3892,
    "write_down_up_ms": 0.028,
    "handler_ms": 20.023681640625,
    "video_seen_ms": 621.61083984375,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674349345.071,
    "write_down_up_ms": 0.034,
    "handler_ms": 18.341796875,
    "video_seen_ms": null,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674351845.739,
    "write_down_up_ms": 0.148,
    "handler_ms": 10.673828125,
    "video_seen_ms": null,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674354348.052,
    "write_down_up_ms": 0.016,
    "handler_ms": 10.36083984375,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
