#!/usr/bin/env bash
# 由 site/favicon.svg 產生 mac/Resources/AppIcon.icns(build-app.sh 會把它放進 App 的 Resources)。
# 只用 macOS 內建工具:qlmanage(Quick Look 把 SVG 轉成 PNG)、sips(縮圖)、iconutil(打包)。
# 用法:sh mac/scripts/gen-appicon.sh;改了 site/favicon.svg 之後重跑,並把 AppIcon.icns 一併提交。
# 依 macOS 圖示規格:1024 畫布、深色圓角方塊約 824px 置中,四周約 10% 透明邊;
# 作法是把 viewBox 從 0 0 32 32 放大成 -3.2 -3.2 38.4 38.4(32/38.4 = 83%)再轉出,邊緣自然透明。
set -euo pipefail
cd "$(dirname "$0")/.."
SRC="../site/favicon.svg"
OUT="Resources/AppIcon.icns"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

sed 's/viewBox="0 0 32 32"/viewBox="-3.2 -3.2 38.4 38.4" width="1024" height="1024"/' "$SRC" > "$TMP/icon.svg"
grep -q 'viewBox="-3.2 -3.2 38.4 38.4"' "$TMP/icon.svg" || { echo "favicon.svg 的 viewBox 不是 0 0 32 32,請更新本腳本" >&2; exit 1; }
qlmanage -t -s 1024 -o "$TMP" "$TMP/icon.svg" >/dev/null 2>&1
BASE="$TMP/icon.svg.png"
[ -f "$BASE" ] || { echo "qlmanage 沒有產出 PNG" >&2; exit 1; }
[ "$(sips -g hasAlpha "$BASE" | awk '/hasAlpha/{print $2}')" = yes ] || { echo "PNG 沒有透明通道" >&2; exit 1; }

SET="$TMP/AppIcon.iconset"
mkdir "$SET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$BASE" --out "$SET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$BASE" --out "$SET/icon_${s}x${s}@2x.png" >/dev/null
done
mkdir -p "$(dirname "$OUT")"
iconutil -c icns "$SET" -o "$OUT"
echo "wrote mac/$OUT"
