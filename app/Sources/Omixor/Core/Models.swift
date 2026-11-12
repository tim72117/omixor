import Foundation
import SwiftUI

// 所有畫面共用的資料模型。資料來源目前是 MockData;之後接真實服務時只換資料來源,不改模型。

// MARK: - 共用列舉

enum Health: String, CaseIterable, Identifiable, Codable {
    case healthy, degraded, down, maintenance, unknown
    var id: String { rawValue }
    var label: String {
        switch self {
        case .healthy: return "正常"
        case .degraded: return "效能下降"
        case .down: return "中斷"
        case .maintenance: return "維護中"
        case .unknown: return "未知"
        }
    }
    var color: Color {
        switch self {
        case .healthy: return .green
        case .degraded: return .orange
        case .down: return .red
        case .maintenance: return .blue
        case .unknown: return .gray
        }
    }
    var icon: String {
        switch self {
        case .healthy: return "checkmark.circle.fill"
        case .degraded: return "exclamationmark.triangle.fill"
        case .down: return "xmark.octagon.fill"
        case .maintenance: return "wrench.and.screwdriver.fill"
        case .unknown: return "questionmark.circle.fill"
        }
    }
}

enum DeployEnv: String, CaseIterable, Identifiable, Codable {
    case production, staging, development
    var id: String { rawValue }
    var label: String {
        switch self {
        case .production: return "Production"
        case .staging: return "Staging"
        case .development: return "Development"
        }
    }
    var short: String {
        switch self {
        case .production: return "prod"
        case .staging: return "stg"
        case .development: return "dev"
        }
    }
    var color: Color {
        switch self {
        case .production: return .red
        case .staging: return .orange
        case .development: return .teal
        }
    }
}

/// 時間範圍篩選(工具列)
enum TimeRange: String, CaseIterable, Identifiable, Encodable {
    case hour1 = "1h", hours6 = "6h", hours24 = "24h", days7 = "7d", days30 = "30d"
    var id: String { rawValue }
    var label: String {
        switch self {
        case .hour1: return "1 小時"
        case .hours6: return "6 小時"
        case .hours24: return "24 小時"
        case .days7: return "7 天"
        case .days30: return "30 天"
        }
    }
    var seconds: TimeInterval {
        switch self {
        case .hour1: return 3600
        case .hours6: return 6 * 3600
        case .hours24: return 24 * 3600
        case .days7: return 7 * 86400
        case .days30: return 30 * 86400
        }
    }
}

/// 執行者:人或 AI。很多紀錄都要標出是誰做的,AI 的動作要能一眼辨認。
enum Actor: Hashable {
    case person(String)       // Person.id
    case ai(String)           // Automation.id
    case system
}

struct Person: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let role: String
    let colorHex: UInt32
    var initials: String { String(name.prefix(1)) }
    var color: Color { Color(hex: colorHex) }
}

// MARK: - 時間序列

struct MetricPoint: Identifiable, Hashable, Encodable {
    var id: Date { date }
    let date: Date
    let value: Double
}

struct MetricSeries: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let unit: String
    var points: [MetricPoint]
    var latest: Double { points.last?.value ?? 0 }
    var values: [Double] { points.map(\.value) }
}

// MARK: - 服務與伺服器

struct Service: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let summary: String
    let team: String
    let owner: String            // Person.id
    let language: String
    let repo: String             // Repository.id
    var health: Health
    var versions: [DeployEnv: String]
    let tier: Int                // 1 = 最關鍵
    var uptime30d: Double        // 百分比,例如 99.95
    var sloTarget: Double        // 例如 99.9
    var latencyP95: Double       // ms
    var errorRate: Double        // 百分比
    var rps: Double
    let dependencies: [String]   // Service.id
    var hostIds: [String]        // Server.id
    var errorBudgetRemaining: Double // 0...1
}

