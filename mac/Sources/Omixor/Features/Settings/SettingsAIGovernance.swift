import AppKit
import SwiftUI
import UniformTypeIdentifiers

// 設定 › AI 治理:等待核准的入口與稽核紀錄。
// 總開關、自主上限、預算、工具與指令限制、核准規則還沒有後端實作,不放只存在畫面上的開關(做了才加回來)。

struct SettingsAIGovernancePane: View {
    @EnvironmentObject var app: AppState

    @State private var auditFilter = "all"

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
            approvalSection
            auditSection
        }
        .formStyle(.grouped)
    }

    // MARK: 區塊

    private var approvalSection: some View {
        Section {
            LabeledContent("等待核准") {
                Button("\(app.pendingApprovals.count) 項,前往處理") { app.go(.automation) }
                    .buttonStyle(.link)
                    .disabled(app.pendingApprovals.isEmpty)
            }
        } header: {
            Label("核准", systemImage: "checkmark.seal")
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

    private func exportAudit(_ items: [ActivityItem]) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.commaSeparatedText]
        panel.nameFieldStringValue = "omixor-ai-audit-\(Date().formatted(.iso8601.year().month().day())).csv"
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
