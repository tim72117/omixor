import Foundation
@testable import Omixor

// 測試用角色:app 不再內建預設角色,需要角色的測試先用這個自己建立。
// id、名稱、文件櫃權限沿用以前的預設值,讓既有測試的斷言不用改。
@MainActor
extension AppState {
    static let testAgentDocAccess: [String: [String: DocAccess]] = [
        "visitor-analyst": ["members": .write, "overview": .read],
        "narrator": ["levels": .read, "/docs/research/voice-cost": .read, "/docs/research/tts-landscape": .read],
        "triager": ["todo": .write],
        "course-dev": ["levels": .write, "dev": .read, "ideas": .read, "docs": .read],
        "marketing": ["overview": .read, "levels": .read, "docs": .read, "/docs/research/marketing": .write],
    ]

    static var testAgents: [Agent] {
        [
            Agent(id: "visitor-analyst", name: "訪客分析師", role: "看訪客數字", symbol: "chart.line.uptrend.xyaxis", color: "teal",
                  instructions: "看訪客數字",
                  routines: [AgentRoutine(title: "每日訪客檢查", instructions: "讀 1d 與 7d", kind: .daily, hour: 9, minute: 0)]),
            Agent(id: "narrator", name: "旁白製作", role: "旁白語音", symbol: "waveform", color: "purple",
                  instructions: "旁白語音",
                  routines: [AgentRoutine(title: "檢查缺少語音的旁白", instructions: "dry_run", kind: .weekly, hour: 10, minute: 0, weekday: 2)]),
            Agent(id: "triager", name: "Issue 整理員", role: "整理 Issue", symbol: "tray.full", color: "orange",
                  instructions: "整理 Issue",
                  routines: [AgentRoutine(title: "每週整理 Issue", instructions: "整理", kind: .weekly, hour: 17, minute: 0, weekday: 6)]),
            Agent(id: "course-dev", name: "課程開發", role: "課程腳本", symbol: "doc.text", color: "indigo",
                  instructions: "課程腳本",
                  routines: [AgentRoutine(title: "課程成效回顧", instructions: "看 7d", kind: .weekly, hour: 10, minute: 0, weekday: 3)]),
            Agent(id: "marketing", name: "行銷", role: "行銷", symbol: "megaphone", color: "pink",
                  instructions: "行銷",
                  routines: [AgentRoutine(title: "每週行銷週報", instructions: "週報", kind: .weekly, hour: 9, minute: 30, weekday: 2)]),
        ].map { a in
            var a = a
            a.docAccess = testAgentDocAccess[a.id] ?? [:]
            return a
        }
    }

    /// 啟動角色並建立測試用角色(已經有同 id 的就不重建)
    @discardableResult
    func startTestAgents() -> AgentStore {
        let s = startAgents(timer: false)
        for a in Self.testAgents where s.agent(a.id) == nil { s.upsert(a) }
        return s
    }
}

// 以角色身分呼叫 MCP 工具:core 的人類端點(App 的 MCP 轉送的 /mcp/org/<組織>)不接受 agent 參數指定角色身分
// (ISS-36,回 400「不能指定角色身分…」),要以角色身分工作只能領任務拿角色憑證、連角色端點 /mcp/agent。
// 這個 helper 替角色排一筆任務並領走(取得憑證),再用憑證呼叫角色端點;同一個 AppState 同一個角色只領一次。
@MainActor
final class RoleMCP {
    let app: AppState
    private var creds: [String: AgentCredential] = [:]
    init(app: AppState) { self.app = app }

    /// 回傳 (解析後的 JSON 物件, 原文, isError)
    func call(_ agent: String, _ name: String, _ args: [String: Any]) async throws -> ([String: Any], String, Bool) {
        let cred = try credential(agent)
        let cfg = IssueServerConfig.current
        let url = URL(string: cred.endpoint).flatMap { $0.scheme == nil ? nil : $0 }
            ?? cfg.url.appendingPathComponent("mcp").appendingPathComponent("agent")
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
        req.setValue("Bearer \(cred.agentId):\(cred.secret)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args],
        ])
        let (data, resp) = try await URLSession.shared.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status),
              let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw IssueRemoteError(message: "角色端點回應不對(HTTP \(status)):\(String(decoding: data, as: UTF8.self))")
        }
        if let e = obj["error"] as? [String: Any] { return ([:], e["message"] as? String ?? "MCP 錯誤", true) }
        let r = obj["result"] as? [String: Any] ?? [:]
        let text = ((r["content"] as? [[String: Any]])?.first?["text"] as? String) ?? ""
        // 角色端點改了 server 上的資料:App 重讀(同 App 的 MCP 轉送後的 refreshAfterServerTool)
        await app.startIssues().reload()
        await app.startAgents().reload()
        await app.startChannels().reload()
        if app.cabinetLoaded { await Cabinet.reload(app) }
        return ((try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] ?? [:], text, r["isError"] as? Bool ?? false)
    }

    private func credential(_ agent: String) throws -> AgentCredential {
        if let c = creds[agent] { return c }
        let store = app.startTestAgents()
        guard let task = store.assign(to: agent, title: "測試:以角色身分工作", priority: .low, by: app.me) else {
            throw IssueRemoteError(message: "替角色 \(agent) 排任務失敗")
        }
        guard let c = try store.claimWithCredential(agent: agent, task: task.id).credential else {
            throw IssueRemoteError(message: "角色 \(agent) 領任務沒拿到憑證")
        }
        creds[agent] = c
        return c
    }
}

/// 失敗結果的錯誤句子:統一格式是 {"error":{"message":...}}(text 與 structuredContent 同內容);不是這個形狀就原樣回傳
enum MCPErrorText {
    static func message(_ text: String) -> String {
        guard let obj = try? JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any] else { return text }
        return message(obj)
    }

    static func message(_ obj: [String: Any]) -> String {
        (obj["error"] as? [String: Any])?["message"] as? String ?? ""
    }
}
