import XCTest
import Security
@testable import Omixor

/// 產品從「Ops」改名成 omixor 的相容:環境變數新舊都讀、鑰匙圈讀舊寫新(刪除兩邊都刪)、偏好設定搬到新鍵。
/// 鑰匙圈一律用隨機的測試 service 名稱(新舊兩邊都是),絕不碰真實的項目。
final class RenameCompatTests: XCTestCase {
    private var savedToken = "", savedSecret = "", savedLocal = ""
    private var services: [String] = []

    override func setUp() {
        savedToken = TokenVault.service
        savedSecret = MCPSecretVault.service
        savedLocal = MCPLocalToken.service
        services = []
    }

    override func tearDown() {
        for s in services { SecItemDelete([kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: s] as CFDictionary) }
        TokenVault.service = savedToken
        TokenVault.legacyServiceOverride = nil
        MCPSecretVault.service = savedSecret
        MCPSecretVault.legacyServiceOverride = nil
        MCPLocalToken.service = savedLocal
        MCPLocalToken.legacyServiceOverride = nil
    }

    /// 隨機的測試 service 名稱(tearDown 會刪掉)
    private func testService(_ tag: String) -> String {
        let s = "com.tim72117.omixor.tests.\(tag).\(UUID().uuidString)"
        services.append(s)
        return s
    }

    private func put(_ value: String, service: String, account: String) {
        let q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                kSecAttrAccount as String: account, kSecValueData as String: Data(value.utf8),
                                kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        XCTAssertEqual(SecItemAdd(q as CFDictionary, nil), errSecSuccess)
    }

    private func get(service: String, account: String) -> String? {
        KeychainLegacy.read(service: service, account: account).map { String(decoding: $0, as: UTF8.self) }
    }

    // MARK: - 環境變數

    func testEnvPrefersNewNameThenLegacy() {
        let old = "OPS" + "_RENAMETEST_X"   // 拆開寫,避免全域改名時被換掉
        XCTAssertNil(OmixorEnv.get("OMIXOR_RENAMETEST_X", legacy: old, environment: [:]))
        XCTAssertEqual(OmixorEnv.get("OMIXOR_RENAMETEST_X", legacy: old, environment: [old: "old"]), "old")
        XCTAssertEqual(OmixorEnv.get("OMIXOR_RENAMETEST_X", legacy: old, environment: [old: "old", "OMIXOR_RENAMETEST_X": "new"]), "new")
        // 新名稱是空字串時當作沒設
        XCTAssertEqual(OmixorEnv.get("OMIXOR_RENAMETEST_X", legacy: old, environment: [old: "old", "OMIXOR_RENAMETEST_X": ""]), "old")
        XCTAssertNil(OmixorEnv.get("OMIXOR_RENAMETEST_X", legacy: old, environment: [old: ""]))
        XCTAssertEqual(OmixorEnv.get("OMIXOR_RENAMETEST_X", environment: ["OMIXOR_RENAMETEST_X": "new"]), "new")
    }

    // MARK: - 鑰匙圈

    /// 換了 service(測試、比對用的實例)時不讀舊名稱;正式的 service 才對應舊名稱
    func testLegacyServiceOnlyForDefaultService() {
        TokenVault.service = testService("conn")
        XCTAssertNil(TokenVault.legacyService)
        MCPSecretVault.service = testService("gw")
        XCTAssertNil(MCPSecretVault.legacyService)
        MCPLocalToken.service = testService("ep")
        XCTAssertNil(MCPLocalToken.legacyService)
        XCTAssertEqual(TokenVault.defaultService, "com.tim72117.omixor.connection")
        XCTAssertEqual(TokenVault.defaultLegacyService, "com.tim72117.ops.connection")
        XCTAssertEqual(MCPSecretVault.defaultService, "com.tim72117.omixor.mcp-gateway")
        XCTAssertEqual(MCPSecretVault.defaultLegacyService, "com.tim72117.ops.mcp-gateway")
        XCTAssertEqual(MCPLocalToken.defaultService, "com.tim72117.omixor.mcp-endpoint")
        XCTAssertEqual(MCPLocalToken.defaultLegacyService, "com.tim72117.ops.mcp-endpoint")
    }

