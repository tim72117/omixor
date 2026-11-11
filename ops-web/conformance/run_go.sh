#!/bin/bash
# 用全新的暫存資料夾啟動 Go 版給比對用(會先重新編譯)。
#   conformance/run_go.sh <MCP 連接埠> <畫面連接埠> [資料夾]
# 停掉:conformance/stop.sh <MCP 連接埠>
set -e
PORT=${1:?要給 MCP 連接埠}
WEB=${2:?要給畫面連接埠}
cd "$(dirname "$0")/.."
DIR=${3:-$(mktemp -d /tmp/ops-conf-go-$PORT.XXXX)}
BIN=/tmp/ops-web-conf-$PORT
go build -o $BIN ./cmd/ops-web
OPS_NO_BROWSER=1 OPS_DATA_DIR="$DIR" OPS_MCP_PORT=$PORT OPS_WEB_PORT=$WEB $BIN >/tmp/ops-web-conf-$PORT.log 2>&1 &
for i in $(seq 1 75); do curl -s -o /dev/null -X POST -d '{}' http://127.0.0.1:$PORT/mcp && break; sleep 0.2; done
echo "Go 版 MCP:http://127.0.0.1:$PORT/mcp  畫面:http://127.0.0.1:$WEB  資料夾:$DIR  紀錄:/tmp/ops-web-conf-$PORT.log"
