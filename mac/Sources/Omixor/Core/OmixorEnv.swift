import Foundation
import Security

/// 讀環境變數,並相容產品早期叫「Ops」時的舊名稱(OPS_*);和 core/env 的 env.Get(新, 舊) 同一套規則:
/// 一律先讀新名稱(OMIXOR_*),新名稱沒設(或是空字串)才讀舊名稱;讀到舊名稱時記一次 log,提醒改用新名稱。
/// 舊名稱之後會拿掉,新的設定、文件都只寫新名稱。
enum OmixorEnv {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var warned = Set<String>()

    /// 讀 name;沒設或空字串時讀 legacy(舊名稱)。environment 給測試換
    static func get(_ name: String, legacy: String? = nil,
                    environment: [String: String] = ProcessInfo.processInfo.environment) -> String? {
        if let v = environment[name], !v.isEmpty { return v }
        guard let legacy, let v = environment[legacy], !v.isEmpty else { return nil }
        lock.lock()
        let first = warned.insert(legacy).inserted
        lock.unlock()
        if first { NSLog("環境變數 %@ 已改名為 %@,請更新設定(舊名稱暫時仍可用)", legacy, name) }
        return v
    }
}

/// 鑰匙圈 service 從舊名稱(com.tim72117.ops.*)改成新名稱(com.tim72117.omixor.*)的相容:
/// 讀新的讀不到才讀舊的,讀到就複製一份到新的(舊的留著,舊版 App 還讀得到);刪除時兩邊一起刪。
enum KeychainLegacy {
    private static func query(_ service: String, _ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    static func read(service: String, account: String) -> Data? {
        var q = query(service, account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    /// 新的 service 沒有時讀舊的;讀到就複製一份到新的(新的已經有就不動),回傳讀到的值
    static func migrate(account: String, from legacy: String?, to service: String) -> Data? {
        guard let legacy, legacy != service, let d = read(service: legacy, account: account) else { return nil }
        var add = query(service, account)
        add[kSecValueData as String] = d
        add[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let s = SecItemAdd(add as CFDictionary, nil)
        if s == errSecSuccess {
            NSLog("鑰匙圈:「%@」從舊名稱 %@ 複製到 %@", account, legacy, service)
        } else if s != errSecDuplicateItem {
            NSLog("鑰匙圈:「%@」複製到 %@ 失敗(OSStatus %d),這次先用舊名稱的值", account, service, s)
        }
        return d
    }

    /// 刪掉舊 service 的同一個項目(沒有舊名稱或不存在時什麼都不做)
    static func delete(account: String, legacy: String?) {
        guard let legacy else { return }
        SecItemDelete(query(legacy, account) as CFDictionary)
    }
}
