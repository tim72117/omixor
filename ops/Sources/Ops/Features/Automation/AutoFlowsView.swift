import SwiftUI

/// 自動化流程:每個流程的自主程度、啟用狀態、權限與成效。
struct AutoFlowsView: View {
    @EnvironmentObject var app: AppState
    var openDetail: (String) -> Void
    @State private var filter: Autonomy?
    @State private var query = ""

    private var items: [Automation] {
        app.automations.filter { a in
            (filter == nil || a.autonomy == filter)
                && (query.isEmpty || a.name.localizedCaseInsensitiveContains(query)
                    || a.summary.localizedCaseInsensitiveContains(query)
                    || a.trigger.localizedCaseInsensitiveContains(query))
        }
    }

    var body: some View {
        PageScroll {
            autonomyLegend
            HStack(spacing: 10) {
                Picker("自主程度", selection: $filter) {
                    Text("全部").tag(Autonomy?.none)
                    ForEach(Autonomy.allCases) { Text($0.label).tag(Autonomy?.some($0)) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 320)
                Spacer()
                TextField("搜尋流程", text: $query)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 220)
            }
            if app.automations.isEmpty {
                Card {
                    EmptyStateView(title: "還沒有自動化流程", icon: "sparkles",
                                   message: "設定好的 AI 自動化流程會列在這裡。")
                        .frame(maxWidth: .infinity, minHeight: 220)
                }
            } else if items.isEmpty {
                Card {
                    EmptyStateView(title: "沒有符合條件的流程", icon: "magnifyingglass")
                        .frame(maxWidth: .infinity, minHeight: 160)
                }
            } else {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 340, maximum: 620), spacing: 14, alignment: .top)],
                          alignment: .leading, spacing: 14) {
                    ForEach(items) { a in
                        AutoFlowCard(automation: a, openDetail: { openDetail(a.id) })
                    }
                }
            }
        }
    }

    private var autonomyLegend: some View {
        let counts = Dictionary(grouping: app.automations, by: \.autonomy).mapValues(\.count)
        return ViewThatFits(in: .horizontal) {
            HStack(spacing: 12) { legendItems(counts) }
            VStack(alignment: .leading, spacing: 8) { legendItems(counts) }
        }
    }

    @ViewBuilder private func legendItems(_ counts: [Autonomy: Int]) -> some View {
        ForEach(Autonomy.allCases) { a in
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: AutoAutonomyInfo.icon(a))
                    .foregroundStyle(a.color)
                    .frame(width: 28, height: 28)
                    .background(a.color.opacity(0.13), in: RoundedRectangle(cornerRadius: 7))
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(a.label).font(.subheadline.weight(.semibold))
                        Text("\(counts[a] ?? 0)").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    }
                    Text(a.detail).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 0)
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.primary.opacity(0.07)))
        }
    }
}

// MARK: - 流程卡片

struct AutoFlowCard: View {
    @EnvironmentObject var app: AppState
    let automation: Automation
    var openDetail: () -> Void
    @State private var hovering = false

    var body: some View {
        let a = automation
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                AutoIconTile(icon: a.icon, size: 38, dimmed: !a.enabled)
                VStack(alignment: .leading, spacing: 3) {
                    Text(a.name).font(.headline).lineLimit(1)
                    Text(a.summary).font(.callout).foregroundStyle(.secondary).lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 6)
                Toggle("啟用", isOn: Binding(get: { a.enabled }, set: { _ in app.toggle(automationId: a.id) }))
                    .toggleStyle(.switch)
                    .labelsHidden()
                    .controlSize(.small)
                    .help(a.enabled ? "停用這個流程" : "啟用這個流程")
            }

            HStack(spacing: 6) {
                Image(systemName: "bolt.horizontal.circle").foregroundStyle(.indigo)
                Text(a.trigger).font(.caption).lineLimit(1)
                Spacer()
                if let pending = app.autoPendingApproval(for: a.id) {
                    Pill(text: "待核准", color: .orange, icon: "hand.raised.fill").help(pending.title)
                }
                if let last = a.lastRun {
                    Text("上次 \(Fmt.relative(last))").font(.caption).foregroundStyle(.secondary)
                }
            }

