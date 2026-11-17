"""從執行中的 omixor macOS App(Swift 版)匯出 MCP 規格,當 Go 版的標準答案。
用法:python3 spec/dump_spec.py [http://127.0.0.1:3765/mcp]
tools.json 只留 web 自己的工具:核心工具(../core/model/tools.go 的 CoreTools 登記表)的定義由 web 直接取自核心
(omixor-core/mcp.CoreToolDefs),這裡不收;Swift App 的 tools/list 會混進 core server 的工具與外部 MCP 閘道的工具(名稱有 __),一併濾掉。
範例資料(templates/seed/)另外用 Swift 版匯出再拆開,見 ../templates/README.md"""
import json, os, re, sys, urllib.request
URL = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:3765/mcp"
HERE = os.path.dirname(os.path.abspath(__file__))
n = 0
def rpc(method, params=None):
    global n; n += 1
    body = json.dumps({"jsonrpc": "2.0", "id": n, "method": method, "params": params or {}}).encode()
    req = urllib.request.Request(URL, body, {"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(req))
init = rpc("initialize", {"protocolVersion": "2025-06-18", "clientInfo": {"name": "spec-dump", "version": "1"}})["result"]
CORE = set(re.findall(r'Name: "(\w+)"', open(os.path.join(HERE, "..", "..", "core", "model", "tools.go")).read()))
tools = [t for t in rpc("tools/list")["result"]["tools"] if t["name"] not in CORE and "__" not in t["name"]]
json.dump(init, open(os.path.join(HERE, "initialize.json"), "w"), ensure_ascii=False, indent=2, sort_keys=True)
json.dump(tools, open(os.path.join(HERE, "tools.json"), "w"), ensure_ascii=False, indent=2, sort_keys=True)
print("initialize + %d tools(web 自己的工具)" % len(tools))
