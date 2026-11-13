import Foundation
import XCTest
@testable import Ops

// 測試用的 core server:App 的 Issue、角色與任務、組織鏈、頻道、文件櫃一律透過 core server(本機模式是本機守護服務),
// 所以原本測「本機模式」的測試改成連一個真的 omixor-local(Go,server/cmd/omixor-local),資料夾是暫存的。
// 執行檔第一次用到時用 go build 編到暫存資料夾(之後同一次測試共用);有 OMIXOR_LOCAL_BIN 就直接用那個。
// 版本跟著測試的編譯條件:有 OMIXOR_DEV 時用 -tags dev 編(開發版),沒有是正式版。
// 連接埠由守護服務自己挑(127.0.0.1 的隨機埠),不會碰到使用者正在用的 8080、3765、3767。
enum TestCore {
    private static let built: Result<URL, Error> = {
        if let p = ProcessInfo.processInfo.environment["OMIXOR_LOCAL_BIN"], !p.isEmpty { return .success(URL(fileURLWithPath: p)) }
        let serverDir = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("server")
        let out = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-local-test-\(ProcessInfo.processInfo.processIdentifier)")
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        // 守護服務和 App 用相同的版本編譯(開發版的測試:swift test -Xswiftc -DOMIXOR_DEV)
        #if OMIXOR_DEV
        let tags = "dev"
        #else
        let tags = ""
        #endif
        p.arguments = ["go", "build", "-tags", tags, "-o", out.path, "./cmd/omixor-local"]
        p.currentDirectoryURL = serverDir
        let err = Pipe()
        p.standardError = err
        do {
            try p.run()
            p.waitUntilExit()
        } catch { return .failure(error) }
        guard p.terminationStatus == 0 else {
            let msg = String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            return .failure(IssueRemoteError(message: "go build omixor-local 失敗:\(msg)"))
        }
        return .success(out)
    }()

    /// 測試編好的 omixor-local 執行檔(LocalCoreTests 用來自己啟動)
    static func binary() throws -> URL { try built.get() }

    /// 替這個測試啟動一個新的守護服務(預設組織 default、使用者 tim),設成 IssueServerConfig.testOverride;
    /// 測試結束時自動停掉並清掉暫存資料夾
    @discardableResult
    static func start(_ test: XCTestCase, user: String = "tim", name: String = "蔡庭宇") throws -> IssueServerConfig {
        let bin = try built.get()
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-core-test-\(UUID().uuidString)")
        let core = LocalCore()
        let cfg = try core.start(binary: bin, dataDir: dir, user: user, name: name, timeout: 20)
        IssueServerConfig.testOverride = cfg
        // 同正式第一次啟動:預設組織沒有 cabinet-layout.json 時,文件櫃結構用研發基地的(LocalCoreImport.bundle)
        let layout = try JSONSerialization.jsonObject(with: try OrgFiles.encoder().encode(CabinetLayout.evoleapStudio))
        _ = try IssueRemote(config: cfg).sendJSON("POST", cfg.url.appendingPathComponent("local/import/default"), ["cabinetLayout": layout])
        test.addTeardownBlock {
            core.stop()
            try? FileManager.default.removeItem(at: dir)
            if IssueServerConfig.testOverride == cfg { IssueServerConfig.testOverride = nil }
        }
        return cfg
    }
}
