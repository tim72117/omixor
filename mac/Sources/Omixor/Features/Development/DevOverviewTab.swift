import Charts
import SwiftUI

struct DevOverviewTab: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    var body: some View {
        PageScroll {
            DevSprintBanner()

            LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 12)], spacing: 12) {
                kpis
            }

            ViewThatFits(in: .horizontal) {
                HStack(alignment: .top, spacing: 16) {
                    DevBurndownCard().frame(minWidth: 420)
                    DevMyReviewsCard().frame(width: 340)
                }
                VStack(spacing: 16) {
                    DevBurndownCard()
                    DevMyReviewsCard()
                }
            }

            ViewThatFits(in: .horizontal) {
                HStack(alignment: .top, spacing: 16) {
                    DevCommitActivityCard().frame(minWidth: 420)
                    DevWorkByTypeCard().frame(width: 340)
                }
                VStack(spacing: 16) {
                    DevCommitActivityCard()
                    DevWorkByTypeCard()
                }
            }
        }
    }

    @ViewBuilder private var kpis: some View {
        let pts = DevMetrics.sprintPoints(app.workItems)
        let completion = pts.total > 0 ? Double(pts.done) / Double(pts.total) * 100 : 0
        let openPRs = app.pullRequests.filter(DevMetrics.isActive)
        let wait = DevMetrics.reviewWaitHours(app.pullRequests)
        let coverage = DevMetrics.avgCoverage(app.repos)
        let lowCoverage = app.repos.filter { $0.coverage < 60 }.count

        MetricTile(title: "Sprint 完成度", value: pts.total > 0 ? Fmt.percent(completion, digits: 0) : "—",
                   delta: pts.total > 0 ? "\(pts.done)/\(pts.total) 點" : nil,
                   icon: "flag.checkered", tint: .green)
        MetricTile(title: "PR 週期時間", value: DevMetrics.hours(DevMetrics.cycleTimeHours(app.pullRequests)),
                   delta: "開啟 → 合併", icon: "timer", tint: .blue)
        MetricTile(title: "進行中 PR", value: "\(openPRs.count)",
                   delta: openPRs.isEmpty ? nil : "\(openPRs.filter { $0.status == .open }.count) 個待審",
                   deltaGood: true, icon: "arrow.triangle.pull", tint: .indigo)
        MetricTile(title: "平均等待審查", value: DevMetrics.hours(wait),
                   delta: (wait ?? 0) > 24 ? "超過 1 天" : nil, deltaGood: false,
                   icon: "hourglass", tint: .orange)
        MetricTile(title: "AI 撰寫的 PR", value: DevMetrics.aiShare(app.pullRequests).map { Fmt.percent($0 * 100, digits: 0) } ?? "—",
                   delta: app.pullRequests.isEmpty ? nil : "\(app.pullRequests.filter { $0.author.devIsAI }.count) 個",
                   icon: "sparkles", tint: .purple)
        MetricTile(title: "平均測試覆蓋率", value: coverage.map { Fmt.percent($0, digits: 0) } ?? "—",
                   delta: lowCoverage > 0 ? "\(lowCoverage) 個低於 60%" : nil, deltaGood: false,
                   icon: "checkmark.shield", tint: .teal)
    }
}

// MARK: - Sprint 橫幅

private struct DevSprintBanner: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        let s = app.sprint
        let pts = DevMetrics.sprintPoints(app.workItems)
        let totalDays = max(1, Calendar.current.dateComponents([.day], from: s.start, to: s.end).day ?? 1)
        let left = max(0, Calendar.current.dateComponents([.day], from: .now, to: s.end).day ?? 0)
        let timeProgress = min(1, max(0, Date.now.timeIntervalSince(s.start) / max(1, s.end.timeIntervalSince(s.start))))
        let workProgress = pts.total > 0 ? Double(pts.done) / Double(pts.total) : 0

        Card {
            HStack(alignment: .top, spacing: 16) {
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Image(systemName: "flag.2.crossed.fill").foregroundStyle(.blue)
                        Text(s.name).font(.headline)
                        Pill(text: s.end > .now ? "剩 \(left) 天" : "已結束", color: left <= 2 ? .orange : .blue)
                    }
                    Text(s.goal.isEmpty ? "尚未設定 Sprint 目標" : s.goal)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                    Text("\(s.start.formatted(.dateTime.month().day())) – \(s.end.formatted(.dateTime.month().day()))・共 \(totalDays) 天")
                        .font(.caption).foregroundStyle(.tertiary)
                }
                Spacer(minLength: 12)
                VStack(alignment: .leading, spacing: 8) {
                    progressRow("時間", timeProgress, .secondary)
                    progressRow("工作", workProgress, workProgress + 0.1 < timeProgress ? .orange : .green)
                }
                .frame(width: 220)
            }
        }
    }

    private func progressRow(_ label: String, _ v: Double, _ color: Color) -> some View {
        HStack(spacing: 8) {
            Text(label).font(.caption).foregroundStyle(.secondary).frame(width: 28, alignment: .leading)
            ProgressView(value: v).tint(color)
            Text(Fmt.percent(v * 100, digits: 0)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                .frame(width: 36, alignment: .trailing)
        }
    }
}

