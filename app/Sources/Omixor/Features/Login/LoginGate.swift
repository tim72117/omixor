import SwiftUI

// 啟動時選擇連接對象(同 Go 版 web/ui/src/auth/ConnectGate.tsx):有設定遠端 omixor 時(OmixorMode.servers),
// 這次執行第一次打開畫面先選「本機」或某個遠端(遠端再選授權方式,例如 evoleap,開瀏覽器核准);選好才進主畫面。
// 沒有設定遠端就不會出現,直接用本機。左下角的帳號選單(RootView 的 AccountFooter)之後隨時可以切換,「登出」後也會回到這裡。
// 只擋畫面;MCP 伺服器等背景服務照常啟動。OPS_SKIP_LOGIN=1 時不顯示(開發、比對用),照資料夾裡記的模式。

struct LoginGate<Content: View>: View {
    @EnvironmentObject var app: AppState
    @ViewBuilder var content: () -> Content

    var body: some View {
        Group {
            if let n = app.noOrgsServer {
                NoOrgsView(server: n.server, account: n.account)
            } else if app.connectChosen {
                // 本機模式:本機守護服務可用才進主畫面(起不來時停在這裡,不退回舊的本機實作)
                if app.mode.mode == OmixorMode.local, app.localCore != .ready {
                    LocalCoreGate()
                } else {
                    content()
                }
            } else {
                ConnectPicker()
            }
        }
        .task {
            OmixorAuth.migrateLegacyToken()
            app.startRemoteRefresh()
            if ProcessInfo.processInfo.environment["OPS_SKIP_LOGIN"] == "1" || OmixorMode.servers.isEmpty {
                if !OmixorMode.isRemote { app.omixorLogin = .skipped }
                app.connectChosen = true
            }
        }
    }
}

/// 選擇連接對象:本機,或啟動設定裡的某個遠端 omixor(不顯示網址)
struct ConnectPicker: View {
    @EnvironmentObject var app: AppState
    @State private var choosing: OmixorMode.Server?   // 這個 server 有多種授權,選哪一種

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "square.stack.3d.up.fill")
                .font(.system(size: 48, weight: .semibold))
                .foregroundStyle(.tint)
            VStack(spacing: 4) {
                Text("omixor").font(.largeTitle.weight(.bold))
                Text("選擇連接對象").font(.callout).foregroundStyle(.secondary)
            }
            VStack(spacing: 8) {
                if let name = app.connecting {
                    VStack(spacing: 8) {
                        ProgressView()
                        Text("請在瀏覽器完成 \(name) 的核准…")
                        Text("核准後會自動進入。evoleap 只證明你是誰,能不能用由 omixor 的使用者名單決定。")
                            .font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.center)
                        Button("取消") { app.cancelConnect() }
                            .keyboardShortcut(.cancelAction)
                            .padding(.top, 4)
                    }
                    .padding(.vertical, 12)
                } else if let s = choosing {
                    Text("\(s.name) 要用哪種方式登入?").font(.caption).foregroundStyle(.secondary)
                    ForEach(s.auth) { a in
                        option(a.label, nil, "key.horizontal") { Task { await app.connectRemote(s, auth: a.id) } }
                    }
                    Button("返回") { choosing = nil }.buttonStyle(.link)
                } else {
                    option("本機", "本機資料,不用登入", "internaldrive") { app.connectLocal() }
                    ForEach(OmixorMode.servers) { s in
                        option(s.name, nil, "cloud") {
                            if s.auth.count > 1 { choosing = s } else { Task { await app.connectRemote(s, auth: s.auth.first?.id) } }
                        }
                    }
                }
            }
            .frame(width: 340)
            if let e = app.connectError {
                Text(e).font(.callout).foregroundStyle(.red).multilineTextAlignment(.center).frame(maxWidth: 420)
                    .textSelection(.enabled)
            }
        }
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func option(_ title: String, _ detail: String?, _ icon: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: icon).font(.title3).frame(width: 24)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.headline)
                    if let detail { Text(detail).font(.caption).foregroundStyle(.secondary) }
                }
                Spacer()
                Image(systemName: "chevron.right").font(.caption).foregroundStyle(.secondary)
            }
            .padding(.horizontal, 14).padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 10))
    }
}

// MARK: - 切換連接對象

extension AppState {
    /// 目前連的遠端(本機模式 nil)
    var remoteServer: OmixorMode.Server? { mode.mode == OmixorMode.remote ? OmixorMode.find(mode.server) : nil }

