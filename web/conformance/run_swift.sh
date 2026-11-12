#!/bin/bash
# 用全新的暫存資料夾啟動一個 Swift 版 omixor(app/build/omixor.app,先 app/scripts/build-app.sh)給比對用。
#   conformance/run_swift.sh <MCP 連接埠> [資料夾]
# 停掉:conformance/stop.sh <MCP 連接埠>
# 注意:這個實例會開一個 Ops 視窗(OPS_SKIP_LOGIN=1:跳過 omixor 登入畫面,開啟時不讀鑰匙圈);它用的鑰匙圈和正式的 Ops 相同,比對時不要呼叫會連外部服務的工具。
set -e
PORT=${1:?要給 MCP 連接埠}
DIR=${2:-$(mktemp -d /tmp/ops-conf-swift-$PORT.XXXX)}
APP="$(cd "$(dirname "$0")/../../app" && pwd)/build/omixor.app/Contents/MacOS/omixor"
OPS_ISSUE_SERVER= OPS_SKIP_LOGIN=1 OPS_DATA_DIR="$DIR" OPS_MCP_PORT=$PORT "$APP" >/dev/null 2>&1 &
for i in $(seq 1 75); do curl -s -o /dev/null -X POST -d '{}' http://127.0.0.1:$PORT/mcp && break; sleep 0.2; done
echo "Swift 版 MCP:http://127.0.0.1:$PORT/mcp  資料夾:$DIR"