            VStack(alignment: .leading, spacing: 5) {
                Picker("自主程度", selection: Binding(get: { a.autonomy },
                                                  set: { app.setAutonomy($0, automationId: a.id) })) {
                    ForEach(Autonomy.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .disabled(!a.enabled)
                Text(a.autonomy.detail).font(.caption).foregroundStyle(a.autonomy.color)
            }

            if !a.allowedTools.isEmpty {
                AutoFlowLayout(spacing: 5) {
                    ForEach(a.allowedTools, id: \.self) { AutoChip(text: $0, icon: "wrench.adjustable") }
                }
            }
            if !a.scope.isEmpty {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text("範圍").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    Text(a.scope.joined(separator: "、")).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                }
            }

            Divider()
            HStack(spacing: 0) {
                stat("30 天執行", "\(a.runs30d)")
                stat("成功率", Fmt.percent(a.successRate * 100), color: a.successRate >= 0.95 ? .green : a.successRate >= 0.8 ? .orange : .red)
                stat("省下時間", String(format: "%.1f 小時", a.timeSavedHours))
                Button("詳細", action: openDetail).controlSize(.small)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12)
            .strokeBorder(hovering ? Color.indigo.opacity(0.45) : Color.primary.opacity(0.07)))
        .opacity(a.enabled ? 1 : 0.72)
        .onHover { hovering = $0 }
        .contentShape(RoundedRectangle(cornerRadius: 12))
        .onTapGesture(count: 2, perform: openDetail)
        .contextMenu {
            Button("查看詳細", action: openDetail)
            Divider()
            ForEach(Autonomy.allCases) { m in
                Button { app.setAutonomy(m, automationId: a.id) } label: {
                    if m == a.autonomy { Label(m.label, systemImage: "checkmark") } else { Text(m.label) }
                }
            }
            Divider()
            Button(a.enabled ? "停用" : "啟用") { app.toggle(automationId: a.id) }
        }
    }

    private func stat(_ title: String, _ value: String, color: Color = .primary) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption2).foregroundStyle(.secondary)
            Text(value).font(.callout.weight(.semibold)).monospacedDigit().foregroundStyle(color)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: - 詳細:流程圖與安全護欄

