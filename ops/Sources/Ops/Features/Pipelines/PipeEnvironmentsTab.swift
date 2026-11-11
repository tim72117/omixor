import Charts
import SwiftUI

/// 環境:服務 × 環境版本矩陣、DORA 指標、每日執行
struct PipeEnvironmentsTab: View {
    @EnvironmentObject var app: AppState
    var openDeployments: (String) -> Void = { _ in }

    @State private var onlyDrift = false
    @State private var promote: PipePromotion?

    private let envOrder: [DeployEnv] = [.development, .staging, .production]

    var body: some View {
        PageScroll {
            doraTiles
            matrixCard
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 360), spacing: 16)], alignment: .leading, spacing: 16) {
                runsPerDayCard
                successRateCard
            }
        }
        .confirmationDialog(promote?.title ?? "", isPresented: Binding(get: { promote != nil }, set: { if !$0 { promote = nil } }),
                            presenting: promote) { p in
            Button(p.to == .production ? "建立部署(需核准)" : "開始部署") { start(p) }
            Button("取消", role: .cancel) {}
        } message: { p in
            Text(p.message(app))
        }
    }

    // MARK: DORA

    private var doraTiles: some View {
        let m = PipeDORA(app: app)
        return VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("DORA 指標").font(.headline)
                Text("Production · 最近 7 天").font(.caption).foregroundStyle(.secondary)
                Spacer()
            }
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 180), spacing: 12)], spacing: 12) {
                MetricTile(title: "部署頻率", value: m.frequencyText, delta: m.frequencyRating.label,
                           deltaGood: m.frequencyRating.good, icon: "shippingbox", tint: .blue, series: m.dailyDeploys)
                MetricTile(title: "變更前置時間", value: Fmt.duration(m.leadTime), delta: m.leadTime.map { PipeRating.lead($0).label },
                           deltaGood: m.leadTime.map { PipeRating.lead($0).good } ?? true, icon: "hourglass", tint: .teal)
                MetricTile(title: "變更失敗率", value: m.failureRate.map { Fmt.percent($0 * 100, digits: 0) } ?? "—",
                           delta: m.failureRate.map { PipeRating.cfr($0).label },
                           deltaGood: m.failureRate.map { PipeRating.cfr($0).good } ?? true,
                           icon: "exclamationmark.triangle", tint: .orange)
                MetricTile(title: "平均恢復時間", value: Fmt.duration(m.mttr), delta: m.mttr.map { PipeRating.mttr($0).label },
                           deltaGood: m.mttr.map { PipeRating.mttr($0).good } ?? true, icon: "cross.case", tint: .pink)
            }
        }
    }

    // MARK: 版本矩陣

    private var matrixCard: some View {
        let services = app.services
            .filter { !onlyDrift || drift($0) }
            .sorted { ($0.tier, $0.name) < ($1.tier, $1.name) }
        let driftCount = app.services.filter(drift).count
        return Card {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("版本矩陣").font(.headline)
                    Text(driftCount == 0 ? "所有服務的 Staging 與 Production 版本一致" : "\(driftCount) 個服務的 Production 落後 Staging")
                        .font(.caption).foregroundStyle(driftCount == 0 ? Color.secondary : Color.orange)
                }
                Spacer()
                Toggle("只看版本不一致", isOn: $onlyDrift).toggleStyle(.checkbox).controlSize(.small)
            }
            if app.services.isEmpty {
                EmptyStateView(title: "沒有服務", icon: "square.stack.3d.up.slash").frame(minHeight: 160)
            } else if services.isEmpty {
                Text("沒有版本不一致的服務").font(.callout).foregroundStyle(.secondary).padding(.vertical, 8)
            } else {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 0) {
                    GridRow {
                        Text("服務")
                        ForEach(envOrder) { e in
                            HStack(spacing: 4) {
                                Circle().fill(e.color).frame(width: 6, height: 6)
                                Text(e.label)
                            }
                            .fontWeight(e == app.env ? .bold : .regular)
                            .foregroundStyle(e == app.env ? Color.primary : Color.secondary)
                        }
                        Text("")
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.bottom, 6)
                    ForEach(services) { s in
                        Divider().gridCellUnsizedAxes(.horizontal)
                        matrixRow(s)
                    }
                }
            }
        }
    }

    private func matrixRow(_ s: Service) -> some View {
        GridRow(alignment: .center) {
            HStack(spacing: 6) {
                HealthBadge(health: s.health, compact: true)
                VStack(alignment: .leading, spacing: 1) {
                    Text(s.name).font(.callout.weight(.medium)).lineLimit(1)
                    Text("Tier \(s.tier) · \(s.team)").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            .frame(minWidth: 130, alignment: .leading)
            ForEach(envOrder) { e in versionCell(s, e) }
            actionCell(s)
                .gridColumnAlignment(.trailing)
        }
        .padding(.vertical, 8)
    }

    private func versionCell(_ s: Service, _ e: DeployEnv) -> some View {
        let v = s.versions[e]
        let behind = isBehind(s, e)
        let active = activeDeployment(s, e)
        return VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 4) {
                Text(v ?? "—")
                    .font(.callout.monospaced())
                    .foregroundStyle(v == nil ? Color.secondary : (behind ? Color.orange : Color.primary))
                    .lineLimit(1)
                if behind {
                    Image(systemName: "arrow.down.circle.fill").font(.caption).foregroundStyle(.orange)
                        .help("落後上一個環境(\(s.versions[previous(e)!] ?? "—"))")
                }
            }
            if let d = active {
                Button { openDeployments(d.id) } label: {
                    HStack(spacing: 4) {
                        PipeStatusIcon(status: d.status).font(.caption2)
                        Text(d.status == .waitingApproval ? "\(d.version) 待核准" : "→ \(d.version) \(Int(d.progress * 100))%")
                            .font(.caption2.monospacedDigit())
                    }
                }
                .buttonStyle(.plain)
                .foregroundStyle(d.status.color)
                .help("查看部署")
            }
        }
        .padding(.horizontal, 8).padding(.vertical, 5)
        .frame(minWidth: 110, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 6).fill(behind ? Color.orange.opacity(0.1) : (e == app.env ? Color.accentColor.opacity(0.06) : .clear)))
    }

    @ViewBuilder private func actionCell(_ s: Service) -> some View {
        let toProd = canPromote(s, to: .production)
        let toStg = canPromote(s, to: .staging)
        HStack(spacing: 6) {
            if toStg {
                Button { promote = PipePromotion(service: s, to: .staging) } label: {
                    Label("推進到 stg", systemImage: "arrow.up.forward")
                }
                .help("把 \(s.versions[.development] ?? "") 部署到 Staging")
            }
            if toProd {
                Button { promote = PipePromotion(service: s, to: .production) } label: {
                    Label("推進到 prod", systemImage: "arrow.up.forward.circle.fill")
                }
                .buttonStyle(.borderedProminent)
                .help("把 \(s.versions[.staging] ?? "") 推進到 Production(需核准)")
            }
            if !toStg && !toProd {
                Label("一致", systemImage: "checkmark").font(.caption).foregroundStyle(.secondary).labelStyle(.titleAndIcon)
            }
        }
        .controlSize(.small)
        .fixedSize()
    }

    // MARK: 推進

    private func previous(_ e: DeployEnv) -> DeployEnv? {
        switch e {
        case .development: return nil
        case .staging: return .development
        case .production: return .staging
        }
    }

    private func isBehind(_ s: Service, _ e: DeployEnv) -> Bool {
        guard let p = previous(e), let pv = s.versions[p], let v = s.versions[e] else { return false }
        // dev 常常跑在前面,只把 Production 落後 Staging 當成要處理的落差
        return e == .production && pv != v
    }

    private func drift(_ s: Service) -> Bool { isBehind(s, .production) }

    private func activeDeployment(_ s: Service, _ e: DeployEnv) -> Deployment? {
        app.deployments.first { $0.service == s.id && $0.env == e && $0.cicdIsActive }
    }

    private func canPromote(_ s: Service, to e: DeployEnv) -> Bool {
        guard let p = previous(e), let pv = s.versions[p] else { return false }
        return pv != s.versions[e] && activeDeployment(s, e) == nil
    }

    private func start(_ p: PipePromotion) {
        guard let version = p.service.versions[p.from] else { return }
        let prod = p.to == .production
        let d = Deployment(id: "dep-\(UUID().uuidString.prefix(8).lowercased())", service: p.service.id, version: version,
                           previousVersion: p.service.versions[p.to] ?? "—", env: p.to,
                           status: prod ? .waitingApproval : .running,
                           strategy: prod ? .canary : .rolling,
                           progress: prod ? 0 : 0.05, canaryPercent: prod ? 10 : nil,
                           startedAt: .now, finishedAt: nil, by: .person(app.me),
                           changes: ["從 \(p.from.label) 推進 \(version)"], healthGate: .pending)
        app.deployments.insert(d, at: 0)
        CICDSession.trackedDeployments.insert(d.id)
        app.log(.person(app.me), "arrow.up.forward.circle", "推進 \(p.service.name) \(version) 到 \(p.to.label)", .pipelines)
        promote = nil
        if prod { openDeployments(d.id) }
    }

    // MARK: 圖表

    private var dayBuckets: [PipeDayBucket] { PipeDayBucket.make(runs: app.pipelineRuns, days: 14) }

    private var runsPerDayCard: some View {
        let buckets = dayBuckets
        let total = buckets.reduce(0) { $0 + $1.count }
        return Card("每日執行次數", subtitle: "最近 14 天 · 共 \(total) 次") {
            if total == 0 {
                Text("最近 14 天沒有執行紀錄").font(.callout).foregroundStyle(.secondary).frame(height: 180)
            } else {
                Chart(buckets) { b in
                    BarMark(x: .value("日期", b.day, unit: .day), y: .value("次數", b.count))
                        .foregroundStyle(by: .value("結果", b.category))
                }
                .chartForegroundStyleScale(["成功": Color.green, "失敗": Color.red, "其他": Color.gray.opacity(0.5)])
                .chartXAxis {
                    AxisMarks(values: .stride(by: .day, count: 2)) {
                        AxisGridLine()
                        AxisValueLabel(format: .dateTime.month(.defaultDigits).day())
                    }
                }
                .frame(height: 180)
            }
        }
    }

    private var successRateCard: some View {
        let rates = PipeDayBucket.successRates(runs: app.pipelineRuns, days: 14)
        let finished = app.pipelineRuns.filter { $0.status == .succeeded || $0.status == .failed }
        let overall = finished.isEmpty ? nil : Double(finished.filter { $0.status == .succeeded }.count) / Double(finished.count)
        return Card("成功率", subtitle: overall.map { "整體 \(Fmt.percent($0 * 100, digits: 0)),不含取消與執行中" } ?? "還沒有完成的執行") {
            if rates.isEmpty {
                Text("沒有資料").font(.callout).foregroundStyle(.secondary).frame(height: 180)
            } else {
                Chart {
                    ForEach(rates, id: \.day) { r in
                        AreaMark(x: .value("日期", r.day, unit: .day), y: .value("成功率", r.rate * 100))
                            .foregroundStyle(Color.green.opacity(0.12).gradient)
                            .interpolationMethod(.monotone)
                        LineMark(x: .value("日期", r.day, unit: .day), y: .value("成功率", r.rate * 100))
                            .foregroundStyle(Color.green)
                            .interpolationMethod(.monotone)
                        PointMark(x: .value("日期", r.day, unit: .day), y: .value("成功率", r.rate * 100))
                            .foregroundStyle(Color.green).symbolSize(20)
                    }
                    RuleMark(y: .value("目標", 90))
                        .foregroundStyle(.secondary.opacity(0.6))
                        .lineStyle(StrokeStyle(lineWidth: 1, dash: [4, 3]))
                        .annotation(position: .top, alignment: .leading) {
                            Text("目標 90%").font(.caption2).foregroundStyle(.secondary)
                        }
                }
                .chartYScale(domain: 0...100)
                .chartYAxis { AxisMarks { v in AxisGridLine(); AxisValueLabel { Text("\(v.as(Int.self) ?? 0)%") } } }
                .frame(height: 180)
            }
        }
    }
}

