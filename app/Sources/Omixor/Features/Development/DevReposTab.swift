import SwiftUI

struct DevReposTab: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    private enum Layout: String { case grid, table }
    @State private var layout: Layout = .grid
    @State private var query = ""
    @State private var sortOrder = [KeyPathComparator(\Repository.lastCommit, order: .reverse)]
    @State private var selection: Repository.ID?

    private var rows: [Repository] {
        app.repos
            .filter { query.isEmpty || $0.name.lowercased().contains(query.lowercased()) || $0.language.lowercased().contains(query.lowercased()) }
            .sorted(using: sortOrder)
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                TextField("搜尋 Repo 或語言", text: $query)
                    .textFieldStyle(.roundedBorder)
                    .frame(minWidth: 120, maxWidth: 220)
                Picker("排序", selection: Binding(
                    get: { sortKey },
                    set: { setSort($0) })) {
                    Text("最近 commit").tag("lastCommit")
                    Text("名稱").tag("name")
                    Text("覆蓋率").tag("coverage")
                    Text("進行中 PR").tag("openPRs")
                    Text("弱點").tag("vulnerabilities")
                }
                .fixedSize()
                Spacer(minLength: 0)
                summary
                Picker("檢視", selection: $layout) {
                    Image(systemName: "square.grid.2x2").tag(Layout.grid).help("卡片")
                    Image(systemName: "list.bullet").tag(Layout.table).help("表格")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 10)
            Divider()

            if app.repos.isEmpty {
                EmptyStateView(title: "沒有 Repository", icon: "shippingbox")
            } else if layout == .grid {
                PageScroll {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 300), spacing: 14)], spacing: 14) {
                        ForEach(rows) { DevRepoCard(repo: $0) }
                    }
                }
            } else {
                table
            }
        }
    }

    private var summary: some View {
        let vulns = app.repos.reduce(0) { $0 + $1.vulnerabilities }
        return HStack(spacing: 12) {
            Text("\(app.repos.count) 個 Repo").foregroundStyle(.secondary)
            if vulns > 0 {
                Label("\(vulns) 個弱點", systemImage: "exclamationmark.shield.fill").foregroundStyle(.red)
            }
        }
        .font(.callout)
        .lineLimit(1)
        .fixedSize()
    }

    // 下拉排序與表格排序共用 sortOrder
    private var sortKey: String {
        switch sortOrder.first?.keyPath {
        case \Repository.name: return "name"
        case \Repository.coverage: return "coverage"
        case \Repository.openPRs: return "openPRs"
        case \Repository.vulnerabilities: return "vulnerabilities"
        default: return "lastCommit"
        }
    }

    private func setSort(_ key: String) {
        switch key {
        case "name": sortOrder = [KeyPathComparator(\Repository.name)]
        case "coverage": sortOrder = [KeyPathComparator(\Repository.coverage)]
        case "openPRs": sortOrder = [KeyPathComparator(\Repository.openPRs, order: .reverse)]
        case "vulnerabilities": sortOrder = [KeyPathComparator(\Repository.vulnerabilities, order: .reverse)]
        default: sortOrder = [KeyPathComparator(\Repository.lastCommit, order: .reverse)]
        }
    }

    private var table: some View {
        Table(rows, selection: $selection, sortOrder: $sortOrder) {
            TableColumn("名稱", value: \.name) { r in
                VStack(alignment: .leading, spacing: 1) {
                    Text(r.name).fontWeight(.medium)
                    Text(r.defaultBranch).font(.caption.monospaced()).foregroundStyle(.secondary)
                }
            }
            .width(min: 120, ideal: 180)
            TableColumn("語言", value: \.language) { r in Text(r.language) }
                .width(min: 60, ideal: 90, max: 120)
            TableColumn("覆蓋率", value: \.coverage) { r in DevCoverageBar(value: r.coverage) }
                .width(min: 100, ideal: 140, max: 200)
            TableColumn("進行中 PR", value: \.openPRs) { r in
                Text("\(r.openPRs)").monospacedDigit()
            }
            .width(min: 60, ideal: 70, max: 90)
            TableColumn("弱點", value: \.vulnerabilities) { r in
                DevVulnLabel(count: r.vulnerabilities)
            }
            .width(min: 50, ideal: 70, max: 90)
            TableColumn("最近 commit", value: \.lastCommit) { r in
                Text(Fmt.relative(r.lastCommit)).foregroundStyle(.secondary)
            }
            .width(min: 70, ideal: 90, max: 120)
            TableColumn("12 週 commit") { r in
                Sparkline(values: r.weeklyCommits.map(Double.init), color: .blue)
                    .frame(height: 20)
            }
            .width(min: 80, ideal: 120, max: 180)
        }
        .contextMenu(forSelectionType: Repository.ID.self) { ids in
            if let id = ids.first {
                Button("查看 Pull Requests") { model.showRepoPRs(id) }
                Button("前往 CI/CD") { app.go(.pipelines, focus: id) }
            }
        } primaryAction: { ids in
            if let id = ids.first { model.showRepoPRs(id) }
        }
    }
}

