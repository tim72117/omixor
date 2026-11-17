import SwiftUI

// 總覽:交付與進度(CI/CD、Sprint、錯誤預算、動態牆)。

// MARK: - CI/CD

struct OverviewDeliveryCard: View {
    @EnvironmentObject var app: AppState

    /// 執行中的優先;不足 4 筆時補上最近完成的
    private var runs: [PipelineRun] {
        let inEnv = app.pipelineRuns.filter { $0.targetEnv == nil || $0.targetEnv == app.env }
        let active = inEnv.filter { $0.status == .running || $0.status == .queued || $0.status == .waitingApproval }
            .sorted { $0.startedAt > $1.startedAt }
        let done = inEnv.filter { !active.contains($0) }.sorted { $0.startedAt > $1.startedAt }
        return Array((active + done).prefix(4))
    }

    private var deployments: [Deployment] {
        Array(app.deployments.filter { $0.env == app.env }.sorted { $0.startedAt > $1.startedAt }.prefix(4))
    }

    var body: some View {
        Card {
            OverviewCardHeader(title: "CI/CD", count: app.runningPipelines.count, countColor: .blue,
                               linkTitle: "所有流程") { app.go(.pipelines) }
            if runs.isEmpty && deployments.isEmpty {
                OverviewInlineEmpty(icon: "arrow.triangle.branch", text: "最近沒有流程執行或部署")
            } else {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 300), spacing: 16, alignment: .top)],
                          alignment: .leading, spacing: 16) {
                    VStack(alignment: .leading, spacing: 4) {
                        sectionLabel("流程執行", icon: "gearshape.2")
                        if runs.isEmpty {
                            Text("沒有流程執行").font(.caption).foregroundStyle(.secondary).padding(.vertical, 6)
                        }
                        ForEach(Array(runs.enumerated()), id: \.element.id) { i, run in
                            if i > 0 { Divider() }
                            OverviewRunRow(run: run)
                        }
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        sectionLabel("部署 · \(app.env.label)", icon: "shippingbox")
                        if deployments.isEmpty {
                            Text("\(app.env.label) 最近沒有部署").font(.caption).foregroundStyle(.secondary).padding(.vertical, 6)
                        }
                        ForEach(Array(deployments.enumerated()), id: \.element.id) { i, d in
                            if i > 0 { Divider() }
                            OverviewDeploymentRow(deployment: d)
                        }
                    }
                }
            }
        }
    }

    private func sectionLabel(_ text: String, icon: String) -> some View {
        Label(text, systemImage: icon)
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            .padding(.bottom, 2)
    }
}

struct OverviewRunRow: View {
    @EnvironmentObject var app: AppState
    let run: PipelineRun

    private var progress: Double {
        guard !run.stages.isEmpty else { return run.status == .succeeded ? 1 : 0 }
        let done = run.stages.filter { $0.status == .succeeded }.count
        let partial = run.stages.contains { $0.status == .running } ? 0.5 : 0
        return min(1, (Double(done) + partial) / Double(run.stages.count))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                Image(systemName: run.status.icon)
                    .foregroundStyle(run.status.color)
                    .symbolEffect(.pulse, isActive: run.status == .running)
                Text(run.pipeline).font(.subheadline.weight(.medium)).lineLimit(1)
                Text("#\(run.id)").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                Spacer(minLength: 4)
                if case .ai = run.triggeredBy { AIBadge() }
                if run.status == .failed {
                    Button("重試") { app.retry(runId: run.id) }.controlSize(.small)
                } else if run.status == .running || run.status == .queued {
                    Button("取消") { app.cancel(runId: run.id) }.controlSize(.small)
                }
            }
            Text(run.commitMessage).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            HStack(spacing: 3) {
                ForEach(run.stages) { stage in
                    Capsule()
                        .fill(stage.status.color.opacity(stage.status == .queued ? 0.25 : 0.85))
                        .frame(height: 5)
                        .help("\(stage.name):\(stage.status.label)")
                }
            }
            HStack(spacing: 6) {
                Image(systemName: "arrow.triangle.branch")
                Text(run.branch).lineLimit(1)
                Text(String(run.commitSHA.prefix(7))).monospaced()
                Spacer()
                if run.status == .running {
                    Text("\(Int(progress * 100))%").monospacedDigit()
                } else {
                    Text(run.duration.map { Fmt.duration($0) } ?? Fmt.relative(run.startedAt))
                }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
            if run.status == .failed, let diag = run.aiDiagnosis {
                HStack(alignment: .top, spacing: 4) {
                    Image(systemName: "sparkles").foregroundStyle(.purple)
                    Text(diag).lineLimit(2)
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 6)
        .contentShape(Rectangle())
        .onTapGesture { app.go(.pipelines, focus: String(run.id)) }
    }
}

struct OverviewDeploymentRow: View {
    @EnvironmentObject var app: AppState
    let deployment: Deployment

