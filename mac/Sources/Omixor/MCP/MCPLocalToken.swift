import Foundation
import Security

/// App 內建 MCP 端點(127.0.0.1:<埠>/mcp)的存取 token。
///
/// 只聽 127.0.0.1 擋不住「這台 Mac 上的其他程式」(包括自動執行時開了 Bash 的角色),所以 /mcp 一律要帶
/// `Authorization: Bearer <這把 token>`,沒帶或不對回 401。這把 token 代表「這台 Mac 的使用者」:
/// 確認卡片與動態牆的身分都用它,不用用戶端自報的 clientInfo。
///
/// - 第一次用到時產生(SecRandomCopyBytes 32 bytes),存在鑰匙圈(只在這台、解鎖時可用);設定頁可以「重新產生」(舊的立刻失效)。
/// - 角色自動執行走 /mcp/gateway,用的是一次性的閘道 token(omxgw_ 開頭),兩邊互不通用。
/// - service 可以換(測試一律用暫存的名稱,不碰正式鑰匙圈)。
enum MCPLocalToken {
    nonisolated(unsafe) static var service = ProcessInfo.processInfo.environment["OPS_MCP_TOKEN_KEYCHAIN_SERVICE"] ?? "com.tim72117.ops.mcp-endpoint"
    static let account = "local-mcp-token"
    static let prefix = "omxl_"

    private static let lock = NSLock()
    /// 這次執行讀到或產生的 token(鍵是 service;測試換 service 時不會讀到別的)
    nonisolated(unsafe) private static var cached: [String: String] = [:]
    /// 測試用:取代亂數來源(回傳 nil = 產生失敗)
    nonisolated(unsafe) static var randomHook: (() -> [UInt8]?)?

    /// 產生一把新的 token;亂數來源失敗時回傳 nil(這時不發 token)
    static func generate() -> String? {
        let bytes: [UInt8]
        if let randomHook {
            guard let b = randomHook() else { return nil }
            bytes = b
        } else {
            var b = [UInt8](repeating: 0, count: 32)
            guard SecRandomCopyBytes(kSecRandomDefault, b.count, &b) == errSecSuccess else { return nil }
            bytes = b
        }
        return prefix + base64url(bytes)
    }

    static func base64url(_ b: [UInt8]) -> String {
        Data(b).base64EncodedString().replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }

    /// 目前的 token(沒有就產生並存進鑰匙圈);產生失敗時回傳 nil(這時 /mcp 一律 401)
    static func current() -> String? {
        lock.lock()
        defer { lock.unlock() }
        if let t = cached[service] { return t }
        if let d = stored(), let t = String(data: d, encoding: .utf8), t.hasPrefix(prefix) {
            cached[service] = t
            return t
        }
        guard let t = generate() else { return nil }
        store(t)
        cached[service] = t   // 寫不進鑰匙圈時這次執行仍用它(重開 App 會換一把)
        return t
    }

    /// 重新產生(舊的立刻失效);失敗時回傳 nil,舊的照舊
    @discardableResult
    static func regenerate() -> String? {
        guard let t = generate() else { return nil }
        lock.lock()
        defer { lock.unlock() }
        store(t)
        cached[service] = t
        return t
    }

    /// Authorization 標頭是不是目前這把 token(比對時間不隨內容變)
    static func authorizes(_ authorization: String?) -> Bool {
        guard let auth = authorization, auth.count > 7, auth.prefix(7).lowercased() == "bearer " else { return false }
        let given = auth.dropFirst(7).trimmingCharacters(in: .whitespaces)
        guard given.hasPrefix(prefix), let want = current() else { return false }
        return constantTimeEqual(given, want)
    }

    static func constantTimeEqual(_ a: String, _ b: String) -> Bool {
        let x = Array(a.utf8), y = Array(b.utf8)
        guard x.count == y.count else { return false }
        var diff: UInt8 = 0
        for i in 0..<x.count { diff |= x[i] ^ y[i] }
        return diff == 0
    }

    /// 測試用:刪掉這個 service 的項目與快取
    static func removeForTests() {
        lock.lock()
        defer { lock.unlock() }
        cached[service] = nil
        SecItemDelete(query() as CFDictionary)
    }

    // MARK: 鑰匙圈

    private static func query() -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    private static func stored() -> Data? {
        var q = query()
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    private static func store(_ t: String) {
        let data = Data(t.utf8)
        let s = SecItemUpdate(query() as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        guard s == errSecItemNotFound else {
            if s != errSecSuccess { NSLog("MCPLocalToken:寫不進鑰匙圈(OSStatus %d),這次執行先用記憶體裡的", s) }
            return
        }
        var add = query()
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let a = SecItemAdd(add as CFDictionary, nil)
        if a != errSecSuccess { NSLog("MCPLocalToken:寫不進鑰匙圈(OSStatus %d),這次執行先用記憶體裡的", a) }
    }

    // MARK: 給使用者的設定指令

    /// 加到 Claude Code 的指令(含 token)。已經加過同名的要先 `claude mcp remove ops`(設定頁有說明)
    static func claudeCommand(url: String, token: String, name: String = "ops") -> String {
        "claude mcp add --transport http \(name) \(url) --header \"Authorization: Bearer \(token)\""
    }
}
