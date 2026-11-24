import Foundation

// 角色執行日誌(App 版;web 版在 console/internal/core/agent_runlog.go,欄位名稱與上限照同一份):
// 自動執行每跑一筆,依序記下 Claude stream-json 的文字、工具呼叫、工具結果與錯誤,事後能查「角色到底有沒有呼叫 omixor 工具、是誰回報的」。
// 存放:agent-run-logs.json(和 agent-runner.json 同一個資料夾),每個組織只留最近 keep 筆。
// 與 web 版的差別:沒有「伺服器確認的呼叫」(server_call;角色端點在 omixor server 上),serverCalls 一律 0。

/// 上限(和 Go 的 runLog* 常數同值)
enum RunLogLimits {
    static let keep = 50
    static let maxEntries = 300
    static let maxBytes = 150_000
    static let argsMax = 200
    static let argValueMax = 60
    static let resultMax = 300
    static let textMax = 300
}

/// 誰回報了任務
enum RunReportedBy {
    static let role = "role"        // 角色自己回報
    static let runner = "runner"    // 角色沒回報,由 omixor 補回報
}

/// 角色看得到 omixor 工具與否的判斷(對應 console 的 mcpGate):connected 照常跑;pending 還在連,先跑著看
/// (結束時再依有沒有呼叫判斷);其餘(failed、needs-auth、沒列出來)馬上中止
enum McpGate {
    case proceed, watch, abort
    static func of(_ status: String?) -> McpGate {
        switch status {
        case "connected": return .proceed
        case "pending": return .watch
        default: return .abort
        }
    }
    static let omixorName = "omixor"
    static let omixorToolPrefix = "mcp__omixor__"
    /// 第一輪前最多等 omixor 的 MCP 連線這麼久(毫秒)
    static let connectWaitMs = 15000
    /// init 事件裡 omixor 伺服器的狀態;不是 init 或沒列出來回 nil
    static func isInit(_ obj: [String: Any]) -> Bool { obj["type"] as? String == "system" && obj["subtype"] as? String == "init" }
    static func omixorStatus(_ obj: [String: Any]) -> String? {
        guard isInit(obj) else { return nil }
        return ((obj["mcp_servers"] as? [[String: Any]]) ?? []).first { $0["name"] as? String == omixorName }?["status"] as? String
    }
    /// init 事件且 omixor 的 MCP 不能用 → 要中止的說明;其他事件回 nil
    static func abortMessage(_ obj: [String: Any]) -> String? {
        guard isInit(obj), of(omixorStatus(obj)) == .abort else { return nil }
        return unusableMessage(omixorStatus(obj))
    }
    static func label(_ status: String?) -> String { status.flatMap { $0.isEmpty ? nil : $0 } ?? "未知" }
    /// 角色沒有拿到 omixor 工具時的說明(寫進任務備註與執行錯誤)
    static func unusableMessage(_ status: String?) -> String { "角色沒有連上 omixor 的工具(MCP 狀態:\(label(status))),沒有實際執行任務" }
    static func noToolCallMessage(_ status: String?) -> String { "角色沒有呼叫任何 omixor 工具(MCP 狀態:\(label(status))),沒有實際執行任務" }
    /// 「全部失敗」說明裡附的最後一筆錯誤的字數上限(與 Go 的 omixorErrExcerptMax 一致)
    static let errExcerptMax = 160
    /// 角色呼叫了 omixor 工具、卻沒有任何一次成功時的說明;lastErr 是最後一筆錯誤(可空)
    static func allCallsFailedMessage(calls: Int, lastErr: String) -> String {
        "角色呼叫了 omixor 的工具,但全部失敗,沒有實際執行任務(呼叫 \(calls) 次)" + (lastErr.isEmpty ? "" : ":" + String(lastErr.prefix(errExcerptMax)))
    }
}

/// 日誌的一個項目(Go 的 RunLogEntry;App 內另有同名前綴的 RunnerLogEntry 是執行紀錄的一列,所以這裡叫 Item)
struct RunLogItem: Codable, Hashable, Identifiable {
    var at: Date
    var kind: String            // start、text、tool_call、tool_result、server_call、error、end
    var tool: String?
    var args: String?
    var status: String?         // ok、error
    var text: String?
    var id: String { "\(at.timeIntervalSince1970)-\(kind)-\(tool ?? "")-\(args ?? "")-\(text ?? "")" }
}

