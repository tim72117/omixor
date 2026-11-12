import Charts
import SwiftUI

/// 日誌分頁:查詢列(支援 `level:error service:api-server trace:abc` 語法)、等級切換、服務篩選、
/// 依等級堆疊的量體直方圖、可展開的日誌列,以及「即時追蹤」。
struct MonLogsTab: View {
    @EnvironmentObject var app: AppState

    @State private var query = ""
    @State private var levels: Set<LogLevel> = Set(LogLevel.allCases)
    @State private var service: String?
    @State private var expanded: Set<UUID> = []
    @State private var liveTail = false
    @State private var tailed: [LogEntry] = []
    @State private var timeSlice: ClosedRange<Date>?
    @State private var tailSeq = 0

    // MARK: 資料

    private var allLogs: [LogEntry] { app.logs + tailed }

    /// 依全域時間範圍(即時追蹤新增的列一律納入)
    private var windowLogs: [LogEntry] {
        let start = Date().addingTimeInterval(-app.timeRange.seconds)
        return allLogs.filter { $0.date >= start }
    }

    private var services: [String] {
        Array(Set(allLogs.map(\.service))).sorted()
    }

    private var parsed: MonLogQuery { MonLogQuery(query) }

    /// 套用查詢、服務、等級(不含時間切片) — 直方圖用
    private var queryFiltered: [LogEntry] {
        let q = parsed
        return windowLogs.filter { e in
            if let service, e.service != service { return false }
            if !levels.contains(e.level) { return false }
            return q.matches(e)
        }
    }

    // MARK: 畫面

