# ARC-144 Part B — live8

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 235.6 ms | 1396 frames with a clock read |
| glass-to-glass p95 | 275.6 ms | |
| glass-to-glass mean | 217.8 ms | 0 samples below zero |
| glass-to-glass + page render bound | 235.6 ms | |
| input, device handler (excludes return video) p50 | 12.7 ms | 8 taps |
| input, video-observed p50 | 203.8 ms | 3 taps |
| Go hop forward p50 | 0.2 ms | 1651 frames |
| browser jitter buffer | 9.728884615384615 ms | |
| browser decode | 0.4437848999393571 ms | |
| browser processing delay | 10.187528259551241 ms | |

## Clock

- skew used: -52.571044921875 (device_ntp_round_trip)
- ntp round trip (device side) -52.571 ms at 8.000 ms best RTT over 25 exchanges; min-delay estimator -49.000 ms over 199 posts (spread 55.000 ms); adb midpoint -72.256 ms (min RTT 48.9 ms, spread 797.905 ms)
- device clock in pixels: {1396 5 18 133 1396 0 27109}

## Decode

- 1396 frames presented, 1396 carried a decodable clock, 0 did not
- browser: received 1649, decoded 1649, dropped 2, lost 0
- probe cost: mean 7.55802292263603 ms, max 40 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": 72.256103515625,
    "spread_ms": 797.904541015625,
    "adb_rtt_min_ms": 48.874,
    "adb_rtt_p50_ms": 65.615
  },
  "page": {
    "host_minus_device_ms_min_delay": -49,
    "samples": 199,
    "spread_ms": 55
  }
}
```

## Taps

```json
[
  {
    "seq": 1,
    "kind": "setup",
    "host_down_ms": 1789670596189.761,
    "write_down_up_ms": 0.067,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789670613376.269,
    "write_down_up_ms": 0.04,
    "handler_ms": 28.159912109375,
    "video_seen_ms": 280.731201171875,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789670615876.703,
    "write_down_up_ms": 0.047,
    "handler_ms": 29.72607421875,
    "video_seen_ms": 159.29736328125,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789670618377.153,
    "write_down_up_ms": 0.105,
    "handler_ms": 9.27587890625,
    "video_seen_ms": 203.84716796875,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789670620878.529,
    "write_down_up_ms": 0.05,
    "handler_ms": 13.89990234375,
    "video_seen_ms": null,
    "matched_device_tap": 4
  },
  {
    "seq": 6,
    "kind": "measure",
    "host_down_ms": 1789670623379.081,
    "write_down_up_ms": 0.049,
    "handler_ms": 15.347900390625,
    "video_seen_ms": null,
    "matched_device_tap": 5
  },
  {
    "seq": 7,
    "kind": "measure",
    "host_down_ms": 1789670625879.738,
    "write_down_up_ms": 0.049,
    "handler_ms": 12.69091796875,
    "video_seen_ms": null,
    "matched_device_tap": 6
  },
  {
    "seq": 8,
    "kind": "measure",
    "host_down_ms": 1789670628383.864,
    "write_down_up_ms": 0.203,
    "handler_ms": 10.56494140625,
    "video_seen_ms": null,
    "matched_device_tap": 7
  },
  {
    "seq": 9,
    "kind": "measure",
    "host_down_ms": 1789670630884.598,
    "write_down_up_ms": 0.096,
    "handler_ms": 7.8310546875,
    "video_seen_ms": null,
    "matched_device_tap": 8
  }
]
```
