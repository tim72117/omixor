import Charts
import SwiftUI

/// 監控 › 網站訪客:evoleap 的會員與訪客統計(後台「會員」頁同一份資料),透過連線以管理員身分讀取。
struct MonVisitorsTab: View {
    @EnvironmentObject var app: AppState
    @State private var range: SiteRange = .d1
    @State private var connId = SiteVisitors.connectionId
    @State private var data: SiteOverview?
    @State private var loading = false
    @State private var error: String?
    @State private var updatedAt: Date?

    private var conn: Connection? { app.connections.first { $0.id == connId } }
    private var loggedIn: Bool { conn.map { app.connectionStates[$0.id]?.account != nil } ?? false }

    var body: some View {
        PageScroll {
            toolbar
            if let error {
                Card { Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange).textSelection(.enabled) }
            }
            if let d = data {
                kpis(d)
                funnelCard(d)
                lessonsCard(d)
                breakdownCards(d)
                pathsCard(d.guestStats)
                homeStaysCard(d.guestStats)
                loginCard(d.guestStats.login)
                idleVisitsCard(d.guestStats)
            } else if !loading && error == nil {
                Card { EmptyStateView(title: "還沒有資料", icon: "person.3",
                                      message: loggedIn ? "按「重新整理」讀取。" : "先登入連線,才能以管理員身分讀取訪客統計。").frame(minHeight: 180) }
            }
        }
        .task(id: "\(connId)|\(range.id)|\(loggedIn)") { await load() }
        .task {
            // 開著這一頁時每 60 秒更新一次
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 60_000_000_000)
                if loggedIn { await load() }
            }
        }
    }

    // MARK: - 工具列

    private var toolbar: some View {
        HStack(spacing: 12) {
            Picker("連線", selection: $connId) {
                ForEach(app.connections) { c in Text(c.name).tag(c.id) }
            }
            .labelsHidden()
            .fixedSize()
            .onChange(of: connId) { _, v in SiteVisitors.connectionId = v; data = nil }
            if let conn {
                if let account = app.connectionStates[conn.id]?.account {
                    Pill(text: account, color: .green, icon: "person.crop.circle.badge.checkmark")
                } else if app.connectionStates[conn.id]?.busy == true {
                    ProgressView().controlSize(.small)
                    Text("請在瀏覽器完成登入").font(.callout).foregroundStyle(.secondary)
                    Button("取消") { app.connStore?.cancelLogin(conn.id) }
                } else {
                    Button("登入「\(conn.name)」…") { Task { await app.connStore?.login(conn.id) } }
                }
            }
            Spacer()
            Picker("時間範圍", selection: $range) {
                ForEach(SiteRange.allCases) { Text($0.label).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            Button {
                Task { await load() }
            } label: {
                if loading { ProgressView().controlSize(.small) } else { Image(systemName: "arrow.clockwise") }
            }
            .help("重新整理")
            .disabled(loading || !loggedIn)
            if let updatedAt { Text(Fmt.time(updatedAt)).font(.caption).foregroundStyle(.secondary) }
        }
    }

    private func load() async {
        guard loggedIn, !loading else { return }
        loading = true
        defer { loading = false }
        do {
            data = try await SiteVisitors.fetch(app, connection: connId, range: range)
            error = nil
            updatedAt = .now
        } catch {
            self.error = "讀取失敗:\(error.localizedDescription)"
        }
    }

    // MARK: - 區塊

    private func kpis(_ d: SiteOverview) -> some View {
        let f = d.funnel
        return HStack(spacing: 12) {
            kpi(range == .all ? "會員" : "新會員", "\(d.users)", "有進度 \(d.activeUsers)")
            kpi("訪客", "\(d.guests)", "不同的訪客代號")
            kpi("課程頁打開", "\(f.tracked)", "真人;爬蟲另有 \(f.bots)")
            kpi("有播放", f.played.percent(of: f.tracked), "\(f.played) 次")
            kpi("首頁造訪", "\(d.guestStats.homeVisits)", "登入頁 \(d.guestStats.login.visits) 次")
        }
    }

    private func kpi(_ title: String, _ value: String, _ sub: String) -> some View {
        Card {
            VStack(alignment: .leading, spacing: 4) {
                Text(title).font(.caption).foregroundStyle(.secondary)
                Text(value).font(.title2.weight(.semibold)).monospacedDigit()
                Text(sub).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private func funnelCard(_ d: SiteOverview) -> some View {
        let f = d.funnel
        let steps: [(String, Int, Color)] = [("打開", f.tracked, .blue), ("載入完成", f.ready, .teal),
                                             ("有播放", f.played, .green), ("載入失敗", f.failed, .red)]
        return Card("訪客漏斗", subtitle: "未登入的真人打開課程頁 → 播放器載入完成 → 有播放(有播放器的課加總;不含爬蟲與登入門檻)") {
            Chart(steps, id: \.0) { s in
                BarMark(x: .value("次數", s.1), y: .value("階段", s.0))
                    .foregroundStyle(s.2)
                    .annotation(position: .trailing) {
                        Text("\(s.1)  \(s.1.percent(of: f.tracked))").font(.caption).monospacedDigit().foregroundStyle(.secondary)
                    }
            }
            .chartXAxis(.hidden)
            .frame(height: 130)
        }
    }

    private func lessonsCard(_ d: SiteOverview) -> some View {
        let rows = d.playerLessons.filter { $0.guestTracked + $0.learners + $0.guestBots > 0 }
            .sorted { ($0.guestTracked, $0.learners) > ($1.guestTracked, $1.learners) }
        return Card("各課概況", subtitle: "訪客的漏斗與會員的進度;只列有紀錄的課") {
            if rows.isEmpty {
                Text("這段時間沒有紀錄").font(.callout).foregroundStyle(.secondary)
            } else {
                Grid(alignment: .trailing, horizontalSpacing: 14, verticalSpacing: 6) {
                    GridRow {
                        Text("課").gridColumnAlignment(.leading)
                        Text("訪客"); Text("打開"); Text("載入"); Text("播放"); Text("失敗"); Text("平均載入")
                        Text("訪客播放"); Text("會員"); Text("看完"); Text("平均進度")
                    }
                    .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    Divider()
                    ForEach(rows) { l in
                        GridRow {
                            Text(l.lesson).font(.callout.monospaced()).gridColumnAlignment(.leading)
                            Text("\(l.guestVisitors)")
                            Text("\(l.guestTracked)")
                            Text(l.guestReady.percent(of: l.guestTracked))
                            Text(l.guestPlayed.percent(of: l.guestTracked)).foregroundStyle(l.guestTracked > 0 && l.guestPlayed * 2 < l.guestTracked ? .orange : .primary)
                            Text(l.guestFailed > 0 ? "\(l.guestFailed)" : "—").foregroundStyle(l.guestFailed > 0 ? .red : .secondary)
                            Text(l.guestReadyAvgMs > 0 ? SiteFmt.ms(l.guestReadyAvgMs) : "—")
                            Text(l.guestWatchedMs > 0 ? SiteFmt.ms(l.guestWatchedMs) : "—")
                            Text("\(l.learners)")
                            Text("\(l.completed)")
                            Text(l.learners > 0 ? String(format: "%.0f%%", l.avgPercent) : "—")
                        }
                        .font(.callout).monospacedDigit()
                    }
                }
            }
        }
    }

    private func breakdownCards(_ d: SiteOverview) -> some View {
        let g = d.guestStats
        return
        HStack(alignment: .top, spacing: 12) {
            countCard("裝置", "每種裝置打開幾次(含爬蟲)", g.devices)
            countCard("進站來源", "真人從哪裡進來", g.sources)
            countCard("沒播放的停留", "打開後沒播放,頁面在前景多久", g.idleVisible)
            countCard("載入錯誤", "真人遇到的播放器錯誤", g.errors)
            countCard("登入門檻", "訪客打開要登入的課", d.gates)
        }
    }

    private func countCard(_ title: String, _ subtitle: String, _ items: [SiteCount]) -> some View {
        let total = max(1, items.map(\.count).max() ?? 1)
        return Card(title, subtitle: subtitle) {
            if items.isEmpty {
                Text("沒有資料").font(.caption).foregroundStyle(.secondary)
            } else {
                VStack(alignment: .leading, spacing: 5) {
                    ForEach(items, id: \.self) { c in
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(Self.label(c.name)).font(.caption).lineLimit(1)
                                Spacer()
                                Text("\(c.count)").font(.caption).monospacedDigit().foregroundStyle(.secondary)
                            }
                            GeometryReader { geo in
                                Capsule().fill(Color.accentColor.opacity(0.5))
                                    .frame(width: geo.size.width * CGFloat(c.count) / CGFloat(total))
                            }
                            .frame(height: 4)
                        }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .top)
    }

    /// evoleap 用的代碼換成好讀的名稱
    static func label(_ name: String) -> String {
        switch name {
        case "": return "(直接進站)"
        case "under10s": return "不到 10 秒"
        case "10to60s": return "10–60 秒"
        case "over60s": return "超過 1 分鐘"
        case "bot": return "爬蟲"
        case "mobile": return "手機"
        case "tablet": return "平板"
        case "desktop": return "電腦"
        default: return name
        }
    }

    private func pathsCard(_ g: SiteGuestBreakdown) -> some View {
        Card("從首頁進來的路徑", subtitle: "第一站是首頁的造訪(相隔 30 分鐘以上算另一次),最常見的在前;▶ 表示那一站有播放") {
            if g.homePaths.isEmpty {
                Text("沒有資料").font(.caption).foregroundStyle(.secondary)
            } else {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(Array(g.homePaths.enumerated()), id: \.offset) { _, p in
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            Text("\(p.count) 次").font(.callout.weight(.semibold)).monospacedDigit().frame(width: 52, alignment: .trailing)
                            Text(p.steps.map { ($0.played ? "▶ " : "") + $0.lesson }.joined(separator: "  →  "))
                                .font(.callout.monospaced()).textSelection(.enabled)
                            Spacer()
                            if p.played > 0 { Pill(text: "有播放 \(p.played)", color: .green) }
                        }
                    }
                }
            }
        }
    }

    private func homeStaysCard(_ g: SiteGuestBreakdown) -> some View {
        Card("首頁停留", subtitle: "每個真人訪客在首頁停留多久(頁面在前景的時間),最近來過的在前") {
            if g.homeStays.isEmpty {
                Text("沒有資料").font(.caption).foregroundStyle(.secondary)
            } else {
                Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 5) {
                    GridRow {
                        Text("最後造訪"); Text("訪客"); Text("裝置"); Text("來源")
                        Text("次數").gridColumnAlignment(.trailing); Text("合計").gridColumnAlignment(.trailing)
                        Text("最長").gridColumnAlignment(.trailing); Text("捲動").gridColumnAlignment(.trailing)
                    }
                    .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    Divider()
                    ForEach(Array(g.homeStays.prefix(20).enumerated()), id: \.offset) { _, h in
                        GridRow {
                            Text(h.lastOpened.map(Fmt.relative) ?? "—")
                            Text(h.guest).font(.callout.monospaced())
                            Text(h.device); Text(h.source.isEmpty ? "直接" : h.source).lineLimit(1)
                            Text("\(h.visits)"); Text(SiteFmt.ms(h.totalMs)); Text(SiteFmt.ms(h.longestMs)); Text("\(h.maxScroll)%")
                        }
                        .font(.callout).monospacedDigit()
                    }
                }
                if g.homeStays.count > 20 { Text("還有 \(g.homeStays.count - 20) 人").font(.caption).foregroundStyle(.secondary) }
            }
        }
    }

    private func loginCard(_ l: SiteLoginStats) -> some View {
        Card("登入／註冊頁", subtitle: "訪客打開登入頁 \(l.visits) 次;Google 按鈕載入 \(l.googleReady) 次(平均 \(l.googleReadyAvgMs > 0 ? SiteFmt.ms(l.googleReadyAvgMs) : "—")),失敗 \(l.googleFailed) 次") {
            HStack(alignment: .top, spacing: 12) {
                miniList("結果", l.outcomes)
                miniList("登入後要去", l.next)
                miniList("失敗訊息", l.fails)
                miniList("Google 按鈕錯誤", l.googleErrors)
            }
        }
    }

    private func miniList(_ title: String, _ items: [SiteCount]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            if items.isEmpty { Text("—").font(.caption).foregroundStyle(.secondary) }
            ForEach(items, id: \.self) { c in
                HStack {
                    Text(c.name).font(.caption).lineLimit(1).textSelection(.enabled)
                    Spacer()
                    Text("\(c.count)").font(.caption).monospacedDigit().foregroundStyle(.secondary)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }

    private func idleVisitsCard(_ g: SiteGuestBreakdown) -> some View {
        Card("最近沒播放的造訪", subtitle: "打開了卻沒有播放的真人造訪(含登入門檻),新的在前;展開看足跡") {
            if g.idleVisits.isEmpty {
                Text("沒有資料").font(.caption).foregroundStyle(.secondary)
            } else {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(Array(g.idleVisits.enumerated()), id: \.offset) { _, v in
                        DisclosureGroup {
                            VStack(alignment: .leading, spacing: 2) {
                                ForEach(Array(v.events.enumerated()), id: \.offset) { _, e in
                                    HStack(spacing: 10) {
                                        Text(SiteFmt.ms(e.t)).foregroundStyle(.secondary).frame(width: 70, alignment: .trailing)
                                        Text(e.e).textSelection(.enabled)
                                    }
                                    .font(.caption.monospaced())
                                }
                                if v.events.isEmpty { Text("沒有足跡").font(.caption).foregroundStyle(.secondary) }
                            }
                            .padding(.leading, 8)
                        } label: {
                            HStack(spacing: 10) {
                                Text(v.openedAt.map(Fmt.relative) ?? "—").font(.caption).foregroundStyle(.secondary).frame(width: 70, alignment: .leading)
                                Text(v.lesson).font(.callout.monospaced())
                                Text("\(v.device) · \(v.source.isEmpty ? "直接" : v.source)").font(.caption).foregroundStyle(.secondary)
                                Text("前景 \(SiteFmt.ms(v.visibleMs))").font(.caption).foregroundStyle(.secondary)
                                if !v.loadError.isEmpty { Pill(text: v.loadError, color: .red) }
                                else if v.readyMs > 0 { Text("載入 \(SiteFmt.ms(v.readyMs))").font(.caption).foregroundStyle(.secondary) }
                            }
                        }
                    }
                }
            }
        }
    }
}