    var body: some View {
        let base = queryFiltered
        let rows = timeSlice.map { r in base.filter { r.contains($0.date) } } ?? base
        let sorted = rows.sorted { $0.date > $1.date }
        let levelCounts = Dictionary(grouping: windowLogs.filter { e in
            (service == nil || e.service == service) && parsed.matches(e)
        }, by: \.level).mapValues(\.count)

        VStack(alignment: .leading, spacing: 12) {
            queryBar
            filterBar(levelCounts)
            MonLogHistogram(entries: base, selection: $timeSlice)
                .frame(height: 110)
            statusLine(count: sorted.count)
            list(sorted)
        }
        .padding(20)
        .task(id: liveTail) {
            guard liveTail else { return }
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: UInt64.random(in: 600_000_000...1_600_000_000))
                if Task.isCancelled { break }
                appendSynthesized()
            }
        }
    }

    private var queryBar: some View {
        HStack(spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("搜尋日誌,例如 level:error service:api-server timeout", text: $query)
                    .textFieldStyle(.plain)
                    .font(.system(.body, design: .monospaced))
                if !query.isEmpty {
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary) }
                        .buttonStyle(.plain)
                        .help("清除查詢")
                }
                Menu {
                    Button("只看錯誤 level:error") { addToken("level:error") }
                    Button("警告以上 level:warn level:error") { addToken("level:warn level:error") }
                    Divider()
                    ForEach(services, id: \.self) { s in Button("service:\(s)") { addToken("service:\(s)") } }
                    Divider()
                    Text("語法:level:、service:、host:、trace:,其餘文字比對訊息;前面加 - 代表排除")
                } label: {
                    Image(systemName: "questionmark.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("查詢語法")
            }
            .padding(.horizontal, 9).padding(.vertical, 6)
            .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.12)))

            Toggle(isOn: $liveTail.animation(.snappy)) {
                HStack(spacing: 6) {
                    if liveTail { StatusDot(color: .green, pulsing: true) } else { Image(systemName: "dot.radiowaves.left.and.right") }
                    Text("即時追蹤")
                }
            }
            .toggleStyle(.button)
            .tint(liveTail ? .green : nil)
            .help(liveTail ? "停止即時追蹤" : "持續接收新日誌")
        }
    }

    private func filterBar(_ counts: [LogLevel: Int]) -> some View {
        HStack(spacing: 8) {
            ForEach(LogLevel.allCases) { l in
                MonToggleChip(title: l.monLabel, count: counts[l] ?? 0, color: l.color, isOn: levels.contains(l)) {
                    if levels.contains(l) { levels.remove(l) } else { levels.insert(l) }
                }
                .contextMenu { Button("只看 \(l.monLabel)") { levels = [l] } }
            }
            Spacer(minLength: 12)
            Picker("服務", selection: $service) {
                Text("全部服務").tag(String?.none)
                if !services.isEmpty { Divider() }
                ForEach(services, id: \.self) { s in
                    Text(app.service(s)?.name ?? s).tag(String?.some(s))
                }
            }
            .frame(maxWidth: 220)
            .fixedSize()
        }
    }

    private func statusLine(count: Int) -> some View {
        HStack(spacing: 10) {
            Text("\(count) 筆").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            if let r = timeSlice {
                HStack(spacing: 4) {
                    Image(systemName: "clock")
                    Text("\(r.lowerBound.formatted(.dateTime.hour().minute().second()))–\(r.upperBound.formatted(.dateTime.hour().minute().second()))")
                    Button { timeSlice = nil } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.plain)
                        .help("清除時間篩選")
                }
                .font(.caption)
                .padding(.horizontal, 7).padding(.vertical, 2)
                .background(Color.accentColor.opacity(0.12), in: Capsule())
            } else {
                Text("點直方圖可只看該時段").font(.caption).foregroundStyle(.tertiary)
            }
            Spacer()
            if !expanded.isEmpty {
                Button("全部收合") { expanded.removeAll() }.buttonStyle(.link).font(.caption)
            }
            if liveTail {
                Text("已接收 \(tailed.count) 筆新日誌").font(.caption).foregroundStyle(.green)
            }
        }
    }

    @ViewBuilder private func list(_ rows: [LogEntry]) -> some View {
        Group {
            if rows.isEmpty {
                EmptyStateView(title: allLogs.isEmpty ? "沒有日誌" : "沒有符合條件的日誌",
                               icon: "text.magnifyingglass",
                               message: allLogs.isEmpty ? "開啟「即時追蹤」開始接收日誌。" : "調整查詢、等級或時間範圍試試看。")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            Color.clear.frame(height: 0).id("mon-log-top")
                            ForEach(rows) { e in
                                MonLogRow(entry: e, expanded: expanded.contains(e.id),
                                          serviceName: app.service(e.service)?.name ?? e.service,
                                          onToggle: { toggle(e.id) },
                                          onFilter: { addToken($0) })
                                Divider().opacity(0.4)
                            }
                        }
                    }
                    .onChange(of: tailed.count) { _, _ in
                        if liveTail { proxy.scrollTo("mon-log-top", anchor: .top) }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(nsColor: .textBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.primary.opacity(0.08)))
    }

    // MARK: 動作

    private func toggle(_ id: UUID) {
        if expanded.contains(id) { expanded.remove(id) } else { expanded.insert(id) }
    }

    private func addToken(_ token: String) {
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        let existing = Set(trimmed.split(separator: " ").map(String.init))
        let new = token.split(separator: " ").map(String.init).filter { !existing.contains($0) }
        guard !new.isEmpty else { return }
        query = ([trimmed] + new).filter { !$0.isEmpty }.joined(separator: " ")
    }

    private func appendSynthesized() {
        tailSeq += 1
        let svcs = services.isEmpty ? (app.services.map(\.id).isEmpty ? ["api-server", "web", "worker"] : app.services.map(\.id)) : services
        let svc = svcs.randomElement()!
        let hosts = Array(Set(allLogs.filter { $0.service == svc }.map(\.host)))
        let host = hosts.randomElement() ?? app.service(svc)?.hostIds.randomElement().flatMap { app.server($0)?.name } ?? "\(svc)-\(Int.random(in: 1...3))"
        let roll = Double.random(in: 0..<1)
        let level: LogLevel = roll < 0.06 ? .error : roll < 0.18 ? .warn : roll < 0.8 ? .info : .debug
        let message = MonLogSynth.message(level: level)
        let trace: String? = level == .debug ? nil : String(UUID().uuidString.replacingOccurrences(of: "-", with: "").prefix(16)).lowercased()
        tailed.append(LogEntry(id: UUID(), date: .now, level: level, service: svc, host: host, message: message, traceId: trace))
        if tailed.count > 2000 { tailed.removeFirst(tailed.count - 2000) }
    }
}

// MARK: - 查詢解析

struct MonLogQuery {
    var levels: Set<LogLevel> = []
    var services: [String] = []
    var hosts: [String] = []
    var traces: [String] = []
    var terms: [String] = []
    var excluded: [String] = []

    init(_ raw: String) {
        for tok in raw.split(whereSeparator: \.isWhitespace).map(String.init) {
            let lower = tok.lowercased()
            if lower.hasPrefix("-"), lower.count > 1 { excluded.append(String(lower.dropFirst())); continue }
            if let colon = lower.firstIndex(of: ":") {
                let key = String(lower[..<colon])
                let value = String(lower[lower.index(after: colon)...])
                guard !value.isEmpty else { continue }
                switch key {
                case "level", "lvl", "等級":
                    for v in value.split(separator: ",") {
                        let v = v == "warning" ? "warn" : v == "err" ? "error" : String(v)
                        if let l = LogLevel(rawValue: v) { levels.insert(l) }
                    }
                    continue
                case "service", "svc", "服務": services.append(value); continue
                case "host", "主機": hosts.append(value); continue
                case "trace", "traceid": traces.append(value); continue
                default: break
                }
            }
            terms.append(lower)
        }
    }

