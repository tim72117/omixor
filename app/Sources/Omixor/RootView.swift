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

/// 側邊欄底部:資料來源與帳號(同 Go 版 AccountBox.tsx)。本機(這台 Mac 的資料)和遠端(啟動設定裡的 omixor,
/// 用 evoleap 授權)完全分開;點一下可以切換,有設定遠端或在遠端時一直看得到「登出」(登出後回到選擇連接對象)
private struct AccountFooter: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        VStack(spacing: 6) {
            Menu {
                Button { app.connectLocal() } label: {
                    if remote == nil { Label("本機", systemImage: "checkmark") } else { Label("本機", systemImage: "internaldrive") }
                }
                ForEach(OmixorMode.servers) { s in
                    Button {
                        guard remote != s else { return }
                        Task { await app.connectRemote(s, auth: s.auth.first?.id) }
                    } label: {
                        if remote == s { Label(s.name, systemImage: "checkmark") } else { Label(s.name, systemImage: "cloud") }
                    }
                }
            } label: {
                HStack(spacing: 8) {
                    avatar
                    VStack(alignment: .leading, spacing: 1) {
                        Text(title).font(.caption.weight(.medium)).lineLimit(1).truncationMode(.middle)
                        Text(subtitle).font(.caption2).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                    Spacer(minLength: 0)
                    if app.connectError != nil || isProblem { Circle().fill(.orange).frame(width: 7, height: 7) }
                    Image(systemName: "chevron.up.chevron.down").font(.caption2).foregroundStyle(.secondary)
                }
                .padding(10)
                .contentShape(Rectangle())
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .disabled(app.connecting != nil)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))
            .help(help)
            if let name = app.connecting {
                Text("請在瀏覽器完成 \(name) 的核准…").font(.caption2).foregroundStyle(.secondary)
            } else if let e = app.connectError {
                Text(e).font(.caption2).foregroundStyle(.red).lineLimit(3)
            }
            if remote != nil || !OmixorMode.servers.isEmpty {
                Button { Task { await app.omixorLogout() } } label: {
                    Label("登出", systemImage: "power").font(.caption)
                }
                .buttonStyle(.borderless)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 4)
                .disabled(app.connecting != nil)
                .help("登出並回到選擇連接對象")
            }
        }
        .padding(10)
    }

    private var remote: OmixorMode.Server? { app.remoteServer }

    private var isProblem: Bool {
        guard remote != nil else { return false }
        if case .loggedIn = app.omixorLogin { return false }
        return true
    }

    private var title: String {
        guard remote != nil else { return "本機使用者" }
        guard let a = app.omixorLogin.account else { return "\(remote?.name ?? "遠端"):未登入" }
        return a.name.isEmpty ? a.email : a.name
    }

    private var subtitle: String {
        guard let s = remote else { return "本機 · 檔案" }
        switch app.omixorLogin {
        case .loggedIn(let a): return s.name + (a.role == "admin" ? " · 管理員" : " · 成員")
        case .unreachable: return "\(s.name) · 連不到"
        case .checking: return "\(s.name) · 確認登入中…"
        case .needLogin, .skipped: return "\(s.name) · 登入已失效,請重新連接"
        }
    }

    private var help: String {
        if case .unreachable(let m) = app.omixorLogin { return m }
        if let a = app.omixorLogin.account { return "\(a.email)(\(a.device))· 點一下切換連接對象" }
        return "點一下切換連接對象"
    }

    private var avatar: some View {
        Group {
            if remote == nil {
                Image(systemName: "internaldrive").font(.caption.weight(.bold))
            } else {
                Text(String(title.first.map { String($0) } ?? "?").uppercased()).font(.caption.weight(.bold))
            }
        }
        .foregroundStyle(.white)
        .frame(width: 24, height: 24)
        .background(remote == nil ? Color.gray : (app.omixorLogin.account == nil ? Color.orange : Color.accentColor), in: Circle())
    }
}

struct MainToolbar: ToolbarContent {
    @EnvironmentObject var app: AppState

    /// 會讀工具列環境(正式、預備、開發)的區塊
    static let envSections: Set<AppSection> = [.overview, .pipelines, .infrastructure]
    /// 會讀工具列時間範圍的區塊
    static let rangeSections: Set<AppSection> = [.overview, .infrastructure, .monitoring]

    var body: some ToolbarContent {
        // 同 web 版(web/ui/src/App.tsx 的 ENV_SECTIONS、RANGE_SECTIONS):只有會讀環境、時間範圍的區塊才顯示切換;
        // Issue、文件櫃、自動化等的資料屬於組織,和環境無關
        ToolbarItemGroup(placement: .principal) {
            if Self.envSections.contains(app.section) {
                Picker("環境", selection: $app.env) {
                    ForEach(DeployEnv.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .help("環境")
            }
            if Self.rangeSections.contains(app.section) {
                Picker("時間範圍", selection: $app.timeRange) {
                    ForEach(TimeRange.allCases) { Text($0.label).tag($0) }
                }
                .help("時間範圍")
            }
        }
        ToolbarItemGroup(placement: .primaryAction) {
            Button { app.showCommandPalette = true } label: { Label("搜尋", systemImage: "magnifyingglass") }
                .help("指令面板(⌘K)")
            Button { app.showAssistant.toggle() } label: { Label("AI 助理", systemImage: "sparkles") }
                .help("AI 助理(⌘J)")
        }
    }
}
