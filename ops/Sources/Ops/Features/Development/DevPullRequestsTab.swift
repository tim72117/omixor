import SwiftUI

struct DevPullRequestsTab: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    @State private var statusFilter = "active"     // active / all / PRStatus.rawValue
    @State private var authorFilter = "all"        // all / me / ai / human
    @State private var query = ""
    @State private var sortOrder = [KeyPathComparator(\PullRequest.updatedAt, order: .reverse)]

    private var rows: [PullRequest] {
        app.pullRequests.filter { pr in
            switch statusFilter {
            case "active": if !DevMetrics.isActive(pr) { return false }
            case "all": break
            default: if pr.status.rawValue != statusFilter { return false }
            }
            if let repo = model.prRepoFilter, pr.repo != repo { return false }
            switch authorFilter {
            case "me": if pr.author != .person(app.me) { return false }
            case "ai": if !pr.author.devIsAI { return false }
            case "human": if pr.author.devIsAI { return false }
            case "review": if !pr.reviewers.contains(app.me) { return false }
            default: break
            }
            if !query.isEmpty {
                let q = query.lowercased()
                if !pr.title.lowercased().contains(q) && !pr.branch.lowercased().contains(q) && "\(pr.id)" != q
                    && !"#\(pr.id)".hasPrefix(q) { return false }
            }
            return true
        }
        .sorted(using: sortOrder)
    }

    var body: some View {
        VStack(spacing: 0) {
            filterBar
                .padding(.horizontal, 20)
                .padding(.vertical, 10)
            Divider()
            GeometryReader { geo in
                if geo.size.width >= 860 {
                    HStack(spacing: 0) {
                        table
                        if model.selectedPR != nil {
                            Divider()
                            detail.frame(width: min(400, geo.size.width * 0.36))
                        }
                    }
                } else {
                    VStack(spacing: 0) {
                        table
                        if model.selectedPR != nil {
                            Divider()
                            detail.frame(height: min(380, geo.size.height * 0.55))
                        }
                    }
                }
            }
        }
    }

    // MARK: 篩選列

    private var filterBar: some View {
        let repoNames = Set(app.pullRequests.map(\.repo)).union(app.repos.map(\.id)).sorted()
        return HStack(spacing: 10) {
            TextField("搜尋標題、分支或編號", text: $query)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 120, maxWidth: 220)
            Picker("狀態", selection: $statusFilter) {
                Text("進行中").tag("active")
                Text("全部狀態").tag("all")
                Divider()
                ForEach(PRStatus.allCases) { s in Text(s.label).tag(s.rawValue) }
            }
            .fixedSize()
            Picker("Repo", selection: $model.prRepoFilter) {
                Text("全部 Repo").tag(String?.none)
                if !repoNames.isEmpty { Divider() }
                ForEach(repoNames, id: \.self) { r in
                    Text(app.repos.first { $0.id == r }?.name ?? r).tag(String?.some(r))
                }
            }
            .fixedSize()
            Picker("作者", selection: $authorFilter) {
                Text("所有作者").tag("all")
                Text("我開的").tag("me")
                Text("等我審查").tag("review")
                Divider()
                Text("AI 撰寫").tag("ai")
                Text("人撰寫").tag("human")
            }
            .fixedSize()
            Spacer(minLength: 0)
            Text("\(rows.count) 個 PR").font(.callout.monospacedDigit()).foregroundStyle(.secondary).fixedSize()
        }
    }

    // MARK: 表格

    @ViewBuilder private var table: some View {
        let data = rows
        if app.pullRequests.isEmpty {
            EmptyStateView(title: "沒有 Pull Request", icon: "arrow.triangle.pull")
        } else {
            Table(data, selection: $model.selectedPR, sortOrder: $sortOrder) {
                TableColumn("#", value: \.id) { pr in
                    Text("#\(pr.id)").monospacedDigit().foregroundStyle(.secondary)
                }
                .width(min: 44, ideal: 54, max: 70)

                TableColumn("標題", value: \.title) { pr in
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 4) {
                            if pr.author.devIsAI {
                                Image(systemName: "sparkles").foregroundStyle(.purple).font(.caption)
                            }
                            Text(pr.title).lineLimit(1)
                        }
                        Text("\(pr.repo) · \(pr.branch)").font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                    .padding(.vertical, 2)
                }
                .width(min: 180, ideal: 320)

                TableColumn("狀態") { pr in
                    Pill(text: pr.status.label, color: pr.status.color)
                }
                .width(min: 60, ideal: 72, max: 90)

                TableColumn("作者") { pr in
                    ActorLabel(actor: pr.author)
                }
                .width(min: 80, ideal: 120, max: 180)

                TableColumn("檢查") { pr in
                    DevChecksIcon(status: pr.checks)
                }
                .width(min: 36, ideal: 40, max: 50)

                TableColumn("變更", value: \.additions) { pr in
                    DevDiffStat(additions: pr.additions, deletions: pr.deletions)
                }
                .width(min: 70, ideal: 90, max: 120)

                TableColumn("審查者") { pr in
                    if pr.reviewers.isEmpty {
                        Text("—").foregroundStyle(.tertiary)
                    } else {
                        DevAvatarStack(ids: pr.reviewers, size: 18)
                    }
                }
                .width(min: 50, ideal: 80, max: 110)

                TableColumn("AI 風險") { pr in
                    if let r = pr.aiReview {
                        HStack(spacing: 4) {
                            Circle().fill(r.risk.color).frame(width: 7, height: 7)
                            Text(r.risk.label).font(.caption)
                        }
                    } else {
                        Text("—").foregroundStyle(.tertiary)
                    }
                }
                .width(min: 50, ideal: 66, max: 90)

                TableColumn("建立", value: \.createdAt) { pr in
                    Text(DevMetrics.age(pr.createdAt)).foregroundStyle(.secondary).monospacedDigit()
                        .help(pr.createdAt.formatted(date: .abbreviated, time: .shortened))
                }
                .width(min: 50, ideal: 60, max: 80)
            }
            .contextMenu(forSelectionType: PullRequest.ID.self) { ids in
                if let id = ids.first, let pr = app.pullRequests.first(where: { $0.id == id }) {
                    DevPRActionsMenu(pr: pr)
                }
            }
            .overlay {
                if data.isEmpty {
                    EmptyStateView(title: "沒有符合條件的 PR", icon: "line.3.horizontal.decrease.circle",
                                   message: "調整上方的篩選條件")
                }
            }
        }
    }

    @ViewBuilder private var detail: some View {
        if let id = model.selectedPR, let pr = app.pullRequests.first(where: { $0.id == id }) {
            DevPRDetail(pr: pr)
        } else {
            EmptyStateView(title: "選擇一個 PR", icon: "arrow.triangle.pull")
        }
    }
}

