# ARC-144 Part B — live7

| measurement | value | n |
|---|---|---|
| glass-to-glass p50 | 134.3 ms | 737 frames with a clock read |
| glass-to-glass p95 | 260.1 ms | |
| glass-to-glass mean | 151.9 ms | 22 samples below zero |
| glass-to-glass + page render bound | 134.3 ms | |
| input, device handler (excludes return video) p50 | 10.7 ms | 8 taps |
| input, video-observed p50 | 127.4 ms | 3 taps |
| Go hop forward p50 | 0.1 ms | 1693 frames |
| browser jitter buffer | 2.2823076205287713 ms | |
| browser decode | 0.2356368979591837 ms | |
| browser processing delay | 2.6223294693877555 ms | |

## Clock

- skew used: -51.668212890625 (device_ntp_round_trip)
- ntp round trip (device side) -51.668 ms at 7.000 ms best RTT over 25 exchanges; min-delay estimator -48.000 ms over 195 posts (spread 478.000 ms); adb midpoint -66.543 ms (min RTT 71.9 ms, spread 17.921 ms)
- device clock in pixels: {737 5 19 2271 737 0 26869}

## Decode

- 737 frames presented, 737 carried a decodable clock, 0 did not
- browser: received 1693, decoded 1225, dropped 61, lost 1119
- probe cost: mean 6.463229308296576 ms, max 58.5 ms per frame

## Skew reports

```json
{
  "adb": {
    "samples": 25,
    "ok": 25,
    "device_minus_host_ms_min_rtt": 66.54296875,
    "spread_ms": 17.921142578125,
    "adb_rtt_min_ms": 71.867,
    "adb_rtt_p50_ms": 84.949
  },
  "page": {
    "host_minus_device_ms_min_delay": -48,
    "samples": 195,
    "spread_ms": 478
  }
}
```

## Taps

```json
[
  {
    "seq": 1,
    "kind": "setup",
    "host_down_ms": 1789670389134.461,
    "write_down_up_ms": 0.073,
    "handler_ms": null,
    "video_seen_ms": null,
    "matched_device_tap": 0
  },
  {
    "seq": 2,
    "kind": "measure",
    "host_down_ms": 1789670406355.7969,
    "write_down_up_ms": 0.03,
    "handler_ms": 24.534912109375,
    "video_seen_ms": 101.302978515625,
    "matched_device_tap": 1
  },
  {
    "seq": 3,
    "kind": "measure",
    "host_down_ms": 1789670408856.908,
    "write_down_up_ms": 0.073,
    "handler_ms": 11.423828125,
    "video_seen_ms": 138.7919921875,
    "matched_device_tap": 2
  },
  {
    "seq": 4,
    "kind": "measure",
    "host_down_ms": 1789670411357.429,
    "write_down_up_ms": 0.029,
    "handler_ms": 12.90283203125,
    "video_seen_ms": 127.37109375,
    "matched_device_tap": 3
  },
  {
    "seq": 5,
    "kind": "measure",
    "host_down_ms": 1789670413857.633,
    "write_down_up_ms": 0.03,
    "handler_ms": 7.69873046875,
    "video_seen_ms": null,
    "matched_device_tap": 4
  },
  {
    "seq": 6,
    "kind": "measure",
    "host_down_ms": 1789670416358.219,
    "write_down_up_ms": 0.04,
    "handler_ms": 9.11279296875,
    "video_seen_ms": null,
    "matched_device_tap": 5
  },
  {
    "seq": 7,
    "kind": "measure",
    "host_down_ms": 1789670418859.0369,
    "write_down_up_ms": 0.031,
    "handler_ms": 16.294921875,
    "video_seen_ms": null,
    "matched_device_tap": 6
  },
  {
    "seq": 8,
    "kind": "measure",
    "host_down_ms": 1789670421359.642,
    "write_down_up_ms": 0.061,
    "handler_ms": 10.689697265625,
    "video_seen_ms": null,
    "matched_device_tap": 7
  },
  {
    "seq": 9,
    "kind": "measure",
    "host_down_ms": 1789670423859.9402,
    "write_down_up_ms": 0.201,
    "handler_ms": 9.3916015625,
    "video_seen_ms": null,
    "matched_device_tap": 8
  }
]
```