struct Server: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let role: String
    let provider: String         // GCP、AWS…
    let region: String
    let env: DeployEnv
    let os: String
    let ip: String
    let cores: Int
    let memoryGB: Int
    var health: Health
    var cpu: Double              // 0...100
    var memory: Double           // 0...100
    var disk: Double             // 0...100
    var networkMbps: Double
    var cpuSeries: [MetricPoint]
    var memorySeries: [MetricPoint]
    var uptimeDays: Int
    var containers: [Container]
    let tags: [String]
}

struct Container: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let image: String
    var status: Health
    var restarts: Int
    var cpu: Double
    var memoryMB: Double
}

// MARK: - 開發

struct Repository: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let language: String
    let defaultBranch: String
    var openPRs: Int
    var coverage: Double         // 百分比
    var lastCommit: Date
    var weeklyCommits: [Int]     // 最近 12 週
    var vulnerabilities: Int
}

enum PRStatus: String, CaseIterable, Identifiable, Encodable {
    case draft, open, approved, changesRequested, merged, closed
    var id: String { rawValue }
    var label: String {
        switch self {
        case .draft: return "草稿"
        case .open: return "待審"
        case .approved: return "已核准"
        case .changesRequested: return "需修改"
        case .merged: return "已合併"
        case .closed: return "已關閉"
        }
    }
    var color: Color {
        switch self {
        case .draft: return .gray
        case .open: return .blue
        case .approved: return .green
        case .changesRequested: return .orange
        case .merged: return .purple
        case .closed: return .secondary
        }
    }
}

enum CheckStatus: String, Encodable {
    case pending, running, passed, failed
    var color: Color {
        switch self {
        case .pending: return .gray
        case .running: return .blue
        case .passed: return .green
        case .failed: return .red
        }
    }
    var icon: String {
        switch self {
        case .pending: return "circle.dotted"
        case .running: return "arrow.triangle.2.circlepath"
        case .passed: return "checkmark.circle.fill"
        case .failed: return "xmark.circle.fill"
        }
    }
}

struct PullRequest: Identifiable, Hashable, Encodable {
    let id: Int                  // PR 編號
    let title: String
    let repo: String
    let author: Actor
    let branch: String
    var status: PRStatus
    var checks: CheckStatus
    var reviewers: [String]      // Person.id
    var aiReview: AIReview?
    let additions: Int
    let deletions: Int
    let createdAt: Date
    var updatedAt: Date
    let linkedItem: String?      // WorkItem.id
}

struct AIReview: Hashable, Encodable {
    let summary: String
    let risk: RiskLevel
    let findings: [String]
}

enum WorkStatus: String, CaseIterable, Identifiable, Encodable {
    case backlog, todo, inProgress, review, done
    var id: String { rawValue }
    var label: String {
        switch self {
        case .backlog: return "Backlog"
        case .todo: return "待辦"
        case .inProgress: return "進行中"
        case .review: return "審查中"
        case .done: return "完成"
        }
    }
}

enum WorkType: String, CaseIterable, Identifiable, Encodable {
    case feature, bug, chore, incident
    var id: String { rawValue }
    var label: String {
        switch self {
        case .feature: return "功能"
        case .bug: return "Bug"
        case .chore: return "雜務"
        case .incident: return "事件後續"
        }
    }
    var color: Color {
        switch self {
        case .feature: return .blue
        case .bug: return .red
        case .chore: return .gray
        case .incident: return .orange
        }
    }
}

struct WorkItem: Identifiable, Hashable, Encodable {
    let id: String               // 例如 OPS-142
    let title: String
    let type: WorkType
    var status: WorkStatus
    var assignee: Actor?
    let points: Int
    let service: String?
    let createdByAI: Bool
}

struct Sprint: Hashable, Encodable {
    let name: String
    let start: Date
    let end: Date
    let goal: String
    var burndown: [MetricPoint]  // 剩餘點數
    var idealBurndown: [MetricPoint]

