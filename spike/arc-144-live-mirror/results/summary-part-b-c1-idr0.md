# ARC-144 Part B — c1-idr0

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 85.6 ms | 142 frames with a clock read |
| glass-to-glass p95 | 119.3 ms | |
| glass-to-glass mean | 66.3 ms | 36 samples below zero |
| glass-to-glass + page render bound | 85.6 ms | |
| input, device handler (excludes return video) p50 | 9.9 ms | 4 taps |
| input, video-observed p50 | 92.7 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1203 frames |
| browser jitter buffer | 0.1509902390438247 ms | |
| browser decode | 0.28619302325581397 ms | |
| browser processing delay | 0.41259767441860457 ms | |

## Clock

- skew used: 53.510986328125 (device_ntp_round_trip)
- ntp round trip (device side) +53.511 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator +58.000 ms over 156 posts (spread 40.000 ms); adb midpoint +33.875 ms (min RTT 59.2 ms, spread 23.180 ms)
- device clock in pixels: {142 0 31 2200 141 0 9861}

## Decode

- 142 frames presented, 142 carried a decodable clock, 0 did not
- browser: received 1203, decoded 473, dropped 29, lost 0
- probe cost: mean 6.2866197232629215 ms, max 19 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": -33.87451171875,
    "spread_ms": 23.18017578125,
    "adb_rtt_min_ms": 59.156,
    "adb_rtt_p50_ms": 88.475
  },
  "page": {
    "host_minus_device_ms_min_delay": 58,
    "samples": 156,
    "spread_ms": 40
  }
}
```

## Encoder

```json
{
  "measured": {
    "arrival_span_s": 19.999,
    "capture_fps": 60.152,
    "encoder_pts_span_s": 36.968,
    "frames": 1203,
    "height": 2280,
    "idr_interval_s": 6.666,
    "kbps": 1583.079,
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
    "host_down_ms": 1789673584965.184,
    "write_down_up_ms": 0.024,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789673609474.371,
    "write_down_up_ms": 0.01,
    "handler_ms": 21.139892578125,
    "video_seen_ms": 9.62890625,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789673611974.523,
    "write_down_up_ms": 0.019,
    "handler_ms": 7.988037109375,
    "video_seen_ms": 92.677001953125,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789673614476.622,
    "write_down_up_ms": 0.031,
    "handler_ms": 9.888916015625,
    "video_seen_ms": 123.778076171875,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789673616977.558,
    "write_down_up_ms": 0.196,
    "handler_ms": 14.952880859375,
    "video_seen_ms": null,
    "matched_device_tap": 4
  }
]
```
