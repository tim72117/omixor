import SwiftUI

/// 單一執行的詳情:標頭、AI 診斷、階段圖、工作、日誌
struct PipeRunDetail: View {
    @EnvironmentObject var app: AppState
    let runId: Int
    var onOpenRun: (Int) -> Void = { _ in }

    @State private var selectedJobId: String?
    @State private var confirmCancel = false
    @State private var confirmApprove = false
    @State private var showAIFix = false
    @State private var analyzing = false
    @State private var width: CGFloat = 800

    private var run: PipelineRun? { app.pipelineRuns.first { $0.id == runId } }

    var body: some View {
        if let run {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    header(run)
                    if run.status == .failed { diagnosis(run) }
                    if run.status == .waitingApproval { approvalGate(run) }
                    Card("階段", subtitle: stageSubtitle(run)) {
                        if run.stages.isEmpty {
                            Text("這次執行沒有階段資料").font(.callout).foregroundStyle(.secondary)
                        } else {
                            PipeStageGraph(run: run, selectedJobId: selectionBinding(run))
                        }
                    }
                    let layout = width > 860
                        ? AnyLayout(HStackLayout(alignment: .top, spacing: 16))
                        : AnyLayout(VStackLayout(alignment: .leading, spacing: 16))
                    layout {
                        jobsCard(run)
                            .frame(width: width > 860 ? 270 : nil)
                        PipeLogViewer(job: currentJob(run), stageName: stage(of: currentJob(run), in: run)?.name)
                    }
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(GeometryReader { g in
                    Color.clear
                        .onAppear { width = g.size.width }
                        .onChange(of: g.size.width) { width = g.size.width }
                })
            }
            .confirmationDialog("取消 \(run.pipeline) #\(run.id)?", isPresented: $confirmCancel) {
                Button("取消執行", role: .destructive) { app.cicdCancel(runId: run.id) }
                Button("繼續執行", role: .cancel) {}
            } message: {
                Text("執行中的工作會被中止,已完成的階段結果會保留。")
            }
            .confirmationDialog("核准部署到 \(run.targetEnv?.label ?? "Production")?", isPresented: $confirmApprove) {
                Button("核准並部署") { app.cicdApproveGate(runId: run.id) }
                Button("先不要", role: .cancel) {}
            } message: {
                Text("\(run.pipeline) · \(run.cicdShortSHA)「\(run.commitMessage)」會部署到正式環境,影響所有使用者。")
            }
            .sheet(isPresented: $showAIFix) {
                PipeAIFixSheet(run: run) { newId in onOpenRun(newId) }
            }
        } else {
            EmptyStateView(title: "找不到這筆執行", icon: "questionmark.circle")
        }
    }

    // MARK: 選取

    private func selectionBinding(_ run: PipelineRun) -> Binding<String?> {
        Binding(get: { currentJob(run)?.id }, set: { selectedJobId = $0 })
    }

    private func currentJob(_ run: PipelineRun) -> PipelineJob? {
        if let id = selectedJobId, let j = run.cicdJobs.first(where: { $0.id == id }) { return j }
        return run.cicdFocusJob
    }

    private func stage(of job: PipelineJob?, in run: PipelineRun) -> PipelineStage? {
        guard let job else { return nil }
        return run.stages.first { $0.jobs.contains { $0.id == job.id } }
    }

    private func stageSubtitle(_ run: PipelineRun) -> String {
        let done = run.stages.filter { $0.status == .succeeded }.count
        return "\(done)/\(run.stages.count) 個階段完成 · \(run.cicdJobs.count) 個工作"
    }

    // MARK: 標頭

    private func header(_ run: PipelineRun) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 12) {
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 8) {
                        Text(run.pipeline).font(.title3.weight(.semibold)).lineLimit(1)
                        Text("#\(run.id)").font(.title3.monospacedDigit()).foregroundStyle(.secondary)
                        RunStatusBadge(status: run.status)
                        if let env = run.targetEnv { EnvBadge(env: env) }
                        if run.cicdIsAI { AIBadge(text: "AI 觸發") }
                    }
                    Text(run.commitMessage).font(.body).textSelection(.enabled).lineLimit(2)
                }
                Spacer(minLength: 8)
                actions(run)
            }
            metaRow(run)
            if run.status == .running {
                PipeProgressBar(value: run.cicdProgress, color: .blue)
                    .frame(maxWidth: 420)
            }
        }
    }

    private func metaRow(_ run: PipelineRun) -> some View {
        let items = HStackLayout(spacing: 14)
        return ViewThatFits(in: .horizontal) {
            items { metaItems(run) }
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 14) { metaItems(run, part: 0) }
                HStack(spacing: 14) { metaItems(run, part: 1) }
            }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }

    @ViewBuilder private func metaItems(_ run: PipelineRun, part: Int? = nil) -> some View {
        if part == nil || part == 0 {
            Label(run.branch, systemImage: "arrow.triangle.branch").lineLimit(1)
            Button { PipeClipboard.copy(run.commitSHA) } label: {
                Label(run.cicdShortSHA, systemImage: "number").monospaced()
            }
            .buttonStyle(.plain).help("複製完整 SHA")
            Label(app.cicdRepoName(run.repo), systemImage: "shippingbox").lineLimit(1)
        }
        if part == nil || part == 1 {
            Label(run.trigger, systemImage: "bolt").lineLimit(1)
            ActorLabel(actor: run.triggeredBy)
            Label(Fmt.relative(run.startedAt), systemImage: "clock").lineLimit(1)
            Label { PipeElapsedText(run: run) } icon: { Image(systemName: "timer") }
        }
    }

    @ViewBuilder private func actions(_ run: PipelineRun) -> some View {
        HStack(spacing: 8) {
            if run.status == .waitingApproval {
                Button { confirmApprove = true } label: { Label("核准部署", systemImage: "checkmark.seal") }
                    .buttonStyle(.borderedProminent).tint(.purple)
            }
            if run.cicdIsActive || run.status == .waitingApproval {
                Button(role: .destructive) { confirmCancel = true } label: { Label("取消", systemImage: "stop.circle") }
            }
            Button {
                app.retry(runId: run.id)
                if let newest = app.pipelineRuns.first?.id { onOpenRun(newest) }
            } label: { Label("重新執行", systemImage: "arrow.clockwise") }
                .disabled(run.cicdIsActive)
                .help(run.cicdIsActive ? "執行中,完成或取消後才能重新執行" : "用同一個 commit 重新執行")
        }
        .controlSize(.regular)
        .fixedSize()
    }

    // MARK: AI 診斷與核准關卡

    @ViewBuilder private func diagnosis(_ run: PipelineRun) -> some View {
        PipeCallout(tint: .purple, icon: "sparkles") {
            HStack(spacing: 8) {
                Text("AI 診斷").font(.headline)
                AIBadge(text: "自動分析")
                Spacer()
            }
            if let text = run.aiDiagnosis {
                Text(text).font(.callout).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 8) {
                    Button { showAIFix = true } label: { Label("交給 AI 修復", systemImage: "wand.and.stars") }
                        .buttonStyle(.borderedProminent).tint(.purple)
                    if let failed = run.cicdJobs.first(where: { $0.status == .failed }) {
                        Button("查看失敗日誌") { selectedJobId = failed.id }
                    }
                }
                .padding(.top, 2)
            } else {
                Text("這次失敗還沒有 AI 分析。AI 會讀取失敗的日誌與本次變更,整理出可能原因。")
                    .font(.callout).foregroundStyle(.secondary)
                Button {
                    analyzing = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.4) {
                        app.cicdDiagnose(runId: run.id)
                        analyzing = false
                    }
                } label: {
                    HStack(spacing: 6) {
                        if analyzing { ProgressView().controlSize(.small) }
                        Text(analyzing ? "分析中…" : "請 AI 分析")
                    }
                }
                .disabled(analyzing)
            }
        }
    }

    private func approvalGate(_ run: PipelineRun) -> some View {
        PipeCallout(tint: .purple, icon: "hand.raised.fill") {
            Text("等待核准").font(.headline)
            Text("前面的階段都已通過。部署到 \(run.targetEnv?.label ?? "Production") 前需要有人核准。")
                .font(.callout).foregroundStyle(.secondary)
            HStack {
                Button { confirmApprove = true } label: { Label("核准部署", systemImage: "checkmark.seal") }
                    .buttonStyle(.borderedProminent).tint(.purple)
                Button("拒絕", role: .destructive) { app.cicdCancel(runId: run.id) }
            }
        }
    }

    // MARK: 工作列表

    private func jobsCard(_ run: PipelineRun) -> some View {
        let current = currentJob(run)?.id
        return Card("工作") {
            VStack(alignment: .leading, spacing: 10) {
                ForEach(run.stages) { stage in
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Image(systemName: stage.status.icon).foregroundStyle(stage.status.color).font(.caption)
                            Text(stage.name).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                            Spacer()
                            Text(Fmt.duration(stage.duration)).font(.caption2.monospacedDigit()).foregroundStyle(.tertiary)
                        }
                        ForEach(stage.jobs) { job in
                            PipeJobRow(job: job, selected: job.id == current) { selectedJobId = job.id }
                        }
                        if stage.jobs.isEmpty {
                            Text("沒有工作").font(.caption).foregroundStyle(.tertiary).padding(.leading, 20)
                        }
                    }
                }
            }
        }
    }
}

