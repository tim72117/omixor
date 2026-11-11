import SwiftUI

/// 執行紀錄:每次 AI 執行的逐步過程。
struct AutoRunsView: View {
    @EnvironmentObject var app: AppState
    @Binding var selection: String?
    var openApproval: (String) -> Void
    var openAutomation: (String) -> Void

    @State private var statusFilter: RunStatus?
    @State private var automationFilter: String?

    private var runs: [AutomationRun] {
        app.automationRuns
            .filter { statusFilter == nil || $0.status == statusFilter }
            .filter { automationFilter == nil || $0.automation == automationFilter }
            .sorted { $0.startedAt > $1.startedAt }
    }

    private var selectedRun: AutomationRun? {
        guard let selection else { return nil }
        return app.automationRuns.first { $0.id == selection }
    }

    var body: some View {
        GeometryReader { geo in
            let compact = geo.size.width < 760
            if app.automationRuns.isEmpty {
                EmptyStateView(title: "還沒有執行紀錄", icon: "list.bullet.rectangle",
                               message: "AI 自動化每次執行的過程都會記錄在這裡。")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if compact {
                if let run = selectedRun {
                    VStack(spacing: 0) {
                        HStack {
                            Button { selection = nil } label: { Label("所有紀錄", systemImage: "chevron.left") }
                                .buttonStyle(.borderless)
                            Spacer()
                        }
                        .padding(.horizontal, 16).padding(.vertical, 8)
                        Divider()
                        AutoRunDetail(run: run, openApproval: openApproval, openAutomation: openAutomation)
                    }
                } else {
                    list
                }
            } else {
                HStack(spacing: 0) {
                    list.frame(width: min(380, max(300, geo.size.width * 0.34)))
                    Divider()
                    Group {
                        if let run = selectedRun {
                            AutoRunDetail(run: run, openApproval: openApproval, openAutomation: openAutomation)
                        } else {
                            EmptyStateView(title: "選擇一筆執行紀錄", icon: "sidebar.left",
                                           message: "查看 AI 每一步用了什麼工具、得到什麼結果。")
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .onAppear {
            if selection == nil, let first = runs.first(where: { $0.status == .running || $0.status == .waitingApproval }) ?? runs.first {
                selection = first.id
            }
        }
    }

    private var list: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Picker("狀態", selection: $statusFilter) {
                    Text("所有狀態").tag(RunStatus?.none)
                    ForEach(RunStatus.allCases) { Text($0.label).tag(RunStatus?.some($0)) }
                }
                Picker("流程", selection: $automationFilter) {
                    Text("所有流程").tag(String?.none)
                    ForEach(app.automations) { Text($0.name).tag(String?.some($0.id)) }
                }
            }
            .labelsHidden()
            .padding(10)
            Divider()
            if runs.isEmpty {
                EmptyStateView(title: "沒有符合條件的紀錄", icon: "line.3.horizontal.decrease.circle")
                    .frame(maxHeight: .infinity)
            } else {
                List(selection: $selection) {
                    ForEach(runs) { r in
                        AutoRunRow(run: r).tag(r.id)
                    }
                }
                .listStyle(.inset)
            }
        }
    }
}

struct AutoRunRow: View {
    @EnvironmentObject var app: AppState
    let run: AutomationRun

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            ZStack {
                if run.status == .running {
                    ProgressView().controlSize(.small)
                } else {
                    Image(systemName: run.status.icon).foregroundStyle(run.status.color)
                }
            }
            .frame(width: 18, height: 18)
            VStack(alignment: .leading, spacing: 3) {
                HStack {
                    Text(app.automation(run.automation)?.name ?? run.automation)
                        .font(.callout.weight(.medium)).lineLimit(1)
                    Spacer(minLength: 4)
                    Text(Fmt.relative(run.startedAt)).font(.caption).foregroundStyle(.secondary)
                }
                Text(run.target).font(.caption.monospaced()).foregroundStyle(.indigo).lineLimit(1)
                Text(run.summary).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                if run.status == .waitingApproval {
                    Pill(text: "需要核准", color: .purple, icon: "hand.raised.fill")
                }
            }
        }
        .padding(.vertical, 4)
    }
}

// MARK: - 詳細

struct AutoRunDetail: View {
    @EnvironmentObject var app: AppState
    let run: AutomationRun
    var openApproval: (String) -> Void
    var openAutomation: (String) -> Void

