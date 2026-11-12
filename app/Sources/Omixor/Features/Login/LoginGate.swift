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
            if app.connectChosen {
                content()
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
        guard mode.mode != OmixorMode.local else { return }
        enterMode(OmixorMode.File(mode: OmixorMode.local))
        log(.person(me), "internaldrive", "切換到本機模式", .settings)
    }

    /// 切到遠端:還沒登入就開瀏覽器用 evoleap 核准;登入失敗或沒有組織就留在原本的模式
    func connectRemote(_ s: OmixorMode.Server, auth: String?) async {
        connectError = nil
        if let auth, auth != "evoleap" { connectError = "還不支援授權方式「\(auth)」"; return }
        connecting = s.name
        defer { connecting = nil }
        do {
            var account = await OmixorAuth.check(server: s.url).account
            if account == nil { account = try await OmixorAuth.login(self, server: s.url) }
            guard let list = await OmixorAuth.fetchOrgs(server: s.url) else { throw ConnectionError("讀不到 \(s.name) 的組織清單") }
            guard !list.isEmpty else { throw ConnectionError("\(s.name) 上沒有你能用的組織:請 omixor 管理員把你加進組織") }
            applyRemoteOrgs(list)
            omixorLogin = account.map { .loggedIn($0) } ?? .needLogin
            connectChosen = true
            enterMode(OmixorMode.File(mode: OmixorMode.remote, server: s.url.absoluteString))
            log(.person(me), "cloud", "切換到遠端 omixor(\(s.name))", .settings)
        } catch {
            connectError = error.localizedDescription
        }
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
        if applyRemoteOrgs(list) { reloadOrgsForMode() }
    }
}
