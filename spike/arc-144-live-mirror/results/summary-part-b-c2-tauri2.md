# ARC-144 Part B — c2-tauri2

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 84 ms | 880 frames with a clock read |
| glass-to-glass p95 | 150 ms | |
| glass-to-glass mean | 90.9 ms | 0 samples below zero |
| glass-to-glass + page render bound | 84 ms | |
| input, device handler (excludes return video) p50 | 10.7 ms | 4 taps |
| input, video-observed p50 | 117.3 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1328 frames |
| browser jitter buffer | 1.3349140650406501 ms | |
| browser decode | 0.518008516020236 ms | |
| browser processing delay | 1.743318634064081 ms | |

## Clock

- skew used: 30.989013671875 (device_ntp_round_trip)
- ntp round trip (device side) +30.989 ms at 5.000 ms best RTT over 25 exchanges; min-delay estimator +35.000 ms over 156 posts (spread 31.000 ms); adb midpoint +9.610 ms (min RTT 59.3 ms, spread 17.826 ms)
- device clock in pixels: {880 11 17 233 880 0 17250}

## Decode

- 880 frames presented, 880 carried a decodable clock, 0 did not
- browser: received 1186, decoded 1186, dropped 44, lost 2981
- probe cost: mean 6.900000000000103 ms, max 19 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -9.60986328125,
    "spread_ms": 17.826416015625,
    "adb_rtt_min_ms": 59.255,
    "adb_rtt_p50_ms": 80.072
  },
  "page": {
    "host_minus_device_ms_min_delay": 35,
    "samples": 156,
    "spread_ms": 31
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.006,
    "capture_fps": 66.38,
    "encoder_pts_span_s": 36.726,
    "frames": 1328,
    "height": 2280,
    "idr_interval_s": 1.667,
    "kbps": 1719.5,
    "key_frames": 12,
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
    "host_down_ms": 1789675013930.373,
    "write_down_up_ms": 0.031,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789675031234.598,
    "write_down_up_ms": 0.03,
    "handler_ms": 30.39111328125,
    "video_seen_ms": 123.402099609375,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789675033734.725,
    "write_down_up_ms": 0.015,
    "handler_ms": 22.263916015625,
    "video_seen_ms": 117.27490234375,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789675036235.0688,
    "write_down_up_ms": 0.006,
    "handler_ms": 6.920166015625,
    "video_seen_ms": 71.93115234375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789675038735.283,
    "write_down_up_ms": 0.036,
    "handler_ms": 10.7060546875,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
