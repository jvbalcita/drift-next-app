# ARC-144 Part B — c1-base4

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 95.5 ms | 516 frames with a clock read |
| glass-to-glass p95 | 130.8 ms | |
| glass-to-glass mean | 87.7 ms | 38 samples below zero |
| glass-to-glass + page render bound | 95.5 ms | |
| input, device handler (excludes return video) p50 | 17.7 ms | 4 taps |
| input, video-observed p50 | 70.3 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1293 frames |
| browser jitter buffer | 0.48384705387205373 ms | |
| browser decode | 0.23769028132992323 ms | |
| browser processing delay | 0.7171747655583973 ms | |

## Clock

- skew used: 58.18603515625 (device_ntp_round_trip)
- ntp round trip (device side) +58.186 ms at 21.000 ms best RTT over 25 exchanges; min-delay estimator +59.000 ms over 156 posts (spread 233.000 ms); adb midpoint +34.600 ms (min RTT 53.5 ms, spread 16.904 ms)
- device clock in pixels: {516 0 17 217 511 0 16850}

## Decode

- 516 frames presented, 516 carried a decodable clock, 0 did not
- browser: received 1293, decoded 1173, dropped 15, lost 900
- probe cost: mean 7.00639534758967 ms, max 63.69999998807907 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -34.599609375,
    "spread_ms": 16.90380859375,
    "adb_rtt_min_ms": 53.546,
    "adb_rtt_p50_ms": 66.845
  },
  "page": {
    "host_minus_device_ms_min_delay": 59,
    "samples": 156,
    "spread_ms": 233
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.002,
    "capture_fps": 64.645,
    "encoder_pts_span_s": 36.824,
    "frames": 1293,
    "height": 2280,
    "idr_interval_s": 1.818,
    "kbps": 1769.291,
    "key_frames": 11,
    "sps_profile_level_id": "420032",
    "width": 1080
  },
  "requested": {
    "bit_rate": 0,
    "codec_options": [
      "i-frame-interval:int=2"
    ],
    "idr_interval_s": 2,
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
    "host_down_ms": 1789673648467.488,
    "write_down_up_ms": 0.308,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789673666029.881,
    "write_down_up_ms": 0.006,
    "handler_ms": 29.304931640625,
    "video_seen_ms": -12.281005859375,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789673668530.517,
    "write_down_up_ms": 0.037,
    "handler_ms": 17.6689453125,
    "video_seen_ms": 70.282958984375,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789673671031.235,
    "write_down_up_ms": 0.036,
    "handler_ms": 11.950927734375,
    "video_seen_ms": 102.864990234375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789673673533.084,
    "write_down_up_ms": 0.073,
    "handler_ms": 18.10205078125,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
