#!/usr/bin/env bash
# ARC-144 Part B: one live, on-device measurement run.
#
#   tools/run-part-b.sh <label> [headless|headed]
#
# Env:
#   SERIAL    device serial (default 192.168.1.104:5555, a lab SM-G9750)
#   DURATION  how long to stream (default 25s)
#   TAPS      how many taps to inject (default 8)
#   MAXSIZE   cap the encoded size (default 0 = the device's own)
#   ADDR      listen address (default 0.0.0.0:8792; the device must reach it)
#
# The rig opens the clock page in the device's browser over the LAN, finds the
# clock strip in a screenshot, then streams scrcpy's H.264 into pion/webrtc while
# injecting taps over scrcpy's control socket. Nothing is wired into the product.
set -euo pipefail
cd "$(dirname "$0")/.."

LABEL=${1:?label}
MODE=${2:-headless}
SERIAL=${SERIAL:-192.168.1.104:5555}
DURATION=${DURATION:-25s}
TAPS=${TAPS:-8}
MAXSIZE=${MAXSIZE:-0}
ADDR=${ADDR:-0.0.0.0:8792}
PORT=${ADDR##*:}
TIMEOUT=${TIMEOUT:-$(( ${DURATION%s} + 150 ))}

mkdir -p results/bin results/logs results/diag

echo "== building =="
go build -o results/bin/live ./cmd/live
go build -o results/bin/analyze-live ./cmd/analyze-live
go build -o results/bin/clockprobe ./cmd/clockprobe

echo "== checking the device clock strip before the run =="
./results/bin/clockprobe -serial "$SERIAL" -samples 3 -out "results/clockprobe-$LABEL.json" || true

echo "== starting the rig on $ADDR for $SERIAL (label $LABEL, $DURATION) =="
./results/bin/live -serial "$SERIAL" -addr "$ADDR" -label "$LABEL" -out results \
  -duration "$DURATION" -taps "$TAPS" -max-size "$MAXSIZE" \
  > "results/logs/$LABEL-server.log" 2>&1 &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT

for _ in $(seq 1 120); do
  if curl -sf "http://127.0.0.1:$PORT/config" >/dev/null; then break; fi
  sleep 0.25
done

sleep 2
echo "== driving one browser ($MODE) =="
if [[ "$MODE" == "webkit" ]]; then
  # Part B4: the console's own engine. The probe posts its own samples to this
  # rig, so a real WebKit window needs no CDP driver -- it needs to be opened and
  # then waited for. Safari is used as the WebKit host: it is the same engine
  # family the Tauri shell embeds on macOS, and it is reachable from this rig
  # without building the console.
  open -a Safari "http://127.0.0.1:$PORT/"
  echo "opened Safari; waiting for the rig to collect the probe's samples"
  wait "$SERVER_PID" || true
else
  DRIVER_FLAGS=()
  [[ "$MODE" == "headed" ]] && DRIVER_FLAGS+=(--headed)
  node tools/drive.mjs "http://127.0.0.1:$PORT/" "results/page-live-$LABEL.json" "$TIMEOUT" ${DRIVER_FLAGS[@]+"${DRIVER_FLAGS[@]}"}
fi

echo "== waiting for the rig to finish =="
wait "$SERVER_PID" || true

echo "== analysis =="
./results/bin/analyze-live \
  -run "results/live-$LABEL.json" \
  -probe "results/client-$LABEL-rtp.json" \
  -label "$LABEL" \
  -out "results/report-live-$LABEL.json" \
  -summary "results/summary-part-b-$LABEL.md"
