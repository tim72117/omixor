import Combine
import Foundation

// LINE 串接:推播通知、在 LINE 核准 AI 請求、在 LINE 問狀況。
// - 推播:看 AppState 的告警、事件、待核准請求、旁白批次、角色任務,有新狀況就 push(同一件事只推一次)
// - webhook:本機 127.0.0.1:<埠>/line/webhook,先驗 X-Line-Signature 才處理;回覆用 reply API
// - 核准與提問只限白名單裡的 LINE userId;其他人一律回「沒有權限」

@MainActor
final class LineStore {
    unowned let app: AppState
    init(app: AppState) { self.app = app }

    static let tokenAccount = "line:accessToken"
    static let secretAccount = "line:channelSecret"
    static let noPermission = "沒有權限。請 Ops 的管理員把你的 LINE userId 加進白名單。"

    private var fileURL: URL { app.dataDir.appendingPathComponent("line.json") }
    private var stateURL: URL { app.dataDir.appendingPathComponent("line-state.json") }

    private var state = LineState()
    private var cancellables: Set<AnyCancellable> = []
    private var inflight: [Task<Void, Never>] = []
    private var server: LineWebhookServer?
    private var serverPort: Int?
    private var serverAllowed = false

    // 去重:看過的事、上次看到的狀態
    private var baselineOrg: String?
    private var baselineAt = Date.distantPast
    private var seen: Set<String> = []
    private var incidentStatus: [String: IncidentStatus] = [:]
    private var taskStatus: [String: AgentTaskStatus] = [:]
    private var voiceState: [String: VoiceJobState] = [:]
    private var recentEvents: [String] = []

    // MARK: - 鑰匙圈(帳號名稱帶組織前綴)

    static func token() -> String? { secretValue(tokenAccount) }
    static func channelSecret() -> String? { secretValue(secretAccount) }

    private static func secretValue(_ account: String) -> String? {
        guard let d = TokenVault.data(TokenVault.scope + account) else { return nil }
        let s = String(decoding: d, as: UTF8.self)
        return s.isEmpty ? nil : s
    }

    static func setToken(_ s: String?) { TokenVault.set(clean(s).map { Data($0.utf8) }, TokenVault.scope + tokenAccount) }
    static func setChannelSecret(_ s: String?) { TokenVault.set(clean(s).map { Data($0.utf8) }, TokenVault.scope + secretAccount) }

    /// 刪除目前 scope 的 LINE 機密(刪除組織時用)
    static func removeSecrets() {
        TokenVault.set(nil, TokenVault.scope + tokenAccount)
        TokenVault.set(nil, TokenVault.scope + secretAccount)
    }

    private static func clean(_ s: String?) -> String? {
        guard let t = s?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { return nil }
        return t
    }

    // MARK: - 讀寫設定

    func load() {
        let dec = OrgFiles.decoder()
        app.lineSettings = (try? Data(contentsOf: fileURL)).flatMap { try? dec.decode(LineSettings.self, from: $0) } ?? LineSettings()
        state = (try? Data(contentsOf: stateURL)).flatMap { try? dec.decode(LineState.self, from: $0) } ?? LineState()
        refreshSecrets()
    }

    func save() {
        if let d = try? OrgFiles.encoder().encode(app.lineSettings) { try? d.write(to: fileURL, options: .atomic) }
    }

    private func saveState() {
        // 一週以前的 nonce 清掉
        let cutoff = Date().addingTimeInterval(-7 * 86400)
        state.nonces = state.nonces.filter { $0.value.createdAt > cutoff }
        if let d = try? OrgFiles.encoder().encode(state) { try? d.write(to: stateURL, options: .atomic) }
    }

    func update(_ change: (inout LineSettings) -> Void) {
        var s = app.lineSettings
        change(&s)
        guard s != app.lineSettings else { return }
        app.lineSettings = s
        save()
        applyServer()
    }

    func saveSecrets(token: String?, secret: String?) {
        if let token { Self.setToken(token) }
        if let secret { Self.setChannelSecret(secret) }
        refreshSecrets()
        applyServer()
    }

    func clearSecrets() {
        Self.removeSecrets()
        refreshSecrets()
        applyServer()
    }

