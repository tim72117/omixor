import AppKit
import SwiftUI
import UniformTypeIdentifiers

// 設定 › AI 治理:總開關、各環境自主上限、預算、允許的工具、核准規則與稽核紀錄。

private extension Autonomy {
    var settingsRank: Int {
        switch self {
        case .suggest: return 0
        case .approve: return 1
        case .auto: return 2
        }
    }
}

struct SettingsAIGovernancePane: View {
    @EnvironmentObject var app: AppState

    @AppStorage(SettingsKeys.aiEnabled) private var aiEnabled = true
    @AppStorage(SettingsKeys.aiCeiling(.production)) private var ceilingProd: Autonomy = .approve
    @AppStorage(SettingsKeys.aiCeiling(.staging)) private var ceilingStaging: Autonomy = .auto
    @AppStorage(SettingsKeys.aiCeiling(.development)) private var ceilingDev: Autonomy = .auto
    @AppStorage(SettingsKeys.aiBudget) private var budgetUSD = 500.0
    @AppStorage(SettingsKeys.aiBudgetAlert) private var budgetAlert = true
    @AppStorage(SettingsKeys.aiOverBudget) private var overBudgetAction = "pause"
    @AppStorage(SettingsKeys.aiTwoPerson) private var twoPersonHighRisk = true
    @AppStorage(SettingsKeys.aiRequireRollback) private var requireRollback = true
    @AppStorage(SettingsKeys.aiApprovalTimeout) private var approvalTimeout = 60

    @State private var disabledTools: Set<String> = []
    @State private var blockedCommands = ["kubectl delete namespace", "terraform destroy", "DROP TABLE", "rm -rf /", "git push --force main"]
    @State private var newBlocked = ""
    @State private var auditFilter = "all"

    private func ceiling(_ env: DeployEnv) -> Binding<Autonomy> {
        switch env {
        case .production: return $ceilingProd
        case .staging: return $ceilingStaging
        case .development: return $ceilingDev
        }
    }

    private var strictestCeiling: Autonomy {
        [ceilingProd, ceilingStaging, ceilingDev].min { $0.settingsRank < $1.settingsRank } ?? .suggest
    }

    /// 自主程度超過 Production 上限的自動化(Production 上限最嚴格,用它檢查)
    private var overCeiling: [Automation] {
        app.automations.filter { $0.enabled && $0.autonomy.settingsRank > ceilingProd.settingsRank }
    }

    private var spend30d: Double {
        let since = Date().addingTimeInterval(-30 * 86400)
        return app.automationRuns.filter { $0.startedAt >= since }.map(\.costUSD).reduce(0, +)
    }

    private var tools: [(name: String, users: Int)] {
        var counts: [String: Int] = [:]
        for a in app.automations { for t in a.allowedTools { counts[t, default: 0] += 1 } }
        return counts.map { (name: $0.key, users: $0.value) }.sorted { ($1.users, $0.name) < ($0.users, $1.name) }
    }

    private var auditItems: [ActivityItem] {
        app.activity.filter { item in
            let isAI: Bool = { if case .ai = item.actor { return true }; return false }()
            switch auditFilter {
            case "ai": return isAI
            case "human": return !isAI && item.section == .automation
            default: return isAI || item.section == .automation
            }
        }
    }

    var body: some View {
        Form {
            masterSection
            ceilingSection
            budgetSection
            toolsSection
            blockedSection
            approvalSection
            auditSection
        }
        .formStyle(.grouped)
    }

    // MARK: 區塊

