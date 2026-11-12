import AppKit
import SwiftUI

// 設定:組織、整合、連線、LINE、團隊與值班、AI 治理、通知、一般。
// 不在 AppState 裡的偏好設定存在 UserDefaults(@AppStorage),鍵名都以 ops.settings. 開頭。

enum SettingsPane: String, CaseIterable, Identifiable {
    case orgs, integrations, connections, line, team, ai, notifications, general
    var id: String { rawValue }
    var title: String {
        switch self {
        case .orgs: return "組織"
        case .integrations: return "整合"
        case .connections: return "連線"
        case .line: return "LINE"
        case .team: return "團隊與值班"
        case .ai: return "AI 治理"
        case .notifications: return "通知"
        case .general: return "一般"
        }
    }
    var icon: String {
        switch self {
        case .orgs: return "building.2"
        case .integrations: return "puzzlepiece.extension"
        case .connections: return "key.horizontal"
        case .line: return "message"
        case .team: return "person.2"
        case .ai: return "sparkles"
        case .notifications: return "bell.badge"
        case .general: return "gearshape"
        }
    }
}

enum SettingsKeys {
    static let pane = "ops.settings.pane"
    static let theme = "ops.settings.theme"
    static let refresh = "ops.settings.refreshSeconds"
    static let pauseInBackground = "ops.settings.pauseInBackground"
    static let timezone = "ops.settings.timezone"
    static let use24h = "ops.settings.use24h"
    static let aiEnabled = "ops.settings.ai.enabled"
    static let aiBudget = "ops.settings.ai.budgetUSD"
    static let aiBudgetAlert = "ops.settings.ai.budgetAlert"
    static let aiOverBudget = "ops.settings.ai.overBudget"
    static let aiTwoPerson = "ops.settings.ai.twoPersonHighRisk"
    static let aiRequireRollback = "ops.settings.ai.requireRollback"
    static let aiApprovalTimeout = "ops.settings.ai.approvalTimeout"
    static func aiCeiling(_ env: DeployEnv) -> String { "ops.settings.ai.ceiling.\(env.rawValue)" }

    /// 「還原預設」時要清掉的鍵
    static var all: [String] {
        [pane, theme, refresh, pauseInBackground, timezone, use24h, aiEnabled, aiBudget, aiBudgetAlert,
         aiOverBudget, aiTwoPerson, aiRequireRollback, aiApprovalTimeout] + DeployEnv.allCases.map(aiCeiling)
    }
}

struct SettingsView: View {
    @EnvironmentObject var app: AppState
    @AppStorage(SettingsKeys.pane) private var pane: SettingsPane = .integrations

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Picker("設定分類", selection: $pane) {
                    ForEach(SettingsPane.allCases) { p in
                        Label(p.title, systemImage: p.icon).tag(p)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 860)
            }
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
            Divider()
            Group {
                switch pane {
                case .orgs: SettingsOrgsPane()
                case .integrations: SettingsIntegrationsPane()
                case .connections: SettingsConnectionsPane()
                case .line: SettingsLinePane()
                case .team: SettingsTeamPane()
                case .ai: SettingsAIGovernancePane()
                case .notifications: SettingsNotificationsPane()
                case .general: SettingsGeneralPane()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

// MARK: - 一般

enum SettingsTheme: String, CaseIterable, Identifiable {
    case system, light, dark
    var id: String { rawValue }
    var label: String {
        switch self {
        case .system: return "跟隨系統"
        case .light: return "淺色"
        case .dark: return "深色"
        }
    }
    var appearance: NSAppearance? {
        switch self {
        case .system: return nil
        case .light: return NSAppearance(named: .aqua)
        case .dark: return NSAppearance(named: .darkAqua)
        }
    }
}

struct SettingsGeneralPane: View {
    @EnvironmentObject var app: AppState
    @AppStorage(SettingsKeys.theme) private var theme: SettingsTheme = .system
    @AppStorage(SettingsKeys.refresh) private var refreshSeconds = 15
    @AppStorage(SettingsKeys.pauseInBackground) private var pauseInBackground = true
    @AppStorage(SettingsKeys.timezone) private var timezone = "Asia/Taipei"
    @AppStorage(SettingsKeys.use24h) private var use24h = true
    @State private var confirmReset = false

    private let refreshOptions: [(Int, String)] = [(5, "每 5 秒"), (15, "每 15 秒"), (30, "每 30 秒"), (60, "每分鐘"), (0, "手動")]
    private let timezones: [(String, String)] = [("Asia/Taipei", "台北(UTC+8)"), ("UTC", "UTC"),
                                                 ("Asia/Tokyo", "東京(UTC+9)"), ("America/Los_Angeles", "洛杉磯")]

    var body: some View {
        Form {
            Section("外觀") {
                Picker("主題", selection: $theme) {
                    ForEach(SettingsTheme.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                Toggle("顯示 AI 助理面板", isOn: $app.showAssistant)
            }

            Section {
                Picker("自動更新頻率", selection: $refreshSeconds) {
                    ForEach(refreshOptions, id: \.0) { Text($0.1).tag($0.0) }
                }
                Toggle("視窗不在前景時暫停更新", isOn: $pauseInBackground)
            } header: {
                Text("資料更新")
            } footer: {
                Text("更新越頻繁,圖表越即時,但會向監控服務送出較多查詢。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("預設檢視") {
                Picker("環境", selection: $app.env) {
                    ForEach(DeployEnv.allCases) { Text($0.label).tag($0) }
                }
                Picker("時間範圍", selection: $app.timeRange) {
                    ForEach(TimeRange.allCases) { Text($0.label).tag($0) }
                }
            }

            Section("地區與格式") {
                Picker("時區", selection: $timezone) {
                    ForEach(timezones, id: \.0) { Text($0.1).tag($0.0) }
                }
                Toggle("使用 24 小時制", isOn: $use24h)
                LabeledContent("語言", value: "繁體中文(台灣)")
            }

            Section("帳號") {
                LabeledContent("登入身分") {
                    HStack(spacing: 6) {
                        ActorAvatar(actor: .person(app.me), size: 18)
                        Text(app.person(app.me)?.name ?? app.me)
                    }
                }
                LabeledContent("角色", value: app.person(app.me)?.role ?? "—")
                LabeledContent("資料來源", value: "模擬資料(尚未連接正式 API)")
                LabeledContent("版本", value: settingsAppVersion)
            }

            Section {
                Button("還原所有本機設定…", role: .destructive) { confirmReset = true }
            } footer: {
                Text("只會清除這台 Mac 上的偏好設定,不影響整合、團隊與自動化設定。")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .onAppear { NSApp.appearance = theme.appearance }
        .onChange(of: theme) { _, t in NSApp.appearance = t.appearance }
        .confirmationDialog("還原所有本機設定?", isPresented: $confirmReset) {
            Button("還原", role: .destructive) {
                SettingsKeys.all.forEach { UserDefaults.standard.removeObject(forKey: $0) }
                NSApp.appearance = nil
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text("主題、更新頻率、AI 治理上限與預算等設定會回到預設值。")
        }
    }

    private var settingsAppVersion: String {
        let v = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
        return v ?? "開發版"
    }
}
