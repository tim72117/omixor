import Foundation

/// 解析 Claude Code 的 jsonl 對話紀錄。
/// 紀錄格式不是公開規格,只讀取需要的欄位,其他類型的行一律略過。
enum TranscriptParser {
    // MARK: - 列表用的摘要

    struct Header {
        var sessionId: String?
        var cwd: String?
        var entrypoint: String?
        var customTitle: String?
        var aiTitle: String?
        var firstPrompt: String?
    }

    /// 只解析列表需要的資訊。對話紀錄可能有上百 MB,所以不整份解析:
    /// cwd 與第一句話從檔頭找,標題從檔尾往回找最後一筆。
    static func header(of data: Data) -> Header {
        var h = Header()
        var headSize = 64 * 1024
        while true {
            let end = min(data.count, headSize)
            scanBasics(data.subdata(in: 0..<end), into: &h)
            if (h.cwd != nil && h.firstPrompt != nil) || end == data.count || headSize >= 8 * 1024 * 1024 { break }
            headSize *= 4
        }
        if let obj = lastRecord(in: data, marker: #""type":"custom-title""#),
           let t = obj["customTitle"] as? String, !t.isEmpty { h.customTitle = t }
        if let obj = lastRecord(in: data, marker: #""type":"ai-title""#),
           let t = obj["aiTitle"] as? String, !t.isEmpty { h.aiTitle = t }
        return h
    }

    /// 找含有 marker 的最後一行並解析。
    static func lastRecord(in data: Data, marker: String) -> [String: Any]? {
        guard let hit = data.range(of: Data(marker.utf8), options: .backwards) else { return nil }
        let start = data[..<hit.lowerBound].lastIndex(of: 0x0A).map { $0 + 1 } ?? data.startIndex
        let end = data[hit.upperBound...].firstIndex(of: 0x0A) ?? data.endIndex
        return (try? JSONSerialization.jsonObject(with: data.subdata(in: start..<end))) as? [String: Any]
    }

    private static func scanBasics(_ data: Data, into h: inout Header) {
        h = Header()
        for line in lines(data) {
            guard h.cwd == nil || h.firstPrompt == nil else { return }
            guard let obj = json(line) else { continue } // 檔頭截斷的最後一行會解析失敗,略過
            switch obj["type"] as? String {
            case "user":
                h.sessionId = h.sessionId ?? obj["sessionId"] as? String
                h.cwd = h.cwd ?? obj["cwd"] as? String
                h.entrypoint = h.entrypoint ?? obj["entrypoint"] as? String
                if h.firstPrompt == nil, isPlainUser(obj), let t = userText(obj) { h.firstPrompt = t }
            default:
                h.cwd = h.cwd ?? obj["cwd"] as? String
                h.entrypoint = h.entrypoint ?? obj["entrypoint"] as? String
            }
        }
    }

    // MARK: - 完整對話

    /// 對話畫面最多讀檔尾這麼多;很長的對話(上 GB)只顯示最近的部分。
    static let displayLimit = 24 * 1024 * 1024

    struct Loaded {
        var items: [TranscriptItem]
        var truncated: Bool
    }

    static func items(from data: Data, limit: Int = displayLimit) -> Loaded {
        let (tail, truncated) = tailSlice(data, limit: limit)
        var out: [TranscriptItem] = []
        forEachLine(tail) { line in
            guard isMessageLine(line), let obj = json(line) else { return }
            append(record: obj, to: &out)
        }
        return Loaded(items: out, truncated: truncated)
    }

    /// 把一筆紀錄(檔案中的一行,或 CLI stream-json 的一個事件)加進對話。
    /// 連續的工具呼叫併成一列,像「Ran 3 commands」。
    static func append(record obj: [String: Any], to out: inout [TranscriptItem]) {
        let uuid = obj["uuid"] as? String ?? UUID().uuidString
        switch obj["type"] as? String {
        case "user":
            guard isPlainUser(obj), let text = userText(obj) else { return }
            out.append(.user(id: uuid, text: text))
        case "assistant":
            if obj["isSidechain"] as? Bool == true { return }
            guard let message = obj["message"] as? [String: Any],
                  let blocks = message["content"] as? [[String: Any]] else { return }
            for (i, block) in blocks.enumerated() {
                let bid = "\(uuid)-\(i)"
                switch block["type"] as? String {
                case "text":
                    let text = (block["text"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !text.isEmpty { out.append(.assistant(id: bid, text: text)) }
                case "tool_use":
                    let call = ToolCall(
                        id: block["id"] as? String ?? bid,
                        name: block["name"] as? String ?? "Tool",
                        summary: toolSummary(block["input"] as? [String: Any] ?? [:])
                    )
                    if case .tools(let id, let calls)? = out.last {
                        out[out.count - 1] = .tools(id: id, calls: calls + [call])
                    } else {
                        out.append(.tools(id: bid, calls: [call]))
                    }
                default:
                    break
                }
            }
        default:
            break
        }
    }

    /// 最後一次主對話回覆用掉的輸入 token(含快取),用來估算 context 使用率。
    static func contextTokens(from data: Data) -> Int? {
        let (tail, _) = tailSlice(data, limit: displayLimit)
        var found: Int?
        forEachLine(tail) { line in
            guard isMessageLine(line), line.range(of: Data(#""usage""#.utf8)) != nil,
                  let obj = json(line), obj["type"] as? String == "assistant",
                  obj["isSidechain"] as? Bool != true,
                  let usage = (obj["message"] as? [String: Any])?["usage"] as? [String: Any]
            else { return }
            found = contextTokens(usage: usage)
        }
        return found
    }

    static func contextTokens(usage: [String: Any]) -> Int {
        ["input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"]
            .reduce(0) { $0 + ((usage[$1] as? Int) ?? 0) }
    }

    // MARK: - helpers

    /// 使用者真正打的字:排除 tool_result、meta、子代理與系統注入的標籤訊息。
    static func isPlainUser(_ obj: [String: Any]) -> Bool {
        if obj["isMeta"] as? Bool == true || obj["isSidechain"] as? Bool == true { return false }
        guard let text = userText(obj) else { return false }
        return !text.hasPrefix("<")
    }

    static func userText(_ obj: [String: Any]) -> String? {
        guard let message = obj["message"] as? [String: Any] else { return nil }
        var text: String?
        if let s = message["content"] as? String {
            text = s
        } else if let blocks = message["content"] as? [[String: Any]] {
            let parts = blocks.compactMap { $0["type"] as? String == "text" ? $0["text"] as? String : nil }
            if !parts.isEmpty { text = parts.joined(separator: "\n") }
        }
        let trimmed = text?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? nil : trimmed
    }

    static func toolSummary(_ input: [String: Any]) -> String {
        for key in ["description", "command", "file_path", "pattern", "url", "query", "prompt"] {
            if let v = input[key] as? String, !v.isEmpty {
                return v.replacingOccurrences(of: "\n", with: " ")
            }
        }
        return ""
    }

    static func json(_ line: Substring) -> [String: Any]? {
        json(Data(line.utf8))
    }

    static func json(_ line: Data) -> [String: Any]? {
        (try? JSONSerialization.jsonObject(with: line)) as? [String: Any]
    }

    static func lines(_ data: Data) -> [Substring] {
        let text = String(decoding: data, as: UTF8.self)
        return text.split(separator: "\n", omittingEmptySubsequences: true)
    }

    /// 逐行處理,不把整個檔案轉成字串。
    static func forEachLine(_ data: Data, _ body: (Data) -> Void) {
        var start = data.startIndex
        while start < data.endIndex {
            let end = data[start...].firstIndex(of: 0x0A) ?? data.endIndex
            if end > start { body(data[start..<end]) }
            start = end + 1
        }
    }

    /// 只有 user / assistant 紀錄需要解析。type 在開頭的紀錄(api-request-blob 等)
    /// 可以先排除,省下解析大型請求內容的時間。
    static func isMessageLine(_ line: Data) -> Bool {
        let prefix = Data(#"{"type":""#.utf8)
        guard line.starts(with: prefix) else { return true }
        return line.starts(with: Data(#"{"type":"user""#.utf8)) || line.starts(with: Data(#"{"type":"assistant""#.utf8))
    }

    /// 檔尾 limit 位元組,從完整的一行開始。
    static func tailSlice(_ data: Data, limit: Int) -> (Data, Bool) {
        guard data.count > limit else { return (data, false) }
        let from = data.index(data.endIndex, offsetBy: -limit)
        let lineStart = data[from...].firstIndex(of: 0x0A).map { $0 + 1 } ?? from
        return (data[lineStart...], true)
    }
}
