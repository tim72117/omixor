import Foundation
import XCTest
@testable import Ops

// 測試用的 core server:App 的 Issue、角色與任務、組織鏈、頻道、文件櫃一律透過 core server(本機模式是本機守護服務),
// 所以原本測「本機模式」的測試改成連一個真的 omixor-local(Go,server/cmd/omixor-local),資料夾是暫存的。
// 執行檔第一次用到時用 go build 編到暫存資料夾(之後同一次測試共用);有 OMIXOR_LOCAL_BIN 就直接用那個。
// 所有功能都編進去(不再有 -tags dev);開放哪些功能由守護服務資料庫裡的兩層開關決定(本機使用者是伺服器管理員)。
// 同一個測試類別共用一個守護服務(每個測試前重建 default 組織),類別跑完時停掉。
// 連接埠由守護服務自己挑(127.0.0.1 的隨機埠),不會碰到使用者正在用的 8080、3765、3767。
// 預設的執行檔編進全部選用模組(同正式版);start(minimal: true) 用 `-tags omixor_minimal` 編的最小版(不含任何選用模組,
// 例如沒有程式碼倉庫模組:/api/modules 回 [],/integrations/github|gitlab 404),測 App 在沒有模組時的行為。
enum TestCore {
    private static let built: Result<URL, Error> = {
        if let p = ProcessInfo.processInfo.environment["OMIXOR_LOCAL_BIN"], !p.isEmpty { return .success(URL(fileURLWithPath: p)) }
        return build(minimal: false)
    }()

    /// 不含任何選用模組的最小版(第一次用到時才編)
    private static let builtMinimal: Result<URL, Error> = build(minimal: true)

    private static func build(minimal: Bool) -> Result<URL, Error> {
        let serverDir = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("server")
        let out = FileManager.default.temporaryDirectory
            .appendingPathComponent("omixor-local-test\(minimal ? "-minimal" : "")-\(ProcessInfo.processInfo.processIdentifier)")
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        p.arguments = ["go", "build"] + (minimal ? ["-tags", "omixor_minimal"] : []) + ["-o", out.path, "./cmd/omixor-local"]
        p.currentDirectoryURL = serverDir
        let err = Pipe()
        p.standardError = err
        do {
            try p.run()
            p.waitUntilExit()
        } catch { return .failure(error) }
        guard p.terminationStatus == 0 else {
            let msg = String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            return .failure(IssueRemoteError(message: "go build omixor-local\(minimal ? "(omixor_minimal)" : "") 失敗:\(msg)"))
        }
        return .success(out)
    }

    /// 測試編好的 omixor-local 執行檔(LocalCoreTests 用來自己啟動)
    static func binary() throws -> URL { try built.get() }

    /// 守護服務自己的預設組織(OMIXOR_LOCAL_ORG):不是 default,這樣 default 才能在每個測試前刪掉重建
    static let baseOrg = "test-base"

    /// 一個測試類別共用的守護服務(類別的測試都跑完時停掉,見 SuiteObserver)
    private final class Shared {
        let core: LocalCore, cfg: IssueServerConfig, dir: URL
        init(core: LocalCore, cfg: IssueServerConfig, dir: URL) { self.core = core; self.cfg = cfg; self.dir = dir }
        func stop() { core.stop(); try? FileManager.default.removeItem(at: dir) }
    }
    /// 測試類別名稱 → (使用者|名稱 → 守護服務)
    nonisolated(unsafe) private static var shared: [String: [String: Shared]] = [:]
    nonisolated(unsafe) private static var observer: SuiteObserver?

    /// 測試類別跑完時停掉它的守護服務
    private final class SuiteObserver: NSObject, XCTestObservation {
        func testSuiteDidFinish(_ testSuite: XCTestSuite) {
            guard let list = TestCore.shared.removeValue(forKey: testSuite.name) else { return }
            for s in list.values {
                if IssueServerConfig.testOverride == s.cfg { IssueServerConfig.testOverride = nil }
                s.stop()
            }
        }
    }

