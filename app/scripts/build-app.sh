#!/usr/bin/env bash
# 編譯 release 版並包成 build/omixor.app(只供本機使用)。
# 本機模式的資料由本機守護服務 omixor-local(Go,server/cmd/omixor-local)提供:一起編進 Contents/MacOS/omixor-local(要有 go)。
# 顯示名稱是 omixor;bundle id(com.tim72117.ops)與資料夾(~/Library/Application Support/Ops)維持不變,
# 鑰匙圈裡的登入、本機資料與 defaults 設定才讀得回來。
#
# 版本(這次編譯有哪些功能,見 core/model/edition.go 與 System/SystemSettings.swift 的 AppEdition):
#   bash scripts/build-app.sh                       正式版(預設):只有已實作的功能
#   OMIXOR_EDITION=dev bash scripts/build-app.sh    開發版:再加上實驗功能(範例資料)與家庭助理等框架階段的功能
# App(swift 編譯條件 OMIXOR_DEV)與包進去的 omixor-local(go build tag dev)一律用相同的版本。
set -euo pipefail
cd "$(dirname "$0")/.."

EDITION="${OMIXOR_EDITION:-stable}"
case "$EDITION" in
  stable) SWIFT_FLAGS=(); GO_TAGS="" ;;
  dev) SWIFT_FLAGS=(-Xswiftc -DOMIXOR_DEV); GO_TAGS="dev" ;;
  *) echo "OMIXOR_EDITION 只能是 stable 或 dev:$EDITION" >&2; exit 1 ;;
esac
echo "版本:$EDITION"

swift build -c release ${SWIFT_FLAGS[@]+"${SWIFT_FLAGS[@]}"}
BIN="$(swift build -c release ${SWIFT_FLAGS[@]+"${SWIFT_FLAGS[@]}"} --show-bin-path)/Ops"
# 輸出位置:預設 build/omixor.app;OMIXOR_APP=<路徑>/omixor.app 另外指定(例如試打包時不動正在用的那一份)
APP="${OMIXOR_APP:-build/omixor.app}"
case "$APP" in /*) ;; *) APP="$PWD/$APP" ;; esac

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/omixor"
# 本機守護服務(App 啟動本機模式時在背景執行,只聽 127.0.0.1)
(cd ../server && CGO_ENABLED=0 go build -trimpath -tags "$GO_TAGS" -o "$APP/Contents/MacOS/omixor-local" ./cmd/omixor-local)
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
  <key>OmixorEdition</key><string>${EDITION}</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
# 簽章:有 Apple Development 憑證就用它(每次 build 的簽章身分相同,鑰匙圈裡的登入 token 才讀得回來);
# 沒有才用 ad-hoc(每次 build 都算新的 app,要重新登入連線)。可用 OPS_SIGN_IDENTITY 指定。
IDENTITY="${OPS_SIGN_IDENTITY:-$(security find-identity -v -p codesigning 2>/dev/null | sed -n 's/.*"\(Apple Development:[^"]*\)".*/\1/p' | head -1)}"
if [ -n "$IDENTITY" ]; then
  codesign --force --sign "$IDENTITY" "$APP/Contents/MacOS/omixor-local" >/dev/null
  codesign --force --sign "$IDENTITY" "$APP" >/dev/null
  echo "signed with: $IDENTITY"
else
  codesign --force --sign - "$APP/Contents/MacOS/omixor-local" >/dev/null
  codesign --force --sign - "$APP" >/dev/null
  echo "signed ad-hoc(重新 build 後要重新登入連線)"
fi
echo "built $APP($EDITION)"
