import XCTest
@testable import Ops

/// LINE 串接:用 StubProtocol 攔截 api.line.me,絕不送出真的請求
@MainActor
final class LineTests: XCTestCase {
    private var app: AppState!
    private var line: LineStore!
    private var dataDir: URL!
    /// 攔截到的 LINE 請求:網址、標頭、body
    nonisolated(unsafe) static var requests: [(url: String, headers: [String: String], body: [String: Any])] = []
    nonisolated(unsafe) static var status = 200

    static let secret = "test-channel-secret"
    static let boss = "U" + String(repeating: "a", count: 32)
    static let stranger = "U" + String(repeating: "b", count: 32)
    static let group = "C" + String(repeating: "c", count: 32)

    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-line-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
        ConnHTTP.protocolClasses = [StubProtocol.self]
        LineAPI.retryDelay = 1_000_000
        Self.requests = []
        Self.status = 200
        StubProtocol.log = []
        StubProtocol.handler = { req, body in
            let obj = (try? JSONSerialization.jsonObject(with: body) as? [String: Any]) ?? [:]
            LineTests.requests.append((req.url?.absoluteString ?? "", req.allHTTPHeaderFields ?? [:], obj))
            return (LineTests.status, LineTests.status < 300 ? [:] : ["message": "The request body has 1 error(s)"])
        }
        app = AppState()
        LineStore.setToken("test-access-token")
        LineStore.setChannelSecret(Self.secret)
        line = app.startLine(observe: false, server: false)
        line.update {
            $0.recipients = [LineRecipient(id: Self.group, label: "值班群組")]
            $0.allowed = [LineAllowedUser(userId: Self.boss, name: "Tim", person: "tim")]
        }
    }

    override func tearDown() async throws {
        LineStore.removeSecrets()
        ConnHTTP.protocolClasses = []
        StubProtocol.handler = nil
        LineAPI.retryDelay = 2_000_000_000
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: dataDir)
    }

    // MARK: - 小工具

    private var pushes: [(url: String, headers: [String: String], body: [String: Any])] {
        Self.requests.filter { $0.url == LineAPI.pushURL.absoluteString }
    }

    private var replies: [String] {
        Self.requests.filter { $0.url == LineAPI.replyURL.absoluteString }.compactMap {
            (($0.body["messages"] as? [[String: Any]])?.first?["text"] as? String)
        }
    }

    private func newAlert(_ id: String, _ sev: Severity) -> Alert {
        Alert(id: id, title: "\(id) 延遲過高", severity: sev, service: app.services[0].id, state: .firing, rule: "p95 > 800ms",
              value: "1.2s", startedAt: .now, acknowledgedBy: nil, incidentId: nil, aiNote: nil)
    }

    private func newApproval(_ id: String) -> ApprovalRequest {
        ApprovalRequest(id: id, title: "重啟 api-gateway", automation: "mcp", risk: .medium, action: "kubectl rollout restart",
                        rationale: "記憶體洩漏", evidence: [], rollbackPlan: "無需回滾", createdAt: .now,
                        expiresAt: .now.addingTimeInterval(3600), state: .pending, decidedBy: nil)
    }

    private func header(_ h: [String: String], _ name: String) -> String? {
        h.first { $0.key.lowercased() == name.lowercased() }?.value
    }

    @discardableResult
    private func webhook(_ events: [[String: Any]], signature: String?? = nil) async throws -> HTTPResponse {
        let body = try JSONSerialization.data(withJSONObject: ["destination": "Uxxx", "events": events])
        let sig: String? = signature ?? LineAPI.sign(body: body, secret: Self.secret)
        var headers = ["content-type": "application/json"]
        if let sig { headers["x-line-signature"] = sig }
        let resp = line.handleWebhook(HTTPRequest(method: "POST", path: LineWebhookServer.path, headers: headers, body: body))
        await line.drain()
        return resp
    }

    private func textEvent(_ user: String, _ text: String) -> [String: Any] {
        ["type": "message", "webhookEventId": UUID().uuidString, "replyToken": "rt-\(UUID().uuidString)",
         "source": ["type": "user", "userId": user], "message": ["type": "text", "id": "1", "text": text]]
    }

    private func postbackEvent(_ user: String, _ data: String) -> [String: Any] {
        ["type": "postback", "webhookEventId": UUID().uuidString, "replyToken": "rt-\(UUID().uuidString)",
         "source": ["type": "group", "groupId": Self.group, "userId": user], "postback": ["data": data]]
    }

    /// 推播待核准請求,從 Flex 的按鈕拿出 postback data
    private func pushApproval(_ id: String) async throws -> (approve: String, reject: String) {
        app.approvals.insert(newApproval(id), at: 0)
        XCTAssertEqual(line.scan().count, 1)
        await line.drain()
        let msg = try XCTUnwrap((pushes.last?.body["messages"] as? [[String: Any]])?.first)
        let footer = try XCTUnwrap(((msg["contents"] as? [String: Any])?["footer"] as? [String: Any])?["contents"] as? [[String: Any]])
        let buttons = try XCTUnwrap(footer.first?["contents"] as? [[String: Any]])
        let data = buttons.compactMap { ($0["action"] as? [String: Any])?["data"] as? String }
        XCTAssertEqual(data.count, 2)
        XCTAssertEqual((buttons[0]["action"] as? [String: Any])?["type"] as? String, "postback")
        return (data[0], data[1])
    }

    // MARK: - 設定

    func testSettingsLenientDecodingAndSecretsStayOutOfJSON() throws {
        let raw = #"{"recipients":[{"id":"Cabc"},{"label":"沒有 id"}],"port":"x","alertMinSeverity":"huge","notifyVoice":false,"extra":1}"#
        let s = try JSONDecoder().decode(LineSettings.self, from: Data(raw.utf8))
        XCTAssertEqual(s.recipients.map(\.id), ["Cabc"])
        XCTAssertEqual(s.port, 3766)
        XCTAssertEqual(s.alertMinSeverity, "warning")
        XCTAssertFalse(s.notifyVoice)
        XCTAssertTrue(s.notifyAlerts)
        XCTAssertNoThrow(try JSONDecoder().decode(LineSettings.self, from: Data("{}".utf8)))

        let file = try String(contentsOf: app.dataDir.appendingPathComponent("line.json"), encoding: .utf8)
        XCTAssertTrue(file.contains(Self.group))
        XCTAssertFalse(file.contains("test-access-token"))
        XCTAssertFalse(file.contains(Self.secret))
        XCTAssertEqual(LineStore.token(), "test-access-token")
        XCTAssertTrue(app.line.hasToken && app.line.hasSecret)

        // 重新讀檔
        let app2 = AppState()
        let s2 = app2.startLine(observe: false, server: false)
        _ = s2
        XCTAssertEqual(app2.lineSettings.allowed.first?.userId, Self.boss)

        XCTAssertTrue(LineSettings.isValidId(Self.boss))
        XCTAssertFalse(LineSettings.isValidId("tim72117"))
        var w = LineSettings()
        w.publicURL = "abc.trycloudflare.com/"
        XCTAssertEqual(w.webhookURL, "https://abc.trycloudflare.com/line/webhook")
    }

    func testSecretsAreScopedPerOrg() {
        TokenVault.scope = "other/"
        XCTAssertNil(LineStore.token())
        TokenVault.scope = ""
        XCTAssertEqual(LineStore.token(), "test-access-token")
    }

    // MARK: - 推播

    func testPushRequestFormatAndHeaders() async throws {
        XCTAssertTrue(line.scan().isEmpty, "啟動時已存在的事不推")
        app.alerts.insert(newAlert("ALT-9001", .critical), at: 0)
        XCTAssertEqual(line.scan().count, 1)
        await line.drain()

        let p = try XCTUnwrap(pushes.first)
        XCTAssertEqual(p.url, "https://api.line.me/v2/bot/message/push")
        XCTAssertEqual(header(p.headers, "Authorization"), "Bearer test-access-token")
        XCTAssertEqual(header(p.headers, "Content-Type"), "application/json")
        XCTAssertNotNil(UUID(uuidString: header(p.headers, "X-Line-Retry-Key") ?? ""))
        XCTAssertEqual(p.body["to"] as? String, Self.group)
        let msg = try XCTUnwrap((p.body["messages"] as? [[String: Any]])?.first)
        XCTAssertEqual(msg["type"] as? String, "flex")
        XCTAssertTrue((msg["altText"] as? String)?.contains("ALT-9001 延遲過高") == true)
        let bubble = try XCTUnwrap(msg["contents"] as? [String: Any])
        XCTAssertEqual(bubble["type"] as? String, "bubble")
        let json = String(decoding: try JSONSerialization.data(withJSONObject: bubble), as: UTF8.self)
        XCTAssertTrue(json.contains("在 omixor 開啟"))
        XCTAssertTrue(json.contains("Critical"))
        XCTAssertEqual(app.line.pushed, 1)

        // 同一件事不重複推
        XCTAssertTrue(line.scan().isEmpty)
        app.acknowledge(alertId: "ALT-9001")
        XCTAssertTrue(line.scan().isEmpty)
        await line.drain()
        XCTAssertEqual(pushes.count, 1)
    }

    func testEachRecipientGetsOwnPush() async {
        line.update { $0.recipients.append(LineRecipient(id: Self.boss, label: "Tim")) }
        app.alerts.insert(newAlert("ALT-9002", .critical), at: 0)
        line.scan()
        await line.drain()
        XCTAssertEqual(Set(pushes.compactMap { $0.body["to"] as? String }), [Self.group, Self.boss])
        XCTAssertEqual(Set(pushes.compactMap { header($0.headers, "X-Line-Retry-Key") }).count, 2)
    }

    func testSeverityThresholdAndToggles() {
        line.update { $0.alertMinSeverity = "critical" }
        app.alerts.insert(newAlert("ALT-9101", .warning), at: 0)
        XCTAssertTrue(line.scan().isEmpty)
        app.alerts.insert(newAlert("ALT-9102", .critical), at: 0)
        XCTAssertEqual(line.scan().map(\.key), ["alert:ALT-9102"])
        line.update { $0.notifyAlerts = false }
        app.alerts.insert(newAlert("ALT-9103", .critical), at: 0)
        XCTAssertTrue(line.scan().isEmpty)
    }

    func testIncidentVoiceAndAgentTaskNotices() {
        line.scan()
        let inc = Incident(id: "INC-9001", title: "結帳失敗", sev: 1, status: .investigating, services: [], commander: "tim",
                           startedAt: .now, resolvedAt: nil, impact: "部分使用者無法結帳", timeline: [], aiSummary: nil, followUps: [])
        app.incidents.insert(inc, at: 0)
        XCTAssertEqual(line.scan().map(\.key), ["incident:INC-9001:declared"])
        // 事件改狀態(事件存在 core server;這裡沒有 server,直接改畫面上的副本,同 IncidentStore 寫入成功後的結果)
        app.incidents[0].status = .identified
        XCTAssertEqual(line.scan().map(\.key), ["incident:INC-9001:identified"])
        XCTAssertTrue(line.scan().isEmpty)
        // 已有的事件改狀態也要推
        let old = app.incidents.first { $0.id != "INC-9001" && $0.status != .resolved }!
        app.incidents[app.incidents.firstIndex { $0.id == old.id }!].status = .resolved
        XCTAssertEqual(line.scan().map(\.key), ["incident:\(old.id):resolved"])

        app.voiceJobs.append(VoiceJob(id: "VJ-99", scripts: ["live/a.md"], all: false, limit: nil, state: .processing,
                                      createdAt: .now, updatedAt: .now, by: "tim"))
        XCTAssertTrue(line.scan().isEmpty)
        app.voiceJobs[app.voiceJobs.count - 1].state = .ready
        XCTAssertEqual(line.scan().map(\.key), ["voice:VJ-99:ready"])

        app.agentTasks.append(AgentTask(id: "T-99", title: "整理訪客報告", agentId: "analyst", createdBy: "tim"))
        XCTAssertTrue(line.scan().isEmpty)
        app.agentTasks[app.agentTasks.count - 1].status = .blocked
        XCTAssertEqual(line.scan().map(\.key), ["task:T-99:blocked"])
        app.agentTasks[app.agentTasks.count - 1].status = .inProgress
        line.scan()
        app.agentTasks[app.agentTasks.count - 1].status = .blocked
        XCTAssertTrue(line.scan().isEmpty, "同一件事只推一次")
    }

    func testPushFailureIsRecordedAndServerErrorRetried() async {
        Self.status = 400
        app.alerts.insert(newAlert("ALT-9201", .critical), at: 0)
        line.scan()
        await line.drain()
        XCTAssertEqual(app.line.failed, 1)
        XCTAssertTrue(app.line.lastError?.contains("HTTP 400") == true, app.line.lastError ?? "")
        XCTAssertEqual(pushes.count, 1, "4xx 不重試")

        Self.requests = []
        Self.status = 500
        app.alerts.insert(newAlert("ALT-9202", .critical), at: 0)
        line.scan()
        await line.drain()
        XCTAssertEqual(pushes.count, 2, "5xx 重試一次")
        XCTAssertEqual(header(pushes[0].headers, "X-Line-Retry-Key"), header(pushes[1].headers, "X-Line-Retry-Key"))
        XCTAssertEqual(app.line.failed, 2)
    }

    func testNoTokenSkipsPushWithVisibleError() async {
        LineStore.setToken(nil)
        app.alerts.insert(newAlert("ALT-9301", .critical), at: 0)
        line.scan()
        await line.drain()
        XCTAssertTrue(pushes.isEmpty)
        XCTAssertTrue(app.line.lastError?.contains("token") == true)
    }

    func testSendTestMessage() async {
        let err = await line.sendTest()
        XCTAssertNil(err)
        XCTAssertEqual((pushes.first?.body["messages"] as? [[String: Any]])?.first?["type"] as? String, "text")
        Self.status = 401
        let err2 = await line.sendTest()
        XCTAssertTrue(err2?.contains("401") == true)
    }

    // MARK: - Webhook 簽章

    func testSignatureVerification() async throws {
        let ok = try await webhook([textEvent(Self.boss, "現在狀況如何")])
        XCTAssertEqual(ok.status, 200)
        XCTAssertEqual(replies.count, 1)

        Self.requests = []
        let wrong = try await webhook([textEvent(Self.boss, "現在狀況如何")], signature: .some("bm90IHRoZSByaWdodCBzaWduYXR1cmU="))
        XCTAssertEqual(wrong.status, 401)
        let garbage = try await webhook([textEvent(Self.boss, "現在狀況如何")], signature: .some("%%%"))
        XCTAssertEqual(garbage.status, 401)
        let missing = try await webhook([textEvent(Self.boss, "現在狀況如何")], signature: .some(nil))
        XCTAssertEqual(missing.status, 401)
        XCTAssertTrue(Self.requests.isEmpty, "驗證失敗不處理")
        XCTAssertEqual(app.line.rejectedSignatures, 3)

        // 用別的 secret 簽的也不行
        let body = Data(#"{"events":[]}"#.utf8)
        XCTAssertFalse(LineAPI.verify(body: body, signature: LineAPI.sign(body: body, secret: "other"), secret: Self.secret))
        XCTAssertTrue(LineAPI.verify(body: body, signature: LineAPI.sign(body: body, secret: Self.secret), secret: Self.secret))
        XCTAssertFalse(LineAPI.verify(body: body, signature: LineAPI.sign(body: body, secret: Self.secret), secret: ""))
        // LINE 的 Verify 按鈕送空事件
        let verify = try await webhook([])
        XCTAssertEqual(verify.status, 200)
    }

    func testWebhookWithoutSecretRejects() async throws {
        LineStore.setChannelSecret(nil)
        let r = try await webhook([textEvent(Self.boss, "狀況")])
        XCTAssertEqual(r.status, 401)
        XCTAssertTrue(replies.isEmpty)
    }

    // MARK: - 在 LINE 問狀況

    func testTextQuestionFromAllowedUserGetsPlainAnswer() async throws {
        try await webhook([textEvent(Self.boss, "有哪些告警")])
        let answer = try XCTUnwrap(replies.first)
        let expected = LineAPI.plainText(AssistResponder(app: app).reply(to: "有哪些告警").text)
        XCTAssertEqual(answer, expected)
        XCTAssertFalse(answer.contains("**"))
        XCTAssertFalse(answer.isEmpty)
        let req = Self.requests.first { $0.url == LineAPI.replyURL.absoluteString }!
        XCTAssertEqual(header(req.headers, "Authorization"), "Bearer test-access-token")
        XCTAssertTrue((req.body["replyToken"] as? String)?.hasPrefix("rt-") == true)
    }

    func testStrangerGetsNoPermissionAndNoData() async throws {
        try await webhook([textEvent(Self.stranger, "有哪些告警")])
        XCTAssertEqual(replies, [LineStore.noPermission])
        XCTAssertFalse(replies[0].contains(app.alerts[0].title))
        XCTAssertEqual(app.line.unknownSenders.first?.userId, Self.stranger)
        // 群組裡沒有 userId 的訊息也一樣
        var ev = textEvent(Self.stranger, "狀況")
        ev["source"] = ["type": "group", "groupId": Self.group]
        try await webhook([ev])
        XCTAssertEqual(replies.last, LineStore.noPermission)
    }

    func testRedeliveredEventHandledOnce() async throws {
        let ev = textEvent(Self.boss, "狀況")
        try await webhook([ev])
        try await webhook([ev])
        XCTAssertEqual(replies.count, 1)
    }

    // MARK: - 在 LINE 核准

    func testPostbackApprovalFlow() async throws {
        let (approve, _) = try await pushApproval("APV-9001")
        try await webhook([postbackEvent(Self.boss, approve)])
        let r = try XCTUnwrap(app.approvals.first { $0.id == "APV-9001" })
        XCTAssertEqual(r.state, .approved)
        XCTAssertEqual(r.decidedBy, "tim")
        XCTAssertTrue(app.activity[0].text.contains("核准 AI 請求「重啟 api-gateway」"))
        XCTAssertTrue(app.activity[0].text.contains("經 LINE"))
        XCTAssertEqual(app.activity[0].actor, .person("tim"))
        XCTAssertTrue(replies.last?.contains("已核准") == true)

        // 重放同一個按鈕(LINE 新的事件 id)
        try await webhook([postbackEvent(Self.boss, approve)])
        XCTAssertTrue(replies.last?.contains("已經用過") == true)
        XCTAssertEqual(app.activity.filter { $0.text.contains("重啟 api-gateway") && $0.text.contains("經 LINE") }.count, 1)
    }

    func testPostbackRejectSignsAsConfiguredPerson() async throws {
        let other = app.people.first { $0.id != app.me }!
        line.update { $0.allowed = [LineAllowedUser(userId: Self.boss, name: "值班", person: other.id)] }
        let (_, reject) = try await pushApproval("APV-9002")
        try await webhook([postbackEvent(Self.boss, reject)])
        let r = try XCTUnwrap(app.approvals.first { $0.id == "APV-9002" })
        XCTAssertEqual(r.state, .rejected)
        XCTAssertEqual(r.decidedBy, other.id)
        XCTAssertEqual(app.activity[0].actor, .person(other.id))
        XCTAssertTrue(app.activity[0].text.contains("拒絕"))
        XCTAssertTrue(app.activity[0].text.contains("經 LINE,值班"))
    }

    func testPostbackRejectedForStrangerBadNonceAndDecidedRequest() async throws {
        let (approve, _) = try await pushApproval("APV-9003")

        // 不在白名單
        try await webhook([postbackEvent(Self.stranger, approve)])
        XCTAssertEqual(replies.last, LineStore.noPermission)
        XCTAssertEqual(app.approvals.first { $0.id == "APV-9003" }?.state, .pending)

        // 偽造的 nonce、換掉請求 id
        try await webhook([postbackEvent(Self.boss, LineAPI.approvalData(id: "APV-9003", approve: true, nonce: "forged"))])
        XCTAssertTrue(replies.last?.contains("已失效") == true)
        let nonce = LineAPI.parseData(approve)["n"]!
        app.approvals.insert(newApproval("APV-9004"), at: 0)
        try await webhook([postbackEvent(Self.boss, LineAPI.approvalData(id: "APV-9004", approve: true, nonce: nonce))])
        XCTAssertTrue(replies.last?.contains("已失效") == true)
        XCTAssertEqual(app.approvals.first { $0.id == "APV-9004" }?.state, .pending)
        XCTAssertEqual(app.approvals.first { $0.id == "APV-9003" }?.state, .pending)

        // 已經在 app 裡決定了
        app.decide(approvalId: "APV-9003", approve: false)
        try await webhook([postbackEvent(Self.boss, approve)])
        XCTAssertEqual(app.approvals.first { $0.id == "APV-9003" }?.state, .rejected)
        XCTAssertTrue(replies.last?.contains("已拒絕") == true, replies.last ?? "")
    }

    func testNonceSurvivesRestart() async throws {
        let (approve, _) = try await pushApproval("APV-9005")
        // 重開 app:新的 LineStore 從 line-state.json 讀 nonce
        let pending = app.approvals
        let app2 = AppState()
        app2.approvals = pending
        line = app2.startLine(observe: false, server: false)
        app = app2
        try await webhook([postbackEvent(Self.boss, approve)])
        XCTAssertEqual(app2.approvals.first { $0.id == "APV-9005" }?.state, .approved)
    }

    // MARK: - 真的 HTTP 伺服器

    func testWebhookServerOverLoopback() async throws {
        let port = Int.random(in: 42000..<48000)
        let app3 = AppState()
        let store = app3.startLine(observe: false, server: false)
        store.update { $0.port = port }
        // 開伺服器
        let srv = LineWebhookServer(port: UInt16(port), handle: { req in await store.handleWebhook(req) }, onState: { _ in })
        srv.start()
        defer { srv.stop() }
        try await Task.sleep(nanoseconds: 300_000_000)

        func post(_ path: String, sig: String?) async throws -> Int {
            var req = URLRequest(url: URL(string: "http://127.0.0.1:\(port)\(path)")!)
            req.httpMethod = "POST"
            let body = Data(#"{"events":[]}"#.utf8)
            req.httpBody = body
            if let sig { req.setValue(sig, forHTTPHeaderField: "X-Line-Signature") }
            let c = URLSessionConfiguration.ephemeral
            let (_, resp) = try await URLSession(configuration: c).data(for: req)
            return (resp as! HTTPURLResponse).statusCode
        }
        let body = Data(#"{"events":[]}"#.utf8)
        let okStatus = try await post("/line/webhook", sig: LineAPI.sign(body: body, secret: Self.secret))
        XCTAssertEqual(okStatus, 200)
        let badStatus = try await post("/line/webhook", sig: "AAAA")
        XCTAssertEqual(badStatus, 401)
        let missingStatus = try await post("/line/webhook", sig: nil)
        XCTAssertEqual(missingStatus, 401)
        let nf = try await post("/mcp", sig: nil)
        XCTAssertEqual(nf, 404)
    }
}
