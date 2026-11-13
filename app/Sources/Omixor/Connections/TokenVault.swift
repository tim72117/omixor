import Foundation
import Security

/// 連線的密碼類資料存在鑰匙圈:token 與 client secret。只在這台 Mac、解鎖時可用,不會隨備份搬到別台。
enum TokenVault {
    /// OPS_KEYCHAIN_SERVICE 可以換成別的名稱(比對或測試用的實例不要碰到正式 Ops 的登入)
    nonisolated(unsafe) static var service = ProcessInfo.processInfo.environment["OPS_KEYCHAIN_SERVICE"] ?? "com.tim72117.ops.connection"
    /// 組織的前綴:預設組織是空字串(沿用舊的帳號名稱,登入不會掉),其他組織是 "<組織 id>/"
    nonisolated(unsafe) static var scope = ""

    private static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    // 寫不進鑰匙圈時(例如舊的項目是別的簽章版本建的,刪不掉也蓋不掉:SecItemDelete 沒刪到、SecItemAdd 回「重複」),
    // 這次執行先把新的值記在記憶體(值是 nil = 已刪除),讀的時候以它為準;不然會一直讀到鑰匙圈裡過期或已撤銷的舊 token。
    // 鍵是「service|帳號」(測試會換 service)
    private static let lock = NSLock()
    nonisolated(unsafe) private static var pending: [String: Data?] = [:]
    /// 最近一次寫入鑰匙圈失敗的原因(成功寫入後清掉;畫面可以提示使用者到「鑰匙圈存取」手動刪掉舊項目)
    nonisolated(unsafe) private(set) static var lastWriteError: String?
    /// 測試用:非 nil 時取代真的寫入(回傳 OSStatus),模擬寫不進鑰匙圈
    nonisolated(unsafe) static var testWriteHook: ((Data?, String) -> OSStatus)?

    private static func pendingKey(_ account: String) -> String { "\(service)|\(account)" }

    static func data(_ account: String) -> Data? {
        lock.lock()
        let p = pending[pendingKey(account)]
        lock.unlock()
        if let p { return p }
        return stored(account)
    }

    /// 鑰匙圈裡實際存的值
    private static func stored(_ account: String) -> Data? {
        var q = query(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    /// 寫入(nil = 刪除)。寫完讀回來確認;鑰匙圈沒有變成新的值就記在記憶體並回傳 false(原因在 lastWriteError)
    @discardableResult
    static func set(_ data: Data?, _ account: String) -> Bool {
        let status = testWriteHook?(data, account) ?? write(data, account)
        let ok = status == errSecSuccess && stored(account) == data
        lock.lock()
        defer { lock.unlock() }
        if ok {
            pending[pendingKey(account)] = nil
            lastWriteError = nil
        } else {
            pending[pendingKey(account)] = .some(data)
            let why = status == errSecSuccess ? "寫完讀回來不是新的值" : (SecCopyErrorMessageString(status, nil) as String? ?? "OSStatus \(status)")
            lastWriteError = "鑰匙圈的「\(service)」裡「\(account)」\(data == nil ? "刪不掉" : "寫不進去")(\(why));"
                + "這次先記在記憶體,重開 App 後會再讀到舊的。可以在「鑰匙圈存取」刪掉這個項目"
            NSLog("TokenVault:%@", lastWriteError!)
        }
        return ok
    }

    /// 先改既有的項目,沒有才新增(舊寫法「先刪再加」在刪不掉時會加成重複而失敗,舊值留著)
    private static func write(_ data: Data?, _ account: String) -> OSStatus {
        let q = query(account) as CFDictionary
        guard let data else {
            let s = SecItemDelete(q)
            return s == errSecItemNotFound ? errSecSuccess : s
        }
        let s = SecItemUpdate(q, [kSecValueData as String: data] as CFDictionary)
        guard s == errSecItemNotFound else { return s }
        var add = query(account)
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        return SecItemAdd(add as CFDictionary, nil)
    }

    static func tokens(_ id: String) -> TokenSet? { tokens(id, scope: scope) }

    /// 指定前綴讀(例如遠端 omixor 的登入,不分組織,存在 "remote/")
    static func tokens(_ id: String, scope: String) -> TokenSet? {
        guard let d = data("\(scope)\(id):token") else { return nil }
        let dec = JSONDecoder()
        dec.dateDecodingStrategy = .iso8601
        return try? dec.decode(TokenSet.self, from: d)
    }

    static func setTokens(_ t: TokenSet?, _ id: String) { _ = setTokens(t, id, scope: scope) }

    /// 回傳是否真的寫進鑰匙圈(寫不進去時這次執行仍讀得到新的值,見 set)
    @discardableResult
    static func setTokens(_ t: TokenSet?, _ id: String, scope: String) -> Bool {
        let enc = JSONEncoder()
        enc.dateEncodingStrategy = .iso8601
        return set(t.flatMap { try? enc.encode($0) }, "\(scope)\(id):token")
    }

    static func secret(_ id: String) -> String? { data("\(scope)\(id):secret").map { String(decoding: $0, as: UTF8.self) } }
    static func setSecret(_ s: String?, _ id: String) { _ = set(s.map { Data($0.utf8) }, "\(scope)\(id):secret") }

    static func removeAll(_ id: String) {
        setTokens(nil, id)
        setSecret(nil, id)
    }
}
