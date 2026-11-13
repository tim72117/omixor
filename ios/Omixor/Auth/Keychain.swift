import Foundation
import Security

/// 登入資訊(伺服器網址與 omixor token)存在 iOS 鑰匙圈,一台手機一份。
/// 測試用 `Keychain(service: "…tests.<UUID>")`,不會碰到 App 真的項目。
struct SavedSession: Codable, Equatable {
    var server: URL
    var token: String
    var tokenId: String
}

struct Keychain {
    static let defaultService = "com.tim72117.omixor.ios.session"
    let service: String
    let account = "session"

    init(service: String = Keychain.defaultService) { self.service = service }

    private var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    func load() -> SavedSession? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return try? JSONDecoder().decode(SavedSession.self, from: data)
    }

    @discardableResult
    func save(_ s: SavedSession) -> Bool {
        guard let data = try? JSONEncoder().encode(s) else { return false }
        let attrs: [String: Any] = [kSecValueData as String: data,
                                    kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        let st = SecItemUpdate(query as CFDictionary, attrs as CFDictionary)
        if st == errSecSuccess { return true }
        guard st == errSecItemNotFound else { return false }
        var add = query
        add.merge(attrs) { $1 }
        return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
    }

    func clear() { SecItemDelete(query as CFDictionary) }
}
