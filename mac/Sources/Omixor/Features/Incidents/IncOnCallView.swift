import SwiftUI

/// 值班:目前值班、一週排班、升級規則
struct IncOnCallView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        PageScroll {
            TimelineView(.periodic(from: .now, by: 60)) { ctx in
                VStack(alignment: .leading, spacing: 16) {
                    current(now: ctx.date)
                    IncWeekSchedule(now: ctx.date)
                    IncEscalationCard()
                }
            }
        }
    }

    private func current(now: Date) -> some View {
        let active = app.onCall.filter { $0.start <= now && $0.end > now }
        let primary = active.first { $0.role == "Primary" }
        let secondary = active.first { $0.role == "Secondary" }
        let upcoming = app.onCall.filter { $0.start > now && $0.role == "Primary" }.min { $0.start < $1.start }
        return HStack(alignment: .top, spacing: 12) {
            IncOnCallPersonCard(title: "主要值班", shift: primary, tint: .blue, now: now)
            IncOnCallPersonCard(title: "次要值班", shift: secondary, tint: .teal, now: now)
            IncOnCallPersonCard(title: "下一班", shift: upcoming, tint: .gray, now: now, upcoming: true)
        }
    }
}

struct IncOnCallPersonCard: View {
    @EnvironmentObject var app: AppState
    let title: String
    let shift: OnCallShift?
    let tint: Color
    let now: Date
    var upcoming = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                if !upcoming && shift != nil { StatusDot(color: .green, pulsing: true) }
                Text(title).font(.subheadline).foregroundStyle(.secondary)
            }
            if let shift {
                let p = app.person(shift.person)
                HStack(spacing: 10) {
                    ActorAvatar(actor: .person(shift.person), size: 36)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(p?.name ?? shift.person).font(.title3.weight(.semibold)).lineLimit(1)
                        Text(p?.role ?? "").font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Text(upcoming
                     ? "\(IncFmt.dateTime(shift.start)) 開始(\(IncFmt.duration(shift.start.timeIntervalSince(now))) 後)"
                     : "到 \(IncFmt.dateTime(shift.end)),還有 \(IncFmt.duration(shift.end.timeIntervalSince(now)))")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Text("沒有排班").font(.title3).foregroundStyle(.tertiary)
                Text(upcoming ? "未來沒有排定的班表" : "這個時段沒有人值班").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(alignment: .leading) {
            UnevenRoundedRectangle(topLeadingRadius: 12, bottomLeadingRadius: 12).fill(tint).frame(width: 4)
        }
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.07)))
    }
}

/// 一週排班表:每人一列、每班一條
struct IncWeekSchedule: View {
    @EnvironmentObject var app: AppState
    let now: Date

    private var rangeStart: Date { Calendar.current.date(byAdding: .day, value: -1, to: Calendar.current.startOfDay(for: now))! }
    private var rangeEnd: Date { Calendar.current.date(byAdding: .day, value: 7, to: rangeStart)! }
    private var days: [Date] { (0..<7).compactMap { Calendar.current.date(byAdding: .day, value: $0, to: rangeStart) } }

    private var shifts: [OnCallShift] { app.onCall.filter { $0.end > rangeStart && $0.start < rangeEnd } }

    private var peopleIds: [String] {
        var seen: [String] = []
        for s in shifts.sorted(by: { $0.start < $1.start }) where !seen.contains(s.person) { seen.append(s.person) }
        return seen
    }

    private let nameW: CGFloat = 110
    private let rowH: CGFloat = 34

