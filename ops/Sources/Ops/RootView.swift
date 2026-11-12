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
        .safeAreaInset(edge: .bottom) { AccountFooter() }
    }

    private func row(_ s: AppSection) -> some View {
        Label(s.title, systemImage: s.icon)
            .badge(app.badge(for: s) ?? 0)
            .tag(s)
    }
}

/// 側邊欄底部:登入的 omixor 帳號(資料來自 omixor 伺服器的 /api/auth/me);點一下到設定看詳細與登出
private struct AccountFooter: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        Button { app.go(.settings) } label: {
            HStack(spacing: 8) {
                avatar
                VStack(alignment: .leading, spacing: 1) {
                    Text(subtitle).font(.caption2).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    Text(title).font(.caption.weight(.medium)).lineLimit(1).truncationMode(.middle)
                }
                Spacer(minLength: 0)
                if let a = app.omixorLogin.account {
                    Text(a.role == "admin" ? "管理員" : "成員")
                        .font(.caption2.weight(.medium))
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background((a.role == "admin" ? Color.purple : Color.blue).opacity(0.15), in: Capsule())
                        .foregroundStyle(a.role == "admin" ? Color.purple : Color.blue)
                }
            }
            .padding(10)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
        .padding(10)
        .help(app.omixorLogin.account.map { "\($0.email)(\($0.device))· 點一下到設定" } ?? "點一下到設定")
    }

    private var title: String {
        guard let a = app.omixorLogin.account else { return "沒有登入" }
        return a.name.isEmpty ? a.email : a.name
    }

    private var subtitle: String {
        switch app.omixorLogin {
        case .loggedIn(let a): return a.name.isEmpty ? "已登入 omixor" : a.email
        case .skipped: return "離線使用"
        case .unreachable: return "連不到 omixor"
        case .checking: return "確認登入中…"
        case .needLogin: return "還沒登入"
        }
    }

    private var avatar: some View {
        let initial = String(title.first.map { String($0) } ?? "?").uppercased()
        return Text(initial)
            .font(.caption.weight(.bold)).foregroundStyle(.white)
            .frame(width: 24, height: 24)
            .background(app.omixorLogin.account == nil ? Color.gray : Color.accentColor, in: Circle())
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
