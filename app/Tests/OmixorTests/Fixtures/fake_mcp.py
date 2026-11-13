#!/usr/bin/env python3
# 測試用的假 MCP 伺服器(只用 Python 標準函式庫,不連任何外部服務)。
#   python3 fake_mcp.py stdio   JSON-RPC 一行一則走標準輸入輸出
#   python3 fake_mcp.py http    在 127.0.0.1 的隨機埠開 Streamable HTTP(第一行印出 "PORT <埠>"),
#                               要帶標頭 Authorization: Bearer <環境變數 EXPECT_AUTH>,否則 401;
#                               initialize 回 Mcp-Session-Id,之後的請求都要帶;tools/call 用 SSE 回應
# 工具:echo(回傳 text)、whoami(回報有沒有拿到環境變數 FAKE_TOKEN、命令列裡有沒有它)、write_note(高風險,寫入類)
import json
import os
import sys

TOOLS = [
    {"name": "echo", "title": "回聲", "description": "回傳 text", "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}}},
    {"name": "whoami", "description": "回報收到的環境變數", "inputSchema": {"type": "object", "properties": {}}},
    {"name": "write_note", "description": "寫一則筆記(寫入類)", "inputSchema": {"type": "object", "properties": {"note": {"type": "string"}}}},
]


def call_tool(name, args):
    if name == "echo":
        return {"content": [{"type": "text", "text": "echo:" + str(args.get("text", ""))}], "isError": False}
    if name == "whoami":
        tok = os.environ.get("FAKE_TOKEN", "")
        in_argv = bool(tok) and any(tok in a for a in sys.argv)
        return {"content": [{"type": "text", "text": json.dumps({"hasToken": bool(tok), "tokenLen": len(tok), "inArgv": in_argv,
                                                                  "hasAppToken": "OPS_ISSUE_SERVER_TOKEN" in os.environ})}], "isError": False}
    if name == "write_note":
        return {"content": [{"type": "text", "text": "已寫入:" + str(args.get("note", ""))}], "isError": False}
    return None


def handle(msg):
    """回傳回應(None = 通知不用回)"""
    if "id" not in msg:
        return None
    mid, method, params = msg["id"], msg.get("method"), msg.get("params") or {}
    if method == "initialize":
        return {"jsonrpc": "2.0", "id": mid, "result": {"protocolVersion": params.get("protocolVersion", "2025-06-18"),
                                                        "capabilities": {"tools": {}}, "serverInfo": {"name": "fake-mcp", "version": "1"}}}
    if method == "tools/list":
        # 分頁:第一頁兩個工具,第二頁一個
        if params.get("cursor") == "p2":
            return {"jsonrpc": "2.0", "id": mid, "result": {"tools": TOOLS[2:]}}
        return {"jsonrpc": "2.0", "id": mid, "result": {"tools": TOOLS[:2], "nextCursor": "p2"}}
    if method == "tools/call":
        r = call_tool(params.get("name"), params.get("arguments") or {})
        if r is None:
            return {"jsonrpc": "2.0", "id": mid, "error": {"code": -32602, "message": "沒有這個工具"}}
        return {"jsonrpc": "2.0", "id": mid, "result": r}
    if method == "ping":
        return {"jsonrpc": "2.0", "id": mid, "result": {}}
    return {"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "不支援"}}


def stdio():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except Exception:
            continue
        if msg.get("method") == "tools/call" and (msg.get("params") or {}).get("name") == "echo" \
                and ((msg.get("params") or {}).get("arguments") or {}).get("text") == "slow":
            continue  # 不回應:測逾時
        r = handle(msg)
        if r is not None:
            sys.stdout.write(json.dumps(r) + "\n")
            sys.stdout.flush()


def http():
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    expect = "Bearer " + os.environ.get("EXPECT_AUTH", "")
    sessions = set()

    class H(BaseHTTPRequestHandler):
        def log_message(self, *a):
            pass

        def _send(self, code, body=b"", ctype=None, extra=None):
            self.send_response(code)
            if ctype:
                self.send_header("Content-Type", ctype)
            for k, v in (extra or {}).items():
                self.send_header(k, v)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_DELETE(self):
            sessions.discard(self.headers.get("Mcp-Session-Id"))
            self._send(200)

        def do_POST(self):
            if self.headers.get("Authorization") != expect:
                return self._send(401, b"unauthorized", "text/plain")
            n = int(self.headers.get("Content-Length") or 0)
            msg = json.loads(self.rfile.read(n) or b"{}")
            extra = {}
            if msg.get("method") == "initialize":
                sid = "sess-%d" % (len(sessions) + 1)
                sessions.add(sid)
                extra["Mcp-Session-Id"] = sid
            elif self.headers.get("Mcp-Session-Id") not in sessions:
                return self._send(400, b"missing session", "text/plain")
            r = handle(msg)
            if r is None:
                return self._send(202)
            if msg.get("method") == "tools/call":
                note = {"jsonrpc": "2.0", "method": "notifications/progress", "params": {"progress": 1}}
                body = ("event: message\ndata: " + json.dumps(note) + "\n\nevent: message\ndata: " + json.dumps(r) + "\n\n").encode()
                return self._send(200, body, "text/event-stream", extra)
            self._send(200, json.dumps(r).encode(), "application/json", extra)

    srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
    sys.stdout.write("PORT %d\n" % srv.server_address[1])
    sys.stdout.flush()
    srv.serve_forever()


if __name__ == "__main__":
    (http if len(sys.argv) > 1 and sys.argv[1] == "http" else stdio)()
