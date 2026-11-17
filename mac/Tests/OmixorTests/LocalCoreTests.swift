import XCTest
@testable import Ops

/// 等到條件成立(主執行緒不擋;最多 timeout 秒)
@MainActor
func waitUntil(_ timeout: TimeInterval = 30, _ what: String = "", _ cond: () -> Bool) async throws {
    let deadline = Date().addingTimeInterval(timeout)
    while !cond() {
        if Date() > deadline { XCTFail("等不到:\(what)"); throw CabinetError("等不到:\(what)") }
        try await Task.sleep(nanoseconds: 20_000_000)
    }
}

/// 守護服務的監看與自動重啟:用 LocalCore.shared 跑一個真的 omixor-local(暫存資料夾),砍掉它看 App 的反應
@MainActor
final class LocalCoreRestartTests: XCTestCase {
    var dataDir: URL!
    var coreDir: URL!
    private var savedLauncher: ((String, String) throws -> IssueServerConfig)!
    private var savedDelays: [TimeInterval] = []

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-localcore-\(UUID().uuidString)")
        coreDir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-core-restart-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        IssueServerConfig.testOverride = nil   // 連線由 LocalCore.shared 決定(同正式的本機模式)
        let bin = try TestCore.binary(), dir = coreDir!
        savedLauncher = AppState.localCoreLauncher
        savedDelays = AppState.localCoreRestartDelays
        AppState.localCoreInTests = true
        AppState.localCoreRestartDelays = [0.2, 0.2, 0.2]
        AppState.localCoreLauncher = { user, name in
            try LocalCore.shared.start(binary: bin, dataDir: dir, user: user, name: name, timeout: 20)
        }
    }

    override func tearDown() async throws {
        LocalCore.shared.onUnexpectedExit = nil
        LocalCore.shared.stop()
        AppState.localCoreInTests = false
        AppState.localCoreLauncher = savedLauncher
        AppState.localCoreRestartDelays = savedDelays
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
        try? FileManager.default.removeItem(at: coreDir)
    }

    /// 守護服務中途被砍:回到等待畫面(localCore 不是 ready、顯示原因),自動重啟後回主畫面、重讀資料
    func testUnexpectedExitReturnsToGateAndRestarts() async throws {
        let app = AppState()
        XCTAssertEqual(app.mode.mode, OmixorMode.local)
        app.startLocalCore()
        try await waitUntil(30, "守護服務可用") { app.localCore == .ready }
        await app.orgReload?.value
        let pid = try XCTUnwrap(LocalCore.shared.pid)
        let made = app.startIssues().create(title: "重啟前的 Issue", by: "tim")
        XCTAssertFalse(made.id.isEmpty)

        kill(pid, SIGKILL)
        try await waitUntil(10, "回到等待畫面") { app.localCore != .ready }
        // 等待畫面:LoginGate 在本機模式、localCore 不是 ready 時顯示 LocalCoreGate
        XCTAssertNotNil(app.localCoreNotice)
        XCTAssertTrue(app.localCoreNotice?.contains("意外結束") ?? false, app.localCoreNotice ?? "")
        if case .failed(let why) = app.localCore { XCTAssertTrue(why.contains("意外結束"), why) }

        try await waitUntil(30, "自動重啟") { app.localCore == .ready }
        await app.orgReload?.value
        XCTAssertNil(app.localCoreNotice)
        XCTAssertNotEqual(LocalCore.shared.pid, pid, "是新的程序")
        XCTAssertNil(IssueServerConfig.current.unavailable)
        XCTAssertEqual(app.issues.map(\.title), ["重啟前的 Issue"], "重啟後重讀資料")
    }

    /// 一直起不來:自動重啟有次數上限,用完停在等待畫面等人按「重試」
    func testRestartGivesUpAfterLimit() async throws {
        AppState.localCoreLauncher = { _, _ in throw IssueRemoteError(message: "起不來") }
        AppState.localCoreRestartDelays = [0.05, 0.05]
        let app = AppState()
        app.localCoreDied("本機服務意外結束(訊號 9)")
        XCTAssertEqual(app.localCore, .failed("本機服務意外結束(訊號 9)"))
        try await waitUntil(10, "用完重啟次數") { app.localCoreNotice?.contains("已自動重啟 2 次") ?? false }
        XCTAssertEqual(app.localCore, .failed("起不來"))
        XCTAssertNil(app.localCoreRestartTask)
        // 按「重試」重新計算次數
        app.retryLocalCore()
        XCTAssertEqual(app.localCoreRestarts, 0)
        XCTAssertNil(app.localCoreNotice)
        try await waitUntil(10, "重試失敗") { app.localCore == .failed("起不來") }
    }
}