struct AutoFlowDetailSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let automationId: String
    /// 跳到其他項目(核准請求或執行紀錄的 id)
    var navigate: (String) -> Void

    var body: some View {
        VStack(spacing: 0) {
            if let a = app.automation(automationId) {
                header(a)
                Divider()
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        Card("流程", subtitle: "人工核准關卡會依自主程度移動") {
                            AutoPipelineDiagram(autonomy: a.autonomy)
                        }
                        guardrails(a)
                        permissions(a)
                        recentRuns(a)
                    }
                    .padding(20)
                }
            } else {
                EmptyStateView(title: "找不到這個流程", icon: "questionmark.circle")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            Divider()
            HStack {
                Spacer()
                Button("完成") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            .padding(12)
        }
        .frame(minWidth: 640, idealWidth: 760, minHeight: 560, idealHeight: 720)
    }

    private func header(_ a: Automation) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 14) {
                AutoIconTile(icon: a.icon, size: 46, dimmed: !a.enabled)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text(a.name).font(.title3.weight(.semibold))
                        AIBadge()
                    }
                    Text(a.summary).foregroundStyle(.secondary)
                    Label(a.trigger, systemImage: "bolt.horizontal.circle").font(.caption).foregroundStyle(.indigo)
                }
                Spacer()
                Toggle(a.enabled ? "啟用中" : "已停用",
                       isOn: Binding(get: { a.enabled }, set: { _ in app.toggle(automationId: a.id) }))
                    .toggleStyle(.switch)
            }
            HStack(spacing: 12) {
                Picker("自主程度", selection: Binding(get: { a.autonomy },
                                                  set: { app.setAutonomy($0, automationId: a.id) })) {
                    ForEach(Autonomy.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .frame(maxWidth: 360)
                Text(a.autonomy.detail).font(.caption).foregroundStyle(a.autonomy.color)
                Spacer()
            }
            HStack(spacing: 12) {
                MetricTile(title: "30 天執行", value: "\(a.runs30d)", icon: "play.circle", tint: .indigo)
                MetricTile(title: "成功率", value: Fmt.percent(a.successRate * 100), icon: "checkmark.circle", tint: .green)
                MetricTile(title: "省下時間", value: String(format: "%.1f h", a.timeSavedHours), icon: "clock.arrow.circlepath", tint: .purple)
            }
        }
        .padding(20)
    }

    private func guardrails(_ a: Automation) -> some View {
        let g = AutoGuardrails.of(a, runs: app.automationRuns)
        let ratio = g.monthlyBudgetUSD > 0 ? min(1, g.spentUSD / g.monthlyBudgetUSD) : 0
        return Card("安全護欄", subtitle: "AI 不論自主程度,都不能超出這些限制") {
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 14, verticalSpacing: 10) {
                GridRow {
                    rowLabel("環境限制", "globe")
                    HStack(spacing: 5) { ForEach(g.environments, id: \.self) { Pill(text: $0, color: .indigo) } }
                }
                GridRow {
                    rowLabel("影響範圍上限", "scope")
                    Text(g.blastRadius)
                }
                GridRow {
                    rowLabel("頻率上限", "speedometer")
                    Text("每小時最多 \(g.maxActionsPerHour) 次動作")
                }
                GridRow {
                    rowLabel("成本預算", "dollarsign.circle")
                    VStack(alignment: .leading, spacing: 4) {
                        Text(String(format: "本月 US$%.2f / US$%.0f", g.spentUSD, g.monthlyBudgetUSD)).monospacedDigit()
                        ProgressView(value: ratio).tint(ratio > 0.8 ? .orange : .purple).frame(maxWidth: 260)
                    }
                }
                GridRow {
                    rowLabel("執行前演練", "checklist")
                    Text(g.requiresDryRun ? "必須先 dry-run / plan,結果附在請求裡" : "不需要")
                }
                GridRow {
                    rowLabel("自動回滾", "arrow.uturn.backward")
                    Text(g.autoRollback ? "驗證失敗時自動回滾並通知值班" : "由人手動回滾")
                }
                GridRow {
                    rowLabel("禁止動作", "nosign")
                    AutoFlowLayout(spacing: 5) {
                        ForEach(g.forbidden, id: \.self) { Pill(text: $0, color: .red, icon: "xmark") }
                    }
                }
            }
        }
    }

    private func permissions(_ a: Automation) -> some View {
        Card("權限", subtitle: "AI 只能用這些工具、只能動這些對象") {
            VStack(alignment: .leading, spacing: 10) {
                Text("可用工具").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                if a.allowedTools.isEmpty {
                    Text("未設定").foregroundStyle(.secondary)
                } else {
                    AutoFlowLayout(spacing: 6) {
                        ForEach(a.allowedTools, id: \.self) { AutoChip(text: $0, icon: "wrench.adjustable") }
                    }
                }
                Text("可操作範圍").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                if a.scope.isEmpty {
                    Text("未設定").foregroundStyle(.secondary)
                } else {
                    AutoFlowLayout(spacing: 6) {
                        ForEach(a.scope, id: \.self) { AutoChip(text: $0, icon: "cube", mono: false) }
                    }
                }
            }
        }
    }

    private func recentRuns(_ a: Automation) -> some View {
        let runs = app.automationRuns.filter { $0.automation == a.id }.sorted { $0.startedAt > $1.startedAt }
        let pending = app.pendingApprovals.filter { $0.automation == a.id }
        return Card("最近執行", subtitle: runs.isEmpty ? "還沒有執行紀錄" : nil) {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(pending) { p in
                    Button { navigate(p.id) } label: {
                        HStack {
                            Image(systemName: "hand.raised.fill").foregroundStyle(.orange)
                            Text(p.title).lineLimit(1)
                            Spacer()
                            AutoRiskBadge(risk: p.risk)
                            Image(systemName: "chevron.right").foregroundStyle(.tertiary)
                        }
                        .contentShape(Rectangle())
                        .padding(.vertical, 6)
                    }
                    .buttonStyle(.plain)
                }
                ForEach(runs.prefix(6)) { r in
                    Button { navigate(r.id) } label: {
                        HStack {
                            Image(systemName: r.status.icon).foregroundStyle(r.status.color).frame(width: 18)
                            Text(r.target).lineLimit(1)
                            Text(r.summary).foregroundStyle(.secondary).lineLimit(1)
                            Spacer()
                            Text(Fmt.relative(r.startedAt)).font(.caption).foregroundStyle(.secondary)
                            Image(systemName: "chevron.right").foregroundStyle(.tertiary)
                        }
                        .contentShape(Rectangle())
                        .padding(.vertical, 6)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    private func rowLabel(_ text: String, _ icon: String) -> some View {
        Label(text, systemImage: icon).foregroundStyle(.secondary).frame(width: 130, alignment: .leading)
    }
}

// MARK: - 流程圖

/// 觸發 → AI 分析 → (核准關卡) → 執行 → 驗證 → 通知/回滾
struct AutoPipelineDiagram: View {
    let autonomy: Autonomy

    private struct Node: Identifiable {
        let id: String
        let title: String
        let caption: String
        let icon: String
        let kind: Kind
        enum Kind { case normal, ai, human, skipped, off }
    }

    private var nodes: [Node] {
        let gate: Node
        switch autonomy {
        case .suggest:
            gate = Node(id: "gate", title: "人工決定", caption: "AI 只給建議,由人自己動手", icon: "person.fill", kind: .human)
        case .approve:
            gate = Node(id: "gate", title: "核准關卡", caption: "人核准、拒絕或修改後才繼續", icon: "hand.raised.fill", kind: .human)
        case .auto:
            gate = Node(id: "gate", title: "核准關卡", caption: "已略過,事後通知", icon: "hand.raised.slash", kind: .skipped)
        }
        let off: Node.Kind = autonomy == .suggest ? .off : .normal
        return [
            Node(id: "trigger", title: "觸發", caption: "事件或排程", icon: "bolt.fill", kind: .normal),
            Node(id: "analyze", title: "AI 分析", caption: "讀取日誌、指標、程式碼", icon: "sparkles", kind: .ai),
            gate,
            Node(id: "exec", title: "執行", caption: autonomy == .suggest ? "AI 不執行" : "在護欄內執行", icon: "play.fill", kind: off),
            Node(id: "verify", title: "驗證", caption: "健康檢查與指標", icon: "checkmark.shield", kind: off == .off ? .off : .ai),
            Node(id: "notify", title: "通知/回滾", caption: autonomy == .auto ? "失敗自動回滾" : "通知負責人", icon: "bell.badge", kind: off),
        ]
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 0) {
                    ForEach(Array(nodes.enumerated()), id: \.element.id) { i, n in
                        if i > 0 { arrow(horizontal: true) }
                        node(n).frame(width: 98)
                    }
                }
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(nodes.enumerated()), id: \.element.id) { i, n in
                        if i > 0 { arrow(horizontal: false).padding(.leading, 30) }
                        node(n).frame(maxWidth: 320, alignment: .leading)
                    }
                }
            }
            HStack(spacing: 6) {
                Image(systemName: "info.circle").foregroundStyle(.secondary)
                Text(explanation).font(.caption).foregroundStyle(.secondary)
            }
        }
        .animation(.easeInOut(duration: 0.25), value: autonomy)
    }

    private var explanation: String {
        switch autonomy {
        case .suggest: return "目前是「只建議」:AI 分析完就停,所有變更都由人執行。"
        case .approve: return "目前是「需核准」:AI 準備好動作,送到待核准收件匣等人決定。"
        case .auto: return "目前是「自動執行」:AI 直接執行,驗證失敗會自動回滾,並通知值班人員。"
        }
    }

    private func color(_ k: Node.Kind) -> Color {
        switch k {
        case .normal: return .blue
        case .ai: return .purple
        case .human: return .orange
        case .skipped, .off: return .gray
        }
    }

    private func node(_ n: Node) -> some View {
        let c = color(n.kind)
        let faded = n.kind == .skipped || n.kind == .off
        return VStack(spacing: 5) {
            ZStack {
                Circle()
                    .fill(faded ? AnyShapeStyle(Color.gray.opacity(0.12))
                          : n.kind == .ai ? AnyShapeStyle(AutoTheme.gradient) : AnyShapeStyle(c.gradient))
                    .frame(width: 38, height: 38)
                if faded {
                    Circle().strokeBorder(Color.gray.opacity(0.5), style: StrokeStyle(lineWidth: 1.5, dash: [3, 3]))
                        .frame(width: 38, height: 38)
                }
                if n.kind == .human {
                    Circle().strokeBorder(Color.orange.opacity(0.35), lineWidth: 5).frame(width: 48, height: 48)
                }
                Image(systemName: n.icon)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(faded ? Color.secondary : Color.white)
            }
            .frame(width: 50, height: 50)
            Text(n.title).font(.caption.weight(.semibold))
                .foregroundStyle(faded ? .secondary : n.kind == .human ? Color.orange : .primary)
                .strikethrough(n.kind == .skipped)
            Text(n.caption).font(.caption2).foregroundStyle(.secondary)
                .multilineTextAlignment(.center).lineLimit(2).fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, 4)
    }

    private func arrow(horizontal: Bool) -> some View {
        Image(systemName: horizontal ? "chevron.right" : "chevron.down")
            .font(.caption.weight(.bold))
            .foregroundStyle(.tertiary)
            .frame(width: horizontal ? 14 : nil, height: horizontal ? 50 : 14)
            .frame(maxHeight: horizontal ? .infinity : nil, alignment: .top)
            .padding(.top, horizontal ? 0 : 2)
    }
}
