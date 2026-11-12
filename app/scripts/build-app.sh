#!/usr/bin/env bash
# 編譯 release 版並包成 build/omixor.app(只供本機使用)。
# 顯示名稱是 omixor;bundle id(com.tim72117.ops)與資料夾(~/Library/Application Support/Ops)維持不變,
# 鑰匙圈裡的登入、本機資料與 defaults 設定才讀得回來。
set -euo pipefail
cd "$(dirname "$0")/.."

swift build -c release
BIN="$(swift build -c release --show-bin-path)/Ops"
APP=build/omixor.app

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/omixor"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>omixor</string>
  <key>CFBundleDisplayName</key><string>omixor</string>
  <key>CFBundleIdentifier</key><string>com.tim72117.ops</string>
  <key>CFBundleExecutable</key><string>omixor</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
# 簽章:有 Apple Development 憑證就用它(每次 build 的簽章身分相同,鑰匙圈裡的登入 token 才讀得回來);
# 沒有才用 ad-hoc(每次 build 都算新的 app,要重新登入連線)。可用 OPS_SIGN_IDENTITY 指定。
IDENTITY="${OPS_SIGN_IDENTITY:-$(security find-identity -v -p codesigning 2>/dev/null | sed -n 's/.*"\(Apple Development:[^"]*\)".*/\1/p' | head -1)}"
if [ -n "$IDENTITY" ]; then
  codesign --force --sign "$IDENTITY" "$APP" >/dev/null
  echo "signed with: $IDENTITY"
else
  codesign --force --sign - "$APP" >/dev/null
  echo "signed ad-hoc(重新 build 後要重新登入連線)"
fi
echo "built $APP"
