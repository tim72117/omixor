import SwiftUI

/// 告警分頁:依狀態分組(觸發中優先、依嚴重度排序),每列可確認/解除/靜音、建立事件、請 AI 分析。
struct MonAlertsTab: View {
    @EnvironmentObject var app: AppState
    @Binding var selectedId: String?

    @State private var severityFilter: Set<Severity> = Set(Severity.allCases)
    @State private var query = ""
    @State private var collapsed: Set<AlertState> = [.resolved]
    @State private var analyses: [String: String] = [:]
    @State private var analyzing: Set<String> = []

    private static let stateOrder: [AlertState] = [.firing, .acknowledged, .silenced, .resolved]

    private var filtered: [Alert] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return app.alerts.filter { a in
            guard severityFilter.contains(a.severity) else { return false }
            guard !q.isEmpty else { return true }
            let svc = app.service(a.service)?.name ?? a.service
            return [a.title, a.service, svc, a.rule, a.id, a.value].contains { $0.lowercased().contains(q) }
        }
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    summary
                    filterBar
                    if app.alerts.isEmpty {
                        EmptyStateView(title: "沒有告警", icon: "bell.slash",
                                       message: "所有規則都在正常範圍內。")
                            .frame(maxWidth: .infinity, minHeight: 260)
                    } else if filtered.isEmpty {
                        EmptyStateView(title: "沒有符合條件的告警", icon: "line.3.horizontal.decrease.circle",
                                       message: "調整嚴重度或搜尋條件試試看。")
                            .frame(maxWidth: .infinity, minHeight: 220)
                    } else {
                        ForEach(Self.stateOrder) { state in
                            let items = filtered.filter { $0.state == state }
                                .sorted { ($0.severity, $1.startedAt) < ($1.severity, $0.startedAt) }
                            if !items.isEmpty { group(state, items) }
                        }
                    }
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .onAppear { scroll(proxy) }
            .onChange(of: selectedId) { _, _ in scroll(proxy) }
        }
    }

    private func scroll(_ proxy: ScrollViewProxy) {
        guard let id = selectedId, let a = app.alerts.first(where: { $0.id == id }) else { return }
        collapsed.remove(a.state)
        severityFilter.insert(a.severity)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
            withAnimation(.easeInOut) { proxy.scrollTo(id, anchor: .center) }
        }
    }

    // MARK: 摘要

    private var summary: some View {
        let firing = app.alerts.filter { $0.state == .firing }
        return ViewThatFits(in: .horizontal) {
            HStack(spacing: 12) { summaryTiles(firing) }
            LazyVGrid(columns: [GridItem(.flexible(), spacing: 12), GridItem(.flexible(), spacing: 12)], spacing: 12) {
                summaryTiles(firing)
            }
        }
    }

    @ViewBuilder private func summaryTiles(_ firing: [Alert]) -> some View {
        ForEach(Severity.allCases) { s in
            MetricTile(title: "\(s.label) 觸發中", value: "\(firing.filter { $0.severity == s }.count)",
                       icon: s.monIcon, tint: s.color)
                .frame(minWidth: 150)
        }
        MetricTile(title: "已確認 / 已靜音",
                   value: "\(app.alerts.filter { $0.state == .acknowledged }.count) / \(app.alerts.filter { $0.state == .silenced }.count)",
                   icon: "eye", tint: .orange)
            .frame(minWidth: 150)
    }

    private var filterBar: some View {
        HStack(spacing: 8) {
            ForEach(Severity.allCases) { s in
                MonToggleChip(title: s.label, count: app.alerts.filter { $0.severity == s && $0.state != .resolved }.count,
                              color: s.color, isOn: severityFilter.contains(s)) {
                    if severityFilter.contains(s) { severityFilter.remove(s) } else { severityFilter.insert(s) }
                }
            }
            Spacer(minLength: 12)
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("搜尋告警、服務、規則", text: $query).textFieldStyle(.plain)
                if !query.isEmpty {
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary) }
                        .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 7))
            .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.primary.opacity(0.1)))
            .frame(maxWidth: 260)
        }
    }

    // MARK: 分組

    private func group(_ state: AlertState, _ items: [Alert]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                withAnimation(.snappy) {
                    if collapsed.contains(state) { collapsed.remove(state) } else { collapsed.insert(state) }
                }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "chevron.right")
                        .font(.caption.weight(.semibold))
                        .rotationEffect(.degrees(collapsed.contains(state) ? 0 : 90))
                        .foregroundStyle(.secondary)
                    Image(systemName: state.monIcon).foregroundStyle(state.monColor)
                    Text(state.label).font(.headline)
                    Text("\(items.count)").font(.subheadline.monospacedDigit()).foregroundStyle(.secondary)
                    Spacer()
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if !collapsed.contains(state) {
                VStack(spacing: 8) {
                    ForEach(items) { a in
                        MonAlertRow(alert: a,
                                    isSelected: selectedId == a.id,
                                    analysis: analyses[a.id],
                                    isAnalyzing: analyzing.contains(a.id),
                                    onSelect: { selectedId = (selectedId == a.id ? nil : a.id) },
                                    onAnalyze: { analyze(a) },
                                    onCreateIncident: { createIncident(from: a) })
                            .id(a.id)
                    }
                }
            }
        }
    }

    // MARK: 動作

    private func analyze(_ a: Alert) {
        guard !analyzing.contains(a.id) else { return }
        analyzing.insert(a.id)
        selectedId = a.id
        let text = MonAlertAnalyzer.analyze(a, app: app)
        Task { @MainActor in
            try? await Task.sleep(nanoseconds: 1_200_000_000)
            withAnimation(.snappy) {
                analyses[a.id] = text
                analyzing.remove(a.id)
            }
            app.log(.ai(app.automations.first { $0.trigger.contains("告警") }?.id ?? "alert-triage"), "sparkles", "分析告警「\(a.title)」", .monitoring)
        }
    }

    private func createIncident(from a: Alert) {
        guard let i = app.alerts.firstIndex(where: { $0.id == a.id }), app.alerts[i].incidentId == nil else { return }
        let maxNum = app.incidents.compactMap { Int($0.id.filter(\.isNumber)) }.max() ?? 2000
        let newId = "INC-\(maxNum + 1)"
        let sev: Int = a.severity == .critical ? 1 : a.severity == .warning ? 2 : 3
        let now = Date()
        let incident = Incident(
            id: newId, title: a.title, sev: sev, status: .investigating, services: [a.service],
            commander: app.me, startedAt: a.startedAt, resolvedAt: nil,
            impact: "由告警建立,影響範圍評估中。",
            timeline: [
                IncidentEvent(id: UUID(), date: a.startedAt, actor: .system, kind: .alert,
                              text: "告警觸發:\(a.title)(\(a.rule),目前 \(a.value))"),
                IncidentEvent(id: UUID(), date: now, actor: .person(app.me), kind: .status,
                              text: "從告警 \(a.id) 建立事件"),
            ],
            aiSummary: a.aiNote ?? analyses[a.id], followUps: [])
        app.incidents.insert(incident, at: 0)
        app.alerts[i].incidentId = newId
        if app.alerts[i].state == .firing { app.acknowledge(alertId: a.id) }
        app.log(.person(app.me), "flame", "從告警「\(a.title)」建立事件 \(newId)", .incidents)
    }
}