    var body: some View {
        let d = deployment
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                Image(systemName: d.status.icon).foregroundStyle(d.status.color)
                Text(app.service(d.service)?.name ?? d.service).font(.subheadline.weight(.medium)).lineLimit(1)
                Text(d.version).font(.caption.monospaced()).foregroundStyle(.secondary)
                Spacer(minLength: 4)
                if d.status == .waitingApproval {
                    Button("核准部署") { app.approveDeployment(id: d.id) }
                        .controlSize(.small)
                        .buttonStyle(.borderedProminent)
                } else {
                    RunStatusBadge(status: d.status)
                }
            }
            if d.status == .running {
                ProgressView(value: d.progress)
                    .tint(.blue)
            }
            HStack(spacing: 6) {
                ActorAvatar(actor: d.by, size: 14)
                Text(app.name(of: d.by)).lineLimit(1)
                Text("·")
                Text(d.strategy.rawValue)
                if let c = d.canaryPercent, d.status == .running { Text("canary \(c)%") }
                Spacer()
                Text(Fmt.relative(d.startedAt))
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
        .padding(.vertical, 6)
        .contentShape(Rectangle())
        .onTapGesture { app.go(.pipelines, focus: d.id) }
        .contextMenu {
            Button("在 CI/CD 開啟") { app.go(.pipelines, focus: d.id) }
            if d.status == .succeeded || d.status == .failed {
                Button("回滾到 \(d.previousVersion)") { app.rollback(deploymentId: d.id) }
            }
        }
    }
}

// MARK: - Sprint

struct OverviewSprintCard: View {
    @EnvironmentObject var app: AppState

    private var totalPoints: Int { app.workItems.map(\.points).reduce(0, +) }
    private var donePoints: Int { app.workItems.filter { $0.status == .done }.map(\.points).reduce(0, +) }

    private var daysLeft: Int {
        max(0, Calendar.current.dateComponents([.day], from: .now, to: app.sprint.end).day ?? 0)
    }

    private var timeProgress: Double {
        let total = app.sprint.end.timeIntervalSince(app.sprint.start)
        guard total > 0 else { return 0 }
        return min(1, max(0, Date().timeIntervalSince(app.sprint.start) / total))
    }

