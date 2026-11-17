import Charts
import SwiftUI

// 各畫面共用的小元件。新增共用元件請加在這個資料夾的新檔案,不要改這裡的既有簽名。

/// 圓角卡片容器
struct Card<Content: View>: View {
    var title: String?
    var subtitle: String?
    var padding: CGFloat = 14
    @ViewBuilder var content: Content

    init(_ title: String? = nil, subtitle: String? = nil, padding: CGFloat = 14, @ViewBuilder content: () -> Content) {
        self.title = title
        self.subtitle = subtitle
        self.padding = padding
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if title != nil || subtitle != nil {
                VStack(alignment: .leading, spacing: 2) {
                    if let title { Text(title).font(.headline) }
                    if let subtitle { Text(subtitle).font(.caption).foregroundStyle(.secondary) }
                }
            }
            content
        }
        .padding(padding)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.07)))
    }
}

/// 頁首:標題、說明、右側動作
struct PageHeader<Actions: View>: View {
    let title: String
    var subtitle: String?
    @ViewBuilder var actions: Actions

    init(_ title: String, subtitle: String? = nil, @ViewBuilder actions: () -> Actions = { EmptyView() }) {
        self.title = title
        self.subtitle = subtitle
        self.actions = actions()
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.title2.weight(.semibold))
                if let subtitle { Text(subtitle).foregroundStyle(.secondary) }
            }
            Spacer()
            actions
        }
    }
}

/// 小標籤
struct Pill: View {
    let text: String
    var color: Color = .secondary
    var icon: String?

    var body: some View {
        HStack(spacing: 4) {
            if let icon { Image(systemName: icon) }
            Text(text).lineLimit(1)
        }
        .fixedSize()   // 標籤不換行(窄的時候由外層決定少放幾個)
        .font(.caption.weight(.medium))
        .padding(.horizontal, 7).padding(.vertical, 3)
        .foregroundStyle(color)
        .background(color.opacity(0.13), in: Capsule())
    }
}

struct HealthBadge: View {
    let health: Health
    var compact = false
    var body: some View {
        if compact {
            Image(systemName: health.icon).foregroundStyle(health.color).help(health.label)
        } else {
            Pill(text: health.label, color: health.color, icon: health.icon)
        }
    }
}

struct StatusDot: View {
    let color: Color
    var pulsing = false
    @State private var on = false
    var body: some View {
        Circle().fill(color).frame(width: 8, height: 8)
            .overlay(Circle().stroke(color.opacity(0.5), lineWidth: 4).scaleEffect(on ? 1.8 : 1).opacity(on ? 0 : 1))
            .onAppear {
                guard pulsing else { return }
                withAnimation(.easeOut(duration: 1.2).repeatForever(autoreverses: false)) { on = true }
            }
    }
}

struct RunStatusBadge: View {
    let status: RunStatus
    var body: some View { Pill(text: status.label, color: status.color, icon: status.icon) }
}

struct EnvBadge: View {
    let env: DeployEnv
    var body: some View { Pill(text: env.short, color: env.color) }
}

/// 標出 AI 執行的動作
struct AIBadge: View {
    var text = "AI"
    var body: some View { Pill(text: text, color: .purple, icon: "sparkles") }
}

/// 人或 AI 的頭像
struct ActorAvatar: View {
    @EnvironmentObject var app: AppState
    let actor: Actor
    var size: CGFloat = 22

    var body: some View {
        switch actor {
        case .person(let id):
            let p = app.person(id)
            Circle().fill((p?.color ?? .gray).gradient)
                .frame(width: size, height: size)
                .overlay(Text(p?.initials ?? "?").font(.system(size: size * 0.45, weight: .semibold)).foregroundStyle(.white))
                .help(p?.name ?? id)
        case .ai:
            Circle().fill(LinearGradient(colors: [.purple, .indigo], startPoint: .topLeading, endPoint: .bottomTrailing))
                .frame(width: size, height: size)
                .overlay(Image(systemName: "sparkles").font(.system(size: size * 0.45)).foregroundStyle(.white))
                .help(app.name(of: actor))
        case .system:
            Circle().fill(Color.gray.opacity(0.4)).frame(width: size, height: size)
                .overlay(Image(systemName: "gearshape.fill").font(.system(size: size * 0.45)).foregroundStyle(.white))
        }
    }
}

/// 頭像 + 名稱
struct ActorLabel: View {
    @EnvironmentObject var app: AppState
    let actor: Actor
    var body: some View {
        HStack(spacing: 6) {
            ActorAvatar(actor: actor, size: 18)
            Text(app.name(of: actor)).lineLimit(1)
        }
    }
}