// MARK: - 動作

enum DevPRActions {
    @MainActor
    static func set(_ app: AppState, _ id: Int, _ status: PRStatus, icon: String, verb: String, note: String = "") {
        guard let i = app.pullRequests.firstIndex(where: { $0.id == id }) else { return }
        app.pullRequests[i].status = status
        app.pullRequests[i].updatedAt = .now
        let trimmed = note.trimmingCharacters(in: .whitespacesAndNewlines)
        let suffix = trimmed.isEmpty ? "" : ":\(trimmed)"
        app.log(.person(app.me), icon, "\(verb) PR #\(id)「\(app.pullRequests[i].title)」\(suffix)", .development)
    }

    @MainActor static func approve(_ app: AppState, _ id: Int, note: String = "") { set(app, id, .approved, icon: "checkmark.seal", verb: "核准", note: note) }
    @MainActor static func requestChanges(_ app: AppState, _ id: Int, note: String = "") { set(app, id, .changesRequested, icon: "exclamationmark.bubble", verb: "要求修改", note: note) }
    @MainActor static func merge(_ app: AppState, _ id: Int) { set(app, id, .merged, icon: "arrow.triangle.merge", verb: "合併") }
    @MainActor static func close(_ app: AppState, _ id: Int) { set(app, id, .closed, icon: "xmark.circle", verb: "關閉") }
    @MainActor static func reopen(_ app: AppState, _ id: Int) { set(app, id, .open, icon: "arrow.uturn.backward.circle", verb: "重新開啟") }

    @MainActor
    static func joinReview(_ app: AppState, _ id: Int) {
        guard let i = app.pullRequests.firstIndex(where: { $0.id == id }),
              !app.pullRequests[i].reviewers.contains(app.me) else { return }
        app.pullRequests[i].reviewers.append(app.me)
    }

    static func canMerge(_ pr: PullRequest) -> Bool { pr.status == .approved && pr.checks == .passed }
    static func canReview(_ pr: PullRequest) -> Bool { pr.status == .open || pr.status == .changesRequested || pr.status == .approved }
}