    private func refreshSecrets() {
        app.line.hasToken = Self.token() != nil
        app.line.hasSecret = Self.channelSecret() != nil
    }

    /// 切換組織後:讀新組織的設定,重新建立基準(不推舊的事)
    func orgChanged() {
        load()
        baselineOrg = nil
        scan()
        applyServer()
    }

    // MARK: - 啟動

    func start(observe: Bool, server: Bool) {
        serverAllowed = server
        scan()  // 建立基準:啟動時已經存在的事不推
        applyServer()
        guard observe else { return }
        let pubs: [AnyPublisher<Void, Never>] = [
            app.$alerts.map { _ in () }.eraseToAnyPublisher(),
            app.$incidents.map { _ in () }.eraseToAnyPublisher(),
            app.$approvals.map { _ in () }.eraseToAnyPublisher(),
            app.$voiceJobs.map { _ in () }.eraseToAnyPublisher(),
            app.$agentTasks.map { _ in () }.eraseToAnyPublisher(),
        ]
        Publishers.MergeMany(pubs)
            .debounce(for: .milliseconds(400), scheduler: DispatchQueue.main)
            .sink { [weak self] in MainActor.assumeIsolated { _ = self?.scan() } }
            .store(in: &cancellables)
    }

    /// 依設定開關 webhook 伺服器(有 channel secret 才開)
    func applyServer() {
        let s = app.lineSettings
        let want = serverAllowed && s.webhookEnabled && app.line.hasSecret
        if !want {
            server?.stop()
            server = nil
            serverPort = nil
            app.line.server = .stopped
            return
        }
        if server != nil, serverPort == s.port { return }
        server?.stop()
        let srv = LineWebhookServer(
            port: UInt16(s.port),
            handle: { [weak self] req in
                guard let self else { return HTTPResponse(status: 503, body: Data(), contentType: nil) }
                return await self.handleWebhook(req)
            },
            onState: { [weak self] st in Task { @MainActor in self?.app.line.server = st } }
        )
        server = srv
        serverPort = s.port
        srv.start()
    }

    /// 等所有送出中的請求完成(測試用,也給「傳測試訊息」等結果)
    func drain() async {
        while !inflight.isEmpty {
            let t = inflight.removeFirst()
            await t.value
        }
    }

    // MARK: - 推播

    /// 比對目前狀態和上次看到的,找出要推的新狀況並送出;回傳這次要推的通知(測試用)
    @discardableResult
    func scan() -> [LineNotice] {
        let s = app.lineSettings
        let first = baselineOrg != app.currentOrgId
        if first {
            baselineOrg = app.currentOrgId
            baselineAt = .now
            seen = []
            incidentStatus = [:]
            taskStatus = [:]
            voiceState = [:]
        }
        // 新出現的項目只有在基準之後發生的才推(避免晚一點才讀進來的舊資料被當成新事)
        func fresh(_ d: Date) -> Bool { !first && d >= baselineAt.addingTimeInterval(-5) }
        var out: [LineNotice] = []

        for a in app.alerts where a.state == .firing {
            if seen.insert("alert:\(a.id)").inserted, fresh(a.startedAt), s.notifyAlerts, a.severity <= s.minSeverity {
                out.append(alertNotice(a))
            }
        }
        for inc in app.incidents {
            let prev = incidentStatus[inc.id]
            incidentStatus[inc.id] = inc.status
            if prev == nil {
                if seen.insert("incident:\(inc.id):declared").inserted, fresh(inc.startedAt), s.notifyIncidents {
                    out.append(incidentNotice(inc, declared: true))
                }
                seen.insert("incident:\(inc.id):\(inc.status.rawValue)")
            } else if prev != inc.status {
                if seen.insert("incident:\(inc.id):\(inc.status.rawValue)").inserted, !first, s.notifyIncidents {
                    out.append(incidentNotice(inc, declared: false))
                }
            }
        }
        for r in app.approvals where r.state == .pending {
            if seen.insert("approval:\(r.id)").inserted, fresh(r.createdAt), s.notifyApprovals {
                out.append(approvalNotice(r))
            }
        }
        for j in app.voiceJobs {
            let prev = voiceState[j.id]
            voiceState[j.id] = j.state
            guard j.state == .ready, prev != .ready else { continue }
            if seen.insert("voice:\(j.id):ready").inserted, prev != nil ? !first : fresh(j.updatedAt), s.notifyVoice {
                out.append(voiceNotice(j))
            }
        }
        for t in app.agentTasks {
            let prev = taskStatus[t.id]
            taskStatus[t.id] = t.status
            guard t.status == .blocked, prev != .blocked else { continue }
            if seen.insert("task:\(t.id):blocked").inserted, prev != nil ? !first : fresh(t.updatedAt), s.notifyAgentBlocked {
                out.append(taskNotice(t))
            }
        }
        if !out.isEmpty { send(out) }
        return out
    }

