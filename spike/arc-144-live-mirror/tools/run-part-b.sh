#!/usr/bin/env bash
# ARC-144 Part B: one live, on-device measurement run.
#
#   tools/run-part-b.sh <label> [headless|headed|webkit|tauri]
#
# Env:
#   SERIAL    device serial (default 192.168.1.104:5555, a lab SM-G9750)
#   DURATION  how long to stream (default 25s)
#   TAPS      how many taps to inject (default 8)
#   MAXSIZE   cap the encoded size (default 0 = the device's own)
#   BITRATE   video bit rate in bits/s (default 0 = scrcpy's own 8 Mbit/s)
#   MAXFPS    cap the device's capture frame rate (default 0 = the device's own)
#   CODECOPTS extra MediaCodec video codec options, scrcpy syntax (default none)
#   IDR       seconds between IDRs the device encoder is asked for (default 2;
#             0 leaves the encoder's own default, which is one IDR per session)
#   ADDR      listen address (default 0.0.0.0:8792; the device must reach it)
#   WPROBE    how long to wait for the browser probe (default 60s; a mode whose
#             browser has to build or start a window needs longer)
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

# Encoder levers this run is varying. They are passed through as given; what the
# device actually did with them is what the report's encoder section measures, so
# a setting the hardware ignores is visible rather than assumed.
BITRATE=${BITRATE:-0}
MAXFPS=${MAXFPS:-0}
CODECOPTS=${CODECOPTS:-}
IDR=${IDR:-2}
ENCODER_ARGS=()
[[ "$BITRATE" != "0" ]] && ENCODER_ARGS+=(-bitrate "$BITRATE")
[[ "$MAXFPS" != "0" ]] && ENCODER_ARGS+=(-max-fps "$MAXFPS")
[[ "$IDR" != "2" ]] && ENCODER_ARGS+=(-keyframe-interval "$IDR")
[[ -n "$CODECOPTS" ]] && ENCODER_ARGS+=(-codec-options "$CODECOPTS")

mkdir -p results/bin results/logs results/diag

echo "== building =="
go build -o results/bin/live ./cmd/live
go build -o results/bin/analyze-live ./cmd/analyze-live
go build -o results/bin/clockprobe ./cmd/clockprobe

echo "== checking the device clock strip before the run =="
./results/bin/clockprobe -serial "$SERIAL" -samples 3 -out "results/clockprobe-$LABEL.json" || true

echo "== starting the rig on $ADDR for $SERIAL (label $LABEL, $DURATION) =="
WPROBE=${WPROBE:-60s}
./results/bin/live -serial "$SERIAL" -addr "$ADDR" -label "$LABEL" -out results \
  -duration "$DURATION" -taps "$TAPS" -max-size "$MAXSIZE" \
  -wait-probe "$WPROBE" \
  ${ENCODER_ARGS[@]+"${ENCODER_ARGS[@]}"} \
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
elif [[ "$MODE" == "tauri" ]]; then
  # ARC-145 C2: the console's OWN Tauri shell, not a WebKit proxy.
  #
  # `tauri dev` is started with a config override that points the shell at this
  # rig's page instead of the console UI. What is the console's is the shell
  # binary, the tauri/wry version, the window configuration, the CSP and the
  # engine WKWebView; what is ours is the URL, because the console UI does not
  # contain the clock probe. The app must already be built
  # (`pnpm exec tauri build --debug --no-bundle`) or the build time would fall
  # inside the run and the rig would time out waiting for the probe.
  TAURI_CONFIG=$(printf '{"build":{"beforeDevCommand":"","devUrl":"http://127.0.0.1:%s/"}}' "$PORT")
  CONSOLE_DIR=${CONSOLE_DIR:-$PWD/../../apps/console}
  CONSOLE_BIN=${CONSOLE_BIN:-$CONSOLE_DIR/src-tauri/target/debug/drift_command_center}
  ( cd "$CONSOLE_DIR" && pnpm exec tauri dev --config "$TAURI_CONFIG" ) \
    > "results/logs/$LABEL-tauri.log" 2>&1 &
  TAURI_PID=$!
  echo "started the console's Tauri shell (pid $TAURI_PID) at the rig page"
  wait "$SERVER_PID" || true
  # Close only what this script started: the app binary it built, by its exact
  # path, and the dev CLI by the pid it was given.
  pkill -f "$CONSOLE_BIN" 2>/dev/null || true
  kill "$TAURI_PID" 2>/dev/null || true
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
