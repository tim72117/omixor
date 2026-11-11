import Foundation
import Security

/// 連線的密碼類資料存在鑰匙圈:token 與 client secret。只在這台 Mac、解鎖時可用,不會隨備份搬到別台。
enum TokenVault {
    nonisolated(unsafe) static var service = "com.tim72117.ops.connection"
    /// 組織的前綴:預設組織是空字串(沿用舊的帳號名稱,登入不會掉),其他組織是 "<組織 id>/"
    nonisolated(unsafe) static var scope = ""

    private static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    static func data(_ account: String) -> Data? {
        var q = query(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    static func set(_ data: Data?, _ account: String) {
        SecItemDelete(query(account) as CFDictionary)
        guard let data else { return }
        var q = query(account)
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        SecItemAdd(q as CFDictionary, nil)
    }

    static func tokens(_ id: String) -> TokenSet? {
        guard let d = data("\(scope)\(id):token") else { return nil }
        let dec = JSONDecoder()
        dec.dateDecodingStrategy = .iso8601
        return try? dec.decode(TokenSet.self, from: d)
    }

    static func setTokens(_ t: TokenSet?, _ id: String) {
        let enc = JSONEncoder()
        enc.dateEncodingStrategy = .iso8601
        set(t.flatMap { try? enc.encode($0) }, "\(scope)\(id):token")
    }

    static func secret(_ id: String) -> String? { data("\(scope)\(id):secret").map { String(decoding: $0, as: UTF8.self) } }
    static func setSecret(_ s: String?, _ id: String) { set(s.map { Data($0.utf8) }, "\(scope)\(id):secret") }

    static func removeAll(_ id: String) {
        setTokens(nil, id)
        setSecret(nil, id)
    }
}
