import Foundation
import SwiftUI
import UIKit

/// 登入狀態、目前的伺服器與組織。畫面從這裡拿 API 用戶端(token 只在這裡和鑰匙圈)。
@MainActor
final class Session: ObservableObject {
    enum Phase: Equatable { case signedOut, signedIn }

    @Published private(set) var phase: Phase = .signedOut
    @Published private(set) var saved: SavedSession?
    @Published private(set) var user: User?
    @Published private(set) var device = ""
    @Published private(set) var orgs: [Org] = []
    @Published var orgsError: String?
    @Published var loadingOrgs = false
    /// 目前選的組織(存在 UserDefaults,下次打開沿用)
    @Published private(set) var orgId: String?
    /// 被登出時的說明(例如 token 被撤銷),顯示在配對畫面
    @Published var notice: String?
    /// 從 omixor://pair 連結進來、等使用者確認的配對。只有使用者在確認畫面按「確認配對」才會 claim
    @Published var pendingLink: PairLink?
    /// 確認畫面正在配對中
    @Published private(set) var confirmingPending = false
    /// 確認畫面配對失敗的說明
    @Published var pendingError: String?

    let keychain: Keychain
    let defaults: UserDefaults
    var session: URLSession = .shared
    private let orgKey = "omixor.selectedOrg"

    init(keychain: Keychain = Keychain(), defaults: UserDefaults = .standard) {
        self.keychain = keychain
        self.defaults = defaults
        if let s = keychain.load() {
            saved = s
            phase = .signedIn
            orgId = defaults.string(forKey: orgKey)
        }
    }

    var api: APIClient? {
        saved.map { APIClient(baseURL: $0.server, token: $0.token, session: session) }
    }

    var org: Org? { orgs.first { $0.id == orgId } }

    /// 本機的裝置名稱(會出現在伺服器的 token 清單)
    static var deviceName: String {
        let d = UIDevice.current
        return "omixor iOS(\(d.model) · iOS \(d.systemVersion))"
    }

    // MARK: 配對與登出

    /// 用配對碼換 token,存進鑰匙圈
    func pair(_ link: PairLink) async throws {
        let client = APIClient(baseURL: link.server, session: session)
        let r = try await client.claimPair(code: link.code, device: Self.deviceName)
        let s = SavedSession(server: link.server, token: r.token, tokenId: r.tokenId)
        let old = api
        guard keychain.save(s) else { throw APIError(message: "存到鑰匙圈失敗") }
        // 重新配對:舊的 token 撤銷掉,伺服器上不留沒人用的登入
        if let old { try? await old.logout() }
        if saved?.server != link.server { defaults.removeObject(forKey: orgKey); orgId = nil }
        saved = s
        user = r.user
        device = Self.deviceName
        orgs = []
        notice = nil
        pendingLink = nil
        phase = .signedIn
        await loadOrgs()
    }

    /// 登出:撤銷伺服器上的這把 token(連不到也照樣清掉本機的)
    func logout() async {
        if let api { try? await api.logout() }
        signOut(notice: nil)
    }

    /// 清掉本機登入,回配對畫面
    func signOut(notice: String?) {
        keychain.clear()
        saved = nil
        user = nil
        orgs = []
        orgId = nil
        defaults.removeObject(forKey: orgKey)
        phase = .signedOut
        self.notice = notice
    }

    /// API 回 401:token 被撤銷或過期
    func handle(_ error: Error) -> String {
        if let e = error as? APIError, e.isUnauthorized {
            signOut(notice: "登入已失效(可能被撤銷或過期),請在 Mac App 重新產生配對碼。")
        }
        return error.localizedDescription
    }

    // MARK: 帳號與組織

    func refreshMe() async {
        guard let api else { return }
        do {
            let me = try await api.me()
            user = me.user
            device = me.token.device
        } catch {
            _ = handle(error)
        }
    }

    func loadOrgs() async {
        guard let api else { return }
        loadingOrgs = true
        defer { loadingOrgs = false }
        do {
            orgs = try await api.orgs()
            orgsError = nil
            if let id = orgId, !orgs.contains(where: { $0.id == id }) { selectOrg(nil) }
            if orgId == nil, orgs.count == 1 { selectOrg(orgs[0].id) }
        } catch {
            orgsError = handle(error)
        }
    }

    func selectOrg(_ id: String?) {
        orgId = id
        if let id { defaults.set(id, forKey: orgKey) } else { defaults.removeObject(forKey: orgKey) }
    }

    // MARK: 連結

    /// omixor://pair 連結(外部網頁、其他 App、訊息裡的連結都可能觸發):
    /// 只記下來等使用者確認,不論有沒有登入都不會自動配對,避免手機被引導去攻擊者的伺服器領 token
    func open(_ url: URL) {
        guard let link = PairLink(url: url) else { return }
        pendingError = nil
        pendingLink = link
    }

    /// 使用者在確認畫面按了「確認配對」才呼叫:用連結裡的配對碼 claim
    func confirmPending() async {
        guard let link = pendingLink, !confirmingPending else { return }
        confirmingPending = true
        pendingError = nil
        defer { confirmingPending = false }
        do {
            try await pair(link)
            pendingLink = nil
        } catch {
            pendingError = "配對失敗:\(error.localizedDescription)"
        }
    }

    /// 使用者取消連結帶進來的配對
    func cancelPending() {
        guard !confirmingPending else { return }
        pendingLink = nil
        pendingError = nil
    }
}
