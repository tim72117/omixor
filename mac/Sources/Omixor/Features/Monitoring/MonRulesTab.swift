import SwiftUI

/// 規則分頁:告警規則表(名稱、運算式、嚴重度、通知管道、自動修復、啟用開關)。
struct MonRulesTab: View {
    @EnvironmentObject var app: AppState
    @Binding var highlightedId: String?

    @State private var query = ""
    @State private var severity: Severity?
    @State private var showDisabled = true
    @State private var sortOrder = [KeyPathComparator(\MonRuleRow.severityRank)]

    private var rows: [MonRuleRow] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return app.alertRules
            .filter { r in
                if let severity, r.severity != severity { return false }
                if !showDisabled && !r.enabled { return false }
                guard !q.isEmpty else { return true }
                return [r.name, r.expression, r.notify.joined(separator: " ")].contains { $0.lowercased().contains(q) }
            }
            .map { r in
                let firing = app.alerts.filter { a in
                    a.state != .resolved && (a.rule == r.expression || a.rule == r.name || a.title.contains(r.name))
                }.count
                return MonRuleRow(rule: r, firing: firing, automationName: r.autoRemediation.map { app.automation($0)?.name ?? $0 })
            }
            .sorted(using: sortOrder)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            toolbar
            if app.alertRules.isEmpty {
                EmptyStateView(title: "沒有告警規則", icon: "list.bullet.rectangle", message: "尚未設定任何告警規則。")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                table
            }
        }
        .padding(20)
    }

    private var toolbar: some View {
        let enabled = app.alertRules.filter(\.enabled).count
        return HStack(spacing: 10) {
            Text("\(app.alertRules.count) 條規則 · \(enabled) 條啟用中")
                .font(.callout).foregroundStyle(.secondary)
            Spacer()
            Picker("嚴重度", selection: $severity) {
                Text("全部嚴重度").tag(Severity?.none)
                ForEach(Severity.allCases) { Text($0.label).tag(Severity?.some($0)) }
            }
            .fixedSize()
            Toggle("顯示停用", isOn: $showDisabled).toggleStyle(.checkbox)
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("搜尋規則", text: $query).textFieldStyle(.plain)
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 7))
            .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.primary.opacity(0.1)))
            .frame(maxWidth: 220)
        }
    }

    private var table: some View {
        Table(rows, selection: $highlightedId, sortOrder: $sortOrder) {
            TableColumn("啟用") { (row: MonRuleRow) in
                Toggle("", isOn: enabledBinding(row.id))
                    .toggleStyle(.switch)
                    .controlSize(.mini)
                    .labelsHidden()
            }
            .width(44)

            TableColumn("名稱", value: \.rule.name) { (row: MonRuleRow) in
                HStack(spacing: 6) {
                    Text(row.rule.name).fontWeight(.medium).lineLimit(1)
                    if row.firing > 0 {
                        Pill(text: "\(row.firing) 觸發", color: .red, icon: "bell.fill")
                    }
                }
                .opacity(row.rule.enabled ? 1 : 0.5)
            }
            .width(min: 140, ideal: 200)

            TableColumn("運算式", value: \.rule.expression) { (row: MonRuleRow) in
                Text(row.rule.expression)
                    .font(.system(.callout, design: .monospaced))
                    .foregroundStyle(row.rule.enabled ? .primary : .secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .help(row.rule.expression)
                    .textSelection(.enabled)
            }
            .width(min: 180, ideal: 300)

            TableColumn("嚴重度", value: \.severityRank) { (row: MonRuleRow) in
                Pill(text: row.rule.severity.label, color: row.rule.severity.color)
            }
            .width(min: 70, ideal: 80, max: 100)

            TableColumn("通知") { (row: MonRuleRow) in
                HStack(spacing: 4) {
                    ForEach(row.rule.notify, id: \.self) { ch in
                        Label(ch, systemImage: MonRuleRow.icon(forChannel: ch))
                            .font(.caption)
                            .lineLimit(1)
                            .padding(.horizontal, 5).padding(.vertical, 2)
                            .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 4))
                    }
                    if row.rule.notify.isEmpty { Text("—").foregroundStyle(.tertiary) }
                }
                .help(row.rule.notify.joined(separator: "、"))
            }
            .width(min: 120, ideal: 200)

            TableColumn("自動修復") { (row: MonRuleRow) in
                if let id = row.rule.autoRemediation, let name = row.automationName {
                    Button { app.go(.automation, focus: id) } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "sparkles").foregroundStyle(.purple)
                            Text(name).lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                    .help("前往 AI 自動化:\(name)")
                } else {
                    Text("無").foregroundStyle(.tertiary)
                }
            }
            .width(min: 110, ideal: 170)
        }
        .tableStyle(.inset(alternatesRowBackgrounds: true))
        .contextMenu(forSelectionType: String.self) { ids in
            if let id = ids.first, let r = app.alertRules.first(where: { $0.id == id }) {
                Button(r.enabled ? "停用規則" : "啟用規則") { setEnabled(id, !r.enabled) }
                Button("複製運算式") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(r.expression, forType: .string)
                }
                if let a = r.autoRemediation {
                    Button("前往自動修復") { app.go(.automation, focus: a) }
                }
            }
        }
    }

    private func enabledBinding(_ id: String) -> Binding<Bool> {
        Binding(
            get: { app.alertRules.first { $0.id == id }?.enabled ?? false },
            set: { setEnabled(id, $0) }
        )
    }

    private func setEnabled(_ id: String, _ on: Bool) {
        guard let i = app.alertRules.firstIndex(where: { $0.id == id }), app.alertRules[i].enabled != on else { return }
        app.alertRules[i].enabled = on
        app.log(.person(app.me), on ? "bell" : "bell.slash", "\(on ? "啟用" : "停用")告警規則「\(app.alertRules[i].name)」", .monitoring)
    }
}

struct MonRuleRow: Identifiable {
    let rule: AlertRule
    let firing: Int
    let automationName: String?
    var id: String { rule.id }
    var severityRank: Int { Severity.allCases.firstIndex(of: rule.severity) ?? 0 }

    static func icon(forChannel ch: String) -> String {
        let c = ch.lowercased()
        if c.contains("slack") { return "number" }
        if c.contains("pagerduty") || c.contains("電話") || c.contains("phone") { return "phone.fill" }
        if c.contains("mail") || c.contains("信") { return "envelope.fill" }
        if c.contains("line") || c.contains("簡訊") || c.contains("sms") { return "message.fill" }
        if c.contains("webhook") { return "link" }
        return "bell.fill"
    }
}
