import SwiftUI

/// 部署列表:版本變化、策略、進度、健康檢查,可核准與回滾
struct PipeDeploymentsTab: View {
    @EnvironmentObject var app: AppState
    @Binding var highlightId: String?

    @State private var envFilter: DeployEnv?
    @State private var followToolbar = true
    @State private var activeOnly = false
    @State private var serviceFilter: String?
    @State private var pending: PipeDeployAction?

    private var filtered: [Deployment] {
        app.deployments.filter { d in
            if let e = envFilter, d.env != e { return false }
            if activeOnly && !d.cicdIsActive { return false }
            if let s = serviceFilter, d.service != s { return false }
            return true
        }
        .sorted { a, b in
            // 待核准、進行中排最上面,其他依時間
            let ra = a.status == .waitingApproval ? 0 : (a.cicdIsActive ? 1 : 2)
            let rb = b.status == .waitingApproval ? 0 : (b.cicdIsActive ? 1 : 2)
            return ra != rb ? ra < rb : a.startedAt > b.startedAt
        }
    }

    var body: some View {
        let list = filtered
        ScrollViewReader { proxy in
            PageScroll {
                summary
                filterBar
                if list.isEmpty {
                    Card {
                        EmptyStateView(title: app.deployments.isEmpty ? "還沒有部署紀錄" : "沒有符合的部署",
                                       icon: "shippingbox",
                                       message: envFilter.map { "目前只顯示 \($0.label)" })
                            .frame(minHeight: 220)
                    }
                } else {
                    Card(padding: 0) {
                        VStack(spacing: 0) {
                            ForEach(Array(list.enumerated()), id: \.element.id) { i, d in
                                if i > 0 { Divider().padding(.leading, 48) }
                                PipeDeploymentRow(deployment: d, highlighted: d.id == highlightId,
                                                  onApprove: { request(.approve, d) },
                                                  onRollback: { request(.rollback, d) })
                                    .id(d.id)
                            }
                        }
                    }
                }
            }
            .onAppear {
                if followToolbar { envFilter = app.env }
                scrollToHighlight(proxy)
            }
            .onChange(of: highlightId) { scrollToHighlight(proxy) }
            .onChange(of: app.env) { if followToolbar { envFilter = app.env } }
        }
        .confirmationDialog(pending?.title ?? "", isPresented: Binding(get: { pending != nil }, set: { if !$0 { pending = nil } }),
                            presenting: pending) { a in
            Button(a.confirmLabel, role: a.kind == .rollback ? .destructive : nil) { perform(a) }
            Button("取消", role: .cancel) {}
        } message: { a in
            Text(a.message(app))
        }
    }

    private func scrollToHighlight(_ proxy: ScrollViewProxy) {
        guard let id = highlightId, let d = app.deployments.first(where: { $0.id == id }) else { return }
        if let e = envFilter, e != d.env { envFilter = nil; followToolbar = false }
        if activeOnly && !d.cicdIsActive { activeOnly = false }
        serviceFilter = nil
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
            withAnimation { proxy.scrollTo(id, anchor: .center) }
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
            if highlightId == id { withAnimation { highlightId = nil } }
        }
    }

    private func request(_ kind: PipeDeployAction.Kind, _ d: Deployment) {
        // 正式環境一律先確認;其他環境直接執行
        if d.env == .production {
            pending = PipeDeployAction(kind: kind, deployment: d)
        } else {
            perform(PipeDeployAction(kind: kind, deployment: d))
        }
    }

    private func perform(_ a: PipeDeployAction) {
        switch a.kind {
        case .approve:
            app.approveDeployment(id: a.deployment.id)
        case .rollback:
            app.rollback(deploymentId: a.deployment.id)
            if let newest = app.deployments.first {
                CICDSession.trackedDeployments.insert(newest.id)
                highlightId = newest.id
            }
        }
        pending = nil
    }

    // MARK: 摘要與篩選

    private var summary: some View {
        let ds = app.deployments
        let weekAgo = Date().addingTimeInterval(-7 * 86400)
        let active = ds.filter { $0.status == .running || $0.status == .queued }.count
        let waiting = ds.filter { $0.status == .waitingApproval }.count
        let weekOK = ds.filter { $0.status == .succeeded && $0.startedAt > weekAgo }.count
        let weekBad = ds.filter { ($0.status == .failed || $0.cicdIsRollback) && $0.startedAt > weekAgo }.count
        return LazyVGrid(columns: [GridItem(.adaptive(minimum: 160), spacing: 12)], spacing: 12) {
            MetricTile(title: "部署中", value: "\(active)", icon: "arrow.triangle.2.circlepath", tint: .blue)
            MetricTile(title: "等待核准", value: "\(waiting)", icon: "hand.raised.fill", tint: .purple)
            MetricTile(title: "7 天內成功", value: "\(weekOK)", icon: "checkmark.circle.fill", tint: .green)
            MetricTile(title: "7 天內失敗或回滾", value: "\(weekBad)", icon: "arrow.uturn.backward.circle", tint: .red)
        }
    }

    private var filterBar: some View {
        let serviceIds = Array(Set(app.deployments.map(\.service))).sorted()
        return HStack(spacing: 12) {
            Picker("環境", selection: Binding(get: { envFilter }, set: { envFilter = $0; followToolbar = false })) {
                Text("全部").tag(DeployEnv?.none)
                ForEach(DeployEnv.allCases) { Text($0.label).tag(DeployEnv?.some($0)) }
            }
            .pickerStyle(.segmented).labelsHidden().fixedSize()

            Picker("服務", selection: $serviceFilter) {
                Text("所有服務").tag(String?.none)
                Divider()
                ForEach(serviceIds, id: \.self) { Text(app.cicdServiceName($0)).tag(String?.some($0)) }
            }
            .labelsHidden().fixedSize()

            Toggle("只看進行中", isOn: $activeOnly).toggleStyle(.checkbox)
            Spacer(minLength: 0)
            if !followToolbar {
                Button("跟隨工具列環境") { followToolbar = true; envFilter = app.env }
                    .buttonStyle(.link).font(.caption)
            }
        }
        .controlSize(.small)
    }
}