    func matches(_ e: LogEntry) -> Bool {
        if !levels.isEmpty, !levels.contains(e.level) { return false }
        if !services.isEmpty, !services.contains(where: { e.service.lowercased().contains($0) }) { return false }
        if !hosts.isEmpty, !hosts.contains(where: { e.host.lowercased().contains($0) }) { return false }
        if !traces.isEmpty {
            guard let t = e.traceId?.lowercased(), traces.contains(where: { t.hasPrefix($0) }) else { return false }
        }
        if terms.isEmpty && excluded.isEmpty { return true }
        let hay = (e.message + " " + e.service + " " + e.host).lowercased()
        if !terms.allSatisfy({ hay.contains($0) }) { return false }
        if excluded.contains(where: { hay.contains($0) }) { return false }
        return true
    }
}

// MARK: - 直方圖

struct MonLogHistogram: View {
    let entries: [LogEntry]
    @Binding var selection: ClosedRange<Date>?

    private struct Bin: Identifiable {
        let id: String
        let start: Date
        let end: Date
        let level: LogLevel
        let y0: Int
        let y1: Int
    }

    private var span: (Date, Date, TimeInterval)? {
        guard let lo = entries.map(\.date).min(), let hi = entries.map(\.date).max() else { return nil }
        let total = max(60, hi.timeIntervalSince(lo))
        let width = total / 48
        return (lo, lo.addingTimeInterval(total + width * 0.001), width)
    }

    private var bins: [Bin] {
        guard let (lo, _, width) = span else { return [] }
        var counts: [Int: [LogLevel: Int]] = [:]
        for e in entries {
            let i = min(47, Int(e.date.timeIntervalSince(lo) / width))
            counts[i, default: [:]][e.level, default: 0] += 1
        }
        var out: [Bin] = []
        let order: [LogLevel] = [.error, .warn, .info, .debug]
        for (i, byLevel) in counts {
            let s = lo.addingTimeInterval(Double(i) * width)
            let e = s.addingTimeInterval(width * 0.9)
            var acc = 0
            for l in order {
                let n = byLevel[l] ?? 0
                guard n > 0 else { continue }
                out.append(Bin(id: "\(i)-\(l.rawValue)", start: s, end: e, level: l, y0: acc, y1: acc + n))
                acc += n
            }
        }
        return out
    }

    private static let legendDomain: [String] = [LogLevel.error, .warn, .info, .debug].map(\.monLabel)
    private static let legendRange: [Color] = [LogLevel.error, .warn, .info, .debug].map(\.color)

    private func barOpacity(_ b: Bin) -> Double {
        guard let selection else { return 1 }
        return selection.contains(b.start) ? 1 : 0.3
    }

    private func mark(_ b: Bin) -> some ChartContent {
        RectangleMark(xStart: .value("開始", b.start), xEnd: .value("結束", b.end),
                yStart: .value("數量", b.y0), yEnd: .value("數量", b.y1))
            .foregroundStyle(by: .value("等級", b.level.monLabel))
            .opacity(barOpacity(b))
    }

    private var barChart: some View {
        Chart(bins) { b in mark(b) }
            .chartForegroundStyleScale(domain: Self.legendDomain, range: Self.legendRange)
    }

    var body: some View {
        if entries.isEmpty {
            RoundedRectangle(cornerRadius: 10).fill(Color.primary.opacity(0.03))
                .overlay(Text("這段時間沒有日誌").font(.caption).foregroundStyle(.secondary))
        } else {
            barChart
            .chartLegend(position: .top, alignment: .trailing, spacing: 4)
            .chartXAxis {
                AxisMarks(values: .automatic(desiredCount: 6)) { _ in
                    AxisGridLine().foregroundStyle(Color.primary.opacity(0.05))
                    AxisValueLabel(format: .dateTime.hour().minute())
                }
            }
            .chartYAxis {
                AxisMarks(position: .leading, values: .automatic(desiredCount: 3)) { _ in
                    AxisGridLine().foregroundStyle(Color.primary.opacity(0.05))
                    AxisValueLabel()
                }
            }
            .chartOverlay { proxy in
                GeometryReader { geo in
                    Rectangle().fill(.clear).contentShape(Rectangle())
                        .onTapGesture { loc in
                            guard let plot = proxy.plotFrame, let (lo, _, width) = span else { return }
                            let x = loc.x - geo[plot].origin.x
                            guard let date: Date = proxy.value(atX: x) else { return }
                            let i = max(0, min(47, Int(date.timeIntervalSince(lo) / width)))
                            let s = lo.addingTimeInterval(Double(i) * width)
                            let r = s...s.addingTimeInterval(width)
                            selection = (selection == r) ? nil : r
                        }
                }
            }
            .help("點一下長條只看該時段,再點一次取消")
        }
    }
}