    /// 新組織沒有 sprint:名稱空白、起訖相同(MCP 的 ops_list sprint 回傳空物件)
    static let empty = Sprint(name: "", start: Date(timeIntervalSince1970: 0), end: Date(timeIntervalSince1970: 0),
                              goal: "", burndown: [], idealBurndown: [])
    var isEmpty: Bool { name.isEmpty && burndown.isEmpty && idealBurndown.isEmpty }
}

// MARK: - CI/CD

enum RunStatus: String, CaseIterable, Identifiable, Encodable {
    case queued, running, succeeded, failed, canceled, waitingApproval
    var id: String { rawValue }
    var label: String {
        switch self {
        case .queued: return "排隊中"
        case .running: return "執行中"
        case .succeeded: return "成功"
        case .failed: return "失敗"
        case .canceled: return "已取消"
        case .waitingApproval: return "等待核准"
        }
    }
    var color: Color {
        switch self {
        case .queued: return .gray
        case .running: return .blue
        case .succeeded: return .green
        case .failed: return .red
        case .canceled: return .secondary
        case .waitingApproval: return .purple
        }
    }
    var icon: String {
        switch self {
        case .queued: return "clock"
        case .running: return "arrow.triangle.2.circlepath"
        case .succeeded: return "checkmark.circle.fill"
        case .failed: return "xmark.circle.fill"
        case .canceled: return "slash.circle"
        case .waitingApproval: return "hand.raised.fill"
        }
    }
}

struct PipelineJob: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    var status: RunStatus
    var duration: TimeInterval?
    var log: [String]
}

struct PipelineStage: Identifiable, Hashable, Encodable {
    let id: String
    let name: String             // Build、Test、Security scan、Deploy staging…
    var status: RunStatus
    var jobs: [PipelineJob]
    var duration: TimeInterval? { jobs.compactMap(\.duration).max() }
}

struct PipelineRun: Identifiable, Hashable, Encodable {
    let id: Int                  // run 編號
    let pipeline: String         // 例如 "api-server / main"
    let repo: String
    let branch: String
    let commitSHA: String
    let commitMessage: String
    let trigger: String          // push、PR、排程、AI 重試…
    let triggeredBy: Actor
    var status: RunStatus
    var stages: [PipelineStage]
    let startedAt: Date
    var duration: TimeInterval?
    var aiDiagnosis: String?     // 失敗時 AI 的分析
    let targetEnv: DeployEnv?
}

enum DeployStrategy: String, CaseIterable, Encodable {
    case rolling = "Rolling", canary = "Canary", blueGreen = "Blue/Green"
}

struct Deployment: Identifiable, Hashable, Encodable {
    let id: String
    let service: String          // Service.id
    let version: String
    let previousVersion: String
    let env: DeployEnv
    var status: RunStatus
    let strategy: DeployStrategy
    var progress: Double         // 0...1
    var canaryPercent: Int?
    let startedAt: Date
    var finishedAt: Date?
    let by: Actor
    let changes: [String]        // 變更摘要
    var healthGate: CheckStatus  // 部署後健康檢查
}

// MARK: - 監控、告警、日誌

enum Severity: String, CaseIterable, Identifiable, Comparable, Encodable {
    case critical, warning, info
    var id: String { rawValue }
    var label: String {
        switch self {
        case .critical: return "Critical"
        case .warning: return "Warning"
        case .info: return "Info"
        }
    }
    var color: Color {
        switch self {
        case .critical: return .red
        case .warning: return .orange
        case .info: return .blue
        }
    }
    private var rank: Int { [.critical: 0, .warning: 1, .info: 2][self]! }
    static func < (a: Severity, b: Severity) -> Bool { a.rank < b.rank }
}

