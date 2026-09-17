# ARC-144 Part B — c1-main2

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 95.9 ms | 176 frames with a clock read |
| glass-to-glass p95 | 113.1 ms | |
| glass-to-glass mean | 69.2 ms | 40 samples below zero |
| glass-to-glass + page render bound | 95.9 ms | |
| input, device handler (excludes return video) p50 | 13.5 ms | 4 taps |
| input, video-observed p50 | 64.9 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1232 frames |
| browser jitter buffer | 0.0694763698630137 ms | |
| browser decode | 0.2728298561151079 ms | |
| browser processing delay | 0.328243345323741 ms | |

## Clock

- skew used: 57.8779296875 (device_ntp_round_trip)
- ntp round trip (device side) +57.878 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +62.000 ms over 155 posts (spread 240.000 ms); adb midpoint +35.021 ms (min RTT 73.8 ms, spread 25.580 ms)
- device clock in pixels: {176 0 31 1333 173 0 10233}

## Decode

- 176 frames presented, 176 carried a decodable clock, 0 did not
- browser: received 1232, decoded 556, dropped 28, lost 0
- probe cost: mean 6.416477274826982 ms, max 21.100000023841858 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -35.0205078125,
    "spread_ms": 25.580078125,
    "adb_rtt_min_ms": 73.813,
    "adb_rtt_p50_ms": 96.421
  },
  "page": {
    "host_minus_device_ms_min_delay": 62,
    "samples": 155,
    "spread_ms": 240
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 20.02,
    "capture_fps": 61.54,
    "encoder_pts_span_s": 36.697,
    "frames": 1232,
    "height": 2280,
    "idr_interval_s": 6.673,
    "kbps": 1435.689,
    "key_frames": 3,
    "sps_profile_level_id": "4d0032",
    "width": 1080
  },
  "requested": {
    "bit_rate": 0,
    "codec_options": [
      "profile:int=2",
      "level:int=256"
    ],
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
    "host_down_ms": 1789674567498.946,
    "write_down_up_ms": 0.057,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789674591951.843,
    "write_down_up_ms": 0.022,
    "handler_ms": 24.034912109375,
    "video_seen_ms": 48.757080078125,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789674594452.251,
    "write_down_up_ms": 0.031,
    "handler_ms": 15.626953125,
    "video_seen_ms": 64.948974609375,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789674596953.376,
    "write_down_up_ms": 0.033,
    "handler_ms": 13.501953125,
    "video_seen_ms": 113.72412109375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789674599454.241,
    "write_down_up_ms": 0.021,
    "handler_ms": 9.636962890625,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
