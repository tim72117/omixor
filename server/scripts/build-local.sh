#!/usr/bin/env bash
# 交叉編譯本機守護服務 omixor-local(server/cmd/omixor-local)到 server/dist/:
#
#   omixor-local-windows-amd64.exe、omixor-local-windows-arm64.exe、
#   omixor-local-darwin-arm64、omixor-local-darwin-amd64、omixor-local-linux-amd64
#
# 用法:server/scripts/build-local.sh            (版本預設 dev;VERSION=1.2.0 server/scripts/build-local.sh 指定)
#       TARGETS="windows/amd64 linux/amd64" server/scripts/build-local.sh   只編其中幾個
#
# 所有功能都編進去;開放哪些功能由伺服器管理員在「系統」頁決定(存在資料夾的 JSON,見 core/model/features.go)。
# 以前的 OMIXOR_EDITION 已經拿掉(給了也沒有作用)。
#
# 純 Go(CGO_ENABLED=0),不需要各平台的工具鏈。只編譯,不簽章、不上傳;
# macOS App 用的那一份由 app/scripts/build-app.sh 另外編進 App 並簽章。
set -euo pipefail

cd "$(dirname "$0")/.."            # server/
OUT="dist"
mkdir -p "$OUT"

VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git rev-parse --short=12 HEAD 2>/dev/null || echo dev)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-s -w -X omixor-core/version.Version=${VERSION} -X omixor-core/version.Commit=${COMMIT} -X omixor-core/version.BuildTime=${BUILD_TIME}"
TARGETS="${TARGETS:-windows/amd64 windows/arm64 darwin/arm64 darwin/amd64 linux/amd64}"

for t in $TARGETS; do
  os="${t%/*}"; arch="${t#*/}"
  name="omixor-local-${os}-${arch}"
  [ "$os" = windows ] && name="${name}.exe"
  echo "編譯 $OUT/$name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$name" ./cmd/omixor-local
done

echo "完成:$(pwd)/$OUT"
ls -l "$OUT"
