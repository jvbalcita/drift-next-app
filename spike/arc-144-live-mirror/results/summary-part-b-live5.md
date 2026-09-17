# ARC-144 Part B — live5

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 127.9 ms | 892 frames with a clock read |
| glass-to-glass p95 | 172.6 ms | |
| glass-to-glass mean | 121.4 ms | 36 samples below zero |
| glass-to-glass + page render bound | 127.9 ms | |
| input, device handler (excludes return video) p50 | 15.7 ms | 8 taps |
| input, video-observed p50 | 85.5 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1700 frames |
| browser jitter buffer | 2.1200683233929754 ms | |
| browser decode | 0.2559967961826858 ms | |
| browser processing delay | 2.429612474437628 ms | |

## Clock

- skew used: -46.618896484375 (device_ntp_round_trip)
- ntp round trip (device side) -46.619 ms at 21.000 ms best RTT over 25 exchanges; min-delay estimator +0.000 ms over 201 posts (spread 236.000 ms); adb midpoint -71.217 ms (min RTT 57.7 ms, spread 28.783 ms)
- device clock in pixels: {892 0 19 1511 888 0 27112}

## Decode

- 892 frames presented, 892 carried a decodable clock, 0 did not
- browser: received 1700, decoded 1467, dropped 42, lost 0
- probe cost: mean 7.23979820484805 ms, max 115.09999996423721 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": 71.216552734375,
    "spread_ms": 28.783447265625,
    "adb_rtt_min_ms": 57.688,
    "adb_rtt_p50_ms": 73.229
  },
  "page": {
    "host_minus_device_ms_min_delay": 0,
    "samples": 201,
    "spread_ms": 236
  }
}
```

## Taps

```json
[
  {
    "seq": 1,
    "kind": "setup",
    "host_down_ms": 1789670153811.693,
    "write_down_up_ms": 0.179,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789670172674.532,
    "write_down_up_ms": 0.027,
    "handler_ms": 23.84912109375,
    "video_seen_ms": 85.468017578125,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789670175175.2441,
    "write_down_up_ms": 0.04,
    "handler_ms": 13.136962890625,
    "video_seen_ms": 68.055908203125,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789670177675.463,
    "write_down_up_ms": 0.307,
    "handler_ms": 21.918212890625,
    "video_seen_ms": 200.93701171875,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789670180176.634,
    "write_down_up_ms": 0.022,
    "handler_ms": 15.7470703125,
    "video_seen_ms": null,
    "matched_device_tap": 4
  },
  {
    "seq": 6,
    "kind": "measure",
    "host_down_ms": 1789670182677.7222,
    "write_down_up_ms": 0.031,
    "handler_ms": 13.658935546875,
    "video_seen_ms": null,
    "matched_device_tap": 5
  },
  {
    "seq": 7,
    "kind": "measure",
    "host_down_ms": 1789670185178.176,
    "write_down_up_ms": 0.04,
    "handler_ms": 17.205078125,
    "video_seen_ms": null,
    "matched_device_tap": 6
  },
  {
    "seq": 8,
    "kind": "measure",
    "host_down_ms": 1789670187679.2341,
    "write_down_up_ms": 0.03,
    "handler_ms": 12.14697265625,
    "video_seen_ms": null,
    "matched_device_tap": 7
  },
  {
    "seq": 9,
    "kind": "measure",
    "host_down_ms": 1789670190180.001,
    "write_down_up_ms": 0.016,
    "handler_ms": 16.380126953125,
    "video_seen_ms": null,
    "matched_device_tap": 8
  }
]
```