    /// 切到本機
    func connectLocal() {
        connectError = nil
        omixorLogin = .skipped
        connectChosen = true
        guard mode.mode != OmixorMode.local else { startLocalCore(); return }
        enterMode(OmixorMode.File(mode: OmixorMode.local))
        startLocalCore()
        log(.person(me), "internaldrive", "切換到本機模式", .settings)
    }

    /// 切到遠端:還沒登入就開瀏覽器用 evoleap 核准;登入失敗就留在原本的模式。
    /// 登入成功但伺服器上還沒有我能用的組織:顯示「還沒有組織」畫面(不擋在登入,也不進入沒有組織的遠端模式)
    func connectRemote(_ s: OmixorMode.Server, auth: String?) async {
        connectError = nil
        if let auth, auth != "evoleap" { connectError = "還不支援授權方式「\(auth)」"; return }
        connecting = s.name
        defer { connecting = nil }
        do {
            var account = await OmixorAuth.check(server: s.url).account
            if account == nil {
                // 等瀏覽器核准:放在可取消的 Task,使用者按「取消」時 cancelConnect() 取消它
                let login = Task { try await OmixorAuth.login(self, server: s.url) }
                connectLogin = login
                defer { connectLogin = nil }
                do { account = try await login.value } catch {
                    // 取消時可能停在等回呼(CancellationError)或某個 HTTP 請求(URLError.cancelled):一律當作使用者取消
                    if login.isCancelled { throw CancellationError() }
                    throw error
                }
            }
            let list: [ServerOrg]
            do { list = try await OmixorAuth.listOrgs(server: s.url) } catch {
                throw ConnectionError("讀不到 \(s.name) 的組織清單:\(error.localizedDescription)")
            }
            guard !list.isEmpty else {
                if let account { noOrgsServer = (s, account) } else { throw ConnectionError("\(s.name) 的登入失效,請再試一次") }
                return
            }
            enterRemote(s, list, account: account)
            if let w = OmixorAuth.takeKeychainWarning() { errorNotice = "已登入 \(s.name),但\(w)" }
        } catch is CancellationError {
            connectError = nil   // 使用者取消:留在原本的畫面,不顯示錯誤
        } catch {
            connectError = error.localizedDescription
        }
    }

    /// 取消等待瀏覽器核准(停止等 evoleap 回呼,回到原本的畫面)
    func cancelConnect() {
        connectLogin?.cancel()
    }

    /// 進入遠端模式(已經讀到組織清單)
    func enterRemote(_ s: OmixorMode.Server, _ list: [ServerOrg], account: OmixorAccount?) {
        noOrgsServer = nil
        applyRemoteOrgs(list, server: s.url)
        omixorLogin = account.map { .loggedIn($0) } ?? .needLogin
        connectChosen = true
        enterMode(OmixorMode.File(mode: OmixorMode.remote, server: s.url.absoluteString))
        log(.person(me), "cloud", "切換到遠端 omixor(\(s.name))", .settings)
    }

    /// 「還沒有組織」畫面:再讀一次組織清單,有了就進入遠端模式
    func retryNoOrgs() async {
        guard let n = noOrgsServer, let list = await OmixorAuth.fetchOrgs(server: n.server.url), !list.isEmpty,  // 背景重試:失敗下次再試
              noOrgsServer?.server == n.server else { return }
        enterRemote(n.server, list, account: n.account)
    }

    /// 「還沒有組織」畫面:伺服器管理員建立第一個組織,建好就進入
    func createFirstRemoteOrg(name: String) async throws {
        guard let n = noOrgsServer else { return }
        let root = OmixorMode.serverOrgId(newOrgId(for: name))
        _ = try await OmixorAuth.createOrg(server: n.server.url, id: root, name: name, symbol: "building.2", color: "blue")
        let list: [ServerOrg]
        do { list = try await OmixorAuth.listOrgs(server: n.server.url) } catch {
            throw ConnectionError("組織建好了,但讀不到 \(n.server.name) 的組織清單:\(error.localizedDescription)")
        }
        guard !list.isEmpty else { throw ConnectionError("組織建好了,但 \(n.server.name) 的組織清單裡沒有它(可能還沒把你加成成員)") }
        enterRemote(n.server, list, account: n.account)
    }