struct PipeDeployAction: Identifiable {
    enum Kind { case approve, rollback }
    let kind: Kind
    let deployment: Deployment
    var id: String { "\(kind)-\(deployment.id)" }

    var title: String {
        switch kind {
        case .approve: return "核准部署到 Production?"
        case .rollback: return "回滾 Production?"
        }
    }

    var confirmLabel: String {
        switch kind {
        case .approve: return "核准部署"
        case .rollback: return "回滾到 \(deployment.previousVersion)"
        }
    }

    @MainActor func message(_ app: AppState) -> String {
        let name = app.cicdServiceName(deployment.service)
        switch kind {
        case .approve:
            return "\(name) \(deployment.previousVersion) → \(deployment.version),以 \(deployment.strategy.rawValue) 方式部署,會影響所有正式使用者。"
        case .rollback:
            return "\(name) 會從 \(deployment.version) 回到 \(deployment.previousVersion)。回滾期間部分請求可能由舊版處理。"
        }
    }
}

struct PipeDeploymentRow: View {
    @EnvironmentObject var app: AppState
    let deployment: Deployment
    var highlighted = false
    var onApprove: () -> Void
    var onRollback: () -> Void

    @State private var expanded = false

    private var d: Deployment { deployment }
    private var canRollback: Bool { (d.status == .succeeded || d.status == .failed) && d.previousVersion != d.version }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            PipeStatusIcon(status: d.status).font(.title3).frame(width: 24).padding(.top, 2)
            VStack(alignment: .leading, spacing: 5) {
                HStack(spacing: 8) {
                    Text(app.cicdServiceName(d.service)).font(.headline).lineLimit(1)
                    EnvBadge(env: d.env)
                    Pill(text: d.strategy.rawValue, color: .blue, icon: strategyIcon)
                    if let c = d.canaryPercent, d.strategy == .canary || c > 0 {
                        Pill(text: "流量 \(c)%", color: .orange, icon: "bird")
                    }
                    if d.cicdIsRollback { Pill(text: "回滾", color: .red, icon: "arrow.uturn.backward") }
                }
                HStack(spacing: 6) {
                    Text(d.previousVersion).foregroundStyle(.secondary)
                    Image(systemName: "arrow.right").font(.caption).foregroundStyle(.tertiary)
                    Text(d.version).fontWeight(.semibold)
                }
                .font(.callout.monospaced())
                .textSelection(.enabled)
                if !d.changes.isEmpty {
                    if expanded {
                        VStack(alignment: .leading, spacing: 2) {
                            ForEach(d.changes, id: \.self) { c in
                                Label(c, systemImage: "smallcircle.filled.circle").font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    } else {
                        Text(d.changes.joined(separator: " · ")).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                    if d.changes.count > 1 {
                        Button(expanded ? "收起" : "全部 \(d.changes.count) 項變更") { withAnimation(.snappy) { expanded.toggle() } }
                            .buttonStyle(.link).font(.caption)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            VStack(alignment: .trailing, spacing: 6) {
                statusLine
                HStack(spacing: 4) {
                    Image(systemName: d.healthGate.icon).foregroundStyle(d.healthGate.color)
                    Text("健康檢查 \(d.healthGate.cicdLabel)")
                }
                .font(.caption)
                HStack(spacing: 4) {
                    ActorLabel(actor: d.by)
                    Text("· \(Fmt.relative(d.startedAt))").foregroundStyle(.secondary)
                }
                .font(.caption)
            }
            .frame(width: 200, alignment: .trailing)

            VStack(alignment: .trailing, spacing: 6) {
                if d.status == .waitingApproval {
                    Button(action: onApprove) { Label("核准", systemImage: "checkmark.seal") }
                        .buttonStyle(.borderedProminent).tint(.purple)
                }
                if canRollback {
                    Button(action: onRollback) { Label("回滾", systemImage: "arrow.uturn.backward") }
                        .help("回到 \(d.previousVersion)")
                }
            }
            .controlSize(.small)
            .frame(width: 84, alignment: .trailing)
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .background(highlighted ? Color.accentColor.opacity(0.12) : Color.clear)
        .animation(.easeInOut, value: highlighted)
    }

    @ViewBuilder private var statusLine: some View {
        switch d.status {
        case .running, .queued:
            VStack(alignment: .trailing, spacing: 3) {
                Text(d.status == .queued ? "排隊中" : "部署中").font(.caption.weight(.medium)).foregroundStyle(.blue)
                PipeProgressBar(value: d.progress, color: .blue).frame(width: 180)
            }
        case .waitingApproval:
            VStack(alignment: .trailing, spacing: 3) {
                RunStatusBadge(status: d.status)
                PipeProgressBar(value: d.progress, color: .purple).frame(width: 180)
            }
        default:
            HStack(spacing: 6) {
                RunStatusBadge(status: d.status)
                if let f = d.finishedAt {
                    Text(Fmt.duration(f.timeIntervalSince(d.startedAt))).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
            }
        }
    }

    private var strategyIcon: String {
        switch d.strategy {
        case .rolling: return "arrow.triangle.2.circlepath"
        case .canary: return "bird"
        case .blueGreen: return "square.split.2x1"
        }
    }
}
