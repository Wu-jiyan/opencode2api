#!/bin/sh
# Launcher for the release archive on Linux and macOS. It mirrors start.bat so
# both platforms behave the same way.
set -eu

cd "$(dirname "$0")"

BINARY=opencode2api
CONFIG=config.json
XRAY_NAME=xray

if [ ! -f "$BINARY" ]; then
    printf '[ERROR] %s not found in %s\n' "$BINARY" "$(pwd)" >&2
    exit 1
fi

if [ ! -f "$CONFIG" ]; then
    printf '[ERROR] %s not found in %s\n' "$CONFIG" "$(pwd)" >&2
    printf 'HINT: create %s from config.example.json and replace the placeholder keys.\n' "$CONFIG" >&2
    exit 1
fi

# A missing Xray is not fatal: with vless.auto_download_xray enabled the
# gateway fetches it in the background on first start.
if [ ! -f "bin/xray/$XRAY_NAME" ] && ! command -v "$XRAY_NAME" >/dev/null 2>&1; then
    printf '[WARN] Xray not found locally; it will be downloaded automatically if vless.auto_download_xray is enabled.\n\n'
fi

printf 'Starting opencode2api ...\n'
printf '  working directory: %s\n' "$(pwd)"
printf '  config file:       %s\n' "$CONFIG"
printf '  stop:              Ctrl+C\n\n'

# shellcheck disable=SC2086
exec "./$BINARY" -config "$CONFIG" "$@"