// MARK: - 日誌列

struct MonLogRow: View {
    let entry: LogEntry
    let expanded: Bool
    let serviceName: String
    let onToggle: () -> Void
    let onFilter: (String) -> Void

    @State private var hovering = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Image(systemName: "chevron.right")
                    .font(.system(size: 9, weight: .semibold))
                    .rotationEffect(.degrees(expanded ? 90 : 0))
                    .foregroundStyle(.tertiary)
                    .frame(width: 10)
                Text(entry.date.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).second(.twoDigits).secondFraction(.fractional(3))))
                    .foregroundStyle(.secondary)
                Text(entry.level.monLabel)
                    .font(.system(size: 10, weight: .bold, design: .monospaced))
                    .foregroundStyle(entry.level.color)
                    .frame(width: 44)
                    .padding(.vertical, 1)
                    .background(entry.level.color.opacity(0.12), in: RoundedRectangle(cornerRadius: 3))
                Text(entry.service)
                    .foregroundStyle(.teal)
                    .lineLimit(1)
                    .frame(width: 110, alignment: .leading)
                Text(entry.message)
                    .foregroundStyle(entry.level == .error ? Color.red : entry.level == .debug ? Color.secondary : Color.primary)
                    .lineLimit(expanded ? nil : 1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
            }
            if expanded { detail }
        }
        .font(.system(size: 12, design: .monospaced))
        .padding(.horizontal, 10).padding(.vertical, 5)
        .background(rowBackground)
        .contentShape(Rectangle())
        .onTapGesture(perform: onToggle)
        .onHover { hovering = $0 }
    }

    private var rowBackground: Color {
        if expanded { return Color.accentColor.opacity(0.07) }
        if entry.level == .error { return Color.red.opacity(hovering ? 0.09 : 0.04) }
        return hovering ? Color.primary.opacity(0.04) : .clear
    }

    private var detail: some View {
        VStack(alignment: .leading, spacing: 6) {
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                field("時間", entry.date.formatted(.iso8601))
                field("服務", "\(entry.service)(\(serviceName))")
                field("主機", entry.host)
                field("Trace ID", entry.traceId ?? "—")
            }
            HStack(spacing: 10) {
                if let t = entry.traceId {
                    Button { onFilter("trace:\(t)") } label: { Label("看同一個 trace", systemImage: "point.3.connected.trianglepath.dotted") }
                }
                Button { onFilter("service:\(entry.service)") } label: { Label("只看這個服務", systemImage: "shippingbox") }
                Button { onFilter("host:\(entry.host)") } label: { Label("只看這台主機", systemImage: "server.rack") }
                Button {
                    let line = "\(entry.date.formatted(.iso8601)) \(entry.level.monLabel) [\(entry.service)@\(entry.host)] \(entry.message)" + (entry.traceId.map { " trace=\($0)" } ?? "")
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(line, forType: .string)
                } label: { Label("複製", systemImage: "doc.on.doc") }
            }
            .buttonStyle(.link)
            .font(.caption)
        }
        .padding(.leading, 20)
        .padding(.vertical, 4)
    }

    private func field(_ k: String, _ v: String) -> some View {
        GridRow {
            Text(k).foregroundStyle(.secondary).font(.system(size: 11))
            Text(v).textSelection(.enabled)
        }
    }
}

// MARK: - 模擬新日誌

enum MonLogSynth {
    static func message(level: LogLevel) -> String {
        let path = ["/api/v1/courses", "/api/v1/lessons/42", "/api/v1/auth/session", "/api/v1/progress", "/healthz", "/api/v1/billing/webhook"].randomElement()!
        let ms = Int.random(in: 4...240)
        switch level {
        case .debug:
            return ["cache hit key=lesson:\(Int.random(in: 100...999))", "db pool idle=\(Int.random(in: 2...12)) busy=\(Int.random(in: 1...8))",
                    "feature flag evaluated name=new_player value=true", "gc pause \(Int.random(in: 1...9))ms"].randomElement()!
        case .info:
            return ["GET \(path) 200 \(ms)ms", "POST \(path) 201 \(ms)ms", "user session refreshed",
                    "job completed queue=email id=\(Int.random(in: 10000...99999)) took=\(ms)ms"].randomElement()!
        case .warn:
            return ["slow query took=\(Int.random(in: 500...2400))ms table=progress", "GET \(path) 200 \(Int.random(in: 800...2500))ms (slow)",
                    "retrying upstream request attempt=2", "rate limit near threshold client=\(Int.random(in: 1...40))"].randomElement()!
        case .error:
            return ["GET \(path) 502 upstream connect timeout", "unhandled exception: connection reset by peer",
                    "POST \(path) 500 database deadlock detected", "payment webhook signature mismatch"].randomElement()!
        }
    }
}