// MARK: - 推進

struct PipePromotion: Identifiable {
    let service: Service
    let to: DeployEnv
    var id: String { service.id + to.rawValue }
    var from: DeployEnv { to == .production ? .staging : .development }

    var title: String { "把 \(service.name) 推進到 \(to.label)?" }

    @MainActor func message(_ app: AppState) -> String {
        let fromV = service.versions[from] ?? "—"
        let toV = service.versions[to] ?? "—"
        if to == .production {
            return "\(toV) → \(fromV),以 Canary(10% 流量)開始。建立後需要核准才會真正部署。"
        }
        return "\(toV) → \(fromV),以 Rolling 方式部署到 Staging。"
    }
}

// MARK: - 指標計算

struct PipeDORA {
    let frequencyPerDay: Double
    let prodDeploys7d: Int
    let dailyDeploys: [Double]
    let leadTime: TimeInterval?
    let failureRate: Double?
    let mttr: TimeInterval?

    @MainActor init(app: AppState) {
        let now = Date()
        let weekAgo = now.addingTimeInterval(-7 * 86400)
        let prod = app.deployments.filter { $0.env == .production }
        let prodOK = prod.filter { $0.status == .succeeded && $0.startedAt > weekAgo }
        prodDeploys7d = prodOK.count
        frequencyPerDay = Double(prodOK.count) / 7

        let cal = Calendar.current
        let today = cal.startOfDay(for: now)
        dailyDeploys = (0..<7).reversed().map { back -> Double in
            let day = cal.date(byAdding: .day, value: -back, to: today)!
            return Double(prodOK.filter { cal.isDate($0.startedAt, inSameDayAs: day) }.count)
        }

        // 前置時間:CI 執行時間 + 部署時間(從 commit 進管線到上線的近似)
        let ciDurations = app.pipelineRuns.filter { $0.status == .succeeded }.compactMap(\.duration)
        let deployDurations = prod.filter { $0.status == .succeeded }.compactMap { d in d.finishedAt.map { $0.timeIntervalSince(d.startedAt) } }
        if ciDurations.isEmpty && deployDurations.isEmpty {
            leadTime = nil
        } else {
            let ci = ciDurations.isEmpty ? 0 : ciDurations.reduce(0, +) / Double(ciDurations.count)
            let dep = deployDurations.isEmpty ? 0 : deployDurations.reduce(0, +) / Double(deployDurations.count)
            leadTime = ci + dep
        }

        let finished = prod.filter { $0.status == .succeeded || $0.status == .failed }
        let bad = finished.filter { $0.status == .failed }.count + prod.filter(\.cicdIsRollback).count
        failureRate = finished.isEmpty ? nil : min(1, Double(bad) / Double(finished.count))

        let resolved = app.incidents.compactMap { i in i.resolvedAt.map { $0.timeIntervalSince(i.startedAt) } }
        mttr = resolved.isEmpty ? nil : resolved.reduce(0, +) / Double(resolved.count)
    }