private struct DevPRActionsMenu: View {
    @EnvironmentObject var app: AppState
    let pr: PullRequest
    var body: some View {
        if DevPRActions.canReview(pr) {
            Button("核准") { DevPRActions.approve(app, pr.id) }.disabled(pr.status == .approved)
            Button("要求修改") { DevPRActions.requestChanges(app, pr.id) }.disabled(pr.status == .changesRequested)
        }
        if DevPRActions.canMerge(pr) {
            Button("合併") { DevPRActions.merge(app, pr.id) }
        }
        Divider()
        if DevMetrics.isActive(pr) {
            Button("關閉 PR") { DevPRActions.close(app, pr.id) }
        } else if pr.status == .closed {
            Button("重新開啟") { DevPRActions.reopen(app, pr.id) }
        }
        Button("複製分支名稱") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(pr.branch, forType: .string)
        }
    }
}

// MARK: - 詳細面板

struct DevPRDetail: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel
    let pr: PullRequest
    @State private var comment = ""
    @State private var confirmMerge = false

    private var linked: WorkItem? { pr.linkedItem.flatMap { id in app.workItems.first { $0.id == id } } }
    private var run: PipelineRun? {
        app.pipelineRuns.filter { $0.repo == pr.repo && $0.branch == pr.branch }.max { $0.startedAt < $1.startedAt }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                actions
                if let r = pr.aiReview { aiReview(r) }
                checks
                if let item = linked { linkedItem(item) } else if let id = pr.linkedItem { missingItem(id) }
                people
            }
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .id(pr.id)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text("#\(pr.id)").font(.callout.monospacedDigit()).foregroundStyle(.secondary)
                Pill(text: pr.status.label, color: pr.status.color)
                if pr.author.devIsAI { AIBadge(text: "AI 撰寫") }
                Spacer()
                Button { model.selectedPR = nil } label: { Image(systemName: "xmark") }
                    .buttonStyle(.borderless)
                    .help("關閉詳細")
            }
            Text(pr.title).font(.title3.weight(.semibold)).fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 6) {
                Image(systemName: "arrow.triangle.branch").foregroundStyle(.secondary)
                Text(pr.branch).font(.callout.monospaced()).lineLimit(1).truncationMode(.middle)
                Image(systemName: "arrow.right").font(.caption2).foregroundStyle(.tertiary)
                Text(app.repos.first { $0.id == pr.repo }?.defaultBranch ?? "main").font(.callout.monospaced())
            }
            .foregroundStyle(.secondary)
            HStack(spacing: 12) {
                Text(pr.repo).font(.callout)
                DevDiffStat(additions: pr.additions, deletions: pr.deletions)
                Text("建立於 \(Fmt.relative(pr.createdAt))・更新 \(Fmt.relative(pr.updatedAt))")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder private var actions: some View {
        if DevMetrics.isActive(pr) {
            VStack(alignment: .leading, spacing: 8) {
                if DevPRActions.canReview(pr) {
                    TextField("審查留言(選填)", text: $comment, axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .lineLimit(1...4)
                }
                HStack(spacing: 8) {
                    if DevPRActions.canReview(pr) {
                        Button {
                            DevPRActions.approve(app, pr.id, note: comment); comment = ""
                        } label: { Label("核准", systemImage: "checkmark") }
                            .buttonStyle(.borderedProminent)
                            .tint(.green)
                            .disabled(pr.status == .approved)
                        Button {
                            DevPRActions.requestChanges(app, pr.id, note: comment); comment = ""
                        } label: { Label("要求修改", systemImage: "exclamationmark.bubble") }
                            .disabled(pr.status == .changesRequested)
                    }
                    if pr.status == .approved {
                        Button {
                            confirmMerge = true
                        } label: { Label("合併", systemImage: "arrow.triangle.merge") }
                            .buttonStyle(.borderedProminent)
                            .tint(.purple)
                            .disabled(!DevPRActions.canMerge(pr))
                            .help(pr.checks == .passed ? "合併到預設分支" : "檢查尚未通過,不能合併")
                    }
                    Spacer(minLength: 0)
                    Menu {
                        if !pr.reviewers.contains(app.me) {
                            Button("加入審查") { DevPRActions.joinReview(app, pr.id) }
                        }
                        Button("請 AI 助理說明這個 PR") { app.showAssistant = true }
                        Divider()
                        Button("關閉 PR", role: .destructive) { DevPRActions.close(app, pr.id) }
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                    .menuStyle(.borderlessButton)
                    .menuIndicator(.hidden)
                    .fixedSize()
                }
                .controlSize(.regular)
            }
            .confirmationDialog("合併 PR #\(pr.id)?", isPresented: $confirmMerge) {
                Button("合併") { DevPRActions.merge(app, pr.id) }
                Button("取消", role: .cancel) {}
            } message: {
                Text("會把 \(pr.branch) 合併到預設分支,並觸發部署流程。")
            }
        } else if pr.status == .closed {
            Button { DevPRActions.reopen(app, pr.id) } label: { Label("重新開啟", systemImage: "arrow.uturn.backward") }
        } else {
            Label("已於 \(Fmt.relative(pr.updatedAt)) 合併", systemImage: "arrow.triangle.merge")
                .foregroundStyle(.purple)
        }
    }

    private func aiReview(_ r: AIReview) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "sparkles").foregroundStyle(.purple)
                Text("AI 審查").font(.subheadline.weight(.semibold))
                Spacer()
                Pill(text: r.risk.label, color: r.risk.color, icon: r.risk == .high ? "exclamationmark.triangle.fill" : nil)
            }
            Text(r.summary).font(.callout).fixedSize(horizontal: false, vertical: true)
            if !r.findings.isEmpty {
                VStack(alignment: .leading, spacing: 5) {
                    ForEach(Array(r.findings.enumerated()), id: \.offset) { _, f in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Image(systemName: "smallcircle.filled.circle").font(.system(size: 6))
                                .foregroundStyle(r.risk.color)
                            Text(f).font(.callout).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.purple.opacity(0.07), in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.purple.opacity(0.18)))
    }

    private var checks: some View {
        DevDetailSection(title: "CI 檢查") {
            HStack(spacing: 8) {
                DevChecksIcon(status: pr.checks, showLabel: true)
                if let run {
                    Text("\(run.pipeline) #\(run.id)").font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                Button(run == nil ? "前往 CI/CD" : "查看執行") {
                    app.go(.pipelines, focus: run.map { String($0.id) })
                }
                .buttonStyle(.link)
                .font(.callout)
            }
            if let stages = run?.stages, !stages.isEmpty {
                HStack(spacing: 4) {
                    ForEach(stages) { s in
                        VStack(spacing: 3) {
                            Capsule().fill(s.status.color.gradient).frame(height: 5)
                            Text(s.name).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                        }
                        .help("\(s.name):\(s.status.label)")
                    }
                }
            }
            if let d = run?.aiDiagnosis, pr.checks == .failed {
                Label(d, systemImage: "sparkles").font(.caption).foregroundStyle(.purple)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func linkedItem(_ item: WorkItem) -> some View {
        DevDetailSection(title: "相關工作項目") {
            Button { model.showItem(item.id) } label: {
                HStack(spacing: 8) {
                    RoundedRectangle(cornerRadius: 2).fill(item.type.color).frame(width: 3, height: 30)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(item.title).lineLimit(2)
                        HStack(spacing: 6) {
                            Text(item.id).monospaced()
                            Text("·")
                            Label(item.status.label, systemImage: item.status.devIcon)
                            Text("· \(item.points) 點")
                        }
                        .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 4)
                    Image(systemName: "chevron.right").font(.caption).foregroundStyle(.tertiary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("在 Sprint 看板中顯示")
        }
    }

    private func missingItem(_ id: String) -> some View {
        DevDetailSection(title: "相關工作項目") {
            Text(id).font(.callout.monospaced()).foregroundStyle(.secondary)
        }
    }

    private var people: some View {
        DevDetailSection(title: "作者與審查者") {
            HStack {
                Text("作者").foregroundStyle(.secondary).frame(width: 50, alignment: .leading)
                ActorLabel(actor: pr.author)
            }
            .font(.callout)
            HStack(alignment: .top) {
                Text("審查者").foregroundStyle(.secondary).frame(width: 50, alignment: .leading)
                if pr.reviewers.isEmpty {
                    Text("尚未指定").foregroundStyle(.tertiary)
                } else {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(pr.reviewers, id: \.self) { id in ActorLabel(actor: .person(id)) }
                    }
                }
            }
            .font(.callout)
        }
    }
}

struct DevDetailSection<Content: View>: View {
    let title: String
    @ViewBuilder var content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.subheadline.weight(.semibold)).foregroundStyle(.secondary)
            content
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.primary.opacity(0.07)))
    }
}