    private var automation: Automation? { app.automation(run.automation) }
    private var approval: ApprovalRequest? { app.autoPendingApproval(for: run.automation) }
    private var doneCount: Int { run.steps.filter { $0.status == .done }.count }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                if run.status == .waitingApproval { approvalBanner }
                metrics
                Card("AI 執行過程", subtitle: run.steps.isEmpty ? "沒有步驟紀錄" : "\(doneCount)/\(run.steps.count) 步完成") {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(run.steps.enumerated()), id: \.element.id) { i, step in
                            AutoStepRow(step: step, index: i + 1, isLast: i == run.steps.count - 1)
                        }
                    }
                }
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 12) {
            AutoIconTile(icon: automation?.icon ?? "sparkles", size: 40)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(automation?.name ?? run.automation).font(.title3.weight(.semibold))
                    RunStatusBadge(status: run.status)
                }
                Text(run.summary).foregroundStyle(.secondary).textSelection(.enabled)
                HStack(spacing: 6) {
                    Text("對象").foregroundStyle(.secondary)
                    AutoChip(text: run.target)
                    Text("· 執行 \(run.id)").foregroundStyle(.tertiary)
                }
                .font(.caption)
            }
            Spacer()
            if automation != nil {
                Button("流程設定") { openAutomation(run.automation) }.controlSize(.small)
            }
        }
    }

    private var approvalBanner: some View {
        HStack(spacing: 10) {
            Image(systemName: "hand.raised.fill").font(.title3).foregroundStyle(.purple)
            VStack(alignment: .leading, spacing: 2) {
                Text("這次執行在等人核准").font(.callout.weight(.semibold))
                Text(approval?.title ?? "AI 已準備好動作,核准後才會繼續。").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if let approval {
                Button("前往核准") { openApproval(approval.id) }
                    .buttonStyle(.borderedProminent).tint(.purple)
            }
        }
        .padding(12)
        .background(Color.purple.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.purple.opacity(0.3)))
    }

    private var metrics: some View {
        let duration = run.finishedAt.map { $0.timeIntervalSince(run.startedAt) }
        return LazyVGrid(columns: [GridItem(.adaptive(minimum: 140), spacing: 12)], spacing: 12) {
            metricTiles(duration)
        }
    }

    @ViewBuilder private func metricTiles(_ duration: TimeInterval?) -> some View {
        MetricTile(title: "開始", value: Fmt.time(run.startedAt), icon: "clock", tint: .indigo)
        MetricTile(title: "耗時", value: run.status == .running ? "進行中" : Fmt.duration(duration), icon: "timer", tint: .blue)
        MetricTile(title: "Tokens", value: Fmt.number(Double(run.tokens)), icon: "text.word.spacing", tint: .purple)
        MetricTile(title: "成本", value: String(format: "$%.3f", run.costUSD), icon: "dollarsign.circle", tint: .green)
    }
}

struct AutoStepRow: View {
    let step: AIStep
    let index: Int
    let isLast: Bool
    @State private var expanded = true

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            VStack(spacing: 0) {
                AutoStepIcon(status: step.status).padding(.top, 1)
                if !isLast {
                    Rectangle().fill(Color.primary.opacity(0.1)).frame(width: 2).frame(maxHeight: .infinity)
                        .padding(.vertical, 3)
                }
            }
            .frame(width: 20)
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    Text("\(index).").foregroundStyle(.tertiary).monospacedDigit()
                    Text(step.title).fontWeight(.medium)
                        .foregroundStyle(step.status == .pending || step.status == .skipped ? .secondary : .primary)
                    if let tool = step.tool {
                        AutoChip(text: tool, icon: "terminal")
                    }
                    Spacer()
                    if step.status == .failed { Pill(text: "失敗", color: .red) }
                    if step.status == .skipped { Pill(text: "略過", color: .secondary) }
                    if step.status == .running { Pill(text: "執行中", color: .blue) }
                    if step.output != nil {
                        Button { withAnimation(.easeInOut(duration: 0.15)) { expanded.toggle() } } label: {
                            Image(systemName: "chevron.right").rotationEffect(.degrees(expanded ? 90 : 0))
                        }
                        .buttonStyle(.borderless)
                        .help(expanded ? "收合輸出" : "展開輸出")
                    }
                }
                if let output = step.output, expanded {
                    AutoCodeBlock(text: output, maxHeight: 200, tint: step.status == .failed ? .red : .primary)
                }
            }
            .padding(.bottom, isLast ? 0 : 12)
        }
    }
}
