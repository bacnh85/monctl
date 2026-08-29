#!/bin/sh
# mkdmg.sh — package monctl.app into a distributable .dmg with a drag-to-
# Applications folder (uses stock hdiutil; no third-party deps).
# Usage: mkdmg.sh <dist-dir-with-monctl.app> [output.dmg] [volume-name]
set -eu

DIST="$(cd "$1" && pwd)"
DMG="${2:-$DIST/monctl.dmg}"
VOLNAME="${3:-monctl}"

[ -d "$DIST/monctl.app" ] || { echo "mkdmg: $DIST/monctl.app missing" >&2; exit 1; }
rm -f "$DMG"

STAGING="$(mktemp -d)"
trap 'rm -rf "$STAGING"' EXIT
cp -R "$DIST/monctl.app" "$STAGING/"
ln -s /Applications "$STAGING/Applications"

hdiutil create -volname "$VOLNAME" -srcfolder "$STAGING" -ov -format UDZO "$DMG" >/dev/null
echo "$DMG"
