#!/bin/sh
# Builds "Agent Relay.app" (menu bar) and installs it in ~/Applications.
set -eu
cd "$(dirname "$0")"
swift build -c release --arch arm64
app="$HOME/Applications/Agent Relay.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp Resources/Info.plist "$app/Contents/Info.plist"
cp "$(swift build -c release --arch arm64 --show-bin-path)/AgentRelayBar" "$app/Contents/MacOS/AgentRelayBar"
if [ -f Resources/AppIcon.png ]; then
  set_dir=$(mktemp -d)/AppIcon.iconset
  mkdir -p "$set_dir" "$app/Contents/Resources"
  for size in 16 32 128 256 512; do
    sips -z $size $size Resources/AppIcon.png --out "$set_dir/icon_${size}x${size}.png" >/dev/null
    sips -z $((size * 2)) $((size * 2)) Resources/AppIcon.png --out "$set_dir/icon_${size}x${size}@2x.png" >/dev/null
  done
  iconutil -c icns "$set_dir" -o "$app/Contents/Resources/AppIcon.icns"
fi
codesign --force --sign - "$app"
echo "installed $app"
if [ "${1:-}" = "--open" ]; then
  pkill -x AgentRelayBar 2>/dev/null || true
  open "$app"
fi
