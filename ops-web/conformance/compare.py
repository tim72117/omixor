"""MCP 一致性比對:同一串工具呼叫分別送給 Swift 版與 Go 版,比較回應。

  python3 conformance/compare.py conformance/scenarios/<名稱>.json --swift http://127.0.0.1:<埠>/mcp --go http://127.0.0.1:<埠>/mcp [-v]

兩邊都要用全新的暫存資料夾啟動(見 conformance/README.md 的 run_swift.sh / run_go.sh),
否則既有資料不同,結果一定不同。

情境檔:{"steps": [{"tool": "ops_list", "args": {...}, "ignore": ["services.*.cpu"], "note": "..."}, ...]}
  - 也可以寫 {"method": "initialize", "params": {...}} 比對非工具的 JSON-RPC
  - ignore:不比對的路徑(. 分隔,* 代表任一 key / 索引),用在會隨時間變動的數字
比對前會先正規化:ISO 時間 → <time>、UUID → <uuid>、ISS-12 這類流水號保留(兩邊從同樣的起點開始,應該一樣)。
工具結果的 text 是 JSON 時會先解析再比對;錯誤訊息(isError)逐字比對。
"""
import json, re, sys, urllib.request, argparse

ISO = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$")
UUID = re.compile(r"^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$")

def rpc(url, method, params, n):
    body = json.dumps({"jsonrpc": "2.0", "id": n, "method": method, "params": params}).encode()
    req = urllib.request.Request(url, body, {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)

def norm(v):
    if isinstance(v, dict): return {k: norm(x) for k, x in v.items()}
    if isinstance(v, list): return [norm(x) for x in v]
    if isinstance(v, str):
        if ISO.match(v): return "<time>"
        if UUID.match(v): return "<uuid>"
        # 文字裡夾的時間也換掉
        return re.sub(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z", "<time>", v)
    if isinstance(v, float) and v == int(v): return int(v)
    return v

def unpack(resp):
    """tools/call 的回應 → (isError, 內容)"""
    if "error" in resp: return ("rpc-error", resp["error"])
    r = resp.get("result", {})
    if "content" in r:
        text = r["content"][0]["text"]
        try: body = json.loads(text)
        except Exception: body = text
        return (r.get("isError"), body)
    return (None, r)

def drop(v, path):
    if not path: return None
    head, rest = path[0], path[1:]
    if isinstance(v, dict):
        keys = list(v.keys()) if head == "*" else [head]
        for k in keys:
            if k in v:
                if rest: drop(v[k], rest)
                else: v[k] = "<ignored>"
    elif isinstance(v, list):
        idx = range(len(v)) if head == "*" else ([int(head)] if head.isdigit() and int(head) < len(v) else [])
        for i in idx:
            if rest: drop(v[i], rest)
            else: v[i] = "<ignored>"

def diff(a, b, path="", out=None, limit=30):
    out = [] if out is None else out
    if len(out) >= limit: return out
    if type(a) != type(b):
        out.append(f"{path or '/'}: Swift={json.dumps(a, ensure_ascii=False)[:160]}  Go={json.dumps(b, ensure_ascii=False)[:160]}")
    elif isinstance(a, dict):
        for k in sorted(set(a) | set(b)):
            if k not in a: out.append(f"{path}.{k}: 只有 Go 有 = {json.dumps(b[k], ensure_ascii=False)[:120]}")
            elif k not in b: out.append(f"{path}.{k}: 只有 Swift 有 = {json.dumps(a[k], ensure_ascii=False)[:120]}")
            else: diff(a[k], b[k], f"{path}.{k}", out, limit)
    elif isinstance(a, list):
        if len(a) != len(b): out.append(f"{path}: 長度 Swift={len(a)} Go={len(b)}")
        for i, (x, y) in enumerate(zip(a, b)): diff(x, y, f"{path}[{i}]", out, limit)
    elif a != b:
        out.append(f"{path or '/'}: Swift={json.dumps(a, ensure_ascii=False)[:160]}  Go={json.dumps(b, ensure_ascii=False)[:160]}")
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("scenario")
    ap.add_argument("--swift", required=True)
    ap.add_argument("--go", required=True)
    ap.add_argument("-v", action="store_true")
    o = ap.parse_args()
    steps = json.load(open(o.scenario))["steps"]
    bad = 0
    for i, s in enumerate(steps, 1):
        if "tool" in s:
            method, params = "tools/call", {"name": s["tool"], "arguments": s.get("args", {})}
            label = s["tool"] + " " + json.dumps(s.get("args", {}), ensure_ascii=False)[:80]
        else:
            method, params, label = s["method"], s.get("params", {}), s["method"]
        a = unpack(rpc(o.swift, method, params, i))
        b = unpack(rpc(o.go, method, params, i))
        a, b = norm(list(a)), norm(list(b))
        for p in s.get("ignore", []):
            drop(a[1], p.split(".")); drop(b[1], p.split("."))
        d = diff(a, b)
        if d:
            bad += 1
            print(f"✗ {i}. {label}")
            for line in d: print("    " + line)
        else:
            print(f"✓ {i}. {label}")
            if o.v: print("    " + json.dumps(b, ensure_ascii=False)[:300])
    print(f"\n{len(steps) - bad}/{len(steps)} 一致")
    sys.exit(1 if bad else 0)

main()
