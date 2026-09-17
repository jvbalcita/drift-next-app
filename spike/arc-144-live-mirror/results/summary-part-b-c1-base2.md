# ARC-144 Part B — c1-base2

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 120 ms | 341 frames with a clock read |
| glass-to-glass p95 | 191.3 ms | |
| glass-to-glass mean | 118.2 ms | 28 samples below zero |
| glass-to-glass + page render bound | 120 ms | |
| input, device handler (excludes return video) p50 | 4.4 ms | 4 taps |
| input, video-observed p50 | 66.6 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1158 frames |
| browser jitter buffer | 1.6118684337349396 ms | |
| browser decode | 0.2691261997405966 ms | |
| browser processing delay | 1.9862439688715952 ms | |

## Clock

- skew used: 36.507080078125 (device_ntp_round_trip)
- ntp round trip (device side) +36.507 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +42.000 ms over 156 posts (spread 246.000 ms); adb midpoint +13.301 ms (min RTT 67.5 ms, spread 19.772 ms)
- device clock in pixels: {341 0 19 1464 338 0 14081}

## Decode

- 341 frames presented, 341 carried a decodable clock, 0 did not
- browser: received 1158, decoded 771, dropped 59, lost 0
- probe cost: mean 6.3583577722748 ms, max 12.800000011920929 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -13.300537109375,
    "spread_ms": 19.7724609375,
    "adb_rtt_min_ms": 67.505,
    "adb_rtt_p50_ms": 85.643
  },
  "page": {
    "host_minus_device_ms_min_delay": 42,
    "samples": 156,
    "spread_ms": 246
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.003,
    "capture_fps": 57.892,
    "encoder_pts_span_s": 37.035,
    "frames": 1158,
    "height": 2280,
    "idr_interval_s": 2,
    "kbps": 1630.2,
    "key_frames": 10,
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
    "host_down_ms": 1789672851439.767,
    "write_down_up_ms": 0.028,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789672872104.6,
    "write_down_up_ms": 0.018,
    "handler_ms": 26.906982421875,
    "video_seen_ms": 66.599853515625,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789672874606.042,
    "write_down_up_ms": 0.036,
    "handler_ms": 8.465087890625,
    "video_seen_ms": 65.05810546875,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789672877107.447,
    "write_down_up_ms": 0.036,
    "handler_ms": 2.06005859375,
    "video_seen_ms": 113.653076171875,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789672879608.1438,
    "write_down_up_ms": 0.022,
    "handler_ms": 4.36328125,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
