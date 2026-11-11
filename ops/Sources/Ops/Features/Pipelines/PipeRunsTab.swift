import SwiftUI

/// 執行紀錄:左邊列表、右邊詳情
struct PipeRunsTab: View {
    @EnvironmentObject var app: AppState
    @Binding var selectedRunId: Int?
    @State private var statusFilter: RunStatus?
    @State private var repoFilter: String?
    @State private var onlyFailed = false
    @State private var onlyAI = false
    @State private var query = ""

    private var filtered: [PipelineRun] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return app.pipelineRuns.filter { r in
            if onlyFailed && r.status != .failed { return false }
            if onlyAI && !r.cicdIsAI { return false }
            if let s = statusFilter, r.status != s { return false }
            if let repo = repoFilter, r.repo != repo { return false }
            if !q.isEmpty {
                let fields = [r.pipeline, r.branch, r.commitMessage, r.commitSHA, "#\(r.id)", app.name(of: r.triggeredBy)]
                return fields.contains { $0.lowercased().contains(q) }
            }
            return true
        }
        .sorted { $0.startedAt > $1.startedAt }
    }

    private var repoIds: [String] { Array(Set(app.pipelineRuns.map(\.repo))).sorted() }

    private var hasFilters: Bool { statusFilter != nil || repoFilter != nil || onlyFailed || onlyAI || !query.isEmpty }

    var body: some View {
        let runs = filtered
        GeometryReader { g in
            HStack(spacing: 0) {
                listPane(runs)
                    .frame(width: min(400, max(230, g.size.width * 0.32)))
                Divider()
                detailPane(runs)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .onAppear { ensureSelection(runs) }
        .onChange(of: runs.map(\.id)) { ensureSelection(filtered) }
    }

    private func ensureSelection(_ runs: [PipelineRun]) {
        if let id = selectedRunId, app.pipelineRuns.contains(where: { $0.id == id }) { return }
        selectedRunId = runs.first?.id
    }

    // MARK: 列表

    private func listPane(_ runs: [PipelineRun]) -> some View {
        VStack(spacing: 0) {
            filterBar
            Divider()
            if runs.isEmpty {
                EmptyStateView(title: app.pipelineRuns.isEmpty ? "還沒有任何執行" : "沒有符合的執行",
                               icon: "arrow.triangle.branch",
                               message: hasFilters ? "調整篩選條件試試看" : nil)
                    .frame(maxHeight: .infinity)
            } else {
                List(selection: $selectedRunId) {
                    ForEach(runs) { run in
                        PipeRunRow(run: run).tag(run.id)
                            .contextMenu { rowMenu(run) }
                    }
                }
                .listStyle(.inset)
                .scrollContentBackground(.hidden)
            }
            Divider()
            HStack {
                Text("\(runs.count) 筆執行").font(.caption).foregroundStyle(.secondary)
                Spacer()
                if hasFilters {
                    Button("清除篩選") {
                        statusFilter = nil; repoFilter = nil; onlyFailed = false; onlyAI = false; query = ""
                    }
                    .buttonStyle(.link).font(.caption)
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 6)
        }
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.5))
    }

    @ViewBuilder private func rowMenu(_ run: PipelineRun) -> some View {
        Button("重新執行") { app.retry(runId: run.id) }.disabled(run.cicdIsActive)
        if run.cicdIsActive { Button("取消") { app.cicdCancel(runId: run.id) } }
        Divider()
        Button("複製 commit SHA") { PipeClipboard.copy(run.commitSHA) }
        Button("複製執行編號") { PipeClipboard.copy("#\(run.id)") }
    }

    private var filterBar: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("搜尋 pipeline、分支、commit…", text: $query)
                    .textFieldStyle(.plain)
                if !query.isEmpty {
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.plain).foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
            .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.primary.opacity(0.1)))

            HStack(spacing: 8) {
                Picker("狀態", selection: $statusFilter) {
                    Text("所有狀態").tag(RunStatus?.none)
                    Divider()
                    ForEach(RunStatus.allCases) { s in Text(s.label).tag(RunStatus?.some(s)) }
                }
                .labelsHidden().frame(maxWidth: .infinity)
                .disabled(onlyFailed)

                Picker("Repo", selection: $repoFilter) {
                    Text("所有 repo").tag(String?.none)
                    Divider()
                    ForEach(repoIds, id: \.self) { r in Text(app.cicdRepoName(r)).tag(String?.some(r)) }
                }
                .labelsHidden().frame(maxWidth: .infinity)
                Spacer(minLength: 0)
            }
            .controlSize(.small)

            HStack(spacing: 14) {
                Toggle("只看失敗", isOn: $onlyFailed)
                Toggle(isOn: $onlyAI) { Label("AI 觸發", systemImage: "sparkles") }
            }
            .toggleStyle(.checkbox)
            .controlSize(.small)
        }
        .padding(10)
    }

    // MARK: 詳情

    @ViewBuilder private func detailPane(_ runs: [PipelineRun]) -> some View {
        if let id = selectedRunId, app.pipelineRuns.contains(where: { $0.id == id }) {
            PipeRunDetail(runId: id, onOpenRun: { selectedRunId = $0 })
                .id(id)
        } else {
            EmptyStateView(title: "選一筆執行", icon: "sidebar.left", message: "左邊選一筆執行就能看到階段、工作與日誌")
        }
    }
}

/// 列表的一列
struct PipeRunRow: View {
    @EnvironmentObject var app: AppState
    let run: PipelineRun

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            PipeStatusIcon(status: run.status)
                .font(.title3)
                .frame(width: 22)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(run.pipeline).font(.callout.weight(.semibold)).lineLimit(1)
                    Spacer(minLength: 4)
                    Text("#\(run.id)").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
                Text(run.commitMessage)
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                HStack(spacing: 6) {
                    Label(run.branch, systemImage: "arrow.triangle.branch")
                        .lineLimit(1).truncationMode(.middle)
                    Text(run.cicdShortSHA).monospaced().foregroundStyle(.tertiary)
                    if let env = run.targetEnv { EnvBadge(env: env).scaleEffect(0.9) }
                }
                .font(.caption2).foregroundStyle(.secondary)

                if run.status == .running {
                    PipeProgressBar(value: run.cicdProgress, color: .blue)
                }

                HStack(spacing: 5) {
                    ActorAvatar(actor: run.triggeredBy, size: 14)
                    Text(app.name(of: run.triggeredBy)).lineLimit(1)
                        .foregroundStyle(run.cicdIsAI ? Color.purple : Color.secondary)
                    Text("·").foregroundStyle(.tertiary)
                    Text(run.trigger).lineLimit(1)
                    Spacer(minLength: 4)
                    Text(Fmt.relative(run.startedAt))
                    Image(systemName: "timer").foregroundStyle(.tertiary)
                    PipeElapsedText(run: run)
                }
                .font(.caption2).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 4)
    }
}
