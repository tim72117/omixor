import Foundation

/// 一個專案資料夾(以對話紀錄裡的 cwd 分組)。
struct Project: Identifiable, Hashable {
    var id: String { path }
    let path: String
    var sessions: [SessionSummary]

    var name: String { (path as NSString).lastPathComponent }
}

/// 對話列表上的一筆 session。
struct SessionSummary: Identifiable, Hashable {
    let id: String          // session id(檔名)
    let file: URL           // jsonl 檔
    let cwd: String
    let title: String
    let updatedAt: Date
    let entrypoint: String? // cli、claude-vscode、claude-desktop…
}

/// 對話畫面上的一則項目。
enum TranscriptItem: Identifiable, Hashable {
    case user(id: String, text: String)
    case assistant(id: String, text: String)
    case tools(id: String, calls: [ToolCall])
    case notice(id: String, text: String, isError: Bool)

    var id: String {
        switch self {
        case .user(let id, _), .assistant(let id, _), .tools(let id, _), .notice(let id, _, _):
            return id
        }
    }
}

struct ToolCall: Identifiable, Hashable {
    let id: String
    let name: String
    let summary: String // 指令、檔案路徑等一行摘要
}

/// 權限模式:對應 claude CLI 的 --permission-mode。
enum PermissionMode: String, CaseIterable, Identifiable {
    case `default`
    case acceptEdits
    case plan

    var id: String { rawValue }

    var label: String {
        switch self {
        case .default: return "Ask"
        case .acceptEdits: return "Accept edits"
        case .plan: return "Plan"
        }
    }
}

enum ModelChoice: String, CaseIterable, Identifiable {
    case opus, sonnet, haiku
    var id: String { rawValue }
    var label: String { rawValue.capitalized }
}