// MARK: - 燃盡圖

private struct DevBurndownCard: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        let s = app.sprint
        Card("燃盡圖", subtitle: "剩餘點數:實際與理想進度") {
            if s.burndown.isEmpty && s.idealBurndown.isEmpty {
                EmptyStateView(title: "沒有燃盡資料", icon: "chart.line.downtrend.xyaxis")
                    .frame(height: 200)
            } else {
                Chart {
                    ForEach(s.idealBurndown) { p in
                        LineMark(x: .value("日期", p.date, unit: .day), y: .value("點數", p.value),
                                 series: .value("類別", "理想"))
                            .foregroundStyle(by: .value("類別", "理想"))
                            .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [5, 4]))
                    }
                    ForEach(s.burndown) { p in
                        AreaMark(x: .value("日期", p.date, unit: .day), y: .value("點數", p.value))
                            .foregroundStyle(.blue.opacity(0.1).gradient)
                        LineMark(x: .value("日期", p.date, unit: .day), y: .value("點數", p.value),
                                 series: .value("類別", "實際"))
                            .foregroundStyle(by: .value("類別", "實際"))
                            .lineStyle(StrokeStyle(lineWidth: 2.5))
                        PointMark(x: .value("日期", p.date, unit: .day), y: .value("點數", p.value))
                            .foregroundStyle(.blue)
                            .symbolSize(18)
                    }
                    if s.start <= .now && s.end >= .now {
                        RuleMark(x: .value("今天", Date.now, unit: .day))
                            .foregroundStyle(.secondary.opacity(0.5))
                            .lineStyle(StrokeStyle(lineWidth: 1, dash: [2, 3]))
                            .annotation(position: .top, alignment: .leading) {
                                Text("今天").font(.caption2).foregroundStyle(.secondary)
                            }
                    }
                }
                .chartForegroundStyleScale(["實際": Color.blue, "理想": Color.gray])
                .chartXAxis {
                    AxisMarks(values: .stride(by: .day, count: 2)) { _ in
                        AxisGridLine()
                        AxisValueLabel(format: .dateTime.month(.defaultDigits).day())
                    }
                }
                .frame(height: 220)

                if let last = s.burndown.last {
                    let ideal = s.idealBurndown.min { abs($0.date.timeIntervalSince(last.date)) < abs($1.date.timeIntervalSince(last.date)) }
                    HStack(spacing: 14) {
                        Label("剩 \(Int(last.value)) 點", systemImage: "circle.fill").foregroundStyle(.blue)
                        if let ideal {
                            let diff = last.value - ideal.value
                            Text(diff > 0.5 ? "落後理想 \(Int(diff.rounded())) 點" : diff < -0.5 ? "超前理想 \(Int((-diff).rounded())) 點" : "與理想進度一致")
                                .foregroundStyle(diff > 0.5 ? .orange : .green)
                        }
                    }
                    .font(.caption)
                }
            }
        }
    }
}

// MARK: - 等我審查的 PR

private struct DevMyReviewsCard: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    var body: some View {
        let mine = app.pullRequests
            .filter { $0.reviewers.contains(app.me) && ($0.status == .open || $0.status == .changesRequested) }
            .sorted { $0.createdAt < $1.createdAt }
        Card("等我審查", subtitle: mine.isEmpty ? "目前沒有待審的 PR" : "\(mine.count) 個 PR 指定你為審查者") {
            if mine.isEmpty {
                HStack {
                    Spacer()
                    VStack(spacing: 6) {
                        Image(systemName: "checkmark.seal").font(.title).foregroundStyle(.green)
                        Text("都審完了").foregroundStyle(.secondary)
                    }
                    .padding(.vertical, 30)
                    Spacer()
                }
            } else {
                VStack(spacing: 0) {
                    ForEach(mine.prefix(6)) { pr in
                        Button { model.showPR(pr.id) } label: { row(pr) }
                            .buttonStyle(.plain)
                        if pr.id != mine.prefix(6).last?.id { Divider() }
                    }
                }
                if mine.count > 6 {
                    Button("查看全部 \(mine.count) 個") { model.tab = .pulls }
                        .buttonStyle(.link)
                }
            }
        }
    }

    private func row(_ pr: PullRequest) -> some View {
        HStack(spacing: 10) {
            ActorAvatar(actor: pr.author, size: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text(pr.title).lineLimit(1)
                HStack(spacing: 6) {
                    Text("#\(pr.id)").monospacedDigit()
                    Text(pr.repo)
                    Text("・等了 \(DevMetrics.age(pr.createdAt))")
                }
                .font(.caption).foregroundStyle(.secondary)
            }
            Spacer(minLength: 4)
            if let r = pr.aiReview {
                Circle().fill(r.risk.color).frame(width: 7, height: 7).help("AI 判斷:\(r.risk.label)")
            }
            DevChecksIcon(status: pr.checks)
            Image(systemName: "chevron.right").font(.caption).foregroundStyle(.tertiary)
        }
        .padding(.vertical, 7)
        .contentShape(Rectangle())
    }
}

