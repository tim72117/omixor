import AppKit
import SwiftUI

// 設定:組織、整合、程式碼倉庫、連線、LINE、團隊、AI 治理、一般。
// 其他頁用 app.go(.settings, focus: <分頁 rawValue>) 跳到指定分頁,例如開發進度的「前往 設定 › 程式碼倉庫」帶 "repos"。
// 不在 AppState 裡的偏好設定存在 UserDefaults(@AppStorage),鍵名都以 omixor.settings. 開頭
// (改名前是 ops.settings.,啟動時由 SettingsKeys.migrateLegacy 搬到新鍵)。

enum SettingsPane: String, CaseIterable, Identifiable {
    case orgs, features, integrations, repos, connections, line, team, ai, general
    var id: String { rawValue }
    var title: String {
        switch self {
        case .orgs: return "組織"
        case .features: return "功能"
        case .integrations: return "整合"
        case .repos: return "程式碼倉庫"
        case .connections: return "連線"
        case .line: return "LINE"
        case .team: return "團隊"
        case .ai: return "AI 治理"
        case .general: return "一般"
        }
    }
    var icon: String {
        switch self {
        case .orgs: return "building.2"
        case .features: return "switch.2"
        case .integrations: return "puzzlepiece.extension"
        case .repos: return "chevron.left.forwardslash.chevron.right"
        case .connections: return "key.horizontal"
        case .line: return "message"
        case .team: return "person.2"
        case .ai: return "sparkles"
        case .general: return "gearshape"
        }
    }
    /// 屬於哪個選用模組(server 沒編進該模組時不顯示,同 web 的 SettingsView 的 PANE_MODULE;nil = 核心)
    var module: String? {
        switch self {
        case .repos: return ServerModule.integrations
        default: return nil
        }
    }
}

enum SettingsKeys {
    static let pane = "omixor.settings.pane"
    static let theme = "omixor.settings.theme"
    static let refresh = "omixor.settings.refreshSeconds"
    static let pauseInBackground = "omixor.settings.pauseInBackground"
    static let timezone = "omixor.settings.timezone"
    static let use24h = "omixor.settings.use24h"
    // ai.*:AI 治理頁以前的偏好設定,畫面已拿掉;鍵名留著給改名搬移與「還原預設」清除
    static let aiEnabled = "omixor.settings.ai.enabled"
    static let aiBudget = "omixor.settings.ai.budgetUSD"
    static let aiBudgetAlert = "omixor.settings.ai.budgetAlert"
    static let aiOverBudget = "omixor.settings.ai.overBudget"
    static let aiTwoPerson = "omixor.settings.ai.twoPersonHighRisk"
    static let aiRequireRollback = "omixor.settings.ai.requireRollback"
    static let aiApprovalTimeout = "omixor.settings.ai.approvalTimeout"
    static func aiCeiling(_ env: DeployEnv) -> String { "omixor.settings.ai.ceiling.\(env.rawValue)" }

    static let prefix = "omixor.settings."
    /// 改名前的鍵名前綴
    static let legacyPrefix = "ops.settings."

    /// 啟動時把改名前的 ops.settings.* 搬到 omixor.settings.*:新鍵不存在才搬,搬完刪掉舊鍵
    /// (不刪的話「還原預設」清掉新鍵後,下次啟動又會把舊值搬回來)。回傳搬了幾個
    @discardableResult
    static func migrateLegacy(_ defaults: UserDefaults = .standard) -> Int {
        var moved = 0
        for (key, value) in defaults.dictionaryRepresentation() where key.hasPrefix(legacyPrefix) {
            let newKey = prefix + key.dropFirst(legacyPrefix.count)
            if defaults.object(forKey: newKey) == nil {
                defaults.set(value, forKey: newKey)
                moved += 1
            }
            defaults.removeObject(forKey: key)
        }
        if moved > 0 { NSLog("設定:%d 個 %@* 搬到 %@*", moved, legacyPrefix, prefix) }
        return moved
    }

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
                    ForEach(shownPanes) { p in
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
                case .features: SettingsFeaturesPane()
                case .integrations: SettingsIntegrationsPane()
                case .repos:
                    if app.hasModule(ServerModule.integrations) { SettingsReposPane() } else { ModuleMissingView(text: IntegrationModuleMissing.text) }
                case .connections: SettingsConnectionsPane()
                case .line: SettingsLinePane()
                case .team: SettingsTeamPane()
                case .ai: SettingsAIGovernancePane()
                case .general: SettingsGeneralPane()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear { applyFocus(app.focusId) }
        .onChange(of: app.focusId) { _, f in applyFocus(f) }
    }

    /// 要列出的分頁:server 沒編進的選用模組的分頁不列(目前所在的分頁照樣列,免得選單沒有選到的項目)
    private var shownPanes: [SettingsPane] {
        SettingsPane.allCases.filter { p in p == pane || p.module.map(app.hasModule) ?? true }
    }

    /// 其他頁跳過來時:focusId 是分頁的 rawValue(例如 "repos"、"features")就打開那個分頁
    private func applyFocus(_ focus: String?) {
        guard let focus, let p = SettingsPane(rawValue: focus) else { return }
        pane = p
        app.focusId = nil
    }
}

/// 這台伺服器沒有編進某個選用模組時的說明(不是錯誤)
struct ModuleMissingView: View {
    let text: String
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: "puzzlepiece.extension").font(.largeTitle).foregroundStyle(.secondary)
            Text(text).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
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
                if app.isFeatureEnabled("assistant") {
                    Toggle("顯示 AI 助理面板", isOn: $app.showAssistant)
                }
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
            Text("主題、更新頻率等設定會回到預設值。")
        }
    }

    private var settingsAppVersion: String {
        let v = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
        return v ?? "開發版"
    }
}
