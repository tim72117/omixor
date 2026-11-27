#!/bin/bash
# 用全新的暫存資料夾啟動 Go 版給比對用(會先重新編譯)。正式的 console 沒有免登入的 MCP,這裡用開發工具 tools/conformance-mcp
# (只聽 127.0.0.1、只有範本資料)。
#   conformance/run_go.sh <MCP 連接埠> [資料夾]
# 停掉:conformance/stop.sh <MCP 連接埠>
set -e
PORT=${1:?要給 MCP 連接埠}
cd "$(dirname "$0")/.."
DIR=${2:-$(mktemp -d /tmp/omixor-conf-go-$PORT.XXXX)}
BIN=/tmp/omixor-conformance-$PORT
go build -o $BIN ./tools/conformance-mcp
OMIXOR_TEMPLATES=1 OMIXOR_CONSOLE_DATA_DIR="$DIR" $BIN -port $PORT >/tmp/omixor-conformance-$PORT.log 2>&1 &
for i in $(seq 1 75); do curl -s -o /dev/null -X POST -d '{}' http://127.0.0.1:$PORT/mcp && break; sleep 0.2; done
echo "Go 版 MCP:http://127.0.0.1:$PORT/mcp  資料夾:$DIR  紀錄:/tmp/omixor-conformance-$PORT.log"
