import SwiftUI

struct RootView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        NavigationSplitView {
            SidebarView()
                .navigationSplitViewColumnWidth(min: 200, ideal: 230, max: 300)
        } detail: {
            // 用 GeometryReader 擋住頁面內容的最小寬度,避免往外撐而把側邊欄和右欄擠出視窗
            GeometryReader { g in
                detail
                    .frame(width: g.size.width, height: g.size.height, alignment: .topLeading)
                    .clipped()
            }
            .background(Color(nsColor: .windowBackgroundColor))
                .inspector(isPresented: $app.showAssistant) {
                    AssistantPanel()
                        .inspectorColumnWidth(min: 280, ideal: 330, max: 440)
                }
        }
        .navigationTitle(app.section.title)
        .toolbar { MainToolbar() }
        .sheet(isPresented: $app.showCommandPalette) { CommandPalette() }
    }

    @ViewBuilder private var detail: some View {
        switch app.section {
        case .overview: OverviewView()
        case .development: DevelopmentView()
        case .pipelines: PipelinesView()
        case .infrastructure: InfrastructureView()
        case .monitoring: MonitoringView()
        case .incidents: IncidentsView()
        case .automation: AutomationView()
        case .cabinet: CabinetView()
        case .settings: SettingsView()
        }
    }
}

struct SidebarView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        List(selection: Binding(get: { app.section }, set: { if let s = $0 { app.go(s) } })) {
            Section("維運") {
                ForEach([AppSection.overview, .development, .pipelines, .infrastructure, .monitoring, .incidents], id: \.self) { row($0) }
            }
            Section("自動化") { row(.automation); row(.cabinet) }
            Section { row(.settings) }
        }
        .listStyle(.sidebar)
        .safeAreaInset(edge: .top) { OrgSwitcher() }
        .safeAreaInset(edge: .bottom) { OnCallFooter() }
    }

    private func row(_ s: AppSection) -> some View {
        Label(s.title, systemImage: s.icon)
            .badge(app.badge(for: s) ?? 0)
            .tag(s)
    }
}

/// 側邊欄底部:目前值班
private struct OnCallFooter: View {
    @EnvironmentObject var app: AppState
    var body: some View {
        if let shift = app.currentOnCall {
            HStack(spacing: 8) {
                ActorAvatar(actor: .person(shift.person), size: 24)
                VStack(alignment: .leading, spacing: 1) {
                    Text("值班中").font(.caption2).foregroundStyle(.secondary)
                    Text(app.person(shift.person)?.name ?? shift.person).font(.caption.weight(.medium))
                }
                Spacer()
                Text("到 \(Fmt.time(shift.end))").font(.caption2).foregroundStyle(.secondary)
            }
            .padding(10)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            .padding(10)
        }
    }
}

struct MainToolbar: ToolbarContent {
    @EnvironmentObject var app: AppState

    var body: some ToolbarContent {
        ToolbarItemGroup(placement: .principal) {
            Picker("環境", selection: $app.env) {
                ForEach(DeployEnv.allCases) { Text($0.label).tag($0) }
            }
            .pickerStyle(.segmented)
            .help("環境")
            Picker("時間範圍", selection: $app.timeRange) {
                ForEach(TimeRange.allCases) { Text($0.label).tag($0) }
            }
            .help("時間範圍")
        }
        ToolbarItemGroup(placement: .primaryAction) {
            Button { app.showCommandPalette = true } label: { Label("搜尋", systemImage: "magnifyingglass") }
                .help("指令面板(⌘K)")
            Button { app.showAssistant.toggle() } label: { Label("AI 助理", systemImage: "sparkles") }
                .help("AI 助理(⌘J)")
        }
    }
}