    var body: some View {
        let progress = totalPoints > 0 ? Double(donePoints) / Double(totalPoints) : 0
        let behind = totalPoints > 0 && progress + 0.1 < timeProgress
        Card {
            OverviewCardHeader(title: app.sprint.name.isEmpty ? "Sprint" : app.sprint.name,
                               linkTitle: "開發進度") { app.go(.development) }
            if app.workItems.isEmpty {
                OverviewInlineEmpty(icon: "list.bullet.rectangle", text: "這個 Sprint 還沒有工作項目")
            } else {
                if !app.sprint.goal.isEmpty {
                    Text(app.sprint.goal).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                }
                HStack(alignment: .lastTextBaseline, spacing: 6) {
                    Text("\(donePoints)").font(.system(size: 24, weight: .semibold, design: .rounded)).monospacedDigit()
                    Text("/ \(totalPoints) 點").foregroundStyle(.secondary)
                    Spacer()
                    Text(daysLeft > 0 ? "剩 \(daysLeft) 天" : "今天結束")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                ProgressView(value: progress)
                    .tint(behind ? .orange : .green)
                if behind {
                    Label("進度落後時程 \(Int((timeProgress - progress) * 100))%", systemImage: "tortoise")
                        .font(.caption).foregroundStyle(.orange)
                }
                if app.sprint.burndown.count > 1 {
                    Sparkline(values: app.sprint.burndown.map(\.value), color: .indigo)
                        .frame(height: 34)
                        .help("燃盡圖(剩餘點數)")
                }
                HStack(spacing: 10) {
                    ForEach([WorkStatus.todo, .inProgress, .review, .done]) { st in
                        VStack(alignment: .leading, spacing: 1) {
                            Text("\(app.workItems.filter { $0.status == st }.count)")
                                .font(.subheadline.weight(.semibold).monospacedDigit())
                            Text(st.label).font(.caption2).foregroundStyle(.secondary)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                let waitingPRs = app.pullRequests.filter { $0.status == .open }.count
                let aiItems = app.workItems.filter(\.createdByAI).count
                if waitingPRs > 0 || aiItems > 0 {
                    HStack(spacing: 8) {
                        if waitingPRs > 0 { Pill(text: "\(waitingPRs) 個 PR 待審", color: .blue, icon: "arrow.triangle.pull") }
                        if aiItems > 0 { Pill(text: "\(aiItems) 項由 AI 建立", color: .purple, icon: "sparkles") }
                    }
                }
            }
        }
    }
}

// MARK: - 錯誤預算

struct OverviewErrorBudgetCard: View {
    @EnvironmentObject var app: AppState

    private func color(_ v: Double) -> Color { v < 0.2 ? .red : v < 0.5 ? .orange : .green }

    var body: some View {
        let s = app.services
        let tier1 = s.filter { $0.tier == 1 }
        let basis = tier1.isEmpty ? s : tier1
        let overall = basis.isEmpty ? 0 : basis.map(\.errorBudgetRemaining).reduce(0, +) / Double(basis.count)
        let lowest = s.sorted { $0.errorBudgetRemaining < $1.errorBudgetRemaining }.prefix(4)
        Card {
            OverviewCardHeader(title: "錯誤預算", linkTitle: "SLO") { app.go(.monitoring) }
            if s.isEmpty {
                OverviewInlineEmpty(icon: "gauge.with.dots.needle.33percent", text: "沒有設定 SLO 的服務")
            } else {
                HStack(spacing: 16) {
                    ZStack {
                        OverviewRing(progress: overall, color: color(overall), lineWidth: 9)
                        VStack(spacing: 0) {
                            Text("\(Int((overall * 100).rounded()))%")
                                .font(.system(size: 18, weight: .semibold, design: .rounded))
                                .monospacedDigit()
                            Text("剩餘").font(.caption2).foregroundStyle(.secondary)
                        }
                    }
                    .frame(width: 84, height: 84)
                    VStack(alignment: .leading, spacing: 4) {
                        Text(tier1.isEmpty ? "所有服務平均" : "第 1 級服務平均")
                            .font(.caption).foregroundStyle(.secondary)
                        Text(overall < 0.2 ? "預算即將用完,建議暫停高風險部署" :
                             overall < 0.5 ? "預算消耗偏快,留意變更風險" : "預算充足,可正常發版")
                            .font(.subheadline)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                VStack(spacing: 6) {
                    ForEach(Array(lowest)) { svc in
                        Button {
                            app.go(.infrastructure, focus: svc.id)
                        } label: {
                            HStack(spacing: 8) {
                                Text(svc.name).font(.caption).lineLimit(1)
                                    .frame(width: 96, alignment: .leading)
                                GeometryReader { g in
                                    ZStack(alignment: .leading) {
                                        Capsule().fill(Color.primary.opacity(0.08))
                                        Capsule().fill(color(svc.errorBudgetRemaining).gradient)
                                            .frame(width: g.size.width * min(1, max(0, svc.errorBudgetRemaining)))
                                    }
                                }
                                .frame(height: 6)
                                Text("\(Int((svc.errorBudgetRemaining * 100).rounded()))%")
                                    .font(.caption.monospacedDigit())
                                    .foregroundStyle(.secondary)
                                    .frame(width: 36, alignment: .trailing)
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
    }
}

// MARK: - 動態牆

struct OverviewActivityCard: View {
    @EnvironmentObject var app: AppState

    private enum Filter: String, CaseIterable, Identifiable {
        case all = "全部", people = "人員", ai = "AI"
        var id: String { rawValue }
    }

    @State private var filter: Filter = .all
    @State private var showAll = false

    private var items: [ActivityItem] {
        app.activity.filter { item in
            switch filter {
            case .all: return true
            case .people:
                if case .person = item.actor { return true }
                return false
            case .ai:
                if case .ai = item.actor { return true }
                return false
            }
        }
    }

    var body: some View {
        let list = items
        let shown = showAll ? Array(list.prefix(40)) : Array(list.prefix(8))
        Card {
            HStack {
                Text("最近動態").font(.headline)
                Spacer()
                Picker("篩選", selection: $filter) {
                    ForEach(Filter.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 180)
            }
            if shown.isEmpty {
                OverviewInlineEmpty(icon: "clock.arrow.circlepath", text: "還沒有任何動態")
            } else {
                VStack(spacing: 0) {
                    ForEach(Array(shown.enumerated()), id: \.element.id) { i, item in
                        if i > 0 { Divider().padding(.leading, 34) }
                        OverviewActivityRow(item: item)
                    }
                }
                if list.count > 8 {
                    Button(showAll ? "顯示較少" : "顯示更多(共 \(list.count) 筆)") {
                        withAnimation(.easeInOut(duration: 0.15)) { showAll.toggle() }
                    }
                    .buttonStyle(.link)
                    .font(.caption)
                }
            }
        }
    }
}

struct OverviewActivityRow: View {
    @EnvironmentObject var app: AppState
    let item: ActivityItem

    private var isAI: Bool {
        if case .ai = item.actor { return true }
        return false
    }

    var body: some View {
        Button {
            app.go(item.section)
        } label: {
            HStack(alignment: .top, spacing: 10) {
                ActorAvatar(actor: item.actor, size: 24)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(app.name(of: item.actor)).font(.subheadline.weight(.medium)).lineLimit(1)
                        if isAI { AIBadge() }
                        Spacer(minLength: 4)
                        Text(Fmt.relative(item.date)).font(.caption).foregroundStyle(.secondary)
                    }
                    HStack(alignment: .top, spacing: 5) {
                        Image(systemName: item.icon).foregroundStyle(isAI ? .purple : .secondary)
                        Text(item.text).multilineTextAlignment(.leading)
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
            }
            .padding(.vertical, 7)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("前往「\(item.section.title)」")
    }
}
