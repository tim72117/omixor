import SwiftUI

// AI 助理面板內部用的資料型別。所有型別以 Assist 開頭,避免和其他畫面撞名。

/// AI 的主色:紫到靛藍
enum AssistTheme {
    /// 對話泡泡與輸入框的字級(macOS 的 .callout 只有 12pt,太小)
    static let messageFont = Font.system(size: 14)
    static let accent = Color.indigo
    static let gradient = LinearGradient(colors: [.purple, .indigo], startPoint: .topLeading, endPoint: .bottomTrailing)
    static let soft = LinearGradient(colors: [Color.purple.opacity(0.10), Color.indigo.opacity(0.06)],
                                     startPoint: .topLeading, endPoint: .bottomTrailing)
}

/// 回答裡可以按的動作按鈕
enum AssistAction: Hashable, Identifiable {
    case acknowledge(alertId: String, title: String)
    case approve(approvalId: String)
    case reject(approvalId: String)
    case retry(runId: Int)
    case go(AppSection, focus: String?, label: String)
    case copy(String)

    var id: String {
        switch self {
        case .acknowledge(let id, _): return "ack-\(id)"
        case .approve(let id): return "approve-\(id)"
        case .reject(let id): return "reject-\(id)"
        case .retry(let id): return "retry-\(id)"
        case .go(let s, let f, _): return "go-\(s.rawValue)-\(f ?? "")"
        case .copy(let t): return "copy-\(t.hashValue)"
        }
    }

    var label: String {
        switch self {
        case .acknowledge(_, let title): return "確認告警:\(title)"
        case .approve: return "核准"
        case .reject: return "拒絕"
        case .retry(let id): return "重新執行 #\(id)"
        case .go(_, _, let label): return label
        case .copy: return "複製文字"
        }
    }

    var icon: String {
        switch self {
        case .acknowledge: return "bell.badge"
        case .approve: return "checkmark.seal"
        case .reject: return "xmark.seal"
        case .retry: return "arrow.clockwise"
        case .go(let s, _, _): return s.icon
        case .copy: return "doc.on.doc"
        }
    }

    /// 會改變狀態的動作用醒目的樣式
    var prominent: Bool {
        switch self {
        case .acknowledge, .approve, .retry: return true
        default: return false
        }
    }
}

/// 一則對話訊息
struct AssistMessage: Identifiable, Equatable {
    enum Role { case user, assistant }
    let id = UUID()
    let role: Role
    var text: String
    var actions: [AssistAction] = []
    var thinking = false
    var streaming = false
    /// 已經按過的動作,按完就停用,避免重複觸發
    var usedActions: Set<String> = []
    let date = Date()
}

/// 回應器產生的完整回答
struct AssistReply {
    var text: String
    var actions: [AssistAction] = []
}

/// 「目前狀況」卡片的一行
struct AssistBriefLine: Identifiable {
    let id = UUID()
    let icon: String
    let color: Color
    let text: String
}

struct AssistBriefing {
    let headline: String
    let tone: Color
    let lines: [AssistBriefLine]
}