    func testTokenVaultCopiesLegacyAndDeletesBoth() {
        let new = testService("conn-new"), old = testService("conn-old")
        TokenVault.service = new
        TokenVault.legacyServiceOverride = old
        let account = "c1:secret"
        put("legacy-secret", service: old, account: account)

        XCTAssertEqual(TokenVault.data(account).map { String(decoding: $0, as: UTF8.self) }, "legacy-secret")
        XCTAssertEqual(get(service: new, account: account), "legacy-secret", "讀到舊的要複製一份到新的")
        XCTAssertEqual(get(service: old, account: account), "legacy-secret", "舊的留著")

        // 寫入只寫新的;新的優先
        XCTAssertTrue(TokenVault.set(Data("new-secret".utf8), account))
        XCTAssertEqual(get(service: new, account: account), "new-secret")
        XCTAssertEqual(get(service: old, account: account), "legacy-secret")
        XCTAssertEqual(TokenVault.data(account).map { String(decoding: $0, as: UTF8.self) }, "new-secret")

        // 刪除兩邊一起刪(不然下次又從舊的讀回來)
        XCTAssertTrue(TokenVault.set(nil, account))
        XCTAssertNil(get(service: new, account: account))
        XCTAssertNil(get(service: old, account: account))
        XCTAssertNil(TokenVault.data(account))
    }

    func testMCPSecretVaultCopiesLegacyAndDeletesBoth() {
        let new = testService("gw-new"), old = testService("gw-old")
        MCPSecretVault.service = new
        MCPSecretVault.legacyServiceOverride = old
        let account = "org1/srv1/env:API_KEY"
        put("legacy-key", service: old, account: account)

        XCTAssertEqual(MCPSecretVault.get(org: "org1", server: "srv1", kind: .env, name: "API_KEY"), "legacy-key")
        XCTAssertEqual(get(service: new, account: account), "legacy-key")
        XCTAssertTrue(MCPSecretVault.has(org: "org1", server: "srv1", kind: .env, name: "API_KEY"))

        MCPSecretVault.set(nil, org: "org1", server: "srv1", kind: .env, name: "API_KEY")
        XCTAssertNil(get(service: new, account: account))
        XCTAssertNil(get(service: old, account: account))
        XCTAssertNil(MCPSecretVault.get(org: "org1", server: "srv1", kind: .env, name: "API_KEY"))
    }

    func testMCPLocalTokenCopiesLegacyAndDeletesBoth() {
        let new = testService("ep-new"), old = testService("ep-old")
        MCPLocalToken.service = new
        MCPLocalToken.legacyServiceOverride = old
        let legacy = MCPLocalToken.prefix + "legacyToken123"
        put(legacy, service: old, account: MCPLocalToken.account)

        XCTAssertEqual(MCPLocalToken.current(), legacy, "沿用舊名稱裡的 token,使用者不用重新設定")
        XCTAssertEqual(get(service: new, account: MCPLocalToken.account), legacy)
        XCTAssertTrue(MCPLocalToken.authorizes("Bearer \(legacy)"))

        MCPLocalToken.removeForTests()
        XCTAssertNil(get(service: new, account: MCPLocalToken.account))
        XCTAssertNil(get(service: old, account: MCPLocalToken.account))
    }

    // MARK: - 偏好設定

    func testSettingsKeysMigrateLegacyOnlyWhenNewMissing() throws {
        let suite = "com.tim72117.omixor.tests.defaults.\(UUID().uuidString)"
        let d = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { d.removePersistentDomain(forName: suite) }
        d.set("dark", forKey: "ops.settings.theme")
        d.set(30, forKey: "ops.settings.refreshSeconds")
        d.set(15, forKey: SettingsKeys.refresh)   // 新鍵已經有值:不覆蓋
        d.set("auto", forKey: "ops.settings.ai.ceiling.production")

        XCTAssertEqual(SettingsKeys.migrateLegacy(d), 2)
        XCTAssertEqual(SettingsKeys.theme, "omixor.settings.theme")
        XCTAssertEqual(d.string(forKey: SettingsKeys.theme), "dark")
        XCTAssertEqual(d.integer(forKey: SettingsKeys.refresh), 15)
        XCTAssertEqual(d.string(forKey: SettingsKeys.aiCeiling(.production)), "auto")
        XCTAssertNil(d.object(forKey: "ops.settings.theme"), "搬完刪掉舊鍵(「還原預設」後才不會又搬回來)")
        XCTAssertNil(d.object(forKey: "ops.settings.refreshSeconds"))
        XCTAssertEqual(SettingsKeys.migrateLegacy(d), 0)
    }
}