enum AlertState: String, CaseIterable, Identifiable, Encodable {
    case firing, acknowledged, resolved, silenced
    var id: String { rawValue }
    var label: String {
        switch self {
        case .firing: return "觸發中"
        case .acknowledged: return "已確認"
        case .resolved: return "已解除"
        case .silenced: return "已靜音"
        }
    }
}

struct Alert: Identifiable, Hashable, Encodable {
    let id: String
    let title: String
    let severity: Severity
    let service: String
    var state: AlertState
    let rule: String             // 例如 "p95 latency > 800ms for 5m"
    let value: String
    let startedAt: Date
    var acknowledgedBy: Actor?
    var incidentId: String?
    var aiNote: String?          // AI 初步判讀
}

struct AlertRule: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let expression: String
    let severity: Severity
    var enabled: Bool
    let notify: [String]         // 通知管道
    let autoRemediation: String? // Automation.id
}

enum LogLevel: String, CaseIterable, Identifiable, Encodable {
    case debug, info, warn, error
    var id: String { rawValue }
    var color: Color {
        switch self {
        case .debug: return .gray
        case .info: return .blue
        case .warn: return .orange
        case .error: return .red
        }
    }
}

struct LogEntry: Identifiable, Hashable, Encodable {
    let id: UUID
    let date: Date
    let level: LogLevel
    let service: String
    let host: String
    let message: String
    let traceId: String?
}

/// 監控儀表板的一張圖
struct Dashboard: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    var panels: [MetricSeries]
}

// MARK: - 事件

enum IncidentStatus: String, CaseIterable, Identifiable, Encodable {
    case investigating, identified, monitoring, resolved
    var id: String { rawValue }
    var label: String {
        switch self {
        case .investigating: return "調查中"
        case .identified: return "已找到原因"
        case .monitoring: return "觀察中"
        case .resolved: return "已解決"
        }
    }
    var color: Color {
        switch self {
        case .investigating: return .red
        case .identified: return .orange
        case .monitoring: return .blue
        case .resolved: return .green
        }
    }
}

struct IncidentEvent: Identifiable, Hashable, Encodable {
    let id: UUID
    let date: Date
    let actor: Actor
    let kind: Kind
    let text: String
    enum Kind: String, Encodable { case status, note, action, alert, deploy, aiFinding }
}

struct Incident: Identifiable, Hashable, Encodable {
    let id: String               // INC-2041
    let title: String
    let sev: Int                 // 1...4
    var status: IncidentStatus
    let services: [String]
    let commander: String        // Person.id
    let startedAt: Date
    var resolvedAt: Date?
    var impact: String
    var timeline: [IncidentEvent]
    var aiSummary: String?
    var followUps: [String]      // WorkItem.id
}

struct OnCallShift: Identifiable, Hashable, Encodable {
    let id: String
    let person: String           // Person.id
    let role: String             // Primary、Secondary
    let start: Date
    let end: Date
}

// MARK: - AI 自動化

/// AI 可自行執行到什麼程度。
enum Autonomy: String, CaseIterable, Identifiable, Encodable {
    case suggest, approve, auto
    var id: String { rawValue }
    var label: String {
        switch self {
        case .suggest: return "只建議"
        case .approve: return "需核准"
        case .auto: return "自動執行"
        }
    }
    var detail: String {
        switch self {
        case .suggest: return "AI 只產生分析與建議,不做任何變更"
        case .approve: return "AI 準備好動作,由人核准後才執行"
        case .auto: return "AI 直接執行,事後通知並可回滾"
        }
    }
    var color: Color {
        switch self {
        case .suggest: return .gray
        case .approve: return .purple
        case .auto: return .green
        }
    }
}

enum RiskLevel: String, CaseIterable, Identifiable, Encodable {
    case low, medium, high
    var id: String { rawValue }
    var label: String {
        switch self {
        case .low: return "低風險"
        case .medium: return "中風險"
        case .high: return "高風險"
        }
    }
    var color: Color {
        switch self {
        case .low: return .green
        case .medium: return .orange
        case .high: return .red
        }
    }
}

