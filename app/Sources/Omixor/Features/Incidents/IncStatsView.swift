import Charts
import SwiftUI

/// 事件統計:MTTA / MTTR、近 30 天各 SEV 事件數
struct IncStatsView: View {
    @EnvironmentObject var app: AppState
    var onSelect: (String) -> Void

    private var since: Date { Calendar.current.date(byAdding: .day, value: -30, to: .now)! }
    private var recent: [Incident] { app.incidents.filter { $0.startedAt >= since } }

    private var mtta: TimeInterval? { avg(recent.compactMap(\.incAckDelay)) }
    private var mttr: TimeInterval? { avg(recent.filter { $0.resolvedAt != nil }.map { $0.incDuration() }) }

    private func avg(_ xs: [TimeInterval]) -> TimeInterval? {
        xs.isEmpty ? nil : xs.reduce(0, +) / Double(xs.count)
    }

    private struct WeekBucket: Identifiable {
        let id = UUID()
        let week: Date
        let sev: String
        let count: Int
    }

    private var weekly: [WeekBucket] {
        let cal = Calendar.current
        var out: [WeekBucket] = []
        for w in 0..<5 {
            let start = cal.date(byAdding: .day, value: -7 * (4 - w) - 7, to: cal.startOfDay(for: .now))!
            let end = cal.date(byAdding: .day, value: 7, to: start)!
            for s in IncSev.all {
                let n = app.incidents.filter { $0.sev == s && $0.startedAt >= start && $0.startedAt < end }.count
                out.append(WeekBucket(week: end, sev: IncSev.label(s), count: n))
            }
        }
        return out
    }

    var body: some View {
        PageScroll {
            HStack(spacing: 12) {
                MetricTile(title: "進行中", value: "\(app.openIncidents.count)", icon: "flame", tint: .red)
                MetricTile(title: "近 30 天事件", value: "\(recent.count)", icon: "calendar", tint: .blue)
                MetricTile(title: "MTTA 平均回應", value: IncFmt.short(mtta), icon: "hand.raised", tint: .orange)
                MetricTile(title: "MTTR 平均修復", value: IncFmt.short(mttr), icon: "wrench.and.screwdriver", tint: .green)
            }

            HStack(alignment: .top, spacing: 16) {
                Card("各 SEV 事件數", subtitle: "近 30 天") {
                    if recent.isEmpty {
                        Text("近 30 天沒有事件").font(.callout).foregroundStyle(.secondary).frame(height: 180)
                    } else {
                        Chart(IncSev.all, id: \.self) { s in
                            BarMark(x: .value("SEV", IncSev.label(s)),
                                    y: .value("事件數", recent.filter { $0.sev == s }.count))
                                .foregroundStyle(IncSev.color(s).gradient)
                                .cornerRadius(4)
                                .annotation(position: .top) {
                                    Text("\(recent.filter { $0.sev == s }.count)").font(.caption2).foregroundStyle(.secondary)
                                }
                        }
                        .chartYAxis { AxisMarks(values: .automatic(desiredCount: 4)) }
                        .frame(height: 180)
                    }
                }
                Card("每週趨勢", subtitle: "依 SEV 堆疊") {
                    Chart(weekly) { b in
                        BarMark(x: .value("週", b.week, unit: .weekOfYear), y: .value("事件數", b.count))
                            .foregroundStyle(by: .value("SEV", b.sev))
                    }
                    .chartForegroundStyleScale(domain: IncSev.all.map(IncSev.label), range: IncSev.all.map(IncSev.color))
                    .chartXAxis {
                        AxisMarks(values: .stride(by: .weekOfYear)) { _ in
                            AxisGridLine()
                            AxisValueLabel(format: .dateTime.month(.defaultDigits).day())
                        }
                    }
                    .chartYAxis { AxisMarks(values: .automatic(desiredCount: 4)) }
                    .frame(height: 180)
                }
            }

            Card("每起事件的修復時間", subtitle: "近 30 天已解決事件,點選查看") {
                let resolved = recent.filter { $0.resolvedAt != nil }.sorted { $0.startedAt > $1.startedAt }
                if resolved.isEmpty {
                    Text("近 30 天沒有已解決的事件").font(.callout).foregroundStyle(.secondary)
                } else {
                    let maxD = resolved.map { $0.incDuration() }.max() ?? 1
                    VStack(spacing: 4) {
                        ForEach(resolved) { inc in
                            Button { onSelect(inc.id) } label: {
                                HStack(spacing: 10) {
                                    IncSevBadge(sev: inc.sev)
                                    Text(inc.title).font(.callout).lineLimit(1).frame(maxWidth: 260, alignment: .leading)
                                    GeometryReader { g in
                                        Capsule().fill(IncSev.color(inc.sev).opacity(0.7).gradient)
                                            .frame(width: max(4, g.size.width * inc.incDuration() / max(maxD, 1)), height: 8)
                                            .frame(maxHeight: .infinity)
                                    }
                                    .frame(height: 16)
                                    Text(IncFmt.short(inc.incDuration())).font(.caption.monospacedDigit())
                                        .foregroundStyle(.secondary).frame(width: 64, alignment: .trailing)
                                }
                                .padding(.vertical, 3)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            }
        }
    }
}
