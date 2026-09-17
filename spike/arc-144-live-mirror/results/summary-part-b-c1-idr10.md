# ARC-144 Part B — c1-idr10

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 112.9 ms | 202 frames with a clock read |
| glass-to-glass p95 | 142.4 ms | |
| glass-to-glass mean | 101.3 ms | 8 samples below zero |
| glass-to-glass + page render bound | 112.9 ms | |
| input, device handler (excludes return video) p50 | 10.2 ms | 4 taps |
| input, video-observed p50 | 60.3 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1116 frames |
| browser jitter buffer | 0.1289467924528302 ms | |
| browser decode | 0.26163262135922327 ms | |
| browser processing delay | 0.3694598058252427 ms | |

## Clock

- skew used: 52.178955078125 (device_ntp_round_trip)
- ntp round trip (device side) +52.179 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +58.000 ms over 156 posts (spread 241.000 ms); adb midpoint +24.622 ms (min RTT 69.6 ms, spread 37.116 ms)
- device clock in pixels: {202 0 23 258 201 0 9511}

## Decode

- 202 frames presented, 202 carried a decodable clock, 0 did not
- browser: received 1116, decoded 515, dropped 15, lost 0
- probe cost: mean 6.794554458101197 ms, max 18.399999976158142 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -24.62158203125,
    "spread_ms": 37.115966796875,
    "adb_rtt_min_ms": 69.558,
    "adb_rtt_p50_ms": 92.909
  },
  "page": {
    "host_minus_device_ms_min_delay": 58,
    "samples": 156,
    "spread_ms": 241
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.007,
    "capture_fps": 55.781,
    "encoder_pts_span_s": 37.066,
    "frames": 1116,
    "height": 2280,
    "idr_interval_s": 10.003,
    "kbps": 1563.108,
    "key_frames": 2,
    "sps_profile_level_id": "420032",
    "width": 1080
  },
  "requested": {
    "bit_rate": 0,
    "codec_options": [
      "i-frame-interval:int=10"
    ],
    "idr_interval_s": 10,
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
    "host_down_ms": 1789673537093.065,
    "write_down_up_ms": 0.074,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789673561917.513,
    "write_down_up_ms": 0.032,
    "handler_ms": 23.666015625,
    "video_seen_ms": 60.286865234375,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789673564419.649,
    "write_down_up_ms": 0.037,
    "handler_ms": 13.530029296875,
    "video_seen_ms": 24.65087890625,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789673566920.9758,
    "write_down_up_ms": 0.042,
    "handler_ms": 10.203125,
    "video_seen_ms": 173.323974609375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789673569423.0542,
    "write_down_up_ms": 0.032,
    "handler_ms": 6.124755859375,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
