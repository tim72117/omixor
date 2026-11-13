import SwiftUI

// 設定 › 組織:新增、切換、改名、刪除組織,以及目前組織工作空間的各項設定入口。
// 遠端模式:組織來自 omixor server,新增與編輯(名稱、圖示、顏色、備註)寫回伺服器,要伺服器管理員;不能在 app 刪除。

struct SettingsOrgsPane: View {
    @EnvironmentObject var app: AppState
    @AppStorage(SettingsKeys.pane) private var pane: SettingsPane = .orgs
    @State private var creating = false
    @State private var editing: Organization?
    @State private var deleting: Organization?
    @State private var editingLayout = false
    @State private var error: String?
    /// 正在刪除的組織(等本機服務回應;期間刪除按鈕停用)
    @State private var removing: String?
    /// 遠端 omixor 的版本(GET /api/version)
    @State private var serverVersion: String?

    private var remote: OmixorMode.Server? { app.remoteServer }
    /// 能不能新增、編輯組織:本機都可以;遠端要伺服器管理員
    private var canEditOrgs: Bool { remote == nil || app.omixorLogin.account?.role == "admin" }

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
                        if canEditOrgs { Button("編輯…") { editing = o } }
                        if o.id != app.currentOrgId, remote == nil {
                            if removing == o.id {
                                ProgressView().controlSize(.small)
                            } else {
                                Button(role: .destructive) { deleting = o } label: { Image(systemName: "trash") }
                                    .buttonStyle(.borderless).disabled(removing != nil)
                            }
                        }
                    }
                    .padding(.vertical, 2)
                }
                if canEditOrgs {
                    Button { creating = true } label: { Label("新增組織…", systemImage: "plus") }
                }
            } header: {
                Text(remote.map { "組織(\($0.name))" } ?? "組織(本機)")
            } footer: {
                if remote != nil {
                    Text("組織的名稱、圖示、顏色、備註以 omixor server 為準,Issue、虛擬角色與任務也存在伺服器;新增與編輯要伺服器管理員,成員由組織管理員用 omixor CLI 管理。文件櫃、連線還是存在這台 Mac。")
                } else {
                    Text("每個組織有自己的 Issue、虛擬角色與任務、文件櫃與結構、連線(登入也分開)。旁白批次與畫面上的範例資料是共用的。")
                }
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
                // 組織樣板(例如家庭助理;功能開關關閉時伺服器不列,這裡就不顯示)
                OrgTemplatesRows()
                setupRow("Issue", "exclamationmark.bubble", "\(app.issues.filter { $0.status == .open }.count) 件未解決") {
                    app.go(.development, focus: "issues")
                }
            } header: {
                Text("「\(app.currentOrg?.name ?? "")」的工作空間")
            }
            connectionSection
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
            Button("刪除", role: .destructive) {
                guard removing == nil else { return }
                removing = o.id
                error = nil
                Task {
                    do { try await app.deleteOrg(o.id) } catch { self.error = error.localizedDescription }
                    removing = nil
                }
            }
        } message: { _ in
            Text("本機服務裡這個組織的 Issue、角色與任務、頻道、文件櫃、事件會永久刪除;組織資料夾(連線設定等)移到垃圾桶,鑰匙圈裡的登入會刪除。")
        }
    }

    /// 連接對象:本機或遠端 omixor(遠端只能選啟動設定裡的,不顯示網址);切換也可以從左下角的帳號選單
    @ViewBuilder private var connectionSection: some View {
        Section {
            if let s = remote {
                HStack(alignment: .firstTextBaseline) {
                    if let a = app.omixorLogin.account {
                        Label {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("\(s.name):\(a.email)(\(a.role == "admin" ? "伺服器管理員" : "成員"))")
                                if let exp = a.expiresAt {
                                    Text("登入到期:\(exp.formatted(date: .abbreviated, time: .omitted))").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        } icon: { Image(systemName: "cloud").foregroundStyle(.green) }
                    } else {
                        Label("\(s.name):登入已失效或連不到", systemImage: "exclamationmark.icloud").foregroundStyle(.orange)
                    }
                    Spacer()
                    Button("登出") { Task { await app.omixorLogout() } }
                }
                LabeledContent("伺服器版本", value: serverVersion ?? "查詢中…")
                    .font(.caption)
                    .task(id: s.url) {
                        do { serverVersion = try await IssueRemote.fetchVersion(s.url).label }
                        catch { serverVersion = "查不到(\(error.localizedDescription))" }
                    }
            } else {
                Label("本機:資料存在這台 Mac,不用登入", systemImage: "internaldrive")
            }
            if !OmixorMode.servers.isEmpty {
                HStack {
                    Text("切換到").foregroundStyle(.secondary)
                    if remote != nil { Button("本機") { app.connectLocal() } }
                    ForEach(OmixorMode.servers.filter { $0 != remote }) { s in
                        Button(s.name) { Task { await app.connectRemote(s, auth: s.auth.first?.id) } }
                    }
                }
                .disabled(app.connecting != nil)
            }
            if let name = app.connecting {
                HStack {
                    Text("請在瀏覽器完成 \(name) 的核准…").font(.caption).foregroundStyle(.secondary)
                    Button("取消") { app.cancelConnect() }.buttonStyle(.link).font(.caption)
                }
            }
            if let e = app.connectError { Text(e).font(.caption).foregroundStyle(.orange) }
        } header: {
            Text("連接對象")
        } footer: {
            Text(OmixorMode.servers.isEmpty
                 ? "沒有設定遠端 omixor,只能用本機。要連遠端:在資料夾放 servers.json,或設環境變數 OMIXOR_SERVERS。"
                 : "本機和遠端的組織、Issue 完全分開。遠端用 evoleap 帳號證明身分,能不能用由 omixor 的使用者名單決定。")
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
            if app.remoteServer == nil || app.omixorLogin.account?.role == "admin" { Button("新增組織…") { creating = true } }
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
    @State private var busy = false
    @State private var error: String?
    /// 遠端 omixor 的版本(GET /api/version)
    @State private var serverVersion: String?

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
                if let s = app.remoteServer { Text("會建立在 \(s.name) 上(要伺服器管理員)。").font(.caption).foregroundStyle(.secondary) }
            }
            .formStyle(.grouped)
            if let error { Text(error).font(.callout).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button(busy ? "建立中…" : "建立") {
                    let n = name.trimmingCharacters(in: .whitespaces)
                    guard app.remoteServer != nil else {
                        let o = app.createOrg(name: n, symbol: symbol, color: color, template: template)
                        if switchNow { app.switchOrg(o.id) }
                        dismiss()
                        return
                    }
                    Task {
                        busy = true
                        defer { busy = false }
                        do {
                            let o = try await app.createRemoteOrg(name: n, symbol: symbol, color: color, template: template)
                            if switchNow { app.switchOrg(o.id) }
                            dismiss()
                        } catch { self.error = "伺服器沒有建立組織:\(error.localizedDescription)" }
                    }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(busy || name.trimmingCharacters(in: .whitespaces).isEmpty)
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
    @State private var busy = false
    @State private var error: String?
    /// 遠端 omixor 的版本(GET /api/version)
    @State private var serverVersion: String?

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
                LabeledContent("id", value: OmixorMode.serverOrgId(draft.id))
                if let s = app.remoteServer { Text("會寫回 \(s.name)(要伺服器管理員)。").font(.caption).foregroundStyle(.secondary) }
            }
            .formStyle(.grouped)
            if let error { Text(error).font(.callout).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button(busy ? "儲存中…" : "儲存") {
                    guard OmixorMode.isRemoteOrg(draft.id) else { app.updateOrg(draft); dismiss(); return }
                    Task {
                        busy = true
                        defer { busy = false }
                        do { try await app.updateRemoteOrg(draft); dismiss() } catch { self.error = "伺服器沒有改組織:\(error.localizedDescription)" }
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(busy || draft.name.trimmingCharacters(in: .whitespaces).isEmpty)
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
    /// 遠端 omixor 的版本(GET /api/version)
    @State private var serverVersion: String?

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
                    do { try Cabinet.saveLayout(app, draft); dismiss() } catch { self.error = error.localizedDescription }
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
