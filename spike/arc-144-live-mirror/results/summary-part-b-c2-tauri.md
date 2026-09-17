# ARC-144 Part B — c2-tauri

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 93.7 ms | 877 frames with a clock read |
| glass-to-glass p95 | 180.7 ms | |
| glass-to-glass mean | 106.3 ms | 1 samples below zero |
| glass-to-glass + page render bound | 93.7 ms | |
| input, device handler (excludes return video) p50 | 18 ms | 4 taps |
| input, video-observed p50 | 97.7 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1246 frames |
| browser jitter buffer | 1.9681098715890848 ms | |
| browser decode | 0.43624409385113266 ms | |
| browser processing delay | 2.4090446601941746 ms | |

## Clock

- skew used: 36.265869140625 (device_ntp_round_trip)
- ntp round trip (device side) +36.266 ms at 22.000 ms best RTT over 25 exchanges; min-delay estimator +35.000 ms over 218 posts (spread 243.000 ms); adb midpoint +5.198 ms (min RTT 60.4 ms, spread 15.259 ms)
- device clock in pixels: {877 10 17 116 877 0 17049}

## Decode

- 877 frames presented, 877 carried a decodable clock, 0 did not
- browser: received 1236, decoded 1236, dropped 10, lost 0
- probe cost: mean 7.392246294184706 ms, max 27.99999999999909 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -5.197509765625,
    "spread_ms": 15.259033203125,
    "adb_rtt_min_ms": 60.412,
    "adb_rtt_p50_ms": 69.462
  },
  "page": {
    "host_minus_device_ms_min_delay": 35,
    "samples": 218,
    "spread_ms": 243
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.008,
    "capture_fps": 62.276,
    "encoder_pts_span_s": 52.407,
    "frames": 1246,
    "height": 2280,
    "idr_interval_s": 1.819,
    "kbps": 1667.713,
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
    "host_down_ms": 1789674933200.4202,
    "write_down_up_ms": 0.039,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674966154.091,
    "write_down_up_ms": 0.025,
    "handler_ms": 31.1748046875,
    "video_seen_ms": 94.908935546875,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674968654.308,
    "write_down_up_ms": 0.025,
    "handler_ms": 17.957763671875,
    "video_seen_ms": 97.69189453125,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674971155.4292,
    "write_down_up_ms": 0.094,
    "handler_ms": 20.836669921875,
    "video_seen_ms": 191.57080078125,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674973656.8362,
    "write_down_up_ms": 0.029,
    "handler_ms": 17.4296875,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
