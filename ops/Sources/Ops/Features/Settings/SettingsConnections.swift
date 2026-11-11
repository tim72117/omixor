import SwiftUI

// 設定 › 連線:Ops 要呼叫的外部服務。每個連線 = 網址 + 一種通用的認證方式(OAuth / 瀏覽器核准 / Token / 不用)。

struct SettingsConnectionsPane: View {
    @EnvironmentObject var app: AppState
    @State private var selection: String?
    @State private var adding = false

    var body: some View {
        // 用 HStack 而不是 HSplitView:HSplitView 是 AppKit 的分割視圖,不會避開側邊欄與 AI 助理面板,內容會被蓋住
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                List(selection: $selection) {
                    ForEach(app.connections) { c in
                        ConnectionRow(conn: c, state: app.connectionStates[c.id]).tag(c.id)
                    }
                }
                .listStyle(.inset)
                Divider()
                HStack {
                    Button { adding = true } label: { Label("新增連線", systemImage: "plus") }
                        .buttonStyle(.borderless)
                    Spacer()
                }
                .padding(8)
            }
            .frame(width: 240)
            Divider()
            Group {
                if let id = selection, let c = app.connections.first(where: { $0.id == id }) {
                    ConnectionEditor(original: c).id(c.id)
                } else {
                    EmptyStateView(title: "選一個連線", icon: "key.horizontal",
                                   message: "每個連線是一個外部服務的網址加上認證方式。登入後,Ops 的其他功能(例如旁白發佈)就能用這個身分呼叫它的 API。")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear {
            app.startConnections()
            if selection == nil { selection = app.connections.first?.id }
        }
        .onChange(of: app.currentOrgId) { selection = app.connections.first?.id }
        .sheet(isPresented: $adding) {
            NewConnectionSheet { c in
                app.connStore?.upsert(c)
                selection = c.id
            }
        }
    }
}

private struct ConnectionRow: View {
    let conn: Connection
    let state: ConnectionState?

    var body: some View {
        HStack(spacing: 8) {
            Circle().fill(state?.account != nil ? Color.green : (state?.error != nil ? .orange : .gray.opacity(0.4)))
                .frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 4) {
                    Text(conn.name).lineLimit(1)
                    if conn.isProduction { Pill(text: "正式", color: .red) }
                }
                Text(conn.auth.label + " · " + (URL(string: conn.baseURL)?.host ?? conn.baseURL))
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }
}

// MARK: - 編輯

private struct ConnectionEditor: View {
    @EnvironmentObject var app: AppState
    let original: Connection
    @State private var draft: Connection
    @State private var secret = ""
    @State private var bearer = ""
    @State private var confirmDelete = false

    init(original: Connection) {
        self.original = original
        _draft = State(initialValue: original)
    }

    private var store: ConnectionStore? { app.connStore }
    private var state: ConnectionState { app.connectionStates[original.id] ?? ConnectionState() }
    private var dirty: Bool { draft != original }

    var body: some View {
        Form {
            Section {
                statusRow
            }
            Section("服務") {
                TextField("名稱", text: $draft.name)
                TextField("網址", text: $draft.baseURL, prompt: Text("https://api.example.com"))
                TextField("測試路徑", text: $draft.testPath, prompt: Text("/api/me(登入後用來確認身分,可留空)"))
                Toggle("正式環境(發佈前會特別提醒)", isOn: $draft.isProduction)
                TextField("備註", text: $draft.note)
            }
            Section {
                Picker("認證方式", selection: $draft.auth) {
                    ForEach(AuthKind.allCases) { Text($0.label).tag($0) }
                }
                Text(draft.auth.detail).font(.caption).foregroundStyle(.secondary)
                switch draft.auth {
                case .oauth: oauthFields
                case .approval: approvalFields
                case .bearer: bearerFields
                case .none: EmptyView()
                }
            } header: { Text("認證") }
            Section {
                HStack {
                    Button("刪除連線", role: .destructive) { confirmDelete = true }
                    Spacer()
                    Button("還原") { draft = original }.disabled(!dirty)
                    Button("儲存") { store?.upsert(draft) }
                        .buttonStyle(.borderedProminent)
                        .disabled(!dirty || draft.name.isEmpty || URL(string: draft.baseURL)?.host == nil)
                }
            }
        }
        .formStyle(.grouped)
        .confirmationDialog("刪除「\(original.name)」?", isPresented: $confirmDelete) {
            Button("刪除(連同鑰匙圈裡的登入)", role: .destructive) { store?.delete(original.id) }
        }
    }

