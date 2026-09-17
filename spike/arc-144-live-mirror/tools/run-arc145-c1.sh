#!/usr/bin/env bash
# ARC-145 C1: one encoder-setting matrix, run one cell at a time.
#
#   tools/run-arc145-c1.sh base a720 idr10 br2m br16m main fps30 base2
#
# Each name selects one cell of the matrix, so the whole table is produced by one
# command and every cell is one live run through tools/run-part-b.sh. The runs are
# serial on purpose: they share one device, one display and one encoder, and
# running two would measure the contention rather than the setting.
#
# Env: SERIAL (default 192.168.1.104:5555), DURATION (default 20s), TAPS (default 4).
#
# The knob each cell varies is stated in its line below. Everything else is the
# baseline the rig used for ARC-144 (the device's own 1080x2280, scrcpy's own bit
# rate, i-frame-interval:int=2), so a cell is comparable with `base`.
set -euo pipefail
cd "$(dirname "$0")/.."

SERIAL=${SERIAL:-192.168.1.104:5555}
DURATION=${DURATION:-20s}
TAPS=${TAPS:-4}

run_cell() {
  local name=$1
  shift
  local label="c1-$name"
  echo "== cell $name (${*:-baseline})"
  SERIAL="$SERIAL" DURATION="$DURATION" TAPS="$TAPS" "$@" \
    tools/run-part-b.sh "$label" headless >"results/logs/$label-run.log" 2>&1 || {
      echo "!! cell $name failed; see results/logs/$label-run.log"
      return 0
    }
  echo "   done: results/report-live-$label.json"
}

for cell in "$@"; do
  case "$cell" in
  # baseline: every lever left at the rig's ARC-144 setting
  base) run_cell base env ;;
  # the same baseline again, later in the matrix: run-to-run drift
  base2) run_cell base2 env ;;
  base3) run_cell base3 env ;;
  base4) run_cell base4 env ;;
  base5) run_cell base5 env ;;
  # resolution: 720-class downscale instead of the device's own 1080x2280.
  # NOTE: max_size together with ANY video_codec_options aborts the scrcpy 4.1
  # server on this fleet ("stack corruption detected"), so this cell can only run
  # with IDR=0 (the encoder's own default, one IDR per session). `a720` as written
  # reproduces the abort; `a720i0` is the runnable form.
  a720) run_cell a720 env MAXSIZE=720 ;;
  a720i0) run_cell a720i0 env MAXSIZE=720 IDR=0 ;;
  # resolution: a 480-class downscale, to see whether the curve is monotone
  a480) run_cell a480 env MAXSIZE=480 ;;
  a540i0) run_cell a540i0 env MAXSIZE=540 IDR=0 ;;
  # IDR interval: 10 s instead of 2 s
  idr10) run_cell idr10 env IDR=10 ;;
  # IDR interval: the encoder's own default (one IDR per session)
  idr0) run_cell idr0 env IDR=0 ;;
  # the IDR=0 reference again, later: it is the comparison base for every cell
  # below that can only run without a codec option
  idr0b) run_cell idr0b env IDR=0 ;;
  idr0c) run_cell idr0c env IDR=0 ;;
  # bit rate: 2 Mbit/s instead of scrcpy's 8 Mbit/s default. Can only run with
  # IDR=0: a bit rate together with a codec option aborts the server (see above).
  br2m) run_cell br2m env BITRATE=2000000 IDR=0 ;;
  br2m2) run_cell br2m2 env BITRATE=2000000 IDR=0 ;;
  # bit rate: 16 Mbit/s, well above what this content needs
  br16m) run_cell br16m env BITRATE=16000000 ;;
  # profile/level: Main profile, level 4.0 (the device chose Baseline level 5.0).
  # Two codec options abort the server, so the profile is asked for without the
  # IDR interval.
  main) run_cell main env CODECOPTS=profile:int=2,level:int=256 IDR=0 ;;
  # the same profile request again: the first attempt's join was one frame out,
  # so it is repeated rather than reported
  main2) run_cell main2 env CODECOPTS=profile:int=2,level:int=256 IDR=0 ;;
  # capture cadence: 30 fps instead of the device's own ~56
  fps30) run_cell fps30 env MAXFPS=30 IDR=0 ;;
  fps302) run_cell fps302 env MAXFPS=30 IDR=0 ;;
  *)
    echo "unknown cell: $cell" >&2
    exit 2
    ;;
  esac
done

echo "== runs complete; summarise with tools/summarise-arc145.py"