    /// 替這個測試準備一個乾淨的守護服務(預設組織 default、使用者 tim),設成 IssueServerConfig.testOverride。
    /// 同一個測試類別(同使用者與名稱)共用一個守護服務,類別的測試都跑完時停掉;
    /// 每個測試開始前把 default 與測試建的其他組織整個刪掉重建,資料不會互相影響
    @discardableResult
    /// minimal = true:用不含選用模組的最小版(另外一個守護服務)
    static func start(_ test: XCTestCase, user: String = "tim", name: String = "蔡庭宇", minimal: Bool = false) throws -> IssueServerConfig {
        let suite = String(describing: type(of: test)), key = "\(user)|\(name)\(minimal ? "|minimal" : "")"
        let s: Shared
        if let x = shared[suite]?[key] {
            s = x
        } else {
            if observer == nil {
                let o = SuiteObserver()
                XCTestObservationCenter.shared.addTestObserver(o)
                observer = o
            }
            let bin = try (minimal ? builtMinimal : built).get()
            let dir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-core-test-\(UUID().uuidString)")
            let core = LocalCore()
            let cfg = try core.start(binary: bin, dataDir: dir, user: user, name: name, org: baseOrg, timeout: 20)
            s = Shared(core: core, cfg: cfg, dir: dir)
            shared[suite, default: [:]][key] = s
        }
        try reset(s.cfg)
        IssueServerConfig.testOverride = s.cfg
        test.addTeardownBlock {
            if IssueServerConfig.testOverride == s.cfg { IssueServerConfig.testOverride = nil }
        }
        return s.cfg
    }

    /// 刪掉 baseOrg 以外的組織(上一個測試留下的資料),重建 default(名稱同守護服務的預設:evoleap);
    /// 系統層的功能開關(整台一份,上一個測試可能改過)回到預設
    private static func reset(_ cfg: IssueServerConfig) throws {
        let r = IssueRemote(config: cfg)
        let changed = try r.serverFeatures().features.filter(\.changed).map(\.key)
        if !changed.isEmpty { _ = try r.saveServerFeatures(features: [:], reset: changed) }
        let orgs = (try JSONSerialization.jsonObject(with: try r.send("GET", cfg.url.appendingPathComponent("api/orgs"), nil)) as? [[String: Any]]) ?? []
        for o in orgs.compactMap({ $0["id"] as? String }) where o != baseOrg {
            // DELETE /local/orgs/{org} 回 {deleted: true}(不是 {result: …}),直接看 HTTP 狀態
            var req = URLRequest(url: cfg.url.appendingPathComponent("local/orgs").appendingPathComponent(o))
            req.httpMethod = "DELETE"
            if let t = cfg.token { req.setValue("Bearer \(t)", forHTTPHeaderField: "Authorization") }
            let (status, data) = try r.perform(req)
            guard status == 200 else {
                throw IssueRemoteError(message: "清掉測試組織 \(o) 失敗(HTTP \(status)):\(String(decoding: data, as: UTF8.self))")
            }
        }
        _ = try r.sendJSON("POST", cfg.url.appendingPathComponent("local/orgs"), ["orgs": [["id": OrgFiles.defaultId, "name": "evoleap"]]])
        // 同正式第一次啟動:預設組織沒有 cabinet-layout.json 時,文件櫃結構用研發基地的(LocalCoreImport.bundle)
        let layout = try JSONSerialization.jsonObject(with: try OrgFiles.encoder().encode(CabinetLayout.evoleapStudio))
        _ = try r.sendJSON("POST", cfg.url.appendingPathComponent("local/import/default"), ["cabinetLayout": layout])
    }
}

extension FeatureFlags {
    /// 測試用:兩層功能開關全部開(範例功能預設關;要測範例資料的工具或畫面時用這個)
    static var allOnForTests: FeatureFlags { withFeatures { _ in true } }

    /// 測試用:每個登記的功能開不開由 on(key) 決定(兩層都照這個,頁面依分頁重算)
    static func withFeatures(_ on: (String) -> Bool) -> FeatureFlags {
        let entries = FeatureFallback.entries
        var list = entries.map { e in
            CoreFeature(key: e.key, name: e.name, parent: e.parent, experimental: e.experimental, defaultOn: e.defaultOn,
                        required: e.required, enabled: e.required || on(e.key), on: e.required || on(e.key), system: true)
        }
        for i in list.indices where list[i].parent == nil && !list[i].required {
            let kids = list.filter { $0.parent == list[i].key }
            if !kids.isEmpty { list[i].enabled = list[i].on && kids.contains(where: \.on) }
        }
        for i in list.indices { if let p = list[i].parent, let pf = list.first(where: { $0.key == p }) { list[i].enabled = list[i].on && pf.on } }
        var sf: [String: Bool] = [:]
        for e in entries { sf[e.key] = true }
        return FeatureFlags(settings: CoreSystemSettings(features: list, serverFeatures: sf), source: .core)
    }
}
