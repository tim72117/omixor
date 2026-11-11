"""從執行中的 Ops(Swift 版)匯出 MCP 規格,當 Go 版的標準答案。
用法:python3 spec/dump_spec.py [http://127.0.0.1:3765/mcp]
範例資料(seed.json)另外用 Swift 版匯出:
  cd ../ops && OPS_EXPORT_SEED=$PWD/../ops-web/spec/seed.json swift test --filter SpecExportTests"""
import json, os, sys, urllib.request
URL = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:3765/mcp"
HERE = os.path.dirname(os.path.abspath(__file__))
n = 0
def rpc(method, params=None):
    global n; n += 1
    body = json.dumps({"jsonrpc": "2.0", "id": n, "method": method, "params": params or {}}).encode()
    req = urllib.request.Request(URL, body, {"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req))
init = rpc("initialize", {"protocolVersion": "2025-06-18", "clientInfo": {"name": "spec-dump", "version": "1"}})["result"]
tools = rpc("tools/list")["result"]["tools"]
json.dump(init, open(os.path.join(HERE, "initialize.json"), "w"), ensure_ascii=False, indent=2, sort_keys=True)
json.dump(tools, open(os.path.join(HERE, "tools.json"), "w"), ensure_ascii=False, indent=2, sort_keys=True)
print("initialize + %d tools" % len(tools))
