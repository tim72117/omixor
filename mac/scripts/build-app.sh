#!/usr/bin/env bash
# 編譯 release 版並包成 build/omixor.app(只供本機使用)。
# 本機模式的資料由本機守護服務 omixor-local(Go,server/cmd/omixor-local)提供:一起編進 Contents/MacOS/omixor-local(要有 go)。
# 顯示名稱是 omixor;bundle id(com.tim72117.ops)與資料夾(~/Library/Application Support/Ops)是改名前留下的,刻意維持不變,
# 鑰匙圈裡的登入、本機資料與 defaults 設定才讀得回來(改了要搬資料、換 defaults domain、重新取得鑰匙圈存取權,風險高)。
#
# 所有功能都編進 App 與 omixor-local(不再有正式版/開發版);開放哪些功能由 core 資料庫裡的兩層開關決定:
# 系統層(伺服器管理員在「系統」頁)與組織層(組織管理員在 設定 › 功能),見 System/SystemSettings.swift。
set -euo pipefail
cd "$(dirname "$0")/.."

swift build -c release
BIN="$(swift build -c release --show-bin-path)/Omixor"
# 輸出位置:預設 build/omixor.app;OMIXOR_APP=<路徑>/omixor.app 另外指定(例如試打包時不動正在用的那一份)
APP="${OMIXOR_APP:-build/omixor.app}"
case "$APP" in /*) ;; *) APP="$PWD/$APP" ;; esac

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/omixor"
# 本機守護服務(App 啟動本機模式時在背景執行,只聽 127.0.0.1)
(cd ../server && CGO_ENABLED=0 go build -trimpath -o "$APP/Contents/MacOS/omixor-local" ./cmd/omixor-local)
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
# 沒有才用 ad-hoc(每次 build 都算新的 app,要重新登入連線)。可用 OMIXOR_SIGN_IDENTITY 指定(舊名稱 OPS_SIGN_IDENTITY 暫時仍可用)。
if [ -z "${OMIXOR_SIGN_IDENTITY:-}" ] && [ -n "${OPS_SIGN_IDENTITY:-}" ]; then
  echo "環境變數 OPS_SIGN_IDENTITY 已改名為 OMIXOR_SIGN_IDENTITY,請更新設定(舊名稱暫時仍可用)" >&2
  OMIXOR_SIGN_IDENTITY="$OPS_SIGN_IDENTITY"
fi
IDENTITY="${OMIXOR_SIGN_IDENTITY:-$(security find-identity -v -p codesigning 2>/dev/null | sed -n 's/.*"\(Apple Development:[^"]*\)".*/\1/p' | head -1)}"
if [ -n "$IDENTITY" ]; then
  codesign --force --sign "$IDENTITY" "$APP/Contents/MacOS/omixor-local" >/dev/null
  codesign --force --sign "$IDENTITY" "$APP" >/dev/null
  echo "signed with: $IDENTITY"
else
  codesign --force --sign - "$APP/Contents/MacOS/omixor-local" >/dev/null
  codesign --force --sign - "$APP" >/dev/null
  echo "signed ad-hoc(重新 build 後要重新登入連線)"
fi
echo "built $APP"