    var frequencyText: String {
        if frequencyPerDay >= 1 { return String(format: "%.1f 次/天", frequencyPerDay) }
        return "\(prodDeploys7d) 次/週"
    }

    var frequencyRating: PipeRating {
        if frequencyPerDay >= 1 { return .elite }
        if prodDeploys7d >= 1 { return .high }
        return .medium
    }
}

enum PipeRating {
    case elite, high, medium, low
    var label: String {
        switch self {
        case .elite: return "菁英"
        case .high: return "高"
        case .medium: return "中"
        case .low: return "低"
        }
    }
    var good: Bool { self == .elite || self == .high }

    static func lead(_ t: TimeInterval) -> PipeRating {
        t < 86400 ? .elite : t < 7 * 86400 ? .high : t < 30 * 86400 ? .medium : .low
    }
    static func cfr(_ r: Double) -> PipeRating {
        r <= 0.05 ? .elite : r <= 0.15 ? .high : r <= 0.3 ? .medium : .low
    }
    static func mttr(_ t: TimeInterval) -> PipeRating {
        t < 3600 ? .elite : t < 86400 ? .high : t < 7 * 86400 ? .medium : .low
    }
}

struct PipeDayBucket: Identifiable {
    let day: Date
    let category: String
    let count: Int
    var id: String { "\(day.timeIntervalSince1970)-\(category)" }

