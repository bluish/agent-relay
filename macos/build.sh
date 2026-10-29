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
codesign --force --sign - "$app"
echo "installed $app"
if [ "${1:-}" = "--open" ]; then
  pkill -x AgentRelayBar 2>/dev/null || true
  open "$app"
fi