struct RunLog: Codable, Hashable, Identifiable {
    var runId = ""
    var taskId = ""
    var title = ""
    var agentId = ""
    var agentName = ""
    var startedAt = Date()
    var finishedAt = Date()
    var durationSeconds = 0
    var costUsd = 0.0
    var outcome = ""
    var error = ""
    var reportedBy = ""
    var model = ""
    var sessionId = ""
    var mcpStatus = ""
    var toolCalls = 0
    var serverCalls = 0
    var truncated = false
    var entries: [RunLogItem] = []
    var id: String { runId }
    /// 結果值;"錯誤" 是舊版 App 寫進日誌檔的值,當作 error
    var outcomeKey: String { outcome == "錯誤" ? RunOutcome.error : outcome }
}

/// 一筆執行的結果值(同 console 的 runLogModel.ts:done、blocked、error)
enum RunOutcome {
    static let done = "done"
    static let blocked = "blocked"
    static let error = "error"
}

/// 結果篩選(同 console 的 OUTCOME_FILTERS):all 不篩
enum RunOutcomeFilter: String, CaseIterable, Identifiable {
    case all, done, blocked, error
    var id: String { rawValue }
}

extension Array where Element == RunLog {
    /// 依角色(空字串 = 全部)與結果篩選,順序不變(已由新到舊)
    func filtered(agent: String, outcome: RunOutcomeFilter) -> [RunLog] {
        filter { (agent.isEmpty || $0.agentId == agent) && (outcome == .all || $0.outcomeKey == outcome.rawValue) }
    }

    /// 歷史裡出現過的角色(依第一次出現的順序,同一個角色只列一次)
    var roles: [(id: String, name: String)] {
        var seen = Set<String>()
        return compactMap { seen.insert($0.agentId).inserted ? ($0.agentId, $0.agentName) : nil }
    }
}

struct RunLogFile: Codable { var runs: [RunLog] = [] }

/// 讀寫 agent-run-logs.json(同一個 App 內一次一筆,在主執行緒用)
enum RunLogStore {
    static func load(_ url: URL) -> RunLogFile {
        guard let d = try? Data(contentsOf: url), let f = try? decoder.decode(RunLogFile.self, from: d) else { return RunLogFile() }
        return f
    }

    /// 放到最前面、同 runId 取代、只留最近 keep 筆
    static func persist(_ log: RunLog, to url: URL) throws {
        var runs = [log] + load(url).runs.filter { $0.runId != log.runId }
        runs.sort { $0.startedAt > $1.startedAt }
        let e = JSONEncoder()
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        e.dateEncodingStrategy = .iso8601
        try e.encode(RunLogFile(runs: Array(runs.prefix(RunLogLimits.keep)))).write(to: url, options: .atomic)
    }

    private static var decoder: JSONDecoder { let d = JSONDecoder(); d.dateDecodingStrategy = .iso8601; return d }
}

