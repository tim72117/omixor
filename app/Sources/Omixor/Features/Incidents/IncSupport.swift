import AppKit
import SwiftUI

// 事件畫面共用的小工具:SEV 樣式、時間軸事件樣式、時間格式、AI 摘要產生。

enum IncSev {
    static let all = [1, 2, 3, 4]

    static func color(_ sev: Int) -> Color {
        switch sev {
        case 1: return .red
        case 2: return .orange
        case 3: return .yellow
        default: return .gray
        }
    }

    static func label(_ sev: Int) -> String { "SEV\(sev)" }

    static func meaning(_ sev: Int) -> String {
        switch sev {
        case 1: return "核心服務中斷,多數使用者受影響"
        case 2: return "主要功能異常,部分使用者受影響"
        case 3: return "次要功能異常,有替代方案"
        default: return "輕微問題,幾乎不影響使用"
        }
    }
}

struct IncSevBadge: View {
    let sev: Int
    var large = false

    var body: some View {
        Text(IncSev.label(sev))
            .font(.system(size: large ? 12 : 10, weight: .bold, design: .monospaced))
            .padding(.horizontal, large ? 8 : 5)
            .padding(.vertical, large ? 3 : 2)
            .foregroundStyle(sev == 3 ? Color.black.opacity(0.75) : Color.white)
            .background(IncSev.color(sev).gradient, in: RoundedRectangle(cornerRadius: 4))
            .help(IncSev.meaning(sev))
    }
}

struct IncStatusPill: View {
    let status: IncidentStatus
    var body: some View {
        Pill(text: status.label, color: status.color, icon: status.incIcon)
    }
}

extension IncidentStatus {
    var incIcon: String {
        switch self {
        case .investigating: return "magnifyingglass"
        case .identified: return "scope"
        case .monitoring: return "eye"
        case .resolved: return "checkmark.circle.fill"
        }
    }

    var incNext: IncidentStatus? {
        switch self {
        case .investigating: return .identified
        case .identified: return .monitoring
        case .monitoring: return .resolved
        case .resolved: return nil
        }
    }

    var incStep: Int { IncidentStatus.allCases.firstIndex(of: self) ?? 0 }
}

extension IncidentEvent.Kind {
    var incIcon: String {
        switch self {
        case .status: return "flag.fill"
        case .note: return "text.bubble.fill"
        case .action: return "hammer.fill"
        case .alert: return "bell.fill"
        case .deploy: return "shippingbox.fill"
        case .aiFinding: return "sparkles"
        }
    }

    var incLabel: String {
        switch self {
        case .status: return "狀態"
        case .note: return "備註"
        case .action: return "處置"
        case .alert: return "告警"
        case .deploy: return "部署"
        case .aiFinding: return "AI 發現"
        }
    }

    var incColor: Color {
        switch self {
        case .status: return .blue
        case .note: return .gray
        case .action: return .teal
        case .alert: return .red
        case .deploy: return .indigo
        case .aiFinding: return .purple
        }
    }

    /// 檢討報告時間軸只列重要節點
    var incIsKey: Bool { self != .note }
}

extension Actor {
    var incIsAI: Bool { if case .ai = self { return true }; return false }
    var incIsPerson: Bool { if case .person = self { return true }; return false }
}

extension Incident {
    var incIsActive: Bool { status != .resolved }

    func incDuration(now: Date = .now) -> TimeInterval {
        max(0, (resolvedAt ?? now).timeIntervalSince(startedAt))
    }

    /// 從事件開始到第一次有人回應的時間(MTTA 用)
    var incAckDelay: TimeInterval? {
        timeline.filter { $0.actor.incIsPerson && $0.date >= startedAt }
            .map(\.date).min()
            .map { $0.timeIntervalSince(startedAt) }
    }

    var incSortedTimeline: [IncidentEvent] { timeline.sorted { $0.date < $1.date } }

    var incNumber: Int { Int(id.filter(\.isNumber)) ?? 0 }
}

