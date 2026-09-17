#!/usr/bin/env bash
# ARC-144 Part A: one reproducible measurement run.
#
# Generates the source media (if absent), verifies the measurement instrument,
# runs the server and one browser, then joins the two logs.
#
#   tools/run-part-a.sh <label> [readbackEvery] [headless|headed] [frag-us]
#
# Examples
#   tools/run-part-a.sh on  1 headless 100000   # barcode probe on, 100 ms fragments
#   tools/run-part-a.sh off 0 headless 100000   # instrument-off control
#   tools/run-part-a.sh hw  1 headed   100000   # headed browser (hardware decode)
#   tools/run-part-a.sh f33 1 headless  33000   # 33 ms fragments
set -euo pipefail
cd "$(dirname "$0")/.."

LABEL=${1:?label}
READBACK=${2:-1}
MODE=${3:-headless}
FRAGUS=${4:-100000}
FRAMES=${FRAMES:-600}
FPS=${FPS:-30}
ADDR=${ADDR:-127.0.0.1:8791}

MEDIA=results/media
mkdir -p "$MEDIA" results/logs results/bin

H264="$MEDIA/spike-$FRAMES.h264"
MP4="$MEDIA/spike-$FRAMES-frag$FRAGUS.mp4"

echo "== building =="
go build -o results/bin/genframes ./cmd/genframes
go build -o results/bin/verifyframes ./cmd/verifyframes
go build -o results/bin/verifyfmp4 ./cmd/verifyfmp4
go build -o results/bin/spike ./cmd/spike
go build -o results/bin/analyze ./cmd/analyze

if [[ ! -f "$H264" ]]; then
  echo "== generating source ($FRAMES frames @ $FPS fps) =="
  ./results/bin/genframes -out "$H264" -frames "$FRAMES" -fps "$FPS"
fi

echo "== verifying the instrument =="
./results/bin/verifyframes -in "$H264" -frames "$FRAMES" -fps "$FPS"

if [[ ! -f "$MP4" ]]; then
  echo "== muxing the same access units into fragmented MP4 (frag $FRAGUS us) =="
  ffmpeg -y -hide_banner -nostdin -loglevel error \
    -f h264 -r "$FPS" -i "$H264" -an -c:v copy -f mp4 \
    -movflags +empty_moov+default_base_moof -frag_duration "$FRAGUS" "$MP4"
fi
./results/bin/verifyfmp4 -in "$MP4" -h264 "$H264" -frames "$FRAMES" -fps "$FPS"

echo "== serving on $ADDR (label $LABEL, readback-every $READBACK) =="
./results/bin/spike \
  -h264 "$H264" -mp4 "$MP4" -fps "$FPS" -addr "$ADDR" -out results -label "$LABEL" \
  -readback-every "$READBACK" -settle 2s -tail 3s \
  > "results/logs/$LABEL-server.log" 2>&1 &
SERVER_PID=$!
trap 'kill $SERVER_PID 2>/dev/null || true' EXIT

for _ in $(seq 1 60); do
  if curl -sf "http://$ADDR/config" >/dev/null; then break; fi
  sleep 0.25
done

echo "== driving one browser ($MODE) =="
DRIVER_FLAGS=()
[[ "$MODE" == "headed" ]] && DRIVER_FLAGS+=(--headed)
TIMEOUT=$(( FRAMES / FPS + 90 ))
node tools/drive.mjs "http://$ADDR/" "results/page-$LABEL.json" "$TIMEOUT" ${DRIVER_FLAGS[@]+"${DRIVER_FLAGS[@]}"}

echo "== waiting for the server to finish =="
wait $SERVER_PID || true

echo "== analysis =="
./results/bin/analyze \
  -send "results/send-$LABEL.json" \
  -rtp "results/client-$LABEL-rtp.json" \
  -mse "results/client-$LABEL-mse.json" \
  -out "results/report-$LABEL.json"
