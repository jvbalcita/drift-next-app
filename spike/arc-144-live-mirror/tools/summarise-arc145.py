#!/usr/bin/env python3
"""ARC-145 C1: one table from every encoder-setting run.

    python3 tools/summarise-arc145.py c1-base base2 a720 idr10 ...

Each argument is a run label; the report it reads is results/report-live-<label>.json,
written by cmd/analyze-live for one live run. Nothing here is measured a second time
and nothing is inferred: every column is a field of that report, printed as it is.

The "encoded" columns are what came out of the device's socket (its session packet,
its SPS, its own PTS); the "asked for" columns are what the run passed to scrcpy.
A row whose two disagree is a knob the hardware did not honour, which is why both
sides are printed.
"""

import json
import os
import sys

RESULTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "results")


def load(label):
    with open(os.path.join(RESULTS, f"report-live-{label}.json")) as fh:
        return json.load(fh)


def num(v, fmt="%.1f"):
    if v is None:
        return "—"
    if isinstance(v, float) and v != v:  # NaN
        return "—"
    return fmt % v


def codec_opts(report):
    opts = report["encoder"]["requested"]["codec_options"] or []
    return ",".join(o.replace("i-frame-interval:int=", "idr").replace(":", "=") for o in opts) or "—"


def main(labels):
    rows = []
    for label in labels:
        r = load(label)
        g = r["glass_to_glass"]
        enc = r["encoder"]
        req, mea = enc["requested"], enc["measured"]
        dec = r["browser_decode"]
        rows.append(
            {
                "label": r["label"],
                "asked": {
                    "max_size": req["max_size"] or "device",
                    "max_fps": req["max_fps"] or "device",
                    "bit_rate": req["bit_rate"] or "8M (default)",
                    "idr_s": req["idr_interval_s"],
                    "codec_options": codec_opts(r),
                },
                "encoded": {
                    "size": f"{mea['width']}x{mea['height']}",
                    "kbps": mea["kbps"],
                    "fps": mea["capture_fps"],
                    "key_frames": mea["key_frames"],
                    "idr_s": mea["idr_interval_s"],
                    "sps": mea["sps_profile_level_id"] or "—",
                },
                "frames": {
                    "forwarded": mea["frames"],
                    "decoded": dec["frames_decoded"],
                    "presented": g["frames_presented"],
                    "with_clock": g["frames_with_clock"],
                },
                "probe_ms": (r["probe_cost"]["read_ms_mean"], r["probe_cost"]["read_ms_max"]),
                "skew_ms": r["explain"]["skew_ms"],
                "skew_source": r["explain"]["skew_source"],
                "g2g": (g["p50_ms"], g["p95_ms"], g["p99_ms"], g["mean_ms"], g["negative_samples"]),
                "browser": r["probe_user_agent"].split(") ")[0].split("(")[-1],
                "env": {
                    "host": r["environment"]["host_loadavg"].strip().replace("\n", " "),
                    "device": r["environment"]["device_loadavg"].strip().replace("\n", " "),
                },
                "error": r["run_error"],
            }
        )

    print("# ARC-145 C1 — device encoder settings vs measured glass-to-glass\n")
    print("| run | requested | encoded | forwarded / decoded / presented | g2g p50 | p95 | p99 | clock read | probe cost mean/max | skew used | browser |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    for x in rows:
        a, e, f = x["asked"], x["encoded"], x["frames"]
        print(
            "| `{label}` | {size}, {fps} fps, {br}, idr {idr}s {co} | {esize}, {kbps} kbit/s, {efps} fps, {kf} IDR (every {eidr}s), SPS {sps} | {fw} / {dp} / {pr} | **{p50}** | {p95} | {p99} | {cl}/{pr} | {pm}/{px} | {sk} ({src}) | {br2} |".format(
                label=x["label"],
                size=a["max_size"], fps=a["max_fps"], br=a["bit_rate"], idr=a["idr_s"], co=a["codec_options"],
                esize=e["size"], kbps=num(e["kbps"], "%.0f"), efps=num(e["fps"]), kf=e["key_frames"],
                eidr=num(e["idr_s"], "%.2f"), sps=e["sps"],
                fw=f["forwarded"], dp=f["decoded"], pr=f["presented"], cl=f["with_clock"],
                p50=num(x["g2g"][0]), p95=num(x["g2g"][1]), p99=num(x["g2g"][2]),
                pm=num(x["probe_ms"][0]), px=num(x["probe_ms"][1], "%.1f"),
                sk=num(x["skew_ms"], "%+.1f"), src=x["skew_source"],
                br2=x["browser"],
            )
        )
    print("\n## Load at the time of each run\n")
    print("| run | host loadavg | device loadavg | mean | negative samples (join noise) | run error |")
    print("|---|---|---|---|---|---|")
    for x in rows:
        print(f"| `{x['label']}` | {x['env']['host']} | {x['env']['device']} | {num(x['g2g'][3])} | {x['g2g'][4]} | {x['error'] or '—'} |")

    out = os.path.join(RESULTS, "arc145-c1-table.json")
    with open(out, "w") as fh:
        json.dump(rows, fh, indent=1)
    print(f"\nwrote {out}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit("usage: summarise-arc145.py <label> [label ...]")
    main(sys.argv[1:])