enum IncFmt {
    /// 中文的持續時間,例如「2 小時 5 分」
    static func duration(_ t: TimeInterval) -> String {
        let m = Int(t) / 60
        if m < 1 { return "不到 1 分" }
        if m < 60 { return "\(m) 分" }
        let h = m / 60
        if h < 24 { return m % 60 == 0 ? "\(h) 小時" : "\(h) 小時 \(m % 60) 分" }
        return h % 24 == 0 ? "\(h / 24) 天" : "\(h / 24) 天 \(h % 24) 小時"
    }

    static func short(_ t: TimeInterval?) -> String {
        guard let t else { return "—" }
        let m = Int(t) / 60
        if m < 60 { return "\(max(m, 0))m" }
        let h = m / 60
        if h < 24 { return "\(h)h \(m % 60)m" }
        return "\(h / 24)d \(h % 24)h"
    }

    static func dateTime(_ d: Date) -> String {
        d.formatted(.dateTime.month(.twoDigits).day(.twoDigits).hour(.twoDigits(amPM: .omitted)).minute(.twoDigits))
    }

    static func day(_ d: Date) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "zh_TW")
        f.dateFormat = "M/d EEE"
        return f.string(from: d)
    }

    static func copy(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }
}

// MARK: - 本機「AI」整理:依資料產生摘要與檢討報告草稿

enum IncAI {
    @MainActor
    static func summary(_ inc: Incident, app: AppState) -> String {
        let services = inc.services.map { app.service($0)?.name ?? $0 }.joined(separator: "、")
        let events = inc.incSortedTimeline
        var parts: [String] = []
        parts.append("\(IncSev.label(inc.sev)) 事件,影響 \(services.isEmpty ? "未指定服務" : services),已持續 \(IncFmt.duration(inc.incDuration()))。")
        if let finding = events.last(where: { $0.kind == .aiFinding }) {
            parts.append("最新發現:\(finding.text)")
        }
        if let action = events.last(where: { $0.kind == .action || $0.kind == .deploy }) {
            parts.append("最近處置:\(action.text)(\(app.name(of: action.actor)))。")
        }
        parts.append("目前狀態為「\(inc.status.label)」,由 \(app.person(inc.commander)?.name ?? inc.commander) 指揮。")
        if inc.status != .resolved, let next = inc.status.incNext {
            parts.append("建議下一步:確認條件後推進到「\(next.label)」。")
        }
        return parts.joined(separator: "\n")
    }

    struct Postmortem {
        var summary: String
        var impact: String
        var timeline: [(Date, String, String)]   // 時間、誰、內容
        var rootCause: String
        var wentWell: [String]
        var improve: [String]
        var actions: [(String, String, Bool)]    // 標題、狀態、是否已建立工作項目

        func markdown(title: String) -> String {
            var s = "# 事件檢討:\(title)\n\n## 摘要\n\(summary)\n\n## 影響\n\(impact)\n\n## 時間軸\n"
            for (d, who, text) in timeline { s += "- \(IncFmt.dateTime(d)) \(who):\(text)\n" }
            s += "\n## 根本原因\n\(rootCause)\n\n## 做得好的地方\n"
            for w in wentWell { s += "- \(w)\n" }
            s += "\n## 可以改進的地方\n"
            for w in improve { s += "- \(w)\n" }
            s += "\n## 行動項目\n"
            for (t, st, _) in actions { s += "- [ ] \(t)(\(st))\n" }
            return s
        }
    }