    var body: some View {
        Card("本週排班", subtitle: "\(IncFmt.day(rangeStart)) – \(IncFmt.day(days.last ?? rangeStart))") {
            if shifts.isEmpty {
                EmptyStateView(title: "沒有排班資料", icon: "calendar.badge.exclamationmark",
                               message: "這一週還沒有值班排程。")
                    .frame(height: 160)
            } else {
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 14) {
                        legend("主要", .blue)
                        legend("次要", .teal)
                        legend("現在", .red)
                    }
                    .font(.caption2).foregroundStyle(.secondary)

                    GeometryReader { g in
                        let w = max(1, g.size.width - nameW)
                        let total = rangeEnd.timeIntervalSince(rangeStart)
                        let x: (Date) -> CGFloat = { d in
                            nameW + CGFloat(min(max(d.timeIntervalSince(rangeStart), 0), total) / total) * w
                        }
                        ZStack(alignment: .topLeading) {
                            // 日期欄
                            ForEach(Array(days.enumerated()), id: \.offset) { i, d in
                                let isToday = Calendar.current.isDate(d, inSameDayAs: now)
                                Rectangle()
                                    .fill(isToday ? Color.accentColor.opacity(0.06) : (i % 2 == 0 ? Color.primary.opacity(0.025) : Color.clear))
                                    .frame(width: w / 7, height: g.size.height)
                                    .offset(x: nameW + CGFloat(i) * w / 7)
                                Text(IncFmt.day(d))
                                    .font(.caption2.weight(isToday ? .semibold : .regular))
                                    .foregroundStyle(isToday ? Color.accentColor : Color.secondary)
                                    .frame(width: w / 7)
                                    .offset(x: nameW + CGFloat(i) * w / 7, y: 2)
                            }
                            // 每人一列
                            ForEach(Array(peopleIds.enumerated()), id: \.element) { row, pid in
                                let y = 22 + CGFloat(row) * rowH
                                HStack(spacing: 6) {
                                    ActorAvatar(actor: .person(pid), size: 20)
                                    Text(app.person(pid)?.name ?? pid).font(.callout).lineLimit(1)
                                }
                                .frame(width: nameW - 8, height: rowH, alignment: .leading)
                                .offset(y: y)
                                ForEach(shifts.filter { $0.person == pid }) { s in
                                    let x0 = x(s.start), x1 = x(s.end)
                                    let primary = s.role == "Primary"
                                    let color: Color = primary ? .blue : .teal
                                    let live = s.start <= now && s.end > now
                                    RoundedRectangle(cornerRadius: 5)
                                        .fill(color.opacity(live ? 0.9 : 0.55).gradient)
                                        .overlay(alignment: .leading) {
                                            Text(primary ? "主要" : "次要")
                                                .font(.caption2.weight(.semibold)).foregroundStyle(.white)
                                                .padding(.leading, 5)
                                                .opacity(x1 - x0 > 34 ? 1 : 0)
                                        }
                                        .frame(width: max(3, x1 - x0 - 2), height: rowH - 12)
                                        .offset(x: x0 + 1, y: y + 6)
                                        .help("\(app.person(pid)?.name ?? pid) · \(primary ? "主要" : "次要")值班\n\(IncFmt.dateTime(s.start)) – \(IncFmt.dateTime(s.end))")
                                }
                            }
                            // 現在
                            if now > rangeStart && now < rangeEnd {
                                Rectangle().fill(Color.red).frame(width: 1.5, height: g.size.height - 16)
                                    .offset(x: x(now), y: 16)
                                Circle().fill(Color.red).frame(width: 7, height: 7)
                                    .offset(x: x(now) - 3, y: 13)
                            }
                        }
                    }
                    .frame(height: 22 + CGFloat(peopleIds.count) * rowH + 4)
                }
            }
        }
    }

    private func legend(_ t: String, _ c: Color) -> some View {
        HStack(spacing: 4) {
            RoundedRectangle(cornerRadius: 2).fill(c).frame(width: 12, height: 6)
            Text(t)
        }
    }
}

/// 升級規則
struct IncEscalationCard: View {
    @EnvironmentObject var app: AppState

    private struct Step: Identifiable {
        let id: Int
        let delay: String
        let who: String
        let detail: String
        let icon: String
        let tint: Color
        let actor: Actor?
    }

    private var steps: [Step] {
        let now = Date()
        let active = app.onCall.filter { $0.start <= now && $0.end > now }
        let primary = active.first { $0.role == "Primary" }?.person
        let secondary = active.first { $0.role == "Secondary" }?.person
        let lead = app.people.first { $0.role.contains("主管") || $0.role.localizedCaseInsensitiveContains("lead") || $0.role.contains("經理") }
        return [
            Step(id: 0, delay: "立即", who: "AI 分診", detail: "彙整告警、近期部署與日誌,附上初步判讀", icon: "sparkles", tint: .purple, actor: nil),
            Step(id: 1, delay: "0 分鐘", who: primary.flatMap { app.person($0)?.name } ?? "主要值班",
                 detail: "推播 + 電話通知", icon: "iphone.radiowaves.left.and.right", tint: .blue, actor: primary.map { .person($0) }),
            Step(id: 2, delay: "5 分鐘未確認", who: secondary.flatMap { app.person($0)?.name } ?? "次要值班",
                 detail: "推播 + 電話通知,並在 #incident 頻道標記", icon: "phone.arrow.up.right", tint: .teal, actor: secondary.map { .person($0) }),
            Step(id: 3, delay: "15 分鐘未確認", who: lead?.name ?? "工程主管",
                 detail: "SEV1/SEV2 直接通知,並開啟戰情會議", icon: "person.badge.shield.checkmark", tint: .orange, actor: lead.map { .person($0.id) }),
            Step(id: 4, delay: "30 分鐘未解決", who: "全體工程團隊",
                 detail: "SEV1 發布狀態頁公告,通知客服", icon: "megaphone", tint: .red, actor: nil),
        ]
    }

    var body: some View {
        Card("升級規則", subtitle: "告警觸發後依序通知,任一人確認即停止升級") {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(steps) { s in
                    HStack(alignment: .top, spacing: 12) {
                        VStack(spacing: 0) {
                            Circle().fill(s.tint.gradient).frame(width: 26, height: 26)
                                .overlay(Image(systemName: s.icon).font(.system(size: 11, weight: .semibold)).foregroundStyle(.white))
                            if s.id != steps.count - 1 {
                                Rectangle().fill(Color.primary.opacity(0.12)).frame(width: 2).frame(maxHeight: .infinity)
                            }
                        }
                        VStack(alignment: .leading, spacing: 3) {
                            HStack(spacing: 8) {
                                Text(s.delay).font(.caption.weight(.semibold)).foregroundStyle(s.tint)
                                    .padding(.horizontal, 6).padding(.vertical, 1)
                                    .background(s.tint.opacity(0.12), in: Capsule())
                                if let a = s.actor { ActorAvatar(actor: a, size: 16) }
                                Text(s.who).font(.callout.weight(.medium))
                                if s.id == 0 { AIBadge() }
                            }
                            Text(s.detail).font(.caption).foregroundStyle(.secondary)
                        }
                        .padding(.bottom, 14)
                        Spacer(minLength: 0)
                    }
                    .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }
}
