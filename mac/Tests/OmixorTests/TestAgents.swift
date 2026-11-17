import Foundation
@testable import Ops

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