// MARK: - Commit 活動

private struct DevCommitActivityCard: View {
    @EnvironmentObject var app: AppState

    private struct Point: Identifiable {
        let id = UUID()
        let week: Date
        let repo: String
        let commits: Int
    }

    private var points: [Point] {
        let cal = Calendar.current
        let thisWeek = cal.dateInterval(of: .weekOfYear, for: .now)?.start ?? .now
        return app.repos.flatMap { repo in
            repo.weeklyCommits.enumerated().map { i, c in
                let offset = repo.weeklyCommits.count - 1 - i
                let week = cal.date(byAdding: .weekOfYear, value: -offset, to: thisWeek) ?? thisWeek
                return Point(week: week, repo: repo.name, commits: c)
            }
        }
    }

    var body: some View {
        let pts = points
        let total = pts.reduce(0) { $0 + $1.commits }
        Card("Commit 活動", subtitle: pts.isEmpty ? "最近 12 週" : "最近 12 週・共 \(total) 個 commit") {
            if pts.isEmpty {
                EmptyStateView(title: "沒有 commit 資料", icon: "chart.bar")
                    .frame(height: 200)
            } else {
                Chart(pts) { p in
                    BarMark(x: .value("週", p.week, unit: .weekOfYear), y: .value("Commit", p.commits))
                        .foregroundStyle(by: .value("Repo", p.repo))
                }
                .chartXAxis {
                    AxisMarks(values: .stride(by: .weekOfYear, count: 2)) { _ in
                        AxisGridLine()
                        AxisValueLabel(format: .dateTime.month(.defaultDigits).day())
                    }
                }
                .chartLegend(position: .bottom, alignment: .leading)
                .frame(height: 220)
            }
        }
    }
}

// MARK: - 工作類型分布

private struct DevWorkByTypeCard: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    var body: some View {
        let items = app.workItems.filter { $0.status != .backlog }
        let maxPts = WorkType.allCases.map { t in items.filter { $0.type == t }.reduce(0) { $0 + $1.points } }.max() ?? 0
        Card("工作類型分布", subtitle: "本 Sprint 點數(不含 Backlog)") {
            if items.isEmpty {
                EmptyStateView(title: "沒有工作項目", icon: "list.bullet.rectangle")
                    .frame(height: 200)
            } else {
                VStack(spacing: 12) {
                    ForEach(WorkType.allCases) { t in
                        let ofType = items.filter { $0.type == t }
                        let total = ofType.reduce(0) { $0 + $1.points }
                        let done = ofType.filter { $0.status == .done }.reduce(0) { $0 + $1.points }
                        VStack(alignment: .leading, spacing: 4) {
                            HStack {
                                DevTypeTag(type: t)
                                Spacer()
                                Text("\(done)/\(total) 點・\(ofType.count) 項")
                                    .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            }
                            GeometryReader { g in
                                let w = maxPts > 0 ? g.size.width * Double(total) / Double(maxPts) : 0
                                ZStack(alignment: .leading) {
                                    Capsule().fill(Color.primary.opacity(0.06))
                                    Capsule().fill(t.color.opacity(0.3)).frame(width: w)
                                    Capsule().fill(t.color.gradient)
                                        .frame(width: total > 0 ? w * Double(done) / Double(total) : 0)
                                }
                            }
                            .frame(height: 8)
                        }
                    }
                    let aiCount = app.workItems.filter(\.createdByAI).count
                    if aiCount > 0 {
                        Divider()
                        HStack {
                            AIBadge()
                            Text("\(aiCount) 個項目由 AI 建立").font(.caption).foregroundStyle(.secondary)
                            Spacer()
                            Button("在看板查看") { model.tab = .board }.buttonStyle(.link).font(.caption)
                        }
                    }
                }
            }
        }
    }
}