    @ViewBuilder private var statusRow: some View {
        HStack(spacing: 10) {
            Image(systemName: state.account != nil ? "checkmark.seal.fill" : "key.horizontal")
                .font(.title2).foregroundStyle(state.account != nil ? .green : .secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(state.account.map { "已登入:\($0)" } ?? (original.auth == .none ? "不用登入" : "尚未登入")).font(.headline)
                if let t = state.lastTest { Text(t).font(.caption).foregroundStyle(.secondary) }
                if let e = state.error { Text(e).font(.caption).foregroundStyle(.orange).textSelection(.enabled) }
            }
            Spacer()
            if state.busy {
                ProgressView().controlSize(.small)
            } else {
                if original.auth == .oauth || original.auth == .approval {
                    if state.account != nil {
                        Button("登出") { Task { await store?.logout(original.id) } }
                    } else {
                        Button("登入…") { Task { await store?.login(original.id) } }
                            .buttonStyle(.borderedProminent)
                            .disabled(dirty)
                    }
                }
                Button("測試連線") { Task { await store?.refreshAccount(original.id) } }
                    .disabled(dirty || original.testPath.isEmpty)
            }
        }
        if dirty { Text("先儲存變更再登入或測試").font(.caption).foregroundStyle(.secondary) }
    }

    @ViewBuilder private var oauthFields: some View {
        HStack {
            TextField("授權伺服器(issuer)", text: $draft.oauth.issuer, prompt: Text("留空 = 從服務網址自動探索"))
            Button("探索") {
                store?.upsert(draft)
                Task {
                    await store?.discover(original.id)
                    if let c = store?.connection(original.id) { draft = c }
                }
            }
            .disabled(URL(string: draft.baseURL)?.host == nil)
        }
        TextField("授權端點", text: $draft.oauth.authorizationEndpoint, prompt: Text("探索後自動填入"))
        TextField("Token 端點", text: $draft.oauth.tokenEndpoint, prompt: Text("探索後自動填入"))
        TextField("註冊端點(動態註冊)", text: $draft.oauth.registrationEndpoint, prompt: Text("伺服器支援時自動填入"))
        TextField("client_id", text: $draft.oauth.clientId, prompt: Text("留空 = 自動註冊(伺服器要支援)"))
        HStack {
            SecureField("client secret(選填,存鑰匙圈)", text: $secret,
                        prompt: Text(original.oauth.hasClientSecret ? "已設定,輸入新的會取代" : "桌面用戶端通常不需要"))
            Button(original.oauth.hasClientSecret && secret.isEmpty ? "清除" : "存入") {
                store?.setClientSecret(secret.isEmpty ? nil : secret, for: original.id)
                secret = ""
                if let c = store?.connection(original.id) { draft.oauth.hasClientSecret = c.oauth.hasClientSecret }
            }
            .disabled(secret.isEmpty && !original.oauth.hasClientSecret)
        }
        TextField("scopes", text: $draft.oauth.scopes)
        TextField("resource(RFC 8707)", text: $draft.oauth.resource, prompt: Text("選填:token 只給這個服務用"))
        TextField("額外參數", text: $draft.oauth.extraParams, prompt: Text("例如 Google:access_type=offline&prompt=consent"))
        Toggle("呼叫 API 時送 id_token(Google IAP / Cloud Run)", isOn: $draft.oauth.sendIdToken)
        Text("回呼網址:http://127.0.0.1:<隨機埠>/callback(原生 app 標準做法,RFC 8252)。在 Google 等主控台建用戶端時選「桌面應用程式」。")
            .font(.caption).foregroundStyle(.secondary)
    }

    @ViewBuilder private var approvalFields: some View {
        TextField("核准頁網站", text: $draft.approval.consoleURL, prompt: Text("留空 = 和服務網址相同"))
        DisclosureGroup("進階:路徑") {
            TextField("登記", text: $draft.approval.startPath)
            TextField("核准頁", text: $draft.approval.approvePath)
            TextField("換 token", text: $draft.approval.exchangePath)
            TextField("登出", text: $draft.approval.logoutPath)
        }
    }

    @ViewBuilder private var bearerFields: some View {
        HStack {
            SecureField("Token", text: $bearer, prompt: Text(state.account != nil ? "已設定,輸入新的會取代" : "貼上 API key 或存取權杖"))
            Button("存入鑰匙圈") {
                store?.setBearer(bearer, for: original.id)
                bearer = ""
            }
            .disabled(bearer.isEmpty || dirty)
        }
    }
}

// MARK: - 新增

private struct NewConnectionSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let onCreate: (Connection) -> Void
    @State private var name = ""
    @State private var url = ""
    @State private var auth: AuthKind = .oauth

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("新增連線").font(.title3.weight(.semibold))
            Form {
                TextField("名稱", text: $name, prompt: Text("例如 Payments API"))
                TextField("網址", text: $url, prompt: Text("https://api.example.com"))
                Picker("認證方式", selection: $auth) {
                    ForEach(AuthKind.allCases) { Text($0.label).tag($0) }
                }
                Text(auth.detail).font(.caption).foregroundStyle(.secondary)
            }
            .formStyle(.grouped)
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("建立") {
                    let id = app.connStore?.newId(for: name) ?? UUID().uuidString
                    onCreate(Connection(id: id, name: name, baseURL: url.trimmingCharacters(in: .whitespaces), auth: auth))
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .disabled(name.isEmpty || URL(string: url)?.host == nil)
            }
        }
        .padding(20)
        .frame(width: 520)
    }
}