/// 數字指標卡
struct MetricTile: View {
    let title: String
    let value: String
    var delta: String?
    var deltaGood: Bool = true
    var icon: String?
    var tint: Color = .accentColor
    var series: [Double] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                if let icon { Image(systemName: icon).foregroundStyle(tint) }
                Text(title).font(.subheadline).foregroundStyle(.secondary)
            }
            HStack(alignment: .lastTextBaseline, spacing: 8) {
                Text(value).font(.system(size: 26, weight: .semibold, design: .rounded)).monospacedDigit()
                if let delta {
                    Text(delta).font(.caption.weight(.medium)).foregroundStyle(deltaGood ? .green : .red)
                }
            }
            if !series.isEmpty {
                Sparkline(values: series, color: tint).frame(height: 28)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.07)))
    }
}

struct Sparkline: View {
    let values: [Double]
    var color: Color = .accentColor

    var body: some View {
        Chart(Array(values.enumerated()), id: \.offset) { i, v in
            AreaMark(x: .value("i", i), y: .value("v", v))
                .foregroundStyle(color.opacity(0.15).gradient)
                .interpolationMethod(.catmullRom)
            LineMark(x: .value("i", i), y: .value("v", v))
                .foregroundStyle(color)
                .lineStyle(StrokeStyle(lineWidth: 1.5))
                .interpolationMethod(.catmullRom)
        }
        .chartXAxis(.hidden)
        .chartYAxis(.hidden)
        .chartYScale(domain: .automatic(includesZero: false))
    }
}

/// 時間序列折線圖
struct SeriesChart: View {
    let series: [MetricSeries]
    var height: CGFloat = 180
    var threshold: Double?

    var body: some View {
        Chart {
            ForEach(series) { s in
                ForEach(s.points) { p in
                    LineMark(x: .value("時間", p.date), y: .value(s.unit, p.value))
                        .foregroundStyle(by: .value("指標", s.name))
                        .interpolationMethod(.monotone)
                }
            }
            if let threshold {
                RuleMark(y: .value("門檻", threshold))
                    .foregroundStyle(.red.opacity(0.6))
                    .lineStyle(StrokeStyle(lineWidth: 1, dash: [4, 3]))
                    .annotation(position: .top, alignment: .trailing) {
                        Text("門檻").font(.caption2).foregroundStyle(.red)
                    }
            }
        }
        .chartLegend(series.count > 1 ? .visible : .hidden)
        .frame(height: height)
    }
}

/// 0...100 的使用率條
struct UsageBar: View {
    let value: Double
    var label: String?

    private var color: Color { value > 85 ? .red : value > 70 ? .orange : .green }

    var body: some View {
        HStack(spacing: 8) {
            if let label { Text(label).font(.caption).foregroundStyle(.secondary).frame(width: 34, alignment: .leading) }
            GeometryReader { g in
                ZStack(alignment: .leading) {
                    Capsule().fill(Color.primary.opacity(0.08))
                    Capsule().fill(color.gradient).frame(width: g.size.width * min(1, max(0, value / 100)))
                }
            }
            .frame(height: 6)
            Text("\(Int(value))%").font(.caption.monospacedDigit()).foregroundStyle(.secondary).frame(width: 36, alignment: .trailing)
        }
    }
}

struct EmptyStateView: View {
    let title: String
    let icon: String
    var message: String?
    var body: some View {
        ContentUnavailableView(title, systemImage: icon, description: message.map(Text.init))
    }
}

/// 頁面內容的標準外框:可捲動、統一邊距
struct PageScroll<Content: View>: View {
    @ViewBuilder var content: Content
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) { content }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

// MARK: - 格式化

enum Fmt {
    static func relative(_ date: Date) -> String {
        let f = RelativeDateTimeFormatter()
        f.locale = Locale(identifier: "zh_TW")
        f.unitsStyle = .short
        return f.localizedString(for: date, relativeTo: .now)
    }

    static func time(_ date: Date) -> String {
        date.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits))
    }

    static func duration(_ t: TimeInterval?) -> String {
        guard let t else { return "—" }
        let s = Int(t)
        if s < 60 { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m \(s % 60)s" }
        return "\(s / 3600)h \(s % 3600 / 60)m"
    }

    static func percent(_ v: Double, digits: Int = 1) -> String {
        String(format: "%.\(digits)f%%", v)
    }

    static func number(_ v: Double) -> String {
        if v >= 1_000_000 { return String(format: "%.1fM", v / 1_000_000) }
        if v >= 1000 { return String(format: "%.1fk", v / 1000) }
        return String(format: "%.0f", v)
    }
}