    /// 離開「還沒有組織」畫面:回到選擇連接對象(logout 時先登出這台伺服器)
    func leaveNoOrgs(logout: Bool) async {
        guard let n = noOrgsServer else { return }
        if logout { await OmixorAuth.logout(server: n.server.url) }
        noOrgsServer = nil
        connectChosen = false
    }

    /// 登出:遠端先撤銷 token 並切回本機;有設定遠端就回到「選擇連接對象」
    func omixorLogout() async {
        if let s = remoteServer { await OmixorAuth.logout(server: s.url) }
        connectLocal()
        omixorLogin = .needLogin
        if !OmixorMode.servers.isEmpty { connectChosen = false }
    }

    private func enterMode(_ f: OmixorMode.File) {
        OmixorMode.save(f)
        mode = OmixorMode.current
        reloadOrgsForMode()
    }

    /// 遠端模式:每 30 秒更新帳號與組織清單(組織的名稱、圖示等以伺服器為準;目前的組織被移除就換到第一個)
    func startRemoteRefresh() {
        guard !remoteRefreshStarted, NSClassFromString("XCTestCase") == nil else { return }
        remoteRefreshStarted = true
        Task { @MainActor in
            while true {
                await self.refreshRemote()
                try? await Task.sleep(for: .seconds(30))
            }
        }
    }

    func refreshRemote() async {
        guard let s = remoteServer else { return }
        let st = await OmixorAuth.check(server: s.url)
        guard remoteServer == s else { return }
        if st != omixorLogin { omixorLogin = st }
        guard st.account != nil, let list = await OmixorAuth.fetchOrgs(server: s.url), !list.isEmpty, remoteServer == s else { return }
        if applyRemoteOrgs(list, server: s.url) { reloadOrgsForMode() }
    }
}

// MARK: - 還沒有組織

/// 登入遠端成功、但伺服器上還沒有我能用的組織時的畫面:伺服器管理員可以建立第一個組織;
/// 一般成員顯示帳號並等被加進組織(每 30 秒再讀一次組織清單,有了就自動進入)。都可以換連接對象或登出
struct NoOrgsView: View {
    @EnvironmentObject var app: AppState
    let server: OmixorMode.Server
    let account: OmixorAccount
    @State private var name = ""
    @State private var busy = false
    @State private var error: String?

    private var admin: Bool { account.role == "admin" }

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "building.2").font(.system(size: 44, weight: .semibold)).foregroundStyle(.tint)
            VStack(spacing: 4) {
                Text("還沒有組織").font(.title.weight(.bold))
                Text("已登入 \(server.name):\(account.email)\(admin ? "(伺服器管理員)" : "")")
                    .font(.callout).foregroundStyle(.secondary)
            }
            VStack(alignment: .leading, spacing: 10) {
                if admin {
                    Text("這台伺服器上還沒有任何組織。建立第一個組織後就會進入主畫面,之後可以在「設定 › 組織」加入成員。")
                        .font(.callout).fixedSize(horizontal: false, vertical: true)
                    TextField("組織名稱(例如 evoleap)", text: $name).textFieldStyle(.roundedBorder).onSubmit(create)
                    Button(busy ? "建立中…" : "建立組織", action: create)
                        .buttonStyle(.borderedProminent).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy)
                } else {
                    Text("你還沒有被加進這台伺服器上的任何組織。請組織管理員把你(\(account.email))加進組織;加入後這個畫面會在 30 秒內自動進入主畫面。")
                        .font(.callout).fixedSize(horizontal: false, vertical: true)
                }
                if let error { Text(error).font(.callout).foregroundStyle(.red).textSelection(.enabled) }
            }
            .frame(width: 380)
            HStack(spacing: 16) {
                Button("換連接對象") { Task { await app.leaveNoOrgs(logout: false) } }.buttonStyle(.link)
                Button("登出") { Task { await app.leaveNoOrgs(logout: true) } }.buttonStyle(.link)
            }
            .disabled(busy)
        }
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task(id: server.id) {
            while !Task.isCancelled, app.noOrgsServer?.server == server {
                try? await Task.sleep(for: .seconds(30))
                await app.retryNoOrgs()
            }
        }
    }

    private func create() {
        let n = name.trimmingCharacters(in: .whitespaces)
        guard !n.isEmpty, !busy else { return }
        busy = true
        error = nil
        Task {
            do { try await app.createFirstRemoteOrg(name: n) } catch { self.error = error.localizedDescription }
            busy = false
        }
    }
}
