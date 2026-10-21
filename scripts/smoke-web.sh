#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
DATA=$(mktemp -d /tmp/opencode/stasher-smoke.XXXX)
STASHER_DATA_DIR="$DATA" STASHER_LISTEN_ADDR=127.0.0.1:18097 ./stasher &
PID=$!
trap 'kill $PID 2>/dev/null || true; rm -rf "$DATA"' EXIT
sleep 1
CHROME=$(command -v chromium || command -v chromium-browser || command -v google-chrome)
[ -n "$CHROME" ] || { echo "no chromium found"; exit 1; }

# desktop: login form renders
"$CHROME" --headless=new --disable-gpu --no-sandbox --dump-dom \
  --virtual-time-budget=5000 http://127.0.0.1:18097/login 2>/dev/null \
  | grep -q 'Sign in to Stasher' || { echo "FAIL: login form missing"; exit 1; }

# mobile viewport: shell nav renders (Review Focus #4)
"$CHROME" --headless=new --disable-gpu --no-sandbox --window-size=375,812 \
  --screenshot="$DATA/mobile.png" --virtual-time-budget=5000 \
  http://127.0.0.1:18097/login 2>/dev/null
[ -s "$DATA/mobile.png" ] || { echo "FAIL: no mobile screenshot"; exit 1; }

# manifest served with the correct content type (mime.AddExtensionType)
CT=$(curl -sf -o /dev/null -w '%{content_type}' http://127.0.0.1:18097/manifest.webmanifest)
[ "$CT" = "application/manifest+json" ] || { echo "FAIL: manifest content-type: $CT"; exit 1; }
echo "smoke ok"