    @MainActor
    static func postmortem(_ inc: Incident, app: AppState) -> Postmortem {
        let events = inc.incSortedTimeline
        let services = inc.services.map { app.service($0)?.name ?? $0 }.joined(separator: "、")
        let duration = IncFmt.duration(inc.incDuration())
        let summary = "\(IncFmt.dateTime(inc.startedAt)) 起,\(services.isEmpty ? "服務" : services) 發生「\(inc.title)」,"
            + "定為 \(IncSev.label(inc.sev)),共持續 \(duration) 後解決。由 \(app.person(inc.commander)?.name ?? inc.commander) 擔任指揮官。"

        let key = events.filter { $0.kind.incIsKey }
        let tl = (key.isEmpty ? events : key).map { ($0.date, app.name(of: $0.actor), $0.text) }

        let identified = events.first { $0.kind == .status && $0.text.contains(IncidentStatus.identified.label) }
        let finding = events.last { $0.kind == .aiFinding }
        let rootCause: String
        if let finding {
            rootCause = finding.text + (identified.map { "\n指揮官確認:\($0.text)" } ?? "")
        } else if let identified {
            rootCause = identified.text
        } else {
            rootCause = "時間軸中沒有明確的根本原因紀錄,請指揮官補充。"
        }

        var well: [String] = []
        if let ack = inc.incAckDelay {
            well.append(ack <= 5 * 60 ? "\(IncFmt.duration(ack)) 內就有人回應,反應迅速" : "第一次回應花了 \(IncFmt.duration(ack))")
        }
        let aiCount = events.filter { $0.actor.incIsAI }.count
        if aiCount > 0 { well.append("AI 在時間軸上提供了 \(aiCount) 則分析,協助縮小範圍") }
        if events.contains(where: { $0.kind == .deploy }) { well.append("能透過部署/回滾快速止血") }
        let people = Set(events.compactMap { e -> String? in if case .person(let id) = e.actor { return id }; return nil })
        if people.count > 1 { well.append("共有 \(people.count) 位成員協作處理") }
        if well.isEmpty { well.append("請團隊補充") }

        var improve: [String] = []
        if let ack = inc.incAckDelay, ack > 5 * 60 { improve.append("縮短告警到有人回應的時間(目前 \(IncFmt.duration(ack)))") }
        if inc.incDuration() > 3600 { improve.append("處理時間超過 1 小時,檢查是否缺少 runbook 或自動化止血") }
        if !events.contains(where: { $0.kind == .alert }) { improve.append("此事件沒有對應的告警紀錄,可能是使用者先發現,需補監控") }
        if improve.isEmpty { improve.append("目前沒有明顯缺口,持續觀察") }

        var actions: [(String, String, Bool)] = inc.followUps.map { id in
            if let w = app.workItems.first(where: { $0.id == id }) { return ("\(w.id) \(w.title)", w.status.label, true) }
            return (id, "未知", true)
        }
        if actions.isEmpty {
            actions.append(("為「\(inc.title)」補上告警與 runbook", "建議", false))
            actions.append(("在測試環境重現並加入回歸測試", "建議", false))
        }
        return Postmortem(summary: summary, impact: inc.impact.isEmpty ? "尚未填寫影響範圍。" : inc.impact,
                          timeline: tl, rootCause: rootCause, wentWell: well, improve: improve, actions: actions)
    }
}

/// 可點的服務標籤
struct IncServiceChip: View {
    @EnvironmentObject var app: AppState
    let serviceId: String

    var body: some View {
        let s = app.service(serviceId)
        Button { app.go(.infrastructure, focus: serviceId) } label: {
            HStack(spacing: 5) {
                Circle().fill((s?.health ?? .unknown).color).frame(width: 7, height: 7)
                Text(s?.name ?? serviceId)
                Image(systemName: "arrow.up.right").font(.system(size: 8, weight: .bold)).foregroundStyle(.tertiary)
            }
            .font(.caption.weight(.medium))
            .padding(.horizontal, 8).padding(.vertical, 4)
            .background(Color.primary.opacity(0.06), in: Capsule())
            .overlay(Capsule().strokeBorder(Color.primary.opacity(0.08)))
        }
        .buttonStyle(.plain)
        .help("到「伺服器與服務」查看 \(s?.name ?? serviceId)")
    }
}

/// 自動換行排列
struct IncFlow: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowH: CGFloat = 0, maxX: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > 0, x + s.width > width { x = 0; y += rowH + spacing; rowH = 0 }
            x += s.width + spacing
            maxX = max(maxX, x - spacing)
            rowH = max(rowH, s.height)
        }
        return CGSize(width: proposal.width ?? maxX, height: y + rowH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x > bounds.minX, x + s.width > bounds.maxX { x = bounds.minX; y += rowH + spacing; rowH = 0 }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(s))
            x += s.width + spacing
            rowH = max(rowH, s.height)
        }
    }
}