struct DevVulnLabel: View {
    let count: Int
    var body: some View {
        if count == 0 {
            Label("0", systemImage: "checkmark.shield").foregroundStyle(.green)
        } else {
            Label("\(count)", systemImage: "exclamationmark.shield.fill").foregroundStyle(count >= 3 ? .red : .orange)
        }
    }
}

// MARK: - Repo 卡片

private struct DevRepoCard: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel
    let repo: Repository

    private var services: [Service] { app.services.filter { $0.repo == repo.id } }
    private var activePRs: [PullRequest] { app.pullRequests.filter { $0.repo == repo.id && DevMetrics.isActive($0) } }

    var body: some View {
        let total = repo.weeklyCommits.reduce(0, +)
        let lastWeek = repo.weeklyCommits.last ?? 0
        let prevWeek = repo.weeklyCommits.dropLast().last ?? 0
        let openCount = max(repo.openPRs, activePRs.count)

        Card {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: "shippingbox.fill").foregroundStyle(.blue)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(repo.name).font(.headline).lineLimit(1)
                        HStack(spacing: 4) {
                            Image(systemName: "arrow.triangle.branch")
                            Text(repo.defaultBranch).monospaced()
                            Text("・最近 commit \(Fmt.relative(repo.lastCommit))")
                        }
                        .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                    Spacer(minLength: 4)
                    Pill(text: repo.language, color: .secondary)
                }

                VStack(alignment: .leading, spacing: 4) {
                    Text("測試覆蓋率").font(.caption).foregroundStyle(.secondary)
                    DevCoverageBar(value: repo.coverage)
                }

                HStack(spacing: 0) {
                    stat("進行中 PR") {
                        Button { model.showRepoPRs(repo.id) } label: {
                            Text("\(openCount)").font(.title3.weight(.semibold)).monospacedDigit()
                        }
                        .buttonStyle(.plain)
                        .help("查看這個 Repo 的 PR")
                    }
                    Divider().frame(height: 30)
                    stat("弱點") {
                        DevVulnLabel(count: repo.vulnerabilities).font(.title3.weight(.semibold))
                    }
                    Divider().frame(height: 30)
                    stat("本週 commit") {
                        HStack(alignment: .firstTextBaseline, spacing: 4) {
                            Text("\(lastWeek)").font(.title3.weight(.semibold)).monospacedDigit()
                            if prevWeek > 0 {
                                let d = lastWeek - prevWeek
                                Text(d >= 0 ? "+\(d)" : "\(d)").font(.caption).foregroundStyle(d >= 0 ? .green : .orange)
                            }
                        }
                    }
                }

                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text("最近 12 週 commit").font(.caption).foregroundStyle(.secondary)
                        Spacer()
                        Text("共 \(total)").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    }
                    if repo.weeklyCommits.isEmpty {
                        Text("沒有資料").font(.caption).foregroundStyle(.tertiary).frame(height: 34)
                    } else {
                        Sparkline(values: repo.weeklyCommits.map(Double.init), color: .blue).frame(height: 34)
                    }
                }

                if !services.isEmpty {
                    HStack(spacing: 6) {
                        Text("部署為").font(.caption).foregroundStyle(.secondary)
                        ForEach(services.prefix(3)) { s in
                            Button { app.go(.infrastructure, focus: s.id) } label: {
                                HStack(spacing: 3) {
                                    Circle().fill(s.health.color).frame(width: 6, height: 6)
                                    Text(s.name)
                                }
                                .font(.caption)
                                .padding(.horizontal, 6).padding(.vertical, 2)
                                .background(Color.primary.opacity(0.06), in: Capsule())
                            }
                            .buttonStyle(.plain)
                            .help("前往服務 \(s.name)")
                        }
                        if services.count > 3 {
                            Text("+\(services.count - 3)").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
        .contextMenu {
            Button("查看 Pull Requests") { model.showRepoPRs(repo.id) }
            Button("前往 CI/CD") { app.go(.pipelines, focus: repo.id) }
        }
    }

    private func stat<V: View>(_ title: String, @ViewBuilder _ value: () -> V) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            value()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.leading, 8)
    }
}