// MARK: - 單列

struct MonAlertRow: View {
    @EnvironmentObject var app: AppState
    let alert: Alert
    let isSelected: Bool
    let analysis: String?
    let isAnalyzing: Bool
    let onSelect: () -> Void
    let onAnalyze: () -> Void
    let onCreateIncident: () -> Void

    @State private var hovering = false

    private var serviceName: String { app.service(alert.service)?.name ?? alert.service }
    private var isActive: Bool { alert.state == .firing || alert.state == .acknowledged }

    var body: some View {
        HStack(spacing: 0) {
            Rectangle().fill(alert.severity.color.gradient).frame(width: 4)
                .opacity(alert.state == .resolved || alert.state == .silenced ? 0.4 : 1)
            VStack(alignment: .leading, spacing: 10) {
                top
                meta
                if let note = alert.aiNote { MonAICallout(title: "AI 初步判讀", text: note) }
                if isAnalyzing {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("AI 正在比對指標、日誌與最近部署…").font(.callout).foregroundStyle(.purple)
                    }
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.purple.opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
                } else if let analysis {
                    MonAICallout(title: "AI 分析結果", text: analysis)
                }
                if isSelected { actions }
            }
            .padding(12)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .strokeBorder(isSelected ? Color.accentColor : Color.primary.opacity(hovering ? 0.15 : 0.07),
                              lineWidth: isSelected ? 2 : 1)
        )
        .contentShape(RoundedRectangle(cornerRadius: 10))
        .onTapGesture { withAnimation(.snappy) { onSelect() } }
        .onHover { hovering = $0 }
        .contextMenu { menuItems }
    }

    private var top: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Image(systemName: alert.severity.monIcon).foregroundStyle(alert.severity.color)
            Text(alert.title).font(.body.weight(.semibold)).lineLimit(2)
            Pill(text: alert.severity.label, color: alert.severity.color)
            if alert.state != .firing {
                Pill(text: alert.state.label, color: alert.state.monColor, icon: alert.state.monIcon)
            }
            Spacer(minLength: 8)
            Text(alert.value)
                .font(.system(.body, design: .monospaced).weight(.semibold))
                .foregroundStyle(isActive ? alert.severity.color : .secondary)
            if !isSelected { compactActions }
        }
    }

    private var meta: some View {
        HStack(spacing: 14) {
            Label(serviceName, systemImage: "shippingbox").lineLimit(1)
            Label {
                Text(alert.rule).font(.system(.caption, design: .monospaced)).lineLimit(1)
            } icon: { Image(systemName: "function") }
            TimelineView(.periodic(from: .now, by: 30)) { ctx in
                Label(isActive ? "持續 \(Fmt.duration(ctx.date.timeIntervalSince(alert.startedAt)))"
                               : "開始於 \(Fmt.relative(alert.startedAt))",
                      systemImage: "clock")
            }
            if let by = alert.acknowledgedBy, alert.state == .acknowledged {
                HStack(spacing: 4) { Text("確認者"); ActorLabel(actor: by) }
            }
            Spacer(minLength: 0)
            if let inc = alert.incidentId {
                Button { app.go(.incidents, focus: inc) } label: {
                    Label(inc, systemImage: "flame.fill")
                }
                .buttonStyle(.link)
                .help("前往事件 \(inc)")
            }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }

    /// 未展開時:右側圖示按鈕
    @ViewBuilder private var compactActions: some View {
        HStack(spacing: 2) {
            if alert.state == .firing {
                iconButton("eye", "確認") { app.acknowledge(alertId: alert.id) }
            }
            if alert.state != .resolved {
                iconButton("checkmark.circle", "解除") { app.resolve(alertId: alert.id) }
            }
            iconButton("sparkles", "讓 AI 分析", tint: .purple) { onAnalyze() }
        }
        .opacity(hovering ? 1 : 0.55)
    }

    private func iconButton(_ icon: String, _ help: String, tint: Color = .secondary, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon).foregroundStyle(tint).frame(width: 22, height: 22).contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .help(help)
    }

    /// 選中時:完整動作列
    private var actions: some View {
        HStack(spacing: 8) {
            Button { app.acknowledge(alertId: alert.id) } label: { Label("確認", systemImage: "eye") }
                .disabled(alert.state != .firing)
            Button { app.resolve(alertId: alert.id) } label: { Label("解除", systemImage: "checkmark.circle") }
                .disabled(alert.state == .resolved)
            Button { app.silence(alertId: alert.id) } label: { Label("靜音", systemImage: "bell.slash") }
                .disabled(alert.state == .silenced || alert.state == .resolved)
            Divider().frame(height: 16)
            if let inc = alert.incidentId {
                Button { app.go(.incidents, focus: inc) } label: { Label("查看事件 \(inc)", systemImage: "flame") }
            } else {
                Button(action: onCreateIncident) { Label("建立事件", systemImage: "flame") }
                    .disabled(alert.state == .resolved)
            }
            Spacer()
            Button(action: onAnalyze) { Label(analysis == nil ? "讓 AI 分析" : "重新分析", systemImage: "sparkles") }
                .tint(.purple)
                .buttonStyle(.borderedProminent)
                .disabled(isAnalyzing)
        }
        .controlSize(.regular)
    }

    @ViewBuilder private var menuItems: some View {
        Button("確認") { app.acknowledge(alertId: alert.id) }.disabled(alert.state != .firing)
        Button("解除") { app.resolve(alertId: alert.id) }.disabled(alert.state == .resolved)
        Button("靜音") { app.silence(alertId: alert.id) }.disabled(alert.state == .silenced || alert.state == .resolved)
        Divider()
        if let inc = alert.incidentId {
            Button("查看事件 \(inc)") { app.go(.incidents, focus: inc) }
        } else {
            Button("建立事件", action: onCreateIncident).disabled(alert.state == .resolved)
        }
        Button("讓 AI 分析", action: onAnalyze)
        Divider()
        Button("前往服務") { app.go(.infrastructure, focus: alert.service) }
        Button("複製告警 ID") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(alert.id, forType: .string)
        }
    }
}

