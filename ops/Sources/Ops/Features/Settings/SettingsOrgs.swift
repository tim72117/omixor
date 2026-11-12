import SwiftUI

// 設定 › 組織:新增、切換、改名、刪除組織,以及目前組織工作空間的各項設定入口

struct SettingsOrgsPane: View {
    @EnvironmentObject var app: AppState
    @AppStorage(SettingsKeys.pane) private var pane: SettingsPane = .orgs
    @State private var creating = false
    @State private var editing: Organization?
    @State private var deleting: Organization?
    @State private var editingLayout = false
    @State private var error: String?
    @AppStorage(IssueServerConfig.defaultsKey) private var issueServer = ""
    @State private var issueServerDraft = ""
    @State private var issueServerStatus: String?
    @State private var omixorAccount: OmixorAccount?
    @State private var omixorBusy = false
    @State private var omixorError: String?

    var body: some View {
        Form {
            Section {
                ForEach(app.orgs) { o in
                    HStack(spacing: 10) {
                        OrgBadge(org: o, size: 28)
                        VStack(alignment: .leading, spacing: 1) {
                            HStack(spacing: 6) {
                                Text(o.name).font(.headline)
                                if o.id == app.currentOrgId { Pill(text: "使用中", color: .green) }
                            }
                            Text(o.note.isEmpty ? "id:\(o.id)" : o.note).font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        if o.id != app.currentOrgId {
                            Button("切換") { app.switchOrg(o.id) }
                        }
                        Button("編輯…") { editing = o }
                        if o.id != app.currentOrgId {
                            Button(role: .destructive) { deleting = o } label: { Image(systemName: "trash") }
                                .buttonStyle(.borderless)
                        }
                    }
                    .padding(.vertical, 2)
                }
                Button { creating = true } label: { Label("新增組織…", systemImage: "plus") }
            } header: {
                Text("組織")
            } footer: {
                Text("每個組織有自己的 Issue、虛擬角色與任務、文件櫃與結構、連線(登入也分開)。旁白批次與畫面上的範例資料是共用的。")
            }
            if let e = error {
                Section { Text(e).foregroundStyle(.orange) }
            }
            Section {
                setupRow("虛擬角色", "person.2", "\(app.agents.count) 個角色;角色設定、例行任務、文件存取範圍") {
                    app.go(.automation, focus: "agents")
                }
                setupRow("文件櫃結構", "archivebox", "\(CabinetLayout.sections.count) 個區塊、\(CabinetLayout.sections.reduce(0) { $0 + $1.tabs.count }) 個頁籤") {
                    editingLayout = true
                }
                setupRow("連線", "key.horizontal", "\(app.connections.count) 個連線;外部服務的網址與登入") {
                    pane = .connections
                }
                setupRow("Issue", "exclamationmark.bubble", "\(app.issues.filter { $0.status == .open }.count) 件未解決") {
                    app.go(.development, focus: "issues")
                }
            } header: {
                Text("「\(app.currentOrg?.name ?? "")」的工作空間")
            }
            issueServerSection
        }
        .formStyle(.grouped)
        .onAppear {
            app.ensureOrgs()
            app.startAgents()
            app.startIssues()
            app.startCabinet()
            app.startConnections()
        }
        .sheet(isPresented: $creating) { NewOrgSheet() }
        .sheet(item: $editing) { o in OrgEditSheet(original: o) }
        .sheet(isPresented: $editingLayout) { CabinetLayoutEditor() }
        .confirmationDialog("刪除組織「\(deleting?.name ?? "")」?", isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
                            presenting: deleting) { o in
            Button("刪除(資料夾移到垃圾桶)", role: .destructive) {
                do { try app.deleteOrg(o.id); error = nil } catch { self.error = error.localizedDescription }
            }
        } message: { _ in
            Text("這個組織的 Issue、角色、任務、文件與連線設定會一起移到垃圾桶,鑰匙圈裡的登入會刪除。")
        }
    }

    /// Issue 伺服器:填了就改透過 Go 版 ops-web 讀寫 Issue(IssueRemote.swift);空的 = 存在這台 Mac 的 issues.json
    @ViewBuilder private var issueServerSection: some View {
        let env = ProcessInfo.processInfo.environment["OPS_ISSUE_SERVER"]
        Section {
            HStack {
                TextField("Issue 伺服器網址", text: $issueServerDraft, prompt: Text("http://127.0.0.1:5191"))
                    .disabled(env != nil)
                    .onSubmit(applyIssueServer)
                Button(issueServerDraft.isEmpty ? "關閉" : "套用", action: applyIssueServer)
                    .disabled(env != nil || issueServerDraft.trimmingCharacters(in: .whitespaces) == issueServer)
            }
            if let env { Text("由環境變數 OPS_ISSUE_SERVER 設定:\(env)").font(.caption).foregroundStyle(.secondary) }
            if let st = issueServerStatus { Text(st).font(.caption).foregroundStyle(.secondary) }
            if let server = IssueServerConfig.current?.url {
                omixorLoginRow(server)
            }
        } header: {
            Text("Issue 伺服器")
        } footer: {
            Text("留空:Issue 存在這台 Mac(issues.json)。填 omixor-issues(或 Go 版 ops-web)的網址:Issue 改存在那台伺服器,各組織分開;畫面與 MCP 工具用法不變。正式的 omixor-issues 要登入 omixor:按「用瀏覽器登入」,在 evoleap 的核准頁核准後,omixor 依自己的使用者名單決定能不能用。")
        }
        .onAppear { issueServerDraft = issueServer }
        .task(id: issueServer) { await refreshOmixorAccount() }
    }

    /// 登入 omixor 的狀態與按鈕
    @ViewBuilder private func omixorLoginRow(_ server: URL) -> some View {
        HStack(alignment: .firstTextBaseline) {
            if let a = omixorAccount {
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("已登入 omixor:\(a.email)(\(a.role == "admin" ? "管理員" : "成員"))")
                        if let exp = a.expiresAt {
                            Text("到期:\(exp.formatted(date: .abbreviated, time: .omitted))").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                } icon: { Image(systemName: "person.crop.circle.badge.checkmark").foregroundStyle(.green) }
                Spacer()
                Button("登出") {
                    Task {
                        omixorBusy = true
                        await OmixorAuth.logout(server: server)
                        omixorAccount = nil
                        omixorBusy = false
                        app.omixorLogin = .needLogin   // 回到登入畫面
                    }
                }
                .disabled(omixorBusy)
            } else {
                Label("還沒登入 omixor", systemImage: "person.crop.circle.badge.questionmark").foregroundStyle(.secondary)
                Spacer()
                Button(omixorBusy ? "等瀏覽器核准…" : "用瀏覽器登入") {
                    Task {
                        omixorBusy = true
                        omixorError = nil
                        do {
                            let a = try await OmixorAuth.login(app, server: server)
                            omixorAccount = a
                            app.omixorLogin = .loggedIn(a)
                            app.startIssues().load()
                        } catch {
                            omixorError = error.localizedDescription
                        }
                        omixorBusy = false
                    }
                }
                .disabled(omixorBusy)
            }
        }
        if let e = omixorError { Text(e).font(.caption).foregroundStyle(.orange) }
        if omixorAccount != nil {
            let cur = app.currentOrgId
            VStack(alignment: .leading, spacing: 4) {
                Text("伺服器上我能用的組織").font(.caption).foregroundStyle(.secondary)
                if app.serverOrgs.isEmpty {
                    Text("沒有(請 omixor 管理員把你加進組織)").font(.caption).foregroundStyle(.orange)
                }
                ForEach(app.serverOrgs) { o in
                    HStack(spacing: 6) {
                        Image(systemName: o.id == cur ? "checkmark.circle.fill" : "building.2").foregroundStyle(o.id == cur ? .green : .secondary)
                        Text(o.name)
                        Text(o.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                        if let r = o.myRole { Text(r == "admin" ? "管理員" : "成員").font(.caption2).foregroundStyle(.secondary) }
                    }
                    .font(.callout)
                }
                if !app.serverOrgs.isEmpty, !app.serverOrgs.contains(where: { $0.id == cur }) {
                    Text("目前的組織(\(cur))不在伺服器上,Issue 會讀寫失敗;請伺服器管理員用 omixor orgs create \(cur) 建立。")
                        .font(.caption).foregroundStyle(.orange)
                }
            }
            .task { if let orgs = await OmixorAuth.fetchOrgs(server: server) { app.serverOrgs = orgs } }
        }
    }

    private func refreshOmixorAccount() async {
        guard let server = IssueServerConfig.current?.url else { omixorAccount = nil; return }
        omixorAccount = await OmixorAuth.me(server: server)
    }

    private func applyIssueServer() {
        let v = issueServerDraft.trimmingCharacters(in: .whitespaces)
        if !v.isEmpty, URL(string: v)?.host == nil || !(v.hasPrefix("http://") || v.hasPrefix("https://")) {
            issueServerStatus = "網址要是 http:// 或 https:// 開頭"
            return
        }
        issueServer = v
        let store = app.startIssues()
        store.load()
        if v.isEmpty {
            issueServerStatus = "已改回本機 issues.json"
        } else if let e = store.remoteError {
            issueServerStatus = "連線失敗:\(e)"
        } else {
            issueServerStatus = "已連上,讀到 \(app.issues.count) 筆 Issue"
        }
    }

    private func setupRow(_ title: String, _ icon: String, _ detail: String, action: @escaping () -> Void) -> some View {
        HStack {
            Label(title, systemImage: icon)
            Spacer()
            Text(detail).font(.caption).foregroundStyle(.secondary)
            Button("設定…", action: action)
        }
    }
}

struct OrgBadge: View {
    let org: Organization
    var size: CGFloat = 24
    var body: some View {
        Image(systemName: org.symbol)
            .font(.system(size: size * 0.5, weight: .semibold))
            .foregroundStyle(.white)
            .frame(width: size, height: size)
            .background(AgentPalette.color(org.color).gradient, in: RoundedRectangle(cornerRadius: size * 0.25))
    }
}

/// 側邊欄最上方:目前的組織與切換
struct OrgSwitcher: View {
    @EnvironmentObject var app: AppState
    @AppStorage(SettingsKeys.pane) private var pane: SettingsPane = .orgs
    @State private var creating = false

    var body: some View {
        Menu {
            ForEach(app.orgs) { o in
                Button {
                    app.switchOrg(o.id)
                } label: {
                    if o.id == app.currentOrgId { Label(o.name, systemImage: "checkmark") } else { Text(o.name) }
                }
            }
            Divider()
            Button("新增組織…") { creating = true }
            Button("管理組織…") { pane = .orgs; app.go(.settings) }
        } label: {
            HStack(spacing: 8) {
                if let o = app.currentOrg { OrgBadge(org: o, size: 22) }
                Text(app.currentOrg?.name ?? "組織").font(.headline).lineLimit(1)
                Spacer()
                Image(systemName: "chevron.up.chevron.down").font(.caption).foregroundStyle(.secondary)
            }
            .contentShape(Rectangle())
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .onAppear { app.ensureOrgs() }
        .sheet(isPresented: $creating) { NewOrgSheet() }
    }
}

private let orgSymbols = ["building.2", "graduationcap", "briefcase", "storefront", "hammer", "flask", "globe", "heart", "leaf", "star"]

private struct NewOrgSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var symbol = "building.2"
    @State private var color = "indigo"
    @State private var template: OrgTemplate = .blank
    @State private var switchNow = true

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("新增組織").font(.title3.weight(.semibold))
            Form {
                TextField("名稱", text: $name, prompt: Text("例如 新產品實驗室"))
                Picker("圖示", selection: $symbol) {
                    ForEach(orgSymbols, id: \.self) { Label($0, systemImage: $0).labelStyle(.iconOnly).tag($0) }
                }
                Picker("顏色", selection: $color) {
                    ForEach(AgentPalette.names, id: \.self) { Text($0).foregroundStyle(AgentPalette.color($0)).tag($0) }
                }
                Picker("起始設定", selection: $template) {
                    ForEach(OrgTemplate.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.radioGroup)
                Text(template.detail).font(.caption).foregroundStyle(.secondary)
                Toggle("建立後切換過去", isOn: $switchNow)
            }
            .formStyle(.grouped)
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("建立") {
                    let o = app.createOrg(name: name.trimmingCharacters(in: .whitespaces), symbol: symbol, color: color, template: template)
                    if switchNow { app.switchOrg(o.id) }
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 520)
    }
}

private struct OrgEditSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let original: Organization
    @State private var draft: Organization

    init(original: Organization) {
        self.original = original
        _draft = State(initialValue: original)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("編輯組織").font(.title3.weight(.semibold))
            Form {
                TextField("名稱", text: $draft.name)
                TextField("說明", text: $draft.note, prompt: Text("選填"))
                Picker("圖示", selection: $draft.symbol) {
                    ForEach(orgSymbols, id: \.self) { Label($0, systemImage: $0).labelStyle(.iconOnly).tag($0) }
                }
                Picker("顏色", selection: $draft.color) {
                    ForEach(AgentPalette.names, id: \.self) { Text($0).foregroundStyle(AgentPalette.color($0)).tag($0) }
                }
                LabeledContent("id", value: draft.id)
            }
            .formStyle(.grouped)
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("儲存") { app.updateOrg(draft); dismiss() }
                    .buttonStyle(.borderedProminent)
                    .disabled(draft.name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 480)
    }
}

/// 文件櫃結構編輯:區塊與頁籤可新增、改名、刪除(裡面還有文件時不能刪)
struct CabinetLayoutEditor: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var draft: [CabinetSection] = CabinetLayout.sections
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("文件櫃結構(\(app.currentOrg?.name ?? ""))").font(.title3.weight(.semibold))
            Text("區塊是第一層,頁籤是區塊底下的分類。角色的文件存取範圍以區塊或頁籤為單位。").font(.caption).foregroundStyle(.secondary)
            List {
                ForEach($draft) { $s in
                    Section {
                        ForEach($s.tabs) { $t in
                            HStack {
                                Image(systemName: "doc.text").foregroundStyle(.secondary)
                                TextField("頁籤名稱", text: $t.label)
                                Text(t.path).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1)
                                Button(role: .destructive) { s.tabs.removeAll { $0.path == t.path } } label: { Image(systemName: "minus.circle") }
                                    .buttonStyle(.borderless)
                            }
                        }
                        Button {
                            s.tabs.append(CabinetTab(path: newTabId(in: s), label: "新頁籤"))
                        } label: { Label("新增頁籤", systemImage: "plus") }
                        .buttonStyle(.borderless)
                    } header: {
                        HStack {
                            Image(systemName: "folder")
                            TextField("區塊名稱", text: $s.label).font(.headline)
                            Text(s.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                            Button { move(s.id, -1) } label: { Image(systemName: "arrow.up") }.buttonStyle(.borderless)
                            Button { move(s.id, 1) } label: { Image(systemName: "arrow.down") }.buttonStyle(.borderless)
                            Button(role: .destructive) { draft.removeAll { $0.id == s.id } } label: { Image(systemName: "trash") }
                                .buttonStyle(.borderless)
                        }
                    }
                }
            }
            .frame(minHeight: 380)
            if let error { Text(error).foregroundStyle(.orange).font(.callout) }
            HStack {
                Button { draft.append(CabinetSection(id: newSectionId(), label: "新區塊")) } label: { Label("新增區塊", systemImage: "folder.badge.plus") }
                Menu("套用範本") {
                    Button("evoleap 研發基地") { draft = CabinetLayout.evoleapStudio }
                    Button("空白(總覽、文件)") { draft = CabinetLayout.blank }
                }
                .fixedSize()
                Spacer()
                Button("取消") { dismiss() }
                Button("儲存") {
                    if let e = Cabinet.validate(app, draft) { error = e; return }
                    Cabinet.saveLayout(app, draft)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(20)
        .frame(width: 640, height: 640)
        .onAppear { app.startCabinet(); draft = CabinetLayout.sections }
    }

    private func newSectionId() -> String {
        var n = draft.count + 1
        while draft.contains(where: { $0.id == "s\(n)" }) { n += 1 }
        return "s\(n)"
    }

    private func newTabId(in s: CabinetSection) -> String {
        var n = s.tabs.count + 1
        let all = Set(draft.flatMap { $0.tabs.map(\.path) })
        while all.contains("/\(s.id)/\(n)") { n += 1 }
        return "/\(s.id)/\(n)"
    }

    private func move(_ id: String, _ d: Int) {
        guard let i = draft.firstIndex(where: { $0.id == id }) else { return }
        let j = i + d
        guard draft.indices.contains(j) else { return }
        draft.swapAt(i, j)
    }
}