    private func send(_ notices: [LineNotice]) {
        let s = app.lineSettings
        guard s.pushEnabled, !s.recipients.isEmpty else { return }
        guard let token = Self.token() else {
            app.line.note(false, "沒有 channel access token,略過 \(notices.count) 則推播")
            return
        }
        for n in notices {
            for r in s.recipients {
                let retryKey = UUID().uuidString.lowercased()
                let message = n.message
                let label = r.label.isEmpty ? r.id : r.label
                inflight.append(Task { [weak self] in
                    do {
                        try await LineAPI.push(token: token, to: r.id, messages: [message], retryKey: retryKey)
                        self?.app.line.pushed += 1
                        self?.app.line.note(true, "推播給 \(label):\(n.altText)")
                    } catch {
                        self?.app.line.failed += 1
                        self?.app.line.note(false, "推播給 \(label) 失敗(\(n.altText)):\(error)")
                    }
                })
            }
        }
    }

    /// 傳一則測試訊息給所有接收者;回傳錯誤訊息(沒有錯就是 nil)
    func sendTest() async -> String? {
        let s = app.lineSettings
        guard let token = Self.token() else { return "還沒填 channel access token" }
        guard !s.recipients.isEmpty else { return "還沒有接收者" }
        var errors: [String] = []
        for r in s.recipients {
            let label = r.label.isEmpty ? r.id : r.label
            do {
                try await LineAPI.push(token: token, to: r.id,
                                       messages: [LineAPI.text("這是 Ops 的測試訊息(組織:\(app.currentOrg?.name ?? app.currentOrgId))。收到這則表示推播設定正確。")],
                                       retryKey: UUID().uuidString.lowercased())
                app.line.pushed += 1
                app.line.note(true, "測試訊息已送給 \(label)")
            } catch {
                app.line.failed += 1
                app.line.note(false, "測試訊息送給 \(label) 失敗:\(error)")
                errors.append("\(label):\(error)")
            }
        }
        return errors.isEmpty ? nil : errors.joined(separator: "\n")
    }

    // MARK: - 通知內容

    private func severityColor(_ s: Severity) -> String {
        switch s {
        case .critical: return "#CF222E"
        case .warning: return "#BF8700"
        case .info: return "#0969DA"
        }
    }

    private func when(_ d: Date) -> String { d.formatted(.dateTime.month().day().hour().minute()) }

    func alertNotice(_ a: Alert) -> LineNotice {
        var fields = [("服務", app.service(a.service)?.name ?? a.service), ("規則", a.rule), ("目前值", a.value), ("開始", when(a.startedAt))]
        if let n = a.aiNote { fields.append(("AI 判讀", n)) }
        return LineNotice(key: "alert:\(a.id)", kind: "告警觸發 · \(a.severity.label)", title: a.title,
                          summary: "\(app.service(a.service)?.name ?? a.service) 的告警觸發中。", fields: fields,
                          openIn: "監控與告警 › 告警 › \(a.id)", color: severityColor(a.severity))
    }

    func incidentNotice(_ i: Incident, declared: Bool) -> LineNotice {
        let fields = [("狀態", i.status.label), ("服務", i.services.map { app.service($0)?.name ?? $0 }.joined(separator: "、")),
                      ("指揮官", app.person(i.commander)?.name ?? i.commander), ("開始", when(i.startedAt))]
        let last = i.timeline.last?.text
        return LineNotice(key: "incident:\(i.id):\(declared ? "declared" : i.status.rawValue)",
                          kind: declared ? "宣告事件 · SEV\(i.sev)" : "事件狀態變更 · \(i.status.label)",
                          title: "\(i.id) \(i.title)",
                          summary: declared ? (i.impact.isEmpty ? "新事件已宣告。" : i.impact) : (last ?? "狀態改為「\(i.status.label)」"),
                          fields: fields, openIn: "事件 › \(i.id)",
                          color: i.status == .resolved ? "#2DA44E" : "#CF222E")
    }