struct PipeJobRow: View {
    let job: PipelineJob
    let selected: Bool
    let action: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                PipeStatusIcon(status: job.status).font(.callout).frame(width: 16)
                Text(job.name).font(.callout).lineLimit(1)
                Spacer(minLength: 4)
                Text(Fmt.duration(job.duration)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(selected ? Color.accentColor.opacity(0.18) : (hover ? Color.primary.opacity(0.05) : .clear))
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
    }
}

/// 有色邊框的提示區塊
struct PipeCallout<Content: View>: View {
    let tint: Color
    let icon: String
    @ViewBuilder var content: Content

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: icon).font(.title2).foregroundStyle(tint).frame(width: 28)
            VStack(alignment: .leading, spacing: 8) { content }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(tint.opacity(0.28)))
    }
}

// MARK: - 交給 AI 修復

struct PipeAIFixSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let run: PipelineRun
    var onStarted: (Int) -> Void

    @State private var openPR = true
    @State private var rerun = true

    private var fixBranch: String { "ai/fix-run-\(run.id)" }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 10) {
                ActorAvatar(actor: app.cicdFixerActor, size: 32)
                VStack(alignment: .leading, spacing: 2) {
                    Text("交給 AI 修復").font(.title3.weight(.semibold))
                    Text("\(run.pipeline) #\(run.id) · \(app.name(of: app.cicdFixerActor))")
                        .font(.callout).foregroundStyle(.secondary)
                }
            }
            if let d = run.aiDiagnosis {
                Text(d).font(.callout)
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.purple.opacity(0.07), in: RoundedRectangle(cornerRadius: 8))
            }
            VStack(alignment: .leading, spacing: 8) {
                Text("AI 會依序執行").font(.headline)
                step(1, "讀取失敗日誌與本次 commit 的差異")
                step(2, "從 \(run.branch) 開出分支 \(fixBranch)")
                step(3, "修改程式碼並在本機跑相關測試")
                if openPR { step(4, "開 PR,附上原因與修改說明,等你審查") }
                if rerun { step(openPR ? 5 : 4, "在修復分支上重新執行 CI 驗證") }
            }
            VStack(alignment: .leading, spacing: 6) {
                Toggle("完成後開 PR(不會直接合併)", isOn: $openPR)
                Toggle("在修復分支重新執行 CI", isOn: $rerun)
            }
            .toggleStyle(.checkbox)
            Label("AI 不會直接推送到 \(run.branch),所有變更都要經過 PR 審查。", systemImage: "lock.shield")
                .font(.caption).foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }.keyboardShortcut(.cancelAction)
                Button { start() } label: { Label("開始修復", systemImage: "wand.and.stars") }
                    .buttonStyle(.borderedProminent).tint(.purple)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(22)
        .frame(width: 480)
    }

    private func step(_ n: Int, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text("\(n)").font(.caption.weight(.bold)).foregroundStyle(.white)
                .frame(width: 18, height: 18).background(Circle().fill(Color.purple))
            Text(text).font(.callout)
        }
    }

    private func start() {
        let actor = app.cicdFixerActor
        app.log(actor, "wand.and.stars", "開始修復 \(run.pipeline) #\(run.id),建立分支 \(fixBranch)", .pipelines)
        if rerun {
            let id = app.cicdStartRun(from: run, trigger: "AI 修復", actor: actor, branch: fixBranch,
                                      message: "fix: 修正 #\(run.id) 的失敗(AI)")
            onStarted(id)
        }
        if openPR {
            app.log(actor, "arrow.triangle.pull", "為 #\(run.id) 的修復開 PR,等待審查", .development)
        }
        dismiss()
    }
}
