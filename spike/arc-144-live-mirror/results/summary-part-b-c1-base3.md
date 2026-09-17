# ARC-144 Part B — c1-base3

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 102.2 ms | 488 frames with a clock read |
| glass-to-glass p95 | 189.6 ms | |
| glass-to-glass mean | 105.7 ms | 22 samples below zero |
| glass-to-glass + page render bound | 102.2 ms | |
| input, device handler (excludes return video) p50 | 12.2 ms | 4 taps |
| input, video-observed p50 | 114.1 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1132 frames |
| browser jitter buffer | 0.6197963796477495 ms | |
| browser decode | 0.2492984833164813 ms | |
| browser processing delay | 0.8784833164812943 ms | |

## Clock

- skew used: 53.338134765625 (device_ntp_round_trip)
- ntp round trip (device side) +53.338 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +57.000 ms over 156 posts (spread 244.000 ms); adb midpoint +26.369 ms (min RTT 63.1 ms, spread 19.395 ms)
- device clock in pixels: {488 0 19 566 481 0 16896}

## Decode

- 488 frames presented, 488 carried a decodable clock, 0 did not
- browser: received 1132, decoded 989, dropped 33, lost 0
- probe cost: mean 6.983196721335903 ms, max 18.900000035762787 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -26.369140625,
    "spread_ms": 19.394775390625,
    "adb_rtt_min_ms": 63.143,
    "adb_rtt_p50_ms": 96.193
  },
  "page": {
    "host_minus_device_ms_min_delay": 57,
    "samples": 156,
    "spread_ms": 244
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.008,
    "capture_fps": 56.579,
    "encoder_pts_span_s": 37.101,
    "frames": 1132,
    "height": 2280,
    "idr_interval_s": 2.001,
    "kbps": 1568.557,
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
    "host_down_ms": 1789673488828.532,
    "write_down_up_ms": 0.109,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789673506583.252,
    "write_down_up_ms": 0.047,
    "handler_ms": 24.086181640625,
    "video_seen_ms": 33.84814453125,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789673509085.053,
    "write_down_up_ms": 0.028,
    "handler_ms": 18.28515625,
    "video_seen_ms": 115.346923828125,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789673511586.1208,
    "write_down_up_ms": 0.044,
    "handler_ms": 12.21728515625,
    "video_seen_ms": 114.0791015625,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789673514087.424,
    "write_down_up_ms": 0.009,
    "handler_ms": 8.9140625,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
