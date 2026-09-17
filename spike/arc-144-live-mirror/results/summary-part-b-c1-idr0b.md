# ARC-144 Part B — c1-idr0b

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 83 ms | 139 frames with a clock read |
| glass-to-glass p95 | 132.8 ms | |
| glass-to-glass mean | 72.3 ms | 32 samples below zero |
| glass-to-glass + page render bound | 83 ms | |
| input, device handler (excludes return video) p50 | 10.5 ms | 4 taps |
| input, video-observed p50 | 127.4 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1241 frames |
| browser jitter buffer | 0.1216508875739645 ms | |
| browser decode | 0.2714284518828452 ms | |
| browser processing delay | 0.36875794979079496 ms | |

## Clock

- skew used: 56.55908203125 (device_ntp_round_trip)
- ntp round trip (device side) +56.559 ms at 8.000 ms best RTT over 25 exchanges; min-delay estimator +62.000 ms over 155 posts (spread 236.000 ms); adb midpoint +30.310 ms (min RTT 71.3 ms, spread 28.688 ms)
- device clock in pixels: {139 0 30 2750 138 0 10467}

## Decode

- 139 frames presented, 139 carried a decodable clock, 0 did not
- browser: received 1241, decoded 478, dropped 29, lost 0
- probe cost: mean 6.5007194246319555 ms, max 21.80000001192093 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -30.31005859375,
    "spread_ms": 28.6875,
    "adb_rtt_min_ms": 71.35,
    "adb_rtt_p50_ms": 102.336
  },
  "page": {
    "host_minus_device_ms_min_delay": 62,
    "samples": 155,
    "spread_ms": 236
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.012,
    "capture_fps": 62.012,
    "encoder_pts_span_s": 36.782,
    "frames": 1241,
    "height": 2280,
    "idr_interval_s": 6.671,
    "kbps": 1518.558,
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
    "host_down_ms": 1789674498308.631,
    "write_down_up_ms": 0.085,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674521664.3079,
    "write_down_up_ms": 0.026,
    "handler_ms": 24.251220703125,
    "video_seen_ms": 129.9921875,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674524165.4668,
    "write_down_up_ms": 0.034,
    "handler_ms": 9.09228515625,
    "video_seen_ms": 62.033203125,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674526666.594,
    "write_down_up_ms": 0.036,
    "handler_ms": 10.965087890625,
    "video_seen_ms": 127.406005859375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674529167.092,
    "write_down_up_ms": 0.051,
    "handler_ms": 10.467041015625,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
