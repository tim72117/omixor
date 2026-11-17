import SwiftUI

// 單一服務的詳細資料:基本資訊、SLO、延遲與錯誤率圖、相依、主機、部署、告警與快速動作。

struct InfraServiceDetail: View {
    @EnvironmentObject var app: AppState
    let service: Service
    var onSelectService: (String) -> Void
    var onOpenServer: (String) -> Void

    @StateObject private var toast = InfraToastModel()
    @State private var confirmRestart = false
    @State private var showScale = false
    @State private var replicas = 2

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                headerCard
                kpis
                sloCard
                charts
                dependencyCard
                hostsCard
                deploymentsCard
                alertsCard
            }
            .padding(18)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .overlay(alignment: .top) {
            if let m = toast.message { InfraToast(text: m).padding(.top, 10) }
        }
        .onAppear { replicas = max(1, service.hostIds.count * 2) }
        .confirmationDialog("要重新啟動 \(service.name) 嗎?", isPresented: $confirmRestart) {
            Button("滾動重新啟動", role: .destructive) {
                app.log(.person(app.me), "arrow.clockwise", "滾動重新啟動 \(service.name)(\(service.hostIds.count) 台主機)", .infrastructure)
                toast.show("已送出 \(service.name) 的滾動重新啟動")
            }
            Button("取消", role: .cancel) {}
        } message: {
            Text("會逐台重新啟動 \(service.hostIds.count) 台主機上的執行個體,過程中流量會先導到其他主機。")
        }
    }

    // MARK: 頁首

    private var headerCard: some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(service.name).font(.title3.weight(.semibold)).textSelection(.enabled)
                    HealthBadge(health: service.health)
                    InfraTierPill(tier: service.tier)
                    Spacer(minLength: 0)
                }
                if !service.summary.isEmpty {
                    Text(service.summary).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
                InfraFlowLayout(spacing: 14) {
                    Label(service.team, systemImage: "person.3")
                    ActorLabel(actor: .person(service.owner))
                    Label(service.language, systemImage: "chevron.left.forwardslash.chevron.right")
                    Button {
                        app.go(.development, focus: service.repo)
                    } label: {
                        Label(service.repo, systemImage: "book.closed")
                    }
                    .buttonStyle(.link)
                    .help("到開發進度查看儲存庫")
                }
                .font(.callout)
                .foregroundStyle(.secondary)

                versionsRow

                Divider()
                actions
            }
        }
    }

    private var versionsRow: some View {
        let prod = service.versions[.production]
        return InfraFlowLayout(spacing: 10) {
            ForEach(DeployEnv.allCases) { env in
                HStack(spacing: 5) {
                    EnvBadge(env: env)
                    Text(service.versions[env] ?? "—")
                        .font(.callout.monospaced())
                        .foregroundStyle(env == app.env ? .primary : .secondary)
                }
            }
            if let prod, let stg = service.versions[.staging], stg != prod {
                Pill(text: "Staging 有未上線版本", color: .orange, icon: "arrow.up.circle")
            }
        }
    }

    private var actions: some View {
        InfraFlowLayout(spacing: 8) {
            Button {
                app.go(.monitoring, focus: service.id)
            } label: { Label("查看日誌與指標", systemImage: "doc.text.magnifyingglass") }

            Button {
                confirmRestart = true
            } label: { Label("重新啟動", systemImage: "arrow.clockwise") }

            Button {
                showScale = true
            } label: { Label("調整副本數", systemImage: "arrow.up.left.and.arrow.down.right") }
            .popover(isPresented: $showScale, arrowEdge: .bottom) { scalePopover }

            Button {
                app.go(.pipelines, focus: service.id)
            } label: { Label("部署紀錄", systemImage: "arrow.triangle.branch") }
        }
        .buttonStyle(.bordered)
        .controlSize(.regular)
    }

    private var scalePopover: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("調整 \(service.name) 副本數").font(.headline)
            Stepper(value: $replicas, in: 1...64) {
                HStack {
                    Text("副本數")
                    Spacer()
                    Text("\(replicas)").font(.title3.monospacedDigit().weight(.semibold))
                }
            }
            Text("目前分佈在 \(service.hostIds.count) 台主機。調整後會依負載自動排程。")
                .font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("取消") { showScale = false }
                Button("套用") {
                    app.log(.person(app.me), "arrow.up.left.and.arrow.down.right", "把 \(service.name) 調整為 \(replicas) 個副本", .infrastructure)
                    showScale = false
                    toast.show("\(service.name) 已調整為 \(replicas) 個副本")
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(16)
        .frame(width: 280)
    }

    // MARK: 指標

    private var span: TimeInterval { min(app.timeRange.seconds, 7 * 86400) }

    private var latencySeries: [MetricPoint] {
        infraSynthSeries(seed: service.id + "-lat", current: service.latencyP95, span: span, volatility: 0.18)
    }

    private var errorSeries: [MetricPoint] {
        infraSynthSeries(seed: service.id + "-err", current: service.errorRate, span: span, volatility: 0.5)
    }

    private var rpsSeries: [MetricPoint] {
        infraSynthSeries(seed: service.id + "-rps", current: service.rps, span: span, volatility: 0.25)
    }

    private var kpis: some View {
        let lat = latencySeries.map(\.value)
        let err = errorSeries.map(\.value)
        let rps = rpsSeries.map(\.value)
        let meets = service.uptime30d >= service.sloTarget
        return LazyVGrid(columns: [GridItem(.adaptive(minimum: 150), spacing: 12)], spacing: 12) {
            MetricTile(title: "可用率(30 天)", value: Fmt.percent(service.uptime30d, digits: 2),
                       delta: "目標 \(Fmt.percent(service.sloTarget, digits: 2))", deltaGood: meets,
                       icon: "checkmark.shield", tint: meets ? .green : .red)
            MetricTile(title: "p95 延遲", value: "\(Int(service.latencyP95)) ms",
                       icon: "timer", tint: .blue, series: lat)
            MetricTile(title: "錯誤率", value: Fmt.percent(service.errorRate, digits: 2),
                       icon: "exclamationmark.bubble", tint: service.errorRate >= 1 ? .red : .orange, series: err)
            MetricTile(title: "流量", value: "\(Fmt.number(service.rps)) rps",
                       icon: "arrow.left.arrow.right", tint: .teal, series: rps)
        }
    }

    private var sloCard: some View {
        let window: Double = 30 * 24 * 60 // 分鐘
        let allowed = (100 - service.sloTarget) / 100 * window
        let used = max(0, (100 - service.uptime30d) / 100 * window)
        let remaining = service.errorBudgetRemaining
        let (statusText, statusColor, statusIcon): (String, Color, String) = remaining < 0.2
            ? ("錯誤預算快用完了,建議暫停非必要部署", .red, "exclamationmark.octagon.fill")
            : remaining < 0.5
                ? ("預算消耗偏快,留意部署節奏", .orange, "exclamationmark.triangle.fill")
                : ("預算充足,可以照常部署", .green, "checkmark.circle.fill")
        return Card("SLO 與錯誤預算", subtitle: "目標 \(Fmt.percent(service.sloTarget, digits: 2)) · 30 天滾動視窗") {
            HStack(alignment: .top, spacing: 24) {
                sloStat("實際可用率", Fmt.percent(service.uptime30d, digits: 3))
                sloStat("允許停機", infraMinutes(allowed))
                sloStat("已用掉", infraMinutes(used))
                Spacer(minLength: 0)
            }
            VStack(alignment: .leading, spacing: 5) {
                HStack {
                    Text("錯誤預算剩餘").font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Text("\(Int(remaining * 100))%")
                        .font(.callout.monospacedDigit().weight(.semibold))
                        .foregroundStyle(infraBudgetColor(remaining))
                }
                InfraBudgetBar(remaining: remaining, height: 8)
            }
            Label(statusText, systemImage: statusIcon)
                .font(.callout)
                .foregroundStyle(statusColor)
        }
    }

    private func sloStat(_ title: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(value).font(.title3.monospacedDigit().weight(.medium))
        }
    }

    private var charts: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 300), spacing: 12)], spacing: 12) {
            Card("p95 延遲", subtitle: "最近 \(app.timeRange.label) · ms") {
                SeriesChart(series: [MetricSeries(id: "lat", name: "p95", unit: "ms", points: latencySeries)], height: 150)
            }
            Card("錯誤率", subtitle: "最近 \(app.timeRange.label) · %") {
                SeriesChart(series: [MetricSeries(id: "err", name: "錯誤率", unit: "%", points: errorSeries)],
                            height: 150, threshold: 1)
            }
        }
    }

    // MARK: 相依

    private var dependencyCard: some View {
        let dependents = app.services.filter { $0.dependencies.contains(service.id) }
        return Card {
            VStack(alignment: .leading, spacing: 12) {
                InfraSectionHeader(title: "相依關係", trailing: "依賴 \(service.dependencies.count) · 被依賴 \(dependents.count)")
                VStack(alignment: .leading, spacing: 6) {
                    Text("依賴的服務").font(.caption).foregroundStyle(.secondary)
                    if service.dependencies.isEmpty {
                        Text("沒有依賴其他服務").font(.callout).foregroundStyle(.tertiary)
                    } else {
                        InfraFlowLayout {
                            ForEach(service.dependencies, id: \.self) { id in depChip(id) }
                        }
                    }
                }
                VStack(alignment: .leading, spacing: 6) {
                    Text("呼叫這個服務的").font(.caption).foregroundStyle(.secondary)
                    if dependents.isEmpty {
                        Text("沒有其他服務依賴它").font(.callout).foregroundStyle(.tertiary)
                    } else {
                        InfraFlowLayout {
                            ForEach(dependents) { d in depChip(d.id) }
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder private func depChip(_ id: String) -> some View {
        if let s = app.service(id) {
            InfraChip(text: s.name, color: s.health.color, icon: s.health.icon) { onSelectService(s.id) }
                .help("\(s.name):\(s.health.label)")
        } else {
            InfraChip(text: id, color: .gray, icon: "questionmark.circle")
                .help("服務目錄裡找不到這個服務")
        }
    }

    // MARK: 主機

    private var hostsCard: some View {
        let hosts = service.hostIds.compactMap { app.server($0) }
        return Card {
            VStack(alignment: .leading, spacing: 8) {
                InfraSectionHeader(title: "執行中的主機", trailing: "\(hosts.count) 台")
                if hosts.isEmpty {
                    Text("目前沒有登記的主機").font(.callout).foregroundStyle(.tertiary)
                } else {
                    ForEach(hosts) { h in
                        Button { onOpenServer(h.id) } label: { hostRow(h) }
                            .buttonStyle(.plain)
                        if h.id != hosts.last?.id { Divider() }
                    }
                }
            }
        }
    }

    private func hostRow(_ h: Server) -> some View {
        HStack(spacing: 10) {
            StatusDot(color: h.health.color, pulsing: h.health == .down)
            VStack(alignment: .leading, spacing: 1) {
                Text(h.name).font(.callout.weight(.medium)).lineLimit(1)
                Text("\(h.region) · \(h.ip)").font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1)
            }
            .frame(minWidth: 120, alignment: .leading)
            EnvBadge(env: h.env)
            Spacer(minLength: 8)
            VStack(spacing: 3) {
                UsageBar(value: h.cpu, label: "CPU")
                UsageBar(value: h.memory, label: "RAM")
            }
            .frame(maxWidth: 180)
            Image(systemName: "chevron.right").font(.caption).foregroundStyle(.tertiary)
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
    }

    // MARK: 部署

    private var deploymentsCard: some View {
        let list = app.deployments.filter { $0.service == service.id }.sorted { $0.startedAt > $1.startedAt }.prefix(6)
        return Card {
            VStack(alignment: .leading, spacing: 8) {
                InfraSectionHeader(title: "最近部署", trailing: list.isEmpty ? nil : "最近 \(list.count) 筆")
                if list.isEmpty {
                    Text("這個服務最近沒有部署").font(.callout).foregroundStyle(.tertiary)
                } else {
                    ForEach(Array(list)) { d in
                        Button { app.go(.pipelines, focus: d.id) } label: { deploymentRow(d) }
                            .buttonStyle(.plain)
                            .help("到 CI/CD 查看這次部署")
                        if d.id != list.last?.id { Divider() }
                    }
                }
            }
        }
    }

    private func deploymentRow(_ d: Deployment) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                RunStatusBadge(status: d.status)
                EnvBadge(env: d.env)
                Text("\(d.previousVersion) → \(d.version)").font(.callout.monospaced()).lineLimit(1)
                Pill(text: d.canaryPercent.map { "\(d.strategy.rawValue) \($0)%" } ?? d.strategy.rawValue)
                Spacer(minLength: 6)
                ActorLabel(actor: d.by).font(.caption)
                Text(Fmt.relative(d.startedAt)).font(.caption).foregroundStyle(.secondary)
            }
            if d.status == .running {
                ProgressView(value: d.progress).controlSize(.small)
            }
            if let first = d.changes.first {
                Text(d.changes.count > 1 ? "\(first) 等 \(d.changes.count) 項變更" : first)
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
    }

    // MARK: 告警

    private var alertsCard: some View {
        let list = app.alerts
            .filter { $0.service == service.id && ($0.state == .firing || $0.state == .acknowledged) }
            .sorted { ($0.severity, $1.startedAt) < ($1.severity, $0.startedAt) }
        return Card {
            VStack(alignment: .leading, spacing: 8) {
                InfraSectionHeader(title: "相關告警", trailing: list.isEmpty ? nil : "\(list.count) 則進行中")
                if list.isEmpty {
                    Label("沒有進行中的告警", systemImage: "checkmark.circle").font(.callout).foregroundStyle(.green)
                } else {
                    ForEach(list) { a in
                        Button { app.go(.monitoring, focus: a.id) } label: { alertRow(a) }
                            .buttonStyle(.plain)
                            .help("到監控與告警查看")
                        if a.id != list.last?.id { Divider() }
                    }
                }
            }
        }
    }

    private func alertRow(_ a: Alert) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Pill(text: a.severity.label, color: a.severity.color)
            VStack(alignment: .leading, spacing: 2) {
                Text(a.title).font(.callout.weight(.medium)).lineLimit(2)
                Text("\(a.rule) · 目前 \(a.value)").font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: 6)
            VStack(alignment: .trailing, spacing: 2) {
                Text(a.state.label).font(.caption.weight(.medium))
                    .foregroundStyle(a.state == .firing ? .red : .secondary)
                Text(Fmt.relative(a.startedAt)).font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
    }
}

func infraMinutes(_ m: Double) -> String {
    if m < 1 { return String(format: "%.0f 秒", m * 60) }
    if m < 120 { return String(format: "%.0f 分鐘", m) }
    return String(format: "%.1f 小時", m / 60)
}
