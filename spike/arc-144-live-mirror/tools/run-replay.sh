#!/usr/bin/env bash
# ARC-144 Part B: replay one captured device stream through the same pion hop,
# with no device attached.
#
# This is the rig's own control for the RTP path: the bytes are exactly what a
# live run captured, at a fixed frame rate, so anything that differs from the
# live run's browser-side result is the live delivery's doing, not the stream's.
#
#   tools/run-replay.sh <label> <captured.h264> [fps] [stripY]
set -euo pipefail
cd "$(dirname "$0")/.."

LABEL=${1:?label}
H264=${2:?captured Annex-B file}
FPS=${3:-60}
STRIPY=${4:-569}
ADDR=${ADDR:-127.0.0.1:8793}
PORT=${ADDR##*:}

mkdir -p results/bin results/logs
go build -o results/bin/rtpreplay ./cmd/rtpreplay

./results/bin/rtpreplay -h264 "$H264" -addr "$ADDR" -label "$LABEL" -out results \
  -fps "$FPS" -bare-peer -strip-x 0 -strip-y "$STRIPY" -cell 32 -video-w 1080 -video-h 2280 \
  > "results/logs/$LABEL.log" 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null || true' EXIT

for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:$PORT/config" >/dev/null; then break; fi
  sleep 0.25
done

node tools/drive.mjs "http://127.0.0.1:$PORT/" "results/page-$LABEL.json" 120
wait "$PID" || true
echo "replay done: results/replay-client-$LABEL.json, log results/logs/$LABEL.log"