    private var masterSection: some View {
        Section {
            Toggle(isOn: $aiEnabled) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("允許 AI 自動化執行變更").font(.body.weight(.medium))
                    Text(aiEnabled ? "AI 會依下方上限自動執行或請求核准。" : "已暫停:所有 AI 自動化只產生建議,不會做任何變更。")
                        .font(.caption)
                        .foregroundStyle(aiEnabled ? Color.secondary : Color.orange)
                }
            }
            .toggleStyle(.switch)
            .onChange(of: aiEnabled) { _, on in
                app.log(.person(app.me), on ? "play.circle" : "pause.circle",
                        on ? "恢復 AI 自動化執行" : "暫停所有 AI 自動化執行(緊急停止)", .automation)
            }
            LabeledContent("目前啟用的自動化") {
                Text("\(app.automations.filter(\.enabled).count) / \(app.automations.count)")
                    .monospacedDigit()
            }
        } header: {
            Label("總開關", systemImage: "power")
        }
    }

    private var ceilingSection: some View {
        Section {
            ForEach(DeployEnv.allCases) { env in
                Picker(selection: ceiling(env)) {
                    ForEach(Autonomy.allCases) { a in Text(a.label).tag(a) }
                } label: {
                    HStack(spacing: 8) {
                        EnvBadge(env: env)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(env.label)
                            Text(ceiling(env).wrappedValue.detail).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
                .disabled(!aiEnabled)
            }
            if !overCeiling.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    Label("\(overCeiling.count) 個自動化的自主程度高於 Production 上限「\(ceilingProd.label)」",
                          systemImage: "exclamationmark.triangle.fill")
                        .font(.callout.weight(.medium))
                        .foregroundStyle(.orange)
                    ForEach(overCeiling) { a in
                        HStack(spacing: 8) {
                            Image(systemName: a.icon).foregroundStyle(.purple).frame(width: 18)
                            Text(a.name)
                            Pill(text: a.autonomy.label, color: a.autonomy.color)
                            Spacer()
                            Button("降為「\(ceilingProd.label)」") {
                                app.setAutonomy(ceilingProd, automationId: a.id)
                            }
                            .controlSize(.small)
                        }
                    }
                }
                .padding(.vertical, 4)
            }
        } header: {
            Label("各環境的 AI 自主上限", systemImage: "slider.horizontal.3")
        } footer: {
            Text("個別自動化的設定不能超過所在環境的上限。目前最嚴格的上限是「\(strictestCeiling.label)」。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private var budgetSection: some View {
        let ratio = budgetUSD > 0 ? spend30d / budgetUSD : 0
        return Section {
            LabeledContent("每月預算上限") {
                HStack(spacing: 6) {
                    TextField("預算", value: $budgetUSD, format: .number.precision(.fractionLength(0)))
                        .textFieldStyle(.roundedBorder)
                        .multilineTextAlignment(.trailing)
                        .frame(width: 90)
                    Text("USD").foregroundStyle(.secondary)
                    Stepper("", value: $budgetUSD, in: 0...100_000, step: 50).labelsHidden()
                }
            }
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text("最近 30 天花費")
                    Spacer()
                    Text(String(format: "$%.2f / $%.0f", spend30d, budgetUSD))
                        .monospacedDigit()
                        .foregroundStyle(ratio > 1 ? .red : ratio > 0.8 ? .orange : .secondary)
                }
                UsageBar(value: ratio * 100)
                Text("\(app.automationRuns.count) 次執行 · \(Fmt.number(Double(app.automationRuns.map(\.tokens).reduce(0, +)))) tokens")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Toggle("用到 80% 時通知管理員", isOn: $budgetAlert)
            Picker("超過上限時", selection: $overBudgetAction) {
                Text("暫停非必要的自動化").tag("pause")
                Text("全部降為只建議").tag("suggest")
                Text("只通知,不限制").tag("notify")
            }
        } header: {
            Label("預算", systemImage: "dollarsign.circle")
        }
    }

    private var toolsSection: some View {
        Section {
            if tools.isEmpty {
                Text("目前沒有自動化宣告要使用的工具").foregroundStyle(.secondary)
            }
            ForEach(tools, id: \.name) { tool in
                Toggle(isOn: Binding(
                    get: { !disabledTools.contains(tool.name) },
                    set: { on in
                        if on { disabledTools.remove(tool.name) } else { disabledTools.insert(tool.name) }
                        app.log(.person(app.me), on ? "checkmark.shield" : "nosign",
                                "\(on ? "允許" : "禁止") AI 使用工具 \(tool.name)", .automation)
                    }
                )) {
                    HStack(spacing: 8) {
                        Text(tool.name).font(.body.monospaced())
                        Text("\(tool.users) 個自動化使用").font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        } header: {
            Label("允許的工具", systemImage: "wrench.and.screwdriver")
        } footer: {
            Text("關閉的工具會從所有自動化的可用工具中移除,正在使用它的步驟會改為請求核准。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private var blockedSection: some View {
        Section {
            ForEach(blockedCommands, id: \.self) { cmd in
                HStack {
                    Image(systemName: "nosign").foregroundStyle(.red)
                    Text(cmd).font(.body.monospaced())
                    Spacer()
                    Button {
                        blockedCommands.removeAll { $0 == cmd }
                    } label: {
                        Image(systemName: "minus.circle")
                    }
                    .buttonStyle(.borderless)
                    .help("移除")
                }
            }
            HStack {
                TextField("新增禁止的指令或關鍵字", text: $newBlocked)
                    .textFieldStyle(.roundedBorder)
                    .font(.body.monospaced())
                    .onSubmit(addBlocked)
                Button("新增", action: addBlocked)
                    .disabled(newBlocked.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        } header: {
            Label("禁止的指令", systemImage: "hand.raised")
        } footer: {
            Text("不論自主程度,AI 都不會執行符合這些關鍵字的指令。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private var approvalSection: some View {
        Section {
            Toggle("高風險動作需要兩人核准", isOn: $twoPersonHighRisk)
            Toggle("AI 提出的變更必須附回滾計畫", isOn: $requireRollback)
            Picker("核准請求逾時", selection: $approvalTimeout) {
                Text("15 分鐘").tag(15)
                Text("30 分鐘").tag(30)
                Text("1 小時").tag(60)
                Text("4 小時").tag(240)
            }
            LabeledContent("等待核准") {
                Button("\(app.pendingApprovals.count) 項,前往處理") { app.go(.automation) }
                    .buttonStyle(.link)
                    .disabled(app.pendingApprovals.isEmpty)
            }
        } header: {
            Label("核准規則", systemImage: "checkmark.seal")
        }
    }

    private var auditSection: some View {
        let items = auditItems
        return Section {
            HStack {
                Picker("顯示", selection: $auditFilter) {
                    Text("全部").tag("all")
                    Text("AI 執行").tag("ai")
                    Text("人員變更").tag("human")
                }
                .pickerStyle(.segmented)
                .frame(maxWidth: 280)
                Spacer()
                Button {
                    exportAudit(items)
                } label: {
                    Label("匯出 CSV", systemImage: "square.and.arrow.up")
                }
                .disabled(items.isEmpty)
            }
            if items.isEmpty {
                Text("沒有符合的稽核紀錄").foregroundStyle(.secondary)
            }
            ForEach(items.prefix(30)) { item in
                HStack(alignment: .top, spacing: 10) {
                    Text(item.date.formatted(.dateTime.month(.defaultDigits).day().hour(.twoDigits(amPM: .omitted)).minute(.twoDigits)))
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .frame(width: 84, alignment: .leading)
                    ActorAvatar(actor: item.actor, size: 18)
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 4) {
                            Text(app.name(of: item.actor)).font(.caption.weight(.medium))
                            if case .ai = item.actor { AIBadge() }
                        }
                        Text(item.text).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                }
            }
            if items.count > 30 {
                Text("只顯示最近 30 筆,完整紀錄請匯出 CSV。").font(.caption).foregroundStyle(.secondary)
            }
        } header: {
            Label("稽核紀錄", systemImage: "list.bullet.clipboard")
        }
    }

    // MARK: 動作

    private func addBlocked() {
        let v = newBlocked.trimmingCharacters(in: .whitespaces)
        guard !v.isEmpty, !blockedCommands.contains(v) else { return }
        blockedCommands.append(v)
        newBlocked = ""
        app.log(.person(app.me), "nosign", "新增 AI 禁止指令「\(v)」", .automation)
    }

    private func exportAudit(_ items: [ActivityItem]) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.commaSeparatedText]
        panel.nameFieldStringValue = "ops-ai-audit-\(Date().formatted(.iso8601.year().month().day())).csv"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        func esc(_ s: String) -> String { "\"" + s.replacingOccurrences(of: "\"", with: "\"\"") + "\"" }
        var lines = ["時間,執行者,類型,內容,區塊"]
        for i in items {
            let kind: String
            switch i.actor {
            case .ai: kind = "AI"
            case .person: kind = "人員"
            case .system: kind = "系統"
            }
            lines.append([i.date.ISO8601Format(), app.name(of: i.actor), kind, i.text, i.section.title].map(esc).joined(separator: ","))
        }
        try? lines.joined(separator: "\n").write(to: url, atomically: true, encoding: .utf8)
    }
}