    private static func category(_ s: RunStatus) -> String {
        switch s {
        case .succeeded: return "成功"
        case .failed: return "失敗"
        default: return "其他"
        }
    }

    static func make(runs: [PipelineRun], days: Int) -> [PipeDayBucket] {
        let cal = Calendar.current
        let today = cal.startOfDay(for: .now)
        var out: [PipeDayBucket] = []
        for back in (0..<days).reversed() {
            guard let day = cal.date(byAdding: .day, value: -back, to: today) else { continue }
            let todays = runs.filter { cal.isDate($0.startedAt, inSameDayAs: day) }
            for c in ["成功", "失敗", "其他"] {
                out.append(PipeDayBucket(day: day, category: c, count: todays.filter { category($0.status) == c }.count))
            }
        }
        return out
    }

    static func successRates(runs: [PipelineRun], days: Int) -> [(day: Date, rate: Double)] {
        let cal = Calendar.current
        let today = cal.startOfDay(for: .now)
        return (0..<days).reversed().compactMap { back in
            guard let day = cal.date(byAdding: .day, value: -back, to: today) else { return nil }
            let done = runs.filter { cal.isDate($0.startedAt, inSameDayAs: day) && ($0.status == .succeeded || $0.status == .failed) }
            guard !done.isEmpty else { return nil }
            return (day, Double(done.filter { $0.status == .succeeded }.count) / Double(done.count))
        }
    }
}