/// 機密遮蔽:core/redact 的 Swift 版(只移植一次要用到的規則,規則清單與 core/redact/redact.go 的 Secrets 同順序;
/// 差別:key=value 只遮值、不保留引號)。改規則時兩邊一起改
enum RunLogRedact {
    static let masked = "[masked]"
    private static let rules: [(NSRegularExpression, String)] = [
        (#"(?i)\bauthorization\s*:[^\r\n]*"#, "Authorization: \(masked)"),
        (#"(?i)\bbearer\s+[^\s"'<>]+"#, "Bearer \(masked)"),
        (#"(?i)\b(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|redis|amqp)://[^\s"'<>]+"#, masked),
        (#"\b(?:omx|omo)_[A-Za-z0-9_\-.]+"#, masked),
        (#"(?i)\b(set-cookie|cookie)\s*:[^\r\n]*"#, "$1: \(masked)"),
        (#"(?i)(["']?\b(?:password|passwd|pwd|secret|client_secret|token|access_token|refresh_token|id_token|api_key|apikey|api-key|private_key|session|sessionid|auth|signature)\b["']?[ \t]*[:=][ \t]*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s&"',;]+)"#, "$1\(masked)"),
        (#"\b(?:eyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}|(?:gh[pous]_|github_pat_|sk_live_|sk_test_)[A-Za-z0-9_]{8,}|sk-[A-Za-z0-9_\-]{16,}|AKIA[0-9A-Z]{16}|AIza[A-Za-z0-9_\-]{35}|xox[abprs]-[A-Za-z0-9\-]{8,})"#, masked),
        (#"[A-Za-z0-9+/=_\-]{40,}"#, masked),
    ].map { (try! NSRegularExpression(pattern: $0.0), $0.1) }

    static func secrets(_ s: String) -> String {
        var out = s
        for (re, template) in rules {
            out = re.stringByReplacingMatches(in: out, range: NSRange(out.startIndex..., in: out), withTemplate: template)
        }
        return out
    }
}

/// 一筆執行進行中的日誌:stream-json 的事件從 ClaudeCLI 的背景執行緒進來,所以要上鎖
final class RunLogRecorder: @unchecked Sendable {
    private let lock = NSLock()
    private var log: RunLog
    private let secrets: () -> [String]
    private var pending: [String: String] = [:]   // tool_use id → 工具名稱
    private var bytes = 0
    private var omixorMCP: String?     // init 裡 omixor 伺服器的狀態(沒出現是 nil)
    private var omixorCalls = 0        // 解析到的 mcp__omixor__ 工具呼叫數
    private var omixorOK = 0           // 其中成功回來的結果數(tool_result 沒有 is_error)
    private var omixorLastErr = ""     // 最後一筆失敗結果的內容(已遮蔽、截斷)

    /// secrets:要整段換成 *** 的字串(角色憑證、閘道 token);用函式是因為它們在執行期間才有值
    init(task: AgentTask, agentName: String, started: Date, secrets: @escaping () -> [String]) {
        log = RunLog(runId: "\(task.id)-\(Int(started.timeIntervalSince1970 * 1000))", taskId: task.id, title: task.title,
                     agentId: task.agentId, agentName: agentName, startedAt: started)
        self.secrets = secrets
        add(RunLogItem(at: started, kind: "start", text: "開始執行"))
    }

    var runId: String { lock.withLock { log.runId } }
    var toolCalls: Int { lock.withLock { log.toolCalls } }

    /// 角色沒有實際做事的原因(沒呼叫過任何 omixor 工具、或呼叫了卻全部失敗);至少一次成功回 nil
    var noWork: String? {
        lock.withLock {
            if omixorOK > 0 { return nil }
            if omixorCalls > 0 { return McpGate.allCallsFailedMessage(calls: omixorCalls, lastErr: omixorLastErr) }
            return omixorMCP == "connected" ? McpGate.noToolCallMessage(omixorMCP) : McpGate.unusableMessage(omixorMCP)
        }
    }

    /// 去控制字元 → 角色憑證 → 機密遮蔽 → 截斷(先遮蔽再截斷,才不會把機密從中間切開)
    func scrub(_ s: String) -> String {
        var out = String(String.UnicodeScalarView(s.unicodeScalars.map { CharacterSet.controlCharacters.contains($0) ? " " : $0 }))
        out = out.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        for v in secrets() where !v.isEmpty { out = out.replacingOccurrences(of: v, with: "***") }
        return RunLogRedact.secrets(out)
    }
    func clean(_ s: String, _ max: Int) -> String { Self.trunc(scrub(s), max) }
    private static func trunc(_ s: String, _ n: Int) -> String { s.count <= n ? s : String(s.prefix(n)) + "…" }

    /// 加一個項目;超過上限就丟掉並標 truncated(留一格給結束項目)
    private func add(_ e: RunLogItem) {
        let size = (e.args?.utf8.count ?? 0) + (e.text?.utf8.count ?? 0) + (e.tool?.utf8.count ?? 0) + 40
        if e.kind != "end", log.entries.count >= RunLogLimits.maxEntries - 1 || bytes + size > RunLogLimits.maxBytes {
            log.truncated = true
            return
        }
        bytes += size
        log.entries.append(e)
    }

    /// 工具參數 → 「鍵=值」摘要;鍵與值一起遮蔽才認得出 token=… 這類以鍵名判斷的機密
    func summarizeArgs(_ input: Any?) -> String {
        guard let m = input as? [String: Any] else { return input.map { clean("\($0)", RunLogLimits.argsMax) } ?? "" }
        let parts = m.keys.sorted().map { k -> String in
            let v = m[k]
            let s = (v as? String) ?? ((v.flatMap { try? JSONSerialization.data(withJSONObject: $0, options: [.fragmentsAllowed, .sortedKeys]) })
                .map { String(decoding: $0, as: UTF8.self) } ?? "\(v ?? "")")
            return Self.trunc(scrub("\(k)=\(s)"), RunLogLimits.argValueMax + k.count + 1)
        }
        return Self.trunc(parts.joined(separator: ", "), RunLogLimits.argsMax)
    }

    /// 接 ClaudeCLI 解析出來的每一行 stream-json 物件
    func consume(_ obj: [String: Any]) {
        lock.withLock {
            let message = obj["message"] as? [String: Any]
            switch obj["type"] as? String {
            case "system" where obj["subtype"] as? String == "init":
                log.model = clean(obj["model"] as? String ?? "", 80)
                log.sessionId = clean(obj["session_id"] as? String ?? "", 80)
                log.mcpStatus = ((obj["mcp_servers"] as? [[String: Any]]) ?? [])
                    .map { clean($0["name"] as? String ?? "", 40) + ":" + clean($0["status"] as? String ?? "", 40) }.joined(separator: ",")
                omixorMCP = McpGate.omixorStatus(obj)
            case "assistant":
                if log.model.isEmpty { log.model = clean(message?["model"] as? String ?? "", 80) }
                for b in (message?["content"] as? [[String: Any]]) ?? [] { assistantBlock(b) }
            case "user":
                for b in (message?["content"] as? [[String: Any]]) ?? [] where b["type"] as? String == "tool_result" { toolResult(b) }
            default:
                break
            }
        }
    }

    private func assistantBlock(_ b: [String: Any]) {
        switch b["type"] as? String {
        case "text":
            let s = clean(b["text"] as? String ?? "", RunLogLimits.textMax)
            if !s.isEmpty { add(RunLogItem(at: Date(), kind: "text", text: s)) }
        case "tool_use":
            log.toolCalls += 1
            if (b["name"] as? String ?? "").hasPrefix(McpGate.omixorToolPrefix) { omixorCalls += 1 }
            let name = clean(b["name"] as? String ?? "", 120)
            if let id = b["id"] as? String { pending[id] = name }
            add(RunLogItem(at: Date(), kind: "tool_call", tool: name, args: summarizeArgs(b["input"])))
        default:
            break
        }
    }

    private func toolResult(_ b: [String: Any]) {
        // content 是字串,或 [{type:text,text}] 陣列
        let text: String
        if let s = b["content"] as? String { text = s }
        else { text = ((b["content"] as? [[String: Any]]) ?? []).compactMap { $0["text"] as? String }.joined(separator: "\n") }
        let isError = b["is_error"] as? Bool ?? false
        let tool = pending[b["tool_use_id"] as? String ?? ""]
        let cleaned = clean(text, RunLogLimits.resultMax)
        if tool?.hasPrefix(McpGate.omixorToolPrefix) == true {
            if isError { omixorLastErr = cleaned } else { omixorOK += 1 }
        }
        add(RunLogItem(at: Date(), kind: "tool_result", tool: tool, status: isError ? "error" : "ok", text: cleaned))
    }

    /// 結束:補上結果(結束項目一定留)
    func finish(outcome: String, error: String, seconds: Int, cost: Double?, reportedBy: String, at end: Date = Date()) -> RunLog {
        lock.withLock {
            if !error.isEmpty { add(RunLogItem(at: end, kind: "error", text: clean(error, RunLogLimits.textMax))) }
            add(RunLogItem(at: end, kind: "end", status: outcome == RunOutcome.done ? "ok" : "error", text: clean(outcome, 40)))
            log.outcome = clean(outcome, 40)
            log.error = clean(error, 500)
            log.durationSeconds = seconds
            log.costUsd = cost ?? 0
            log.finishedAt = end
            log.reportedBy = reportedBy
            return log
        }
    }
}