    func approvalNotice(_ r: ApprovalRequest) -> LineNotice {
        let nonce = LineAPI.nonce()
        state.nonces[nonce] = LineNonce(approvalId: r.id, createdAt: .now)
        saveState()
        return LineNotice(key: "approval:\(r.id)", kind: "待核准 AI 請求 · \(r.risk.label)", title: r.title,
                          summary: r.rationale,
                          fields: [("來源", app.name(of: .ai(r.automation))), ("動作", r.action), ("回滾", r.rollbackPlan),
                                   ("期限", when(r.expiresAt))],
                          openIn: "AI 自動化 › 待核准 › \(r.id)", color: "#8250DF", approval: (r.id, nonce))
    }

    func voiceNotice(_ j: VoiceJob) -> LineNotice {
        LineNotice(key: "voice:\(j.id):ready", kind: "旁白批次待發佈", title: "\(j.id) \(j.title)",
                   summary: "批次已收完,\(j.changes.count) 個檔案有變動。請在 Ops 試聽後按發佈(LINE 不能發佈)。",
                   fields: j.failedClips.isEmpty ? [] : [("失敗段落", "\(j.failedClips.count) 段")],
                   openIn: "CI/CD › 旁白發佈 › \(j.id)", color: "#0969DA")
    }

    func taskNotice(_ t: AgentTask) -> LineNotice {
        let agent = app.agents.first { $0.id == t.agentId }?.name ?? t.agentId
        let last = t.notes.last?.text ?? (t.result.isEmpty ? "角色標成「需要協助」" : t.result)
        return LineNotice(key: "task:\(t.id):blocked", kind: "虛擬角色需要協助", title: "\(t.id) \(t.title)",
                          summary: LineAPI.plainText(last), fields: [("角色", agent), ("優先度", t.priority.label)],
                          openIn: "AI 自動化 › 虛擬角色 › \(t.id)", color: "#BF8700")
    }

    // MARK: - Webhook

    func handleWebhook(_ req: HTTPRequest) -> HTTPResponse {
        app.line.webhookRequests += 1
        guard let secret = Self.channelSecret(),
              LineAPI.verify(body: req.body, signature: req.headers["x-line-signature"], secret: secret) else {
            app.line.rejectedSignatures += 1
            app.line.note(false, req.headers["x-line-signature"] == nil ? "webhook 缺少 X-Line-Signature,已拒絕"
                                                                       : "webhook 簽章驗證失敗,已拒絕")
            return HTTPResponse(status: 401, body: Data("Invalid signature".utf8), contentType: "text/plain")
        }
        guard let obj = try? JSONSerialization.jsonObject(with: req.body) as? [String: Any] else {
            return HTTPResponse(status: 400, body: Data("Bad JSON".utf8), contentType: "text/plain")
        }
        for case let ev as [String: Any] in obj["events"] as? [Any] ?? [] { process(ev) }
        return HTTPResponse(status: 200, body: Data("{}".utf8), contentType: "application/json")
    }

