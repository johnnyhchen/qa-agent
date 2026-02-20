#!/bin/bash
# Build ContactsApp for the iOS Simulator.
# Usage: ./build.sh [clean|buggy]
# Outputs: path to the .app bundle on stdout (last line).
set -euo pipefail

MODE="${1:-clean}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PKG_PATH="$SCRIPT_DIR/ContactsApp.swiftpm"
DERIVED_DATA="/tmp/contactsapp-build-${MODE}"

EXTRA=""
if [ "$MODE" = "buggy" ]; then
  EXTRA="SWIFT_ACTIVE_COMPILATION_CONDITIONS=BUGGY"
fi

# xcodebuild must be run from the Swift package directory
cd "$PKG_PATH"

xcodebuild build \
  -scheme ContactsApp \
  -destination 'platform=iOS Simulator,name=iPhone 17' \
  -derivedDataPath "$DERIVED_DATA" \
  ${EXTRA:+$EXTRA} \
  2>&1

# The Swift package produces a raw executable, not a .app bundle.
# We must wrap it in a .app for simctl install.
BIN_PATH="$DERIVED_DATA/Build/Products/Debug-iphonesimulator/ContactsApp"
if [ ! -f "$BIN_PATH" ]; then
  echo "ERROR: ContactsApp binary not found at $BIN_PATH" >&2
  exit 1
fi

APP_DIR="$SCRIPT_DIR/ContactsApp-${MODE}.app"
mkdir -p "$APP_DIR"
cp "$BIN_PATH" "$APP_DIR/"

cat > "$APP_DIR/Info.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>ContactsApp</string>
  <key>CFBundleIdentifier</key><string>com.benchmark.contactsapp</string>
  <key>CFBundleName</key><string>ContactsApp</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1.0</string>
  <key>MinimumOSVersion</key><string>17.0</string>
  <key>CFBundleSupportedPlatforms</key>
  <array><string>iPhoneSimulator</string></array>
  <key>DTPlatformName</key><string>iphonesimulator</string>
</dict>
</plist>
PLIST

echo "$APP_DIR"
