#!/bin/sh
# mkapp.sh — assemble monctl.app from a built monctl binary.
# Usage: mkapp.sh <path-to-monctl-binary> [output-dir] [version]
# Produces: <output-dir>/monctl.app  (monctl + osd helper inside Contents/MacOS)
set -eu

BIN="$1"
OUT="${2:-dist}"
VER="${3:-1.0}"

[ -x "$BIN" ] || { echo "mkapp: $BIN is not executable" >&2; exit 1; }
# tools/app/mkapp.sh -> repo root is two levels up from this script.
SRC_DIR="$(cd "$(dirname "$0")/../.." && pwd)"

APP="$OUT/monctl.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
cp "$SRC_DIR/tools/app/Info.plist" "$APP/Contents/Info.plist"
sed -i '' "s/<string>1\.0<\/string>/<string>$VER<\/string>/" "$APP/Contents/Info.plist" 2>/dev/null || \
  sed -i "s/<string>1\.0<\/string>/<string>$VER<\/string>/" "$APP/Contents/Info.plist"

cp "$BIN" "$APP/Contents/MacOS/monctl"

# OSD helper (native brightness overlay) — needs the macOS SDK; skip if absent.
if [ -x /usr/bin/swiftc ] && [ -f "$SRC_DIR/tools/osd/main.swift" ]; then
  /usr/bin/swiftc -O "$SRC_DIR/tools/osd/main.swift" -o "$APP/Contents/MacOS/osd"
fi

codesign --force --deep -s - "$APP"
echo "$APP"