// MARK: - 模擬的 AI 分析

enum MonAlertAnalyzer {
    @MainActor
    static func analyze(_ a: Alert, app: AppState) -> String {
        let svcName = app.service(a.service)?.name ?? a.service
        let since = a.startedAt.addingTimeInterval(-30 * 60)
        let related = app.logs.filter { $0.service == a.service && $0.date >= since }
        let errors = related.filter { $0.level == .error }
        let warns = related.filter { $0.level == .warn }
        var parts: [String] = []
        parts.append("\(svcName) 在 \(Fmt.time(a.startedAt)) 開始違反「\(a.rule)」,目前值 \(a.value)。")
        if !errors.isEmpty || !warns.isEmpty {
            var s = "告警前後 30 分鐘內有 \(errors.count) 筆 error、\(warns.count) 筆 warn 日誌"
            if let top = mostCommon(errors.map(\.message)) { s += ",最常見的是「\(top)」" }
            parts.append(s + "。")
        } else {
            parts.append("同時段沒有明顯的錯誤日誌,較可能是流量或資源面的問題。")
        }
        let recentDeploy = app.deployments
            .filter { $0.service == a.service && $0.startedAt <= a.startedAt && a.startedAt.timeIntervalSince($0.startedAt) < 6 * 3600 }
            .max { $0.startedAt < $1.startedAt }
        if let d = recentDeploy {
            parts.append("告警前 \(Fmt.duration(a.startedAt.timeIntervalSince(d.startedAt))) 有一次部署(\(d.previousVersion) → \(d.version)),建議優先檢查這次變更,必要時回滾。")
        } else {
            parts.append("6 小時內沒有這個服務的部署紀錄。")
        }
        if let svc = app.service(a.service), !svc.dependencies.isEmpty {
            let deps = svc.dependencies.compactMap { app.service($0) }.filter { $0.health != .healthy }
            if !deps.isEmpty {
                parts.append("相依服務 \(deps.map(\.name).joined(separator: "、")) 也處於異常狀態,可能是上游問題。")
            }
        }
        switch a.severity {
        case .critical: parts.append("建議:立即建立事件並通知值班人員。")
        case .warning: parts.append("建議:先確認並持續觀察 15 分鐘,若沒有改善再升級。")
        case .info: parts.append("建議:不需立即處理,可列入下次例行檢查。")
        }
        return parts.joined(separator: "")
    }

    private static func mostCommon(_ xs: [String]) -> String? {
        guard !xs.isEmpty else { return nil }
        let counts = Dictionary(xs.map { ($0, 1) }, uniquingKeysWith: +)
        guard let top = counts.max(by: { $0.value < $1.value })?.key else { return nil }
        return top.count > 60 ? String(top.prefix(60)) + "…" : top
    }
}