    private func process(_ ev: [String: Any]) {
        if let id = ev["webhookEventId"] as? String {
            if recentEvents.contains(id) { return }  // LINE 重送的同一個事件
            recentEvents.append(id)
            if recentEvents.count > 200 { recentEvents.removeFirst(recentEvents.count - 200) }
        }
        let source = ev["source"] as? [String: Any] ?? [:]
        let userId = source["userId"] as? String
        let groupId = (source["groupId"] as? String) ?? (source["roomId"] as? String)
        let replyToken = ev["replyToken"] as? String
        let type = ev["type"] as? String ?? ""
        let user = app.lineSettings.isAllowed(userId)

        switch type {
        case "message":
            let msg = ev["message"] as? [String: Any] ?? [:]
            let text = (msg["type"] as? String) == "text" ? (msg["text"] as? String ?? "") : ""
            guard let user else {
                rememberUnknown(userId: userId, groupId: groupId, text: text.isEmpty ? "(非文字訊息)" : text)
                reply(replyToken, Self.noPermission)
                return
            }
            guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                reply(replyToken, "目前只看得懂文字訊息。可以問「現在狀況如何」「有哪些告警」「待核准請求」。")
                return
            }
            let answer = LineAPI.plainText(AssistResponder(app: app).reply(to: text).text)
            app.line.note(true, "\(user.name.isEmpty ? user.userId : user.name) 在 LINE 問:\(text.prefix(60))")
            reply(replyToken, answer.isEmpty ? "目前沒有可以回答的資料。" : answer)
        case "postback":
            let data = (ev["postback"] as? [String: Any])?["data"] as? String ?? ""
            guard let user else {
                rememberUnknown(userId: userId, groupId: groupId, text: "(按了按鈕)")
                app.line.note(false, "不在白名單的 LINE 使用者按了核准按鈕,已拒絕")
                reply(replyToken, Self.noPermission)
                return
            }
            reply(replyToken, handlePostback(data, user: user))
        case "follow", "join":
            rememberUnknown(userId: userId, groupId: groupId, text: type == "follow" ? "(加入好友)" : "(加入群組)")
        default:
            break
        }
    }

    /// 處理核准 / 拒絕按鈕,回傳要回覆的文字
    func handlePostback(_ data: String, user: LineAllowedUser) -> String {
        let d = LineAPI.parseData(data)
        guard d["ops"] == "approval", let id = d["id"], let n = d["n"], let decision = d["d"], ["approve", "reject"].contains(decision) else {
            return "看不懂這個按鈕。"
        }
        guard var rec = state.nonces[n], rec.approvalId == id else { return "這個按鈕已失效,請到 Ops 裡決定。" }
        guard rec.usedAt == nil else { return "這個按鈕已經用過了。" }
        guard let r = app.approvals.first(where: { $0.id == id }) else { return "找不到這個請求。" }
        rec.usedAt = .now
        state.nonces[n] = rec
        saveState()
        guard r.state == .pending else {
            let who = r.decidedBy.map { app.person($0)?.name ?? $0 }
            return "「\(r.title)」已經是\(stateLabel(r.state))\(who.map { "(\($0))" } ?? ""),不用再決定。"
        }
        if r.expiresAt < .now { return "「\(r.title)」已過期,請到 Ops 裡處理。" }
        let approve = decision == "approve"
        let person = user.person.isEmpty ? nil : user.person
        app.decide(approvalId: id, approve: approve, by: person, via: "LINE" + (user.name.isEmpty ? "" : ",\(user.name)"))
        app.line.note(true, "\(user.name.isEmpty ? user.userId : user.name) 在 LINE \(approve ? "核准" : "拒絕")「\(r.title)」")
        return "已\(approve ? "核准" : "拒絕")「\(r.title)」,已記錄到 Ops 動態牆。"
    }

    private func stateLabel(_ s: ApprovalState) -> String {
        switch s {
        case .pending: return "待核准"
        case .approved: return "已核准"
        case .rejected: return "已拒絕"
        case .expired: return "已過期"
        }
    }

    private func rememberUnknown(userId: String?, groupId: String?, text: String) {
        for id in [userId, groupId].compactMap({ $0 }) where app.lineSettings.isAllowed(id) == nil {
            if let i = app.line.unknownSenders.firstIndex(where: { $0.userId == id }) {
                app.line.unknownSenders[i].lastText = String(text.prefix(80))
                app.line.unknownSenders[i].date = .now
            } else {
                app.line.unknownSenders.insert(.init(userId: id, lastText: String(text.prefix(80)), date: .now), at: 0)
                if app.line.unknownSenders.count > 20 { app.line.unknownSenders.removeLast() }
            }
        }
    }

    private func reply(_ replyToken: String?, _ text: String) {
        guard let replyToken, !replyToken.isEmpty else { return }
        guard let token = Self.token() else {
            app.line.note(false, "沒有 channel access token,無法回覆 LINE 訊息")
            return
        }
        inflight.append(Task { [weak self] in
            do {
                try await LineAPI.reply(token: token, replyToken: replyToken, messages: [LineAPI.text(text)])
            } catch {
                self?.app.line.note(false, "回覆 LINE 訊息失敗:\(error)")
            }
        })
    }
}
