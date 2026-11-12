import Foundation

// omixor-mcp:把 stdio 上的 MCP 訊息轉給執行中的 Ops app(http://127.0.0.1:3765/mcp)。
// 給只支援 stdio 的 MCP 用戶端用;支援 HTTP 的用戶端(例如 Claude Code)可以直接連 app。
//
// 一行一則 JSON-RPC 訊息(MCP stdio 規格)。app 沒開時,請求會收到 JSON-RPC 錯誤。
// 連線位址:OPS_MCP_URL,或 OPS_MCP_PORT 指定連接埠。

let env = ProcessInfo.processInfo.environment
let endpoint: URL = {
    if let s = env["OPS_MCP_URL"], let u = URL(string: s) { return u }
    return URL(string: "http://127.0.0.1:\(env["OPS_MCP_PORT"] ?? "3765")/mcp")!
}()

let session: URLSession = {
    let c = URLSessionConfiguration.ephemeral
    c.timeoutIntervalForRequest = 60
    c.connectionProxyDictionary = [:]  // 不走系統代理
    return URLSession(configuration: c)
}()

func log(_ s: String) {
    FileHandle.standardError.write(Data("[omixor-mcp] \(s)\n".utf8))
}

func write(_ data: Data) {
    var line = data
    // 回應必須是單行
    line.removeAll { $0 == UInt8(ascii: "\n") || $0 == UInt8(ascii: "\r") }
    line.append(UInt8(ascii: "\n"))
    FileHandle.standardOutput.write(line)
}

func errorReply(id: Any, _ message: String) -> Data {
    let obj: [String: Any] = ["jsonrpc": "2.0", "id": id, "error": ["code": -32000, "message": message]]
    return (try? JSONSerialization.data(withJSONObject: obj)) ?? Data()
}

/// 同步送出一則訊息,回傳 (HTTP 狀態, 內容) 或錯誤
func post(_ body: Data) -> Result<(Int, Data), Error> {
    var req = URLRequest(url: endpoint)
    req.httpMethod = "POST"
    req.httpBody = body
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
    let sem = DispatchSemaphore(value: 0)
    var out: Result<(Int, Data), Error> = .failure(URLError(.unknown))
    session.dataTask(with: req) { data, resp, err in
        if let err { out = .failure(err) }
        else { out = .success(((resp as? HTTPURLResponse)?.statusCode ?? 0, data ?? Data())) }
        sem.signal()
    }.resume()
    sem.wait()
    return out
}

log("轉接到 \(endpoint.absoluteString)")

while let line = readLine(strippingNewline: true) {
    let trimmed = line.trimmingCharacters(in: .whitespaces)
    if trimmed.isEmpty { continue }
    let body = Data(trimmed.utf8)
    let msg = try? JSONSerialization.jsonObject(with: body) as? [String: Any]
    let id = msg?["id"]  // nil = 通知

    switch post(body) {
    case .success(let (status, data)):
        if status == 202 || data.isEmpty { continue }
        if status == 200 { write(data); continue }
        let text = String(decoding: data, as: UTF8.self)
        log("HTTP \(status): \(text)")
        if let id { write(errorReply(id: id, "omixor app 回應 HTTP \(status):\(text)")) }
    case .failure(let err):
        log("無法連線:\(err.localizedDescription)")
        if let id {
            write(errorReply(id: id, "無法連到 Ops app(\(endpoint.absoluteString))。請先開啟 Ops,再重試。"))
        }
    }
}