/// 假的本機守護服務的 /local/orgs:記下請求,DELETE 照 deleteStatus 回
final class FakeLocalCore: URLProtocol {
    nonisolated(unsafe) static var requests: [(method: String, path: String)] = []
    nonisolated(unsafe) static var deleteStatus = 200

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let method = request.httpMethod ?? "GET", path = request.url!.path
        Self.requests.append((method, path))
        var code = 200
        var obj: Any = ["result": [String: Any]()]
        if method == "DELETE" {
            code = Self.deleteStatus
            obj = code == 200 ? ["deleted": true] : ["error": "清除匯入紀錄失敗"]
        }
        let resp = HTTPURLResponse(url: request.url!, statusCode: code, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: try! JSONSerialization.data(withJSONObject: obj))
        client?.urlProtocolDidFinishLoading(self)
    }
}

/// 刪除組織要通知本機守護服務(DELETE /local/orgs/{org}),失敗時不刪、讓使用者知道
@MainActor
final class DeleteOrgTests: XCTestCase {
    var dataDir: URL!

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-delete-org-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        IssueServerConfig.testOverride = nil
        IssueRemote.protocolClasses = []
        unsetenv("OPS_DATA_DIR")
        TokenVault.scope = ""
        try? FileManager.default.removeItem(at: dataDir)
    }

    private func useFake() {
        IssueRemote.protocolClasses = [FakeLocalCore.self]
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5997")!, token: "tok", localCore: true)
        FakeLocalCore.requests = []
        FakeLocalCore.deleteStatus = 200
    }

    func testDeleteCallsLocalCore() async throws {
        useFake()
        let app = AppState()
        let org = app.createOrg(name: "Lab", template: .blank)
        await app.localOrgSync?.value
        try await app.deleteOrg(org.id)
        XCTAssertTrue(FakeLocalCore.requests.contains { $0.method == "DELETE" && $0.path == "/local/orgs/lab" })
        XCTAssertFalse(app.orgs.contains { $0.id == org.id })
    }

    func testDeleteFailureKeepsOrgAndReportsError() async throws {
        useFake()
        let app = AppState()
        let org = app.createOrg(name: "Lab", template: .blank)
        await app.localOrgSync?.value
        FakeLocalCore.deleteStatus = 500
        do {
            try await app.deleteOrg(org.id)
            XCTFail("守護服務刪失敗時不能當作成功")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("清除匯入紀錄失敗"), error.localizedDescription)
        }
        XCTAssertTrue(app.orgs.contains { $0.id == org.id }, "沒有刪")
        XCTAssertTrue(FileManager.default.fileExists(atPath: dataDir.appendingPathComponent("orgs/lab").path))
    }

    func testDeleteRefusedWhenLocalCoreUnavailable() async throws {
        IssueServerConfig.testOverride = IssueServerConfig(url: URL(string: "http://127.0.0.1:5997")!, token: nil, unavailable: "本機服務啟動中,請稍候")
        let app = AppState()
        let org = app.createOrg(name: "Lab", template: .blank)
        do { try await app.deleteOrg(org.id); XCTFail("本機服務不能用時不能刪") } catch {
            XCTAssertTrue(error.localizedDescription.contains("本機服務啟動中"), error.localizedDescription)
        }
        XCTAssertTrue(app.orgs.contains { $0.id == org.id })
    }

    /// 真的守護服務:刪掉組織再建同名組織,舊資料不會回來
    func testRecreatedOrgStartsEmpty() async throws {
        try TestCore.start(self)
        let app = AppState()
        app.startIssues()
        let org = app.createOrg(name: "Lab", template: .blank)
        await app.switchOrg(org.id).value
        app.startIssues().create(title: "舊組織的 Issue", by: "tim")
        XCTAssertEqual(app.issues.count, 1)
        await app.switchOrg(OrgFiles.defaultId).value
        try await app.deleteOrg(org.id)

        let again = app.createOrg(name: "Lab", template: .blank)
        XCTAssertEqual(again.id, org.id, "同名組織拿到同一個 id")
        await app.switchOrg(again.id).value
        XCTAssertNil(app.startIssues().remoteError)
        XCTAssertTrue(app.issues.isEmpty, "刪掉的組織的資料不該回來")
    }
}
