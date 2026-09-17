# ARC-144 Part B — live4

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 43.1 ms | 840 frames with a clock read |
| glass-to-glass p95 | 77.7 ms | |
| glass-to-glass mean | 4.277517409551711 ms | |
| glass-to-glass + page render bound | 43.1 ms | |
| input, device handler (excludes return video) p50 | 2564.7 ms | 8 taps |
| input, video-observed p50 | 3175.4 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1720 frames |
| browser jitter buffer | 0.2057615284128021 ms | |
| browser decode | 0.2632398653198653 ms | |
| browser processing delay | 0.46618154882154883 ms | |

## Clock

- skew used: 0 (page_min_delay)
- page min-delay estimator 0.000 ms over 202 posts (spread 185.000 ms); adb midpoint estimator -72.008 ms (min RTT 61.6 ms, spread 14.522 ms)
- device clock in pixels: {840 0 19 1560 833 0 26862}

## Decode

- 840 frames presented, 840 carried a decodable clock, 0 did not
- browser: received 1720, decoded 1485, dropped 46, lost 0
- probe cost: mean 7.283690477127121 ms, max 75.69999998807907 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": 72.007568359375,
    "spread_ms": 14.521728515625,
    "adb_rtt_min_ms": 61.626,
    "adb_rtt_p50_ms": 71.788
  },
  "page": {
    "host_minus_device_ms_min_delay": 0,
    "samples": 202,
    "spread_ms": 185
  }
}
```

## Taps

```json
[
  {
    "seq": 1,
    "kind": "setup",
    "host_down_ms": 1789670001304.814,
    "write_down_up_ms": 0.056,
    "handler_ms": 17809.18603515625,
    "video_seen_ms": 17748.086181640625
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789670019043.9502,
    "write_down_up_ms": 0.049,
    "handler_ms": 2562.0498046875,
    "video_seen_ms": 3175.449951171875
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789670021544.9138,
    "write_down_up_ms": 0.034,
    "handler_ms": 2572.086181640625,
    "video_seen_ms": 2907.7861328125
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789670024046.308,
    "write_down_up_ms": 0.039,
    "handler_ms": 2564.69189453125,
    "video_seen_ms": null
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789670026546.851,
    "write_down_up_ms": 0.027,
    "handler_ms": 2568.14892578125,
    "video_seen_ms": null
  },
  {
    "seq": 6,
    "kind": "measure",
    "host_down_ms": 1789670029047.942,
    "write_down_up_ms": 0.025,
    "handler_ms": 2566.05810546875,
    "video_seen_ms": null
  },
  {
    "seq": 7,
    "kind": "measure",
    "host_down_ms": 1789670031549.032,
    "write_down_up_ms": 0.038,
    "handler_ms": 2562.968017578125,
    "video_seen_ms": null
  },
  {
    "seq": 8,
    "kind": "measure",
    "host_down_ms": 1789670034049.458,
    "write_down_up_ms": 0.027,
    "handler_ms": 2563.5419921875,
    "video_seen_ms": null
  },
  {
    "seq": 9,
    "kind": "measure",
    "host_down_ms": 1789670036550.572,
    "write_down_up_ms": 0.04,
    "handler_ms": null,
    "video_seen_ms": null
  }
]
```
