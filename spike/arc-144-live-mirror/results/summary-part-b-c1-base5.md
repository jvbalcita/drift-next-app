# ARC-144 Part B — c1-base5

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 83.2 ms | 520 frames with a clock read |
| glass-to-glass p95 | 132.3 ms | |
| glass-to-glass mean | 85.1 ms | 24 samples below zero |
| glass-to-glass + page render bound | 83.2 ms | |
| input, device handler (excludes return video) p50 | 8.6 ms | 4 taps |
| input, video-observed p50 | 142.7 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1258 frames |
| browser jitter buffer | 0.46679965307892457 ms | |
| browser decode | 0.2539245166959578 ms | |
| browser processing delay | 0.7162607205623902 ms | |

## Clock

- skew used: 59.440185546875 (device_ntp_round_trip)
- ntp round trip (device side) +59.440 ms at 8.000 ms best RTT over 25 exchanges; min-delay estimator +63.000 ms over 155 posts (spread 300.000 ms); adb midpoint +39.477 ms (min RTT 55.8 ms, spread 16.724 ms)
- device clock in pixels: {520 0 17 367 515 0 16884}

## Decode

- 520 frames presented, 520 carried a decodable clock, 0 did not
- browser: received 1258, decoded 1138, dropped 15, lost 0
- probe cost: mean 6.6482692316174505 ms, max 50.5 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -39.476806640625,
    "spread_ms": 16.723876953125,
    "adb_rtt_min_ms": 55.761,
    "adb_rtt_p50_ms": 90.498
  },
  "page": {
    "host_minus_device_ms_min_delay": 63,
    "samples": 155,
    "spread_ms": 300
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.008,
    "capture_fps": 62.876,
    "encoder_pts_span_s": 36.894,
    "frames": 1258,
    "height": 2280,
    "idr_interval_s": 1.819,
    "kbps": 1646.176,
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
    "host_down_ms": 1789674686537.617,
    "write_down_up_ms": 0.045,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674703855.205,
    "write_down_up_ms": 0.043,
    "handler_ms": 27.235107421875,
    "video_seen_ms": 10.19482421875,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674706355.851,
    "write_down_up_ms": 0.016,
    "handler_ms": 18.589111328125,
    "video_seen_ms": 142.748779296875,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674708856.791,
    "write_down_up_ms": 0.035,
    "handler_ms": 8.649169921875,
    "video_seen_ms": 191.708984375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674711357.059,
    "write_down_up_ms": 0.047,
    "handler_ms": 8.381103515625,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