struct Automation: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let summary: String
    let icon: String             // SF Symbol
    let trigger: String          // 例如 "CI 失敗時"、"告警 HighLatency 觸發時"、"每天 03:00"
    var autonomy: Autonomy
    var enabled: Bool
    let scope: [String]          // 可操作的範圍
    let allowedTools: [String]   // kubectl、gh、terraform plan…
    var runs30d: Int
    var successRate: Double      // 0...1
    var timeSavedHours: Double
    var lastRun: Date?
}

enum AIStepStatus: String, Encodable { case done, running, pending, failed, skipped }

struct AIStep: Identifiable, Hashable, Encodable {
    let id: UUID
    let title: String
    let tool: String?            // 使用的工具或指令
    let output: String?
    var status: AIStepStatus
}

struct AutomationRun: Identifiable, Hashable, Encodable {
    let id: String
    let automation: String       // Automation.id
    let target: String           // 作用對象,例如 PR #212、api-server
    var status: RunStatus
    let startedAt: Date
    var finishedAt: Date?
    var steps: [AIStep]
    var summary: String
    var tokens: Int
    var costUSD: Double
}

enum ApprovalState: String, Encodable { case pending, approved, rejected, expired }

struct ApprovalRequest: Identifiable, Hashable, Encodable {
    let id: String
    let title: String
    let automation: String       // Automation.id
    let risk: RiskLevel
    let action: String           // 要執行的指令或變更
    let rationale: String        // AI 的理由
    let evidence: [String]       // 依據
    let rollbackPlan: String
    let createdAt: Date
    let expiresAt: Date
    var state: ApprovalState
    var decidedBy: String?       // Person.id
}

/// 總覽頁的動態牆
struct ActivityItem: Identifiable, Hashable, Encodable {
    let id: UUID
    let date: Date
    let actor: Actor
    let icon: String
    let text: String
    let section: AppSection
    /// 屬於哪個組織;預設組織不帶(nil 時 JSON 不輸出這個欄位,和 Go 版一樣)
    var org: String? = nil
}

// MARK: - 設定

struct Integration: Identifiable, Hashable, Encodable {
    let id: String
    let name: String
    let category: String         // 原始碼、雲端、通知、監控…
    let icon: String
    var connected: Bool
    let detail: String
}

// MARK: - 導覽

enum AppSection: String, CaseIterable, Identifiable, Hashable, Encodable {
    // channels 放最後:⌘1…⌘9 的快速鍵維持原本的頁面
    case overview, development, pipelines, infrastructure, monitoring, incidents, automation, cabinet, settings, channels
    var id: String { rawValue }
    var title: String {
        switch self {
        case .overview: return "總覽"
        case .development: return "開發進度"
        case .pipelines: return "CI/CD"
        case .infrastructure: return "伺服器與服務"
        case .monitoring: return "監控與告警"
        case .incidents: return "事件"
        case .automation: return "AI 自動化"
        case .cabinet: return "文件櫃"
        case .settings: return "設定"
        case .channels: return "對話"
        }
    }
    var icon: String {
        switch self {
        case .overview: return "square.grid.2x2"
        case .development: return "hammer"
        case .pipelines: return "arrow.triangle.branch"
        case .infrastructure: return "server.rack"
        case .monitoring: return "waveform.path.ecg"
        case .incidents: return "flame"
        case .automation: return "sparkles"
        case .cabinet: return "archivebox"
        case .settings: return "gearshape"
        case .channels: return "bubble.left.and.bubble.right"
        }
    }
}

// MARK: - Color(hex:)

extension Color {
    init(hex: UInt32) {
        self.init(.sRGB,
                  red: Double((hex >> 16) & 0xFF) / 255,
                  green: Double((hex >> 8) & 0xFF) / 255,
                  blue: Double(hex & 0xFF) / 255)
    }
}
