# ARC-144 Part B — c1-base

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 203.4 ms | 476 frames with a clock read |
| glass-to-glass p95 | 260.7 ms | |
| glass-to-glass mean | 190.0 ms | 9 samples below zero |
| glass-to-glass + page render bound | 203.4 ms | |
| input, device handler (excludes return video) p50 | 12.3 ms | 4 taps |
| input, video-observed p50 | 163.4 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1123 frames |
| browser jitter buffer | 7.650157291666666 ms | |
| browser decode | 0.267794539614561 ms | |
| browser processing delay | 8.113076659528907 ms | |

## Clock

- skew used: 22.94775390625 (device_ntp_round_trip)
- ntp round trip (device side) +22.948 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +27.000 ms over 156 posts (spread 41.000 ms); adb midpoint +1.387 ms (min RTT 53.2 ms, spread 913.743 ms)
- device clock in pixels: {476 0 19 1413 473 0 17130}

## Decode

- 476 frames presented, 476 carried a decodable clock, 0 did not
- browser: received 1123, decoded 934, dropped 26, lost 0
- probe cost: mean 7.063235293541636 ms, max 30.599999964237213 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -1.387451171875,
    "spread_ms": 913.743408203125,
    "adb_rtt_min_ms": 53.213,
    "adb_rtt_p50_ms": 72.916
  },
  "page": {
    "host_minus_device_ms_min_delay": 27,
    "samples": 156,
    "spread_ms": 41
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.003,
    "capture_fps": 56.142,
    "encoder_pts_span_s": 37.1,
    "frames": 1123,
    "height": 2280,
    "idr_interval_s": 2,
    "kbps": 1414.283,
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
    "host_down_ms": 1789672686839.71,
    "write_down_up_ms": 0.159,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789672705328.648,
    "write_down_up_ms": 0.031,
    "handler_ms": 19.2998046875,
    "video_seen_ms": 231.152099609375,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789672707829.085,
    "write_down_up_ms": 0.033,
    "handler_ms": 11.86279296875,
    "video_seen_ms": 80.614990234375,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789672710329.624,
    "write_down_up_ms": 0.034,
    "handler_ms": 12.32373046875,
    "video_seen_ms": 163.3759765625,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789672712830.593,
    "write_down_up_ms": 0.035,
    "handler_ms": 17.354736328125,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
