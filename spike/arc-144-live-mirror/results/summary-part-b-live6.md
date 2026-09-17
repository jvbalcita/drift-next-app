# ARC-144 Part B — live6

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 96 ms | 835 frames with a clock read |
| glass-to-glass p95 | 134.6 ms | |
| glass-to-glass mean | 98.9 ms | 29 samples below zero |
| glass-to-glass + page render bound | 96 ms | |
| input, device handler (excludes return video) p50 | 10.4 ms | 8 taps |
| input, video-observed p50 | 92.1 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1679 frames |
| browser jitter buffer | 0.2594391943734015 ms | |
| browser decode | 0.25649183006535947 ms | |
| browser processing delay | 0.514827908496732 ms | |

## Clock

- skew used: -51.330078125 (device_ntp_round_trip)
- ntp round trip (device side) -51.330 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator -48.000 ms over 200 posts (spread 227.000 ms); adb midpoint -70.969 ms (min RTT 58.6 ms, spread 29.276 ms)
- device clock in pixels: {835 0 19 471 832 0 26913}

## Decode

- 835 frames presented, 835 carried a decodable clock, 0 did not
- browser: received 1679, decoded 1530, dropped 34, lost 0
- probe cost: mean 7.39544910228181 ms, max 78.80000001192093 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": 70.968994140625,
    "spread_ms": 29.2763671875,
    "adb_rtt_min_ms": 58.632,
    "adb_rtt_p50_ms": 87.15
  },
  "page": {
    "host_minus_device_ms_min_delay": -48,
    "samples": 200,
    "spread_ms": 227
  }
}
```

## Taps

```json
[
  {
    "seq": 1,
    "kind": "setup",
    "host_down_ms": 1789670307126.322,
    "write_down_up_ms": 0.903,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789670324669.102,
    "write_down_up_ms": 0.031,
    "handler_ms": 20.56787109375,
    "video_seen_ms": 109.2978515625,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789670327169.229,
    "write_down_up_ms": 0.021,
    "handler_ms": 10.44091796875,
    "video_seen_ms": -74.22900390625,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789670329669.4211,
    "write_down_up_ms": 0.018,
    "handler_ms": 11.248779296875,
    "video_seen_ms": 92.078857421875,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789670332169.847,
    "write_down_up_ms": 0.026,
    "handler_ms": 9.822998046875,
    "video_seen_ms": null,
    "matched_device_tap": 4
  },
  {
    "seq": 6,
    "kind": "measure",
    "host_down_ms": 1789670334670.0469,
    "write_down_up_ms": 0.027,
    "handler_ms": 36.623046875,
    "video_seen_ms": null,
    "matched_device_tap": 5
  },
  {
    "seq": 7,
    "kind": "measure",
    "host_down_ms": 1789670337170.1172,
    "write_down_up_ms": 0.012,
    "handler_ms": 12.552734375,
    "video_seen_ms": null,
    "matched_device_tap": 6
  },
  {
    "seq": 8,
    "kind": "measure",
    "host_down_ms": 1789670339670.382,
    "write_down_up_ms": 0.04,
    "handler_ms": 9.287841796875,
    "video_seen_ms": null,
    "matched_device_tap": 7
  },
  {
    "seq": 9,
    "kind": "measure",
    "host_down_ms": 1789670342170.797,
    "write_down_up_ms": 0.035,
    "handler_ms": 7.872802734375,
    "video_seen_ms": null,
    "matched_device_tap": 8
  }
]
```
