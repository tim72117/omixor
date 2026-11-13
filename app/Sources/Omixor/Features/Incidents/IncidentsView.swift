import SwiftUI

/// 事件管理:事件列表 + 詳情、值班、統計
struct IncidentsView: View {
    @EnvironmentObject var app: AppState
    @State private var tab: IncTab = .incidents
    @State private var selectedId: String?
    @State private var showDeclare = false
    @State private var compactShowsDetail = false

    enum IncTab: String, CaseIterable, Identifiable {
        case incidents, oncall, stats
        var id: String { rawValue }
        var label: String {
            switch self {
            case .incidents: return "事件"
            case .oncall: return "值班"
            case .stats: return "統計"
            }
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 20).padding(.top, 16).padding(.bottom, 12)
            Divider()
            switch app.shownTab(tab) {
            case .incidents: incidentsPane
            case .oncall: IncOnCallView()
            case .stats: IncStatsView(onSelect: { id in select(id) })
            }
        }
        .sheet(isPresented: $showDeclare) {
            IncDeclareSheet { newId in select(newId) }
        }
        .onAppear {
            applyFocus()
            if selectedId == nil { selectedId = defaultSelection }
        }
        .onChange(of: app.focusId) { _, _ in applyFocus() }
        // 系統設定關掉的分頁:切到第一個開著的並提示(例如從別頁跳過來)
        .onChange(of: tab) { _, t in if let r = app.redirectTab(t) { tab = r } }
        .onChange(of: app.incidents.map(\.id)) { _, ids in
            if let s = selectedId, ids.contains(s) { return }
            selectedId = defaultSelection
        }
    }

    private var header: some View {
        PageHeader("事件", subtitle: subtitle) {
            HStack(spacing: 10) {
                Picker("分頁", selection: Binding(get: { app.shownTab(tab) }, set: { tab = $0 })) {
                    ForEach(app.visibleTabs(IncTab.self)) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
                Button { showDeclare = true } label: {
                    Label("宣告事件", systemImage: "flame.fill")
                }
                .buttonStyle(.borderedProminent)
                .tint(.red)
                .keyboardShortcut("n", modifiers: [.command, .shift])
                .help("宣告新的事件(⇧⌘N)")
            }
        }
    }

    private var subtitle: String {
        let open = app.openIncidents
        if open.isEmpty { return "目前沒有進行中的事件" }
        let worst = open.map(\.sev).min() ?? 4
        return "\(open.count) 起進行中 · 最高 \(IncSev.label(worst))"
    }

    private var incidentsPane: some View {
        GeometryReader { g in
            let compact = g.size.width < 680
            if compact {
                if compactShowsDetail, let id = selectedId, app.incident(id) != nil {
                    VStack(spacing: 0) {
                        HStack {
                            Button { compactShowsDetail = false } label: {
                                Label("事件列表", systemImage: "chevron.left")
                            }
                            .buttonStyle(.borderless)
                            Spacer()
                        }
                        .padding(.horizontal, 14).padding(.vertical, 8)
                        Divider()
                        IncDetailView(incidentId: id)
                    }
                } else {
                    IncListPane(selectedId: Binding(get: { selectedId }, set: { v in
                        selectedId = v
                        if v != nil { compactShowsDetail = true }
                    }))
                }
            } else {
                let listW = min(360, max(260, g.size.width * 0.32))
                HStack(spacing: 0) {
                    IncListPane(selectedId: $selectedId)
                        .frame(width: listW)
                    Divider()
                    Group {
                        if let id = selectedId, app.incident(id) != nil {
                            IncDetailView(incidentId: id)
                        } else {
                            EmptyStateView(title: "選一個事件", icon: "flame",
                                           message: app.incidents.isEmpty ? "目前沒有任何事件紀錄。有狀況時按「宣告事件」。" : "從左側列表選擇事件查看詳情。")
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
    }

    private var defaultSelection: String? {
        let open = app.openIncidents.sorted { ($0.sev, $0.startedAt) < ($1.sev, $1.startedAt) }
        return open.first?.id ?? app.incidents.max { $0.startedAt < $1.startedAt }?.id
    }

    private func select(_ id: String) {
        tab = .incidents
        selectedId = id
        compactShowsDetail = true
    }

    private func applyFocus() {
        guard let f = app.focusId, app.incident(f) != nil else { return }
        select(f)
        app.focusId = nil
    }
}
