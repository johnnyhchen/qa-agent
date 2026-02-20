#!/bin/bash
# Build UnitConverter as a .app bundle for macOS.
# Usage: ./build.sh [clean|buggy]
# Outputs: path to the .app bundle on stdout (last line).
set -euo pipefail

MODE="${1:-clean}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PKG_PATH="$SCRIPT_DIR/UnitConverter.swiftpm"

EXTRA_FLAGS=""
if [ "$MODE" = "buggy" ]; then
  EXTRA_FLAGS="-Xswiftc -DBUGGY"
fi

# Build the binary
swift build --package-path "$PKG_PATH" $EXTRA_FLAGS 2>&1
BIN_PATH=$(swift build --show-bin-path --package-path "$PKG_PATH" $EXTRA_FLAGS 2>/dev/null)

# Create .app bundle structure (single name to avoid LaunchServices collision)
APP_DIR="$SCRIPT_DIR/UnitConverter.app/Contents/MacOS"
mkdir -p "$APP_DIR"
cp "$BIN_PATH/UnitConverter" "$APP_DIR/"

# Write Info.plist
cat > "$(dirname "$APP_DIR")/Info.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>UnitConverter</string>
  <key>CFBundleIdentifier</key><string>com.benchmark.unitconverter</string>
  <key>CFBundleName</key><string>UnitConverter</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1.0</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
</dict>
</plist>
PLIST

# Output the .app path
echo "$SCRIPT_DIR/UnitConverter.app"
