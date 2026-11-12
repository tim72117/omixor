import SwiftUI

/// app 剛開啟時先確認 omixor 的登入:有效就進主畫面,沒有就顯示登入畫面。
/// 只擋畫面;MCP 伺服器等背景服務照常啟動(比對、AI 用戶端不受影響)。OPS_SKIP_LOGIN=1 時直接進主畫面(開發、比對用)。
struct LoginGate<Content: View>: View {
    @EnvironmentObject var app: AppState
    @ViewBuilder var content: () -> Content

    var body: some View {
        Group {
            switch app.omixorLogin {
            case .loggedIn, .skipped:
                content()
            case .checking:
                ProgressView("確認 omixor 的登入…").frame(maxWidth: .infinity, maxHeight: .infinity)
            case .needLogin, .unreachable:
                LoginView()
            }
        }
        .task {
            guard app.omixorLogin == .checking else { return }
            if ProcessInfo.processInfo.environment["OPS_SKIP_LOGIN"] == "1" {
                app.omixorLogin = .skipped
                return
            }
            app.omixorLogin = await OmixorAuth.check(server: OmixorAuth.server)
            if app.omixorLogin.account != nil, let orgs = await OmixorAuth.fetchOrgs(server: OmixorAuth.server) {
                app.serverOrgs = orgs
            }
        }
    }
}

/// 登入畫面:用瀏覽器在 evoleap 核准,換 omixor 自己的 token
struct LoginView: View {
    @EnvironmentObject var app: AppState
    @AppStorage(IssueServerConfig.defaultsKey) private var issueServer = ""
    @State private var serverDraft = ""
    @State private var busy = false
    @State private var error: String?
    @State private var showServer = false

    private var envServer: String? { ProcessInfo.processInfo.environment["OPS_ISSUE_SERVER"] }

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: "square.stack.3d.up.fill")
                .font(.system(size: 52, weight: .semibold))
                .foregroundStyle(.tint)
            VStack(spacing: 6) {
                Text("omixor").font(.largeTitle.weight(.bold))
                Text("用 evoleap 帳號登入。evoleap 只證明你是誰,能不能用由 omixor 的使用者名單決定。")
                    .font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    .frame(maxWidth: 380)
            }

            if case .unreachable(let msg) = app.omixorLogin {
                VStack(spacing: 8) {
                    Label("連不到 omixor 伺服器", systemImage: "wifi.exclamationmark").foregroundStyle(.orange)
                    Text(msg).font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 380)
                    HStack {
                        Button("重試") { Task { await recheck() } }
                        Button("離線繼續") { app.omixorLogin = .skipped }
                    }
                }
            }

            Button {
                Task { await login() }
            } label: {
                Label(busy ? "等瀏覽器核准…" : "用瀏覽器登入", systemImage: "safari")
                    .frame(minWidth: 200)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .disabled(busy)
            if busy {
                Text("瀏覽器會打開 evoleap 的核准頁,按核准後會自動回到這裡。")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let error {
                Text(error).font(.callout).foregroundStyle(.red).multilineTextAlignment(.center).frame(maxWidth: 420)
                    .textSelection(.enabled)
            }

            DisclosureGroup("伺服器", isExpanded: $showServer) {
                VStack(alignment: .leading, spacing: 6) {
                    TextField("omixor 伺服器網址", text: $serverDraft, prompt: Text(OmixorAuth.defaultServer.absoluteString))
                        .textFieldStyle(.roundedBorder)
                        .disabled(envServer != nil)
                    Text(envServer != nil ? "由環境變數 OPS_ISSUE_SERVER 設定" : "留空用正式站。登入後 Issue 會存在這台伺服器。")
                        .font(.caption).foregroundStyle(.secondary)
                }
                .padding(.top, 4)
            }
            .frame(width: 380)
            .font(.callout)
        }
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onAppear { serverDraft = envServer ?? issueServer }
    }

    private var chosenServer: URL? {
        let v = serverDraft.trimmingCharacters(in: .whitespaces)
        if v.isEmpty { return OmixorAuth.defaultServer }
        guard let u = URL(string: v), let s = u.scheme, ["http", "https"].contains(s), u.host != nil else { return nil }
        return u
    }

    private func login() async {
        guard let server = chosenServer else { error = "伺服器網址要是 http:// 或 https:// 開頭"; return }
        busy = true
        error = nil
        defer { busy = false }
        do {
            let a = try await OmixorAuth.login(app, server: server)
            if envServer == nil { issueServer = server.absoluteString }   // 登入後 Issue 改存在這台伺服器
            app.startIssues().load()
            app.omixorLogin = .loggedIn(a)
            if let orgs = await OmixorAuth.fetchOrgs(server: server) { app.serverOrgs = orgs }
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func recheck() async {
        app.omixorLogin = .checking
        app.omixorLogin = await OmixorAuth.check(server: OmixorAuth.server)
    }
}
