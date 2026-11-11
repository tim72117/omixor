#!/usr/bin/env bash
# 編譯 release 版並包成 build/Desk.app(未簽章,只供本機使用)。
set -euo pipefail
cd "$(dirname "$0")/.."

swift build -c release
BIN="$(swift build -c release --show-bin-path)/Desk"
APP=build/Desk.app

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/Desk"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Desk</string>
  <key>CFBundleDisplayName</key><string>Desk</string>
  <key>CFBundleIdentifier</key><string>com.tim72117.desk</string>
  <key>CFBundleExecutable</key><string>Desk</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
# 本機 ad-hoc 簽章,避免 Gatekeeper 直接擋下
codesign --force --sign - "$APP" >/dev/null
echo "built $APP"
