import SwiftUI

// 設定 › 通知:依嚴重度選擇通知管道、事件與 AI 通知、勿擾時段。

enum SettingsChannel: String, CaseIterable, Identifiable {
    case slack, email, pagerduty, sms, desktop
    var id: String { rawValue }
    var label: String {
        switch self {
        case .slack: return "Slack"
        case .email: return "Email"
        case .pagerduty: return "PagerDuty"
        case .sms: return "簡訊"
        case .desktop: return "macOS 通知"
        }
    }
    var icon: String {
        switch self {
        case .slack: return "number"
        case .email: return "envelope"
        case .pagerduty: return "phone.arrow.up.right"
        case .sms: return "message"
        case .desktop: return "bell"
        }
    }
    /// 用來比對整合名稱的關鍵字(macOS 通知與簡訊不需要整合)
    var integrationKeyword: String? {
        switch self {
        case .slack: return "slack"
        case .pagerduty: return "pagerduty"
        case .email, .sms, .desktop: return nil
        }
    }
}

struct SettingsNotificationsPane: View {
    @EnvironmentObject var app: AppState

    @State private var matrix: [Severity: Set<SettingsChannel>] = [
        .critical: [.slack, .pagerduty, .sms, .desktop],
        .warning: [.slack, .desktop],
        .info: [.email],
    ]
    @State private var slackChannel = "#ops-alerts"
    @State private var notifyNewIncident = true
    @State private var notifyIncidentStatus = true
    @State private var postmortemReminder = true
    @State private var aiApprovalNotify = true
    @State private var aiDigest = "daily"
    @State private var aiAutoActionNotify = true
    @State private var quietEnabled = false
    @State private var quietStart = Calendar.current.date(bySettingHour: 22, minute: 0, second: 0, of: .now) ?? .now
    @State private var quietEnd = Calendar.current.date(bySettingHour: 8, minute: 0, second: 0, of: .now) ?? .now
    @State private var criticalBypass = true
    @State private var lastTest: Date?

    /// 找出對應的整合;找不到就視為不需要整合
    private func integration(for ch: SettingsChannel) -> Integration? {
        guard let key = ch.integrationKeyword else { return nil }
        return app.integrations.first { $0.name.lowercased().contains(key) }
    }

    private func binding(_ sev: Severity, _ ch: SettingsChannel) -> Binding<Bool> {
        Binding(
            get: { matrix[sev, default: []].contains(ch) },
            set: { on in
                if on { matrix[sev, default: []].insert(ch) } else { matrix[sev, default: []].remove(ch) }
            }
        )
    }

    var body: some View {
        Form {
            Section {
                Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 10) {
                    GridRow {
                        Text("嚴重度").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                        ForEach(SettingsChannel.allCases) { ch in
                            VStack(spacing: 2) {
                                Image(systemName: ch.icon)
                                Text(ch.label).lineLimit(1)
                            }
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.secondary)
                            .gridColumnAlignment(.center)
                        }
                    }
                    Divider().gridCellUnsizedAxes(.horizontal)
                    ForEach(Severity.allCases) { sev in
                        GridRow {
                            HStack(spacing: 6) {
                                Circle().fill(sev.color).frame(width: 8, height: 8)
                                Text(sev.label)
                            }
                            ForEach(SettingsChannel.allCases) { ch in
                                Toggle(ch.label, isOn: binding(sev, ch))
                                    .toggleStyle(.checkbox)
                                    .labelsHidden()
                                    .disabled(integration(for: ch)?.connected == false)
                            }
                        }
                    }
                }
                .padding(.vertical, 4)

                let missing = SettingsChannel.allCases.compactMap { ch -> Integration? in
                    guard let i = integration(for: ch), !i.connected else { return nil }
                    return i
                }
                if !missing.isEmpty {
                    HStack(spacing: 6) {
                        Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        Text("\(missing.map(\.name).joined(separator: "、")) 尚未連線,無法透過它通知。")
                            .font(.caption)
                        Spacer()
                        Button("前往整合") {
                            UserDefaults.standard.set(SettingsPane.integrations.rawValue, forKey: SettingsKeys.pane)
                        }
                        .buttonStyle(.link)
                        .font(.caption)
                    }
                }
                TextField("Slack 頻道", text: $slackChannel)
                HStack {
                    if let lastTest {
                        Label("已在 \(Fmt.time(lastTest)) 送出測試通知", systemImage: "checkmark.circle.fill")
                            .font(.caption)
                            .foregroundStyle(.green)
                    }
                    Spacer()
                    Button("傳送測試通知") {
                        let chans = matrix[.critical, default: []].map(\.label).sorted().joined(separator: "、")
                        app.log(.person(app.me), "bell.and.waves.left.and.right",
                                "傳送測試通知到 \(chans.isEmpty ? "(未選擇管道)" : chans)", .settings)
                        lastTest = .now
                    }
                }
            } header: {
                Label("告警通知管道", systemImage: "bell.badge")
            } footer: {
                Text("每個嚴重度可以同時送到多個管道。Critical 建議至少開啟 PagerDuty 或簡訊,確保值班人員會被叫醒。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                Toggle("建立新事件時通知全體維運成員", isOn: $notifyNewIncident)
                Toggle("事件狀態變更時通知訂閱者", isOn: $notifyIncidentStatus)
                Toggle("事件結案 48 小時後提醒撰寫檢討報告", isOn: $postmortemReminder)
            } header: {
                Label("事件", systemImage: "flame")
            }

            Section {
                Toggle("AI 請求核准時通知我", isOn: $aiApprovalNotify)
                Toggle("AI 自動執行變更後立即通知", isOn: $aiAutoActionNotify)
                Picker("AI 工作摘要", selection: $aiDigest) {
                    Text("每天 09:00").tag("daily")
                    Text("每週一 09:00").tag("weekly")
                    Text("不寄送").tag("off")
                }
            } header: {
                Label("AI 自動化", systemImage: "sparkles")
            }

            Section {
                Toggle("啟用勿擾時段", isOn: $quietEnabled)
                DatePicker("開始", selection: $quietStart, displayedComponents: .hourAndMinute)
                    .disabled(!quietEnabled)
                DatePicker("結束", selection: $quietEnd, displayedComponents: .hourAndMinute)
                    .disabled(!quietEnabled)
                Toggle("Critical 告警與值班呼叫不受勿擾限制", isOn: $criticalBypass)
                    .disabled(!quietEnabled)
            } header: {
                Label("勿擾時段", systemImage: "moon")
            } footer: {
                Text("勿擾時段內的 Warning 與 Info 通知會累積,到結束時一次送出。")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
    }
}
