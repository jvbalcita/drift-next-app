#!/usr/bin/env python3
"""Summarise ARC-144 Part B reports into one table (rig helper, not a deliverable)."""
import json
import sys
import glob

rows = []
for path in sorted(glob.glob("results/report-live-*.json")):
    try:
        r = json.load(open(path))
    except Exception:
        # A run whose analysis failed before the NaN fix left a zero-byte
        # report; skip it rather than hiding the runs that did produce numbers.
        continue
    g, i, b, h, e = (r["glass_to_glass"], r["input_round_trip"],
                     r["browser_decode"], r["go_hop"], r["explain"])
    rows.append({
        "label": r["label"],
        "browser": "headless",
        "frames": g["frames_with_clock"],
        "unreadable": g["frames_without_clock"],
        "p50": g["p50_ms"], "p95": g["p95_ms"], "p99": g["p99_ms"],
        "mean": round(g["mean_ms"], 1), "min": round(g["min_ms"], 1),
        "max": round(g["max_ms"], 1), "neg": g.get("negative_samples", "n/a"),
        "skew": round(e["skew_ms"], 2), "skew_source": e["skew_source"],
        "in_p50": i["handler_p50_ms"], "in_p95": i["handler_p95_ms"],
        "in_n": i["handler_n"], "in_injected": i.get("taps_injected", "n/a"),
        "in_reported": i.get("device_taps_reported", "n/a"),
        "vid_p50": i["video_seen_p50_ms"], "vid_n": i["video_seen_n"],
        "hop_p50": h["hop_ms_p50"], "hop_max": h["hop_ms_max"],
        "hop_frames": h["frames"], "keyframes": h["key_frames"],
        "pts_p50": h["encoder_pts_delta_ms_p50"],
        "jb": round(b["jitter_buffer_ms"], 2), "dec": round(b["decode_ms"], 2),
        "proc": round(b["processing_delay_ms"], 2),
        "recv": b["frames_received"], "decoded": b["frames_decoded"],
        "lost": b["packets_lost"],
        "decode_rate": r["probe_cost"]["decode_rate"],
        "read_mean": round(r["probe_cost"]["read_ms_mean"] or 0, 2),
        "watch": "",
        "read_max": round(r["probe_cost"]["read_ms_max"] or 0, 2),
        "host_load": r["environment"]["host_loadavg"].split()[0],
        "device_load": (r["environment"]["device_loadavg"] or "?").split()[0],
        "notes": r["notes"],
    })

hdr = ("label", "frames", "unread", "p50", "p95", "p99", "min", "max", "neg", "skew",
       "in_p50", "in_p95", "vid_p50", "hop_p50", "jb", "dec", "proc", "recv", "lost",
       "decodeŧ", "read_mean")
print(" | ".join(hdr))
for r in rows:
    print(" | ".join(str(r[k]) for k in
                     ("label", "frames", "unreadable", "p50", "p95", "p99", "min", "max",
                      "neg", "skew", "in_p50", "in_p95", "vid_p50", "hop_p50", "jb",
                      "dec", "proc", "recv", "lost", "decode_rate", "read_mean")))
print()
for r in rows:
    print(json.dumps(r, indent=1))
