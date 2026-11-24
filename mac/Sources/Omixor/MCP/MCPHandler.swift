import Foundation

/// MCP(Model Context Protocol)協定層:收 JSON-RPC 訊息、回傳回應,直接讀寫 AppState。
/// 傳輸(HTTP)在 MCPServer.swift;這裡不碰網路,方便測試。
///
/// 透過 MCP 做的變更都記為 `Actor.mcp`,會出現在動態牆與事件時間軸。
/// 依 app 的 AI 治理原則,核准 AI 請求、核准部署、回滾、改自主程度、切換整合
/// 這些決定只能由人在 app 裡做;MCP 用戶端要做這類變更時,用 request_approval 送出待核准請求。
@MainActor
final class MCPHandler {
    let app: AppState

    static let serverName = "omixor"
    static let serverVersion = "0.1.0"
    static let supportedVersions = ["2025-06-18", "2025-03-26", "2024-11-05"]

    init(app: AppState) { self.app = app }

    // MARK: - JSON-RPC

    /// 處理一則(或一批)JSON-RPC 訊息。回傳 nil 表示只有通知,不需要回應內容。
    func handle(_ data: Data) async -> Data? {
        guard let parsed = try? JSONSerialization.jsonObject(with: data) else {
            return MCPJSON.data(errorResponse(id: NSNull(), code: -32700, message: "Failed to parse JSON"))
        }
        if let batch = parsed as? [Any] {
            var responses: [[String: Any]] = []
            for m in batch { if let r = await handleMessage(m) { responses.append(r) } }
            return responses.isEmpty ? nil : MCPJSON.data(responses)
        }
        return await handleMessage(parsed).map(MCPJSON.data)
    }

    private func handleMessage(_ obj: Any) async -> [String: Any]? {
        guard let msg = obj as? [String: Any], let method = msg["method"] as? String else {
            // 用戶端送來的 response(我們不會發 request 給它)或格式錯誤
            if let msg = obj as? [String: Any], msg["result"] != nil || msg["error"] != nil { return nil }
            return errorResponse(id: NSNull(), code: -32600, message: "Not a valid JSON-RPC request")
        }
        guard let id = msg["id"] else { return nil }  // 通知:notifications/initialized 等,不回應
        let params = msg["params"] as? [String: Any] ?? [:]
        app.mcp.requests += 1
        app.mcp.lastRequestAt = .now

        switch method {
        case "initialize":
            let requested = params["protocolVersion"] as? String ?? ""
            let version = Self.supportedVersions.contains(requested) ? requested : Self.supportedVersions[0]
            if let info = params["clientInfo"] as? [String: Any], let name = info["name"] as? String {
                app.mcp.lastClient = name
            }
            // resources 能力只在 core server 有畫面資源(MCP Apps 的 ui://)時才宣告;initialize 最多等 2 秒(見 declaresResources)
            var caps: [String: Any] = ["tools": ["listChanged": false]]
            if await declaresResources() { caps["resources"] = ["listChanged": false, "subscribe": false] }
            return result(id: id, [
                "protocolVersion": version,
                "capabilities": caps,
                "serverInfo": ["name": Self.serverName, "title": "omixor", "version": Self.serverVersion],
                "instructions": Self.instructions,
            ])
        case "ping":
            return result(id: id, [:])
        case "tools/list":
            // 核心工具用 core server 的定義(只有核心一份,App 沒有自己的實作),App 自己的工具照舊;讀不到 server 時只列 App 自己的工具
            // 外部 MCP 閘道開著時,再加上組織設定裡開著的外部工具(<伺服器>__<工具>,見 MCP/Gateway)
            // 範例資料工具:目前組織對應的功能關了就不列(見 sampleToolFeatures)
            let external = app.isFeatureEnabled("mcpGateway") ? await app.mcpGateway.toolDefinitions(agent: nil) : []
            let local = Self.tools.filter { toolEnabled($0.name) }
            guard let (_, remote) = await serverTools(), !remote.isEmpty else {
                return result(id: id, ["tools": local.map(\.definition) + external])
            }
            let names = Set(remote.compactMap { $0["name"] as? String })
            return result(id: id, ["tools": local.filter { !names.contains($0.name) }.map(\.definition) + remote + external])
        case "tools/call":
            guard let raw = params["name"] as? String else {
                return errorResponse(id: id, code: -32602, message: "Missing tool name")
            }
            // 外部 MCP 閘道的工具(權限問 core、需要確認的先問人)
            if raw.contains("__"), app.isFeatureEnabled("mcpGateway") {
                await app.mcpGateway.refreshIfNeeded()
                if app.mcpGateway.isGatewayTool(raw) {
                    app.mcp.lastTool = raw
                    // 身分是本機 MCP token 代表的「這台 Mac 的使用者」(HTTP 層已驗證);clientInfo 是自報的,不拿來當身分
                    return result(id: id, await app.mcpGateway.call(raw, params["arguments"] as? [String: Any] ?? [:], caller: .member))
                }
            }
            // 以角色身分派任務:core 的人類端點不接受 agent(ISS-36,回 400),角色端點也沒有 agent_task_create;
            // 角色派工的路是依組織鏈委派(agent_task_delegate,App 自己的實作走 REST /agent-tasks/delegate),先告訴用戶端改用它
            // 同 core 的 RejectAgentArg:有給 agent 且(不是字串——含 null、數字——或去掉空白後非空)就擋;沒給、空字串、空白放行
            if Self.legacyToolName(raw) == "agent_task_create",
               let who = (params["arguments"] as? [String: Any])?["agent"],
               !((who as? String)?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? false) {
                app.mcp.lastTool = "agent_task_create"
                return result(id: id, MCPResult.error(message: Self.agentTaskCreateAsAgentError, status: 400))
            }
            if let proxied = await callOnServer(id: id, raw, params["arguments"] as? [String: Any] ?? [:]) { return proxied }
            let name = Self.legacyToolName(raw)
            // 核心工具一律由 core server 執行(App 沒有自己的實作):server 連不上,或連得上但工具清單沒有這個工具時回錯誤
            if Self.coreTools.contains(name) {
                app.mcp.lastTool = name
                let why: String
                if await serverTools() == nil {
                    why = "it is currently unreachable: " + (serverToolError ?? "could not read the core server tool list")
                } else {
                    why = "the core server tool list does not include this tool (the server may be older than the app; update the server)"
                }
                return result(id: id, MCPResult.error(code: "core_unavailable", message: "\(name) is handled by the omixor core server, but \(why)", status: 503))
            }
            guard Self.tools.contains(where: { $0.name == name }) else {
                return errorResponse(id: id, code: -32602, message: "Unknown tool: \(name)")
            }
            let args = MCPArgs(raw: params["arguments"] as? [String: Any] ?? [:])
            app.mcp.lastTool = name
            if !toolEnabled(name) {
                return result(id: id, MCPResult.error(code: "feature_disabled", message: featureOffMessage(name), status: 403))
            }
            do {
                return result(id: id, MCPResult.ok(try await call(name, args)))
            } catch {
                return result(id: id, MCPResult.error(error))
            }
        case "resources/list":
            // 轉給 core server(MCP Apps 的 ui:// 畫面);連不上或 server 沒有 resources(舊版)時回空清單
            return result(id: id, ["resources": await serverResources() ?? []])
        case "resources/read":
            guard let uri = params["uri"] as? String, !uri.isEmpty else {
                return errorResponse(id: id, code: -32602, message: "Missing resource uri")
            }
            return await readOnServer(id: id, uri: uri)
        case "prompts/list":
            return result(id: id, ["prompts": []])
        default:
            return errorResponse(id: id, code: -32601, message: "Unsupported method: \(method)")
        }
    }

    // MARK: - 轉給 omixor server

    /// agent_task_create 帶了 agent(以角色身分派任務)時的錯誤
    static let agentTaskCreateAsAgentError = "agent_task_create cannot take agent to create a task as a role (tasks are always attributed to the signed-in person; use assign_to to choose the role that receives the task). "
        + "To hand work from one role to another, use agent_task_delegate (agent = the delegating role, to = the receiving role; the org chain must have agent:<delegator> -> agent:<receiver>)"

    /// 連 server 時也留在 App 處理的工具:App 自己的組織清單(含本機與各伺服器)、版本
    static let keepLocal: Set<String> = ["orgs", "server"]
    /// 屬於 core 的工具(Issue、角色與任務、文件櫃、組織鏈、頻道、事件、繪圖):一律轉給 core server 的管理端點(遠端 omixor 或本機守護服務),
    /// App 沒有這些工具的 Swift 實作;server 連不上、或工具清單裡沒有時回錯誤。
    /// agent_task_delegate 不在這裡:core 的管理端點只開放給角色身分,App 代角色委派用自己的實作(走 REST /agent-tasks/delegate)
    static let coreTools: Set<String> = ["issues", "issue_create", "issue_update", "agents", "agent_context", "agent_tasks",
                                          "agent_task_claim", "agent_task_report", "agent_upsert", "agent_task_create",
                                          "cabinet", "docs", "doc_save", "cabinet_layout_save", "chain", "chain_save",
                                          // 專案(只分文件櫃;core/mcp/tools_cabinet.go)
                                          "projects", "project_save",
                                          "channels", "channel_create", "channel_messages", "channel_post",
                                          // 事件(存在 core;core/mcp/tools_incident.go)
                                          "incident_declare", "incident_update",
                                          // 繪圖工具(core/tools/wireframe、flowchart 的渲染器)
                                          "wireframe_render", "flowchart_render",
                                          // 待決定、子項、任務轉移、事件查詢(core/model/tools.go 的 CoreTools,Admin 的;orgs 是 keepLocal)
                                          "issue_decisions", "issue_decision_add", "issue_decision_answer", "issue_decision_to_issue",
                                          "issue_items", "issue_item_add", "issue_item_update", "issue_item_claim",
                                          "issue_item_report", "issue_item_promote", "agent_task_transfer", "incidents",
                                          // Issue 接受與放回(issue_update 旁的 Admin 工具)、資料庫結構檢核(core/model/tools.go 的 CoreTools)
                                          "issue_accept", "schema_check",
                                          // 回報 MCP 使用問題(core/mcp/tools_report.go;存成 mcp-feedback Issue)
                                          "report_problem",
                                          // commit 匯入與 Issue 連結(core/mcp/tools_commits.go)
                                          "commits", "commits_report", "issue_commit_link", "issue_commit_unlink"]
    // MARK: - 範例資料工具與功能開關

    /// 集合 → 所屬的範例功能(core/model/features.go 的 key)。沒列的(people、incidents、activity、integrations)不擋:
    /// 人員、動態牆與整合不是哪個範例分頁的資料,事件存在 core
    static let collectionFeatures: [String: String] = [
        "services": "infrastructure.services",
        "servers": "infrastructure.servers",
        "repos": "development.pulls",
        "pull_requests": "development.pulls",
        "work_items": "development.overview",
        "sprint": "development.overview",
        "pipeline_runs": "pipelines.runs",
        "deployments": "pipelines.deployments",
        "alerts": "monitoring.alerts",
        "alert_rules": "monitoring.rules",
        "logs": "monitoring.logs",
        "dashboards": "monitoring.dashboards",
        "on_call": "incidents.oncall",
        "automations": "automation.flows",
        "automation_runs": "automation.runs",
        "approvals": "automation.inbox",
    ]

    /// 所有範例功能(集合用到的)
    static var sampleFeatureKeys: [String] { Array(Set(collectionFeatures.values)).sorted() }

    /// App 的範例資料工具 → 需要的功能(任一個開著就能用)。沒列的工具不擋(navigate、activity_post、GCP、GitHub、旁白等;
    /// 核心工具 coreTools 由 server 處理,這裡也不擋)。
    /// overview、list、get 讀所有範例集合:任一個範例功能開著就列;list、get 再依集合擋(collectionFeatures)
    static let sampleToolFeatures: [String: [String]] = [
        "overview": sampleFeatureKeys,
        "list": sampleFeatureKeys,
        "get": sampleFeatureKeys,
        "alert_update": ["monitoring.alerts"],
        "alert_create": ["monitoring.alerts"],
        "work_item_create": ["development.overview"],
        "work_item_move": ["development.overview"],
        "pipeline_update": ["pipelines.runs"],
        "request_approval": ["automation.inbox"],
        "log_append": ["monitoring.logs"],
    ]

    /// 這個 App 工具在目前組織能不能用(依兩層功能開關的有效值)
    func toolEnabled(_ name: String) -> Bool {
        guard let keys = Self.sampleToolFeatures[name] else { return true }
        return keys.contains { app.isFeatureEnabled($0) }
    }

    /// 集合在目前組織能不能讀
    func collectionEnabled(_ collection: String) -> Bool {
        guard let key = Self.collectionFeatures[collection] else { return true }
        return app.isFeatureEnabled(key)
    }

    /// 功能關了時回給用戶端的錯誤文字(subject = 工具或集合名稱;keys = 對應的功能 key,只有一個時附上)
    private static func featureOffText(_ subject: String, keys: [String]) -> String {
        let which = keys.count == 1 ? " (\(keys[0]))" : ""
        return "\(subject): this feature is turned off\(which). The current organization has not enabled the matching sample feature; ask a person to turn it on in omixor Settings > Features (or the System page)"
    }

    /// 工具的功能關了時回給用戶端的錯誤
    func featureOffMessage(_ name: String) -> String {
        Self.featureOffText(name, keys: Self.sampleToolFeatures[name] ?? [])
    }

    /// 最近一次讀 server 工具清單的錯誤
    private var serverToolError: String?
    /// server 工具清單的快取秒數(讀不到時的快取較短,之後再試)
    static var serverToolTTL: TimeInterval = 60
    private var serverToolCache: (key: String, at: Date, defs: [[String: Any]]?)?

    /// core server 管理端點(/mcp/org/<組織>)的工具定義(Issue、角色、文件櫃、組織鏈、頻道);讀不到時回 nil
    /// (這時核心工具的呼叫回錯誤,見 coreTools)。請求在背景跑,不擋主執行緒
    func serverTools() async -> (IssueRemote, [[String: Any]])? {
        let r = app.startIssues().remote
        let org = app.currentOrgId
        let key = r.config.url.absoluteString + "|" + org
        if let c = serverToolCache, c.key == key, Date().timeIntervalSince(c.at) < (c.defs == nil ? 15 : Self.serverToolTTL) {
            return c.defs.map { (r, $0) }
        }
        do {
            let res = try await r.run { try $0.mcp(org: org, method: "tools/list") }
            let defs = (res["tools"] as? [[String: Any]] ?? []).filter { !Self.keepLocal.contains($0["name"] as? String ?? "") }
            serverToolCache = (key, .now, defs)
            serverToolError = nil
            return (r, defs)
        } catch {
            NSLog("MCP:讀不到 omixor server 的工具清單:%@", error.localizedDescription)
            serverToolError = error.localizedDescription
            serverToolCache = (key, .now, nil)
            return nil
        }
    }

    /// server 資源清單(resources/list)的快取:TTL 同工具清單,讀不到(連不上、舊版 server 沒有 resources)時短快取
    private var serverResourceCache: (key: String, at: Date, list: [[String: Any]]?)?
    /// initialize 最多等 server 幾秒才決定要不要宣告 resources
    static var initializeResourceWait: TimeInterval = 2

    /// core server 的資源清單(ui:// 畫面);讀不到或是空的回 nil
    func serverResources() async -> [[String: Any]]? {
        let r = app.startIssues().remote
        let org = app.currentOrgId
        let key = r.config.url.absoluteString + "|" + org
        if let c = serverResourceCache, c.key == key, Date().timeIntervalSince(c.at) < (c.list == nil ? 15 : Self.serverToolTTL) {
            return c.list
        }
        do {
            let res = try await r.run { try $0.mcp(org: org, method: "resources/list") }
            let list = (res["resources"] as? [[String: Any]]).flatMap { $0.isEmpty ? nil : $0 }
            serverResourceCache = (key, .now, list)
            return list
        } catch {
            NSLog("MCP:讀不到 omixor server 的資源清單:%@", error.localizedDescription)
            serverResourceCache = (key, .now, nil)
            return nil
        }
    }

    private final class Once { var done = false }

    /// initialize 要不要宣告 resources:server 的 resources/list 非空才宣告(舊版 server 沒有這個方法或回空,就不宣告)。
    /// 有快取就直接用;沒有就現查,但最多等 initializeResourceWait 秒——逾時視為沒有(不拖慢 initialize),
    /// 背景的查詢繼續跑並寫進快取,用戶端之後重新 initialize 就會看到
    private func declaresResources() async -> Bool {
        let wait = Self.initializeResourceWait
        return await withCheckedContinuation { (c: CheckedContinuation<Bool, Never>) in
            let once = Once()
            Task { @MainActor in
                let has = await self.serverResources() != nil
                if !once.done { once.done = true; c.resume(returning: has) }
            }
            Task { @MainActor in
                try? await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000))
                if !once.done { once.done = true; c.resume(returning: false) }
            }
        }
    }

    /// resources/read 轉給 server,uri 原樣帶過去,回傳 server 的結果;失敗回「找不到資源」(-32002)
    private func readOnServer(id: Any, uri: String) async -> [String: Any] {
        let r = app.startIssues().remote
        let org = app.currentOrgId
        do {
            let res = try await r.run { try $0.mcp(org: org, method: "resources/read", params: ["uri": uri]) }
            nonisolated(unsafe) let out = res
            return result(id: id, out)
        } catch {
            return errorResponse(id: id, code: -32002, message: "Resource not found: \(uri) (\(error.localizedDescription))")
        }
    }

    /// 核心工具轉給 server 執行,回傳 server 的結果(nil = 不是 server 的工具,由 App 處理)。
    /// 轉送的請求在背景跑,主執行緒只更新結果
    private func callOnServer(id: Any, _ raw: String, _ args: [String: Any]) async -> [String: Any]? {
        guard let (r, remote) = await serverTools(), !remote.isEmpty else { return nil }
        let names = Set(remote.compactMap { $0["name"] as? String })
        var name = raw
        if !names.contains(name), name.hasPrefix("ops_"), names.contains(String(name.dropFirst(4))) { name = String(name.dropFirst(4)) }
        guard names.contains(name) else { return nil }
        app.mcp.lastTool = name
        let org = app.currentOrgId
        nonisolated(unsafe) let sendArgs = args
        do {
            let res = try await r.run { try $0.mcp(org: org, method: "tools/call", params: ["name": name, "arguments": sendArgs]) }
            await refreshAfterServerTool(name, savedDoc: args["save"] != nil && !(args["save"] is NSNull))
            return result(id: id, res)
        } catch {
            // 沒連上或 server 不是回 tools/call 結果(HTTP 錯誤、JSON-RPC 錯誤):有 HTTP 狀態就用它,沒有是 502
            let e = error as? IssueRemoteError
            let status = (e?.status ?? 0) > 0 ? e!.status : 502
            return result(id: id, MCPResult.error(code: status == 502 ? "upstream_unavailable" : nil, message: error.localizedDescription, status: status))
        }
    }

    /// server 上改了資料:馬上重讀對應的畫面資料(不用等定時更新;請求在背景跑,回應前等它更新好)
    /// savedDoc:繪圖工具(wireframe_render、flowchart_render)帶了 save,會透過 doc_save 改文件櫃
    private func refreshAfterServerTool(_ name: String, savedDoc: Bool = false) async {
        if name.hasPrefix("incident_") { await app.startIncidents().reload() }   // incident_declare、incident_update
        if name.hasSuffix("_render"), savedDoc {
            if !app.cabinetLoaded { Cabinet.begin(app) }
            await Cabinet.reload(app)
            return
        }
        // channel_messages 也會標已讀
        let writes = ["create", "update", "save", "report", "claim", "upsert", "delegate", "post", "channel_messages"]
        guard writes.contains(where: { name.contains($0) }) else { return }
        if name.hasPrefix("issue") { await app.startIssues().reload() }
        if name.hasPrefix("agent") || name.hasPrefix("chain") { await app.startAgents().reload() }
        if name.hasPrefix("channel") { await app.startChannels().reload() }
        // 文件櫃與專案(project_save 也會改專案清單;cabinet_layout_save 改結構)
        if name == "doc_save" || name == "cabinet_layout_save" || name == "project_save" {
            if !app.cabinetLoaded { Cabinet.begin(app) }
            await Cabinet.reload(app)
        }
    }

    private func result(id: Any, _ r: [String: Any]) -> [String: Any] {
        ["jsonrpc": "2.0", "id": id, "result": r]
    }

    private func errorResponse(id: Any, code: Int, message: String) -> [String: Any] {
        ["jsonrpc": "2.0", "id": id, "error": ["code": code, "message": message]]
    }

    static let instructions = """
    omixor is a macOS app for operating live services. These tools read and write the live data in the app directly; changes show up on screen immediately and are recorded on the activity feed as "MCP client".
    Start with overview to get the big picture, then use list / get for details.
    Approving AI requests, approving deployments, rollbacks, and changing automation autonomy can only be decided by a person in the app; when you need such a change, create an approval request with request_approval and attach the rationale, evidence, and rollback plan.
    GCP (gcp, gcp_logs, gcp_metrics) is read-only: check Cloud Run status, error logs, request volume, and latency; omixor does not deploy, change traffic, or run Jobs.
    GitHub (github, github_runs, github_run, github_pull) is read-only: check PRs, checks, and why Actions failed; omixor does not open PRs, comment, or re-run workflows.
    Issues, virtual roles and tasks, the document cabinet, and connections all belong to the current organization (orgs shows which one); switching organizations can only be done by a person in the app.
    omixor has virtual roles (agents). When the user asks you to work as a role: first use agent_context to get what that role can read, then claim a task with agent_task_claim, follow the returned role settings and task instructions, and report progress and results with agent_task_report; report blocked when a person needs to decide. To write Issues, comments, documents, or channel messages as a role, you must connect to the role endpoint (/mcp/agent) with the role credential returned by agent_task_claim; on this general omixor connection, issue_create, issue_update, and the cabinet and channel tools cannot take an agent argument to sign as a role (it is rejected), and the author is always the signed-in account.
    The document cabinet (cabinet, docs, doc_save) is organized by the current organization's cabinet structure (sections and tabs); a role can only read and write documents within its docAccess scope (decided by the credential on the role endpoint). An organization can be split into projects (projects, project_save); cabinet tools take project (a project id) to read and write that project's cabinet; omitted = no project.
    When you find a problem worth tracking (for example an anomaly in the site_visitors numbers), save it as an Issue with issue_create; search with issues first to avoid duplicates, and if one exists, add a comment with issue_update.
    Narration voice (npm run voice in the shuttle project) can be dispatched as a batch with voice_submit: omixor polls periodically and collects the results, then a person auditions them in the app and presses publish. There is no MCP tool for publishing.
    Message channels (channels, channel_messages, channel_post, channel_create) bring people and roles into one conversation; @-mentioning a role only notifies it and does not queue a task. When working as a role, the mentions field of agent_context lists unread messages that mention it; reading (channel_messages) and replying (channel_post) as a role must be done on the role endpoint.
    Facebook Pages (fb_pages, fb_posts, fb_insights, fb_drafts) are read-only; fb_draft_save can only create or edit drafts (pass agent when writing as a role); publishing and scheduling can only be done by a person in the app under AI Automation > Pages.
    """

    // MARK: - 工具定義

    struct Tool {
        let name: String
        let title: String
        let description: String
        let properties: [String: Any]
        let required: [String]
        var readOnly = false

        var definition: [String: Any] {
            [
                "name": name,
                "title": title,
                "description": description,
                "inputSchema": ["type": "object", "properties": properties, "required": required, "additionalProperties": false],
                "annotations": ["title": title, "readOnlyHint": readOnly, "destructiveHint": false, "openWorldHint": false],
            ]
        }
    }

    static func prop(_ type: String, _ description: String, enum values: [String]? = nil) -> [String: Any] {
        var p: [String: Any] = ["type": type, "description": description]
        if let values { p["enum"] = values }
        return p
    }

    static func strings(_ description: String) -> [String: Any] {
        ["type": "array", "items": ["type": "string"], "description": description]
    }

    static func cases<E: RawRepresentable & CaseIterable>(_: E.Type) -> [String] where E.RawValue == String {
        E.allCases.map(\.rawValue)
    }

    static let collections = [
        "people", "services", "servers", "repos", "pull_requests", "work_items", "sprint",
        "pipeline_runs", "deployments", "alerts", "alert_rules", "logs", "dashboards",
        "incidents", "on_call", "automations", "automation_runs", "approvals", "activity", "integrations",
    ]

    /// 舊的工具名稱(ops_ 開頭)換成現在的名稱:工具改名後過渡用,tools/list 只列新名稱,舊名稱還能呼叫(同核心的 LegacyToolName)。
    /// 核心工具(coreTools)也換,讓 server 連不上時的錯誤訊息用現在的名稱
    static func legacyToolName(_ name: String) -> String {
        func known(_ n: String) -> Bool { tools.contains(where: { $0.name == n }) || coreTools.contains(n) }
        guard name.hasPrefix("ops_"), !known(name) else { return name }
        let n = String(name.dropFirst(4))
        return known(n) ? n : name
    }

    static let tools: [Tool] = [
        Tool(name: "overview", title: "Overview",
             description: "Current screen state, service health, firing alerts, open incidents, running CI/CD and deployments, AI requests awaiting approval, on-call staff, and recent activity.",
             properties: [:], required: [], readOnly: true),
        Tool(name: "list", title: "List records",
             description: "List one data collection. where filters by field (fields can use an a.b path; an array field matches if it contains the value; actor fields match by id); search does a case-insensitive text search over the whole record. Time series and logs are truncated; use get for the full content.",
             properties: [
                 "collection": prop("string", "Data collection", enum: collections),
                 "where": ["type": "object", "description": "field = value, for example {\"state\":\"firing\",\"service\":\"payments\"}", "additionalProperties": true],
                 "search": prop("string", "Text search"),
                 "limit": prop("integer", "Maximum number of records, default 30, max 200"),
                 "offset": prop("integer", "Number of records to skip"),
             ], required: ["collection"], readOnly: true),
        Tool(name: "get", title: "Get a record",
             description: "Get one full record by id (including time series, work log, and incident timeline).",
             properties: [
                 "collection": prop("string", "Data collection", enum: collections.filter { $0 != "sprint" }),
                 "id": prop("string", "id, for example ALT-5121, INC-2041, OPS-142; PRs and pipeline runs use their number"),
             ], required: ["collection", "id"], readOnly: true),
        Tool(name: "navigate", title: "Navigate",
             description: "Switch the app to a page, optionally selecting an item (for example an alert or incident id). Can also change the environment and time range.",
             properties: [
                 "section": prop("string", "Page", enum: cases(AppSection.self)),
                 "focus_id": prop("string", "Id of the item to select"),
                 "env": prop("string", "Environment filter", enum: cases(DeployEnv.self)),
                 "time_range": prop("string", "Time range", enum: cases(TimeRange.self)),
             ], required: []),
        Tool(name: "alert_update", title: "Handle an alert",
             description: "Acknowledge, resolve, or silence an alert, optionally with an AI assessment.",
             properties: [
                 "alert_id": prop("string", "Alert id"),
                 "action": prop("string", "Action", enum: ["acknowledge", "resolve", "silence"]),
                 "ai_note": prop("string", "AI assessment to attach to the alert (optional)"),
             ], required: ["alert_id"]),
        Tool(name: "alert_create", title: "Create an alert",
             description: "Push a firing alert in from external monitoring.",
             properties: [
                 "title": prop("string", "Title"),
                 "severity": prop("string", "Severity", enum: cases(Severity.self)),
                 "service": prop("string", "Service id"),
                 "rule": prop("string", "Trigger condition, for example p95 latency > 800ms for 5m"),
                 "value": prop("string", "Current value"),
                 "ai_note": prop("string", "Initial AI assessment"),
             ], required: ["title", "severity", "service", "rule", "value"]),
        Tool(name: "work_item_create", title: "Create a work item",
             description: "Create a work item on the board; it can be attached to an incident as follow-up work.",
             properties: [
                 "title": prop("string", "Title"),
                 "type": prop("string", "Type", enum: cases(WorkType.self)),
                 "status": prop("string", "Column, default todo", enum: cases(WorkStatus.self)),
                 "points": prop("integer", "Story points, default 3"),
                 "service": prop("string", "Service id"),
                 "assignee": prop("string", "Assignee person id"),
                 "incident_id": prop("string", "Add as follow-up work to this incident"),
             ], required: ["title", "type"]),
        Tool(name: "work_item_move", title: "Move a work item",
             description: "Move a work item to another board column.",
             properties: [
                 "id": prop("string", "Work item id, for example OPS-142"),
                 "status": prop("string", "Target column", enum: cases(WorkStatus.self)),
             ], required: ["id", "status"]),
        Tool(name: "pipeline_update", title: "Control a CI/CD run",
             description: "Retry or cancel a pipeline run, or write an AI failure diagnosis.",
             properties: [
                 "run_id": prop("integer", "Run number"),
                 "action": prop("string", "Action", enum: ["retry", "cancel"]),
                 "ai_diagnosis": prop("string", "AI failure diagnosis to write"),
             ], required: ["run_id"]),
        Tool(name: "request_approval", title: "Submit an approval request",
             description: "Create an AI request that waits for human approval (shown under AI Automation > Pending approval and Overview). Use it for any change that needs a human decision: scaling, rollback, database operations, and so on. Returns the request id; check the outcome later with get approvals.",
             properties: [
                 "title": prop("string", "Title"),
                 "action": prop("string", "Command or change to execute"),
                 "rationale": prop("string", "Rationale"),
                 "evidence": strings("Supporting evidence"),
                 "rollback_plan": prop("string", "Rollback plan"),
                 "risk": prop("string", "Risk", enum: cases(RiskLevel.self)),
                 "automation": prop("string", "Id of the automation it belongs to, default mcp"),
                 "expires_in_minutes": prop("integer", "Minutes until it expires, default 60"),
             ], required: ["title", "action", "rationale", "rollback_plan", "risk"]),
        Tool(name: "log_append", title: "Append a log entry",
             description: "Write a log entry to Monitoring > Logs.",
             properties: [
                 "service": prop("string", "Service id"),
                 "level": prop("string", "Level", enum: cases(LogLevel.self)),
                 "message": prop("string", "Content"),
                 "host": prop("string", "Host, default mcp"),
                 "trace_id": prop("string", "trace id"),
             ], required: ["service", "level", "message"]),
        Tool(name: "activity_post", title: "Post to the activity feed",
             description: "Post an entry to the Overview activity feed, attributed to \"MCP client\".",
             properties: [
                 "text": prop("string", "Content"),
                 "section": prop("string", "Page to open when clicked, default overview", enum: cases(AppSection.self)),
                 "icon": prop("string", "SF Symbol name, default sparkles"),
             ], required: ["text"]),
        Tool(name: "voice_submit", title: "Dispatch a narration voice batch",
             description: "Hand the shuttle project narration voice generation to omixor: runs npm run voice -- <script> --batch (Gemini Batch API, about half price, up to 24 hours). Returns a job id immediately, no need to wait; omixor polls every few minutes and collects the results automatically (download, convert to mp3, upload to GCS, rewrite the local .voice.json), then a person auditions them in omixor and presses publish. Only one batch runs at a time; the rest are queued. dry_run only lists which segments would be sent, without calling the API or spending money.",
             properties: [
                 "scripts": strings("Script paths under web/src/content/live/, for example launch/2.1.json"),
                 "all": prop("boolean", "All scripts"),
                 "limit": prop("integer", "Send only the first N segments of each script (for trials)"),
                 "dry_run": prop("boolean", "Dry run only, do not submit"),
             ], required: []),
        Tool(name: "voice_jobs", title: "Check narration batches",
             description: "Check the status of narration voice batches (queued, generating, awaiting publish, published, failed, ...), the list of changed narrations, and the log. Without id, lists the most recent batches.",
             properties: ["id": prop("string", "Job id, for example VJ-3")], required: [], readOnly: true),
        Tool(name: "site_visitors", title: "Site visitor statistics",
             description: "Read the evoleap site member and visitor statistics (the same data as the admin Members page, GET /api/admin/overview): member count, visitor count, visitor funnel (opened -> loaded -> played; like the admin page it excludes the login gate and ordinary pages), per-course summary, login gate, device / source / load errors, paths entered from the home page, home page dwell time, login page, and recent visit trails without playback. Requires omixor to be signed in to that connection.",
             properties: [
                 "range": prop("string", "Time range, default 1d", enum: ["all", "30m", "12h", "1d", "7d"]),
                 "connection": prop("string", "Connection id; default is the one selected on the omixor site visitors page (evoleap)"),
             ], required: [], readOnly: true),
        Tool(name: "server", title: "Server and connection info",
             description: "Current connection info and versions: mode (local / remote), remote server name (without URL), sign-in state (account, role, token expiry) and the current organization, plus the omixor app and remote server versions (version, commit, buildTime). Never shows any secret.",
             properties: [:], required: [], readOnly: true),
        Tool(name: "orgs", title: "Organizations",
             description: "List the omixor organizations and the one currently in use. Issues, virtual roles and tasks, the document cabinet, and connections all belong to the current organization; switching organizations can only be done by a person in the app.",
             properties: [:], required: [], readOnly: true),
        Tool(name: "agent_task_delegate", title: "Delegate a task along the org chain",
             description: "As a role, delegate work to a downstream role on the org chain (the chain must have agent:<yourself> -> agent:<to>). The task is attributed to your role; links carries the task that delegated it and its links; a label (goal, task, ...) goes at the start of the task instructions; when the other side finishes it reports back to you automatically (a \"review the report\" task is queued for you). See chain.downstream in agent_context for who you can delegate to.",
             properties: [
                 "agent": prop("string", "Id or name of the role you are acting as (the delegator)"),
                 "to": prop("string", "Id or name of the role receiving the task (must be downstream on the chain)"),
                 "title": prop("string", "Title"),
                 "instructions": prop("string", "Task instructions (Markdown)"),
                 "priority": prop("string", "Priority, default normal", enum: cases(IssuePriority.self)),
                 "task": prop("string", "Id of the source task that delegates it (optional; defaults to your role's task in progress)"),
             ], required: ["agent", "to", "title"]),
        Tool(name: "github", title: "GitHub overview",
             description: "List the GitHub repos omixor tracks (for the current organization), open PRs (with check results), and recent Actions runs. The data is what omixor last read (refreshed about every 5 minutes); refresh=true re-reads first. Read-only. A person must first sign in to GitHub in omixor Settings > Repositories.",
             properties: [
                 "refresh": prop("boolean", "Re-read from GitHub first"),
                 "repo": prop("string", "Only this repo (owner/name)"),
                 "limit": prop("integer", "Maximum runs per repo, default 10"),
             ], required: [], readOnly: true),
        Tool(name: "github_runs", title: "List GitHub Actions runs",
             description: "Query a repo's Actions run history directly from GitHub, filterable by branch, status, and workflow. Read-only.",
             properties: [
                 "repo": prop("string", "owner/name; may be omitted when only one repo is tracked"),
                 "branch": prop("string", "Branch"),
                 "status": prop("string", "Status filter", enum: ["failure", "success", "in_progress", "queued", "completed", "cancelled"]),
                 "limit": prop("integer", "Number of runs, default 20, max 100"),
             ], required: [], readOnly: true),
        Tool(name: "github_run", title: "GitHub Actions run details",
             description: "The jobs and steps of one Actions run; with log=true, includes the last lines of failed job logs (to find the failure cause). Read-only.",
             properties: [
                 "id": prop("integer", "Run id (the id from github / github_runs)"),
                 "repo": prop("string", "owner/name; may be omitted when only one repo is tracked"),
                 "log": prop("boolean", "Include the last lines of failed job logs"),
                 "lines": prop("integer", "Number of log lines, default 80, max 400"),
             ], required: ["id"], readOnly: true),
        Tool(name: "github_pull", title: "GitHub PR details",
             description: "A PR's description, branches, check results, and list of changed files. Read-only; to change code, follow the engineer role workflow: work on a new branch and wait for a person to approve.",
             properties: [
                 "number": prop("integer", "PR number"),
                 "repo": prop("string", "owner/name; may be omitted when only one repo is tracked"),
             ], required: ["number"], readOnly: true),
        Tool(name: "gcp", title: "GCP overview",
             description: "List the status, current revision, traffic, and URL of Cloud Run services in the GCP project omixor is configured with (for the current organization), plus the latest execution of each Cloud Run Job. With service, also returns its recent revisions; with job, returns its recent executions. Read-only. A person must first sign in under omixor Servers and Services > GCP.",
             properties: [
                 "refresh": prop("boolean", "Re-read first"),
                 "service": prop("string", "Cloud Run service name, for example shuttle-server"),
                 "job": prop("string", "Cloud Run Job name, for example shuttle-course-sync"),
             ], required: [], readOnly: true),
        Tool(name: "gcp_logs", title: "Query GCP logs",
             description: "Read Cloud Run service or Job logs from Cloud Logging (newest first). By default only ERROR and above from the last 24 hours. Read-only.",
             properties: [
                 "service": prop("string", "Cloud Run service name"),
                 "job": prop("string", "Cloud Run Job name (choose one of this and service; with neither, all of Cloud Run)"),
                 "severity": prop("string", "Minimum severity, default ERROR", enum: GCP.severities),
                 "hours": prop("integer", "Hours to look back, default 24, max 720"),
                 "contains": prop("string", "Text the log content must contain"),
                 "limit": prop("integer", "Number of entries, default 50, max 500"),
             ], required: [], readOnly: true),
        Tool(name: "gcp_metrics", title: "GCP service metrics",
             description: "Read a Cloud Run service's request count (by 2xx/4xx/5xx), p95 latency, and instance count from Cloud Monitoring, with totals and time series. Read-only.",
             properties: [
                 "service": prop("string", "Cloud Run service name; may be omitted when there is only one service"),
                 "hours": prop("integer", "Hours to look back, default 24, max 720"),
             ], required: [], readOnly: true),
        Tool(name: "fb_pages", title: "Facebook Pages",
             description: "List the Facebook Pages omixor is connected to: id, name, category, URL, like count, follower count, whether it is managed in omixor, and whether a token exists. Read-only. A person must first set up the connection in omixor AI Automation > Pages.",
             properties: ["refresh": prop("boolean", "Re-read the Page data from Facebook first")], required: [], readOnly: true),
        Tool(name: "fb_posts", title: "Facebook posts and performance",
             description: "Recent posts (newest first) and scheduled posts of the managed Pages. Each post includes reactions, comments, shares, and total engagement; with insights=true, adds reach, clicks, and reaction breakdown for the first 10 posts (metrics that Meta has deprecated or cannot return have status deprecated / unavailable). Read-only.",
             properties: [
                 "page": prop("string", "Page id or name; omitted means all managed Pages"),
                 "limit": prop("integer", "Maximum recent posts per Page, default 10, max 50"),
                 "insights": prop("boolean", "Include per-post performance metrics, default true"),
                 "scheduled": prop("boolean", "Include scheduled posts, default true"),
             ], required: [], readOnly: true),
        Tool(name: "fb_insights", title: "Facebook Page insights",
             description: "Page performance: reach, views, post engagement, followers, and new followers, with period totals and a time series per period. Metrics that Meta has deprecated or cannot return have status deprecated / unavailable and a null total. Read-only.",
             properties: [
                 "page": prop("string", "Page id or name; omitted means all managed Pages"),
                 "period": prop("string", "Period of each data point, default day", enum: FB.periods),
                 "days": prop("integer", "Days to look back, default 28, 1-90"),
             ], required: [], readOnly: true),
        Tool(name: "fb_drafts", title: "Facebook drafts",
             description: "With id, returns one post draft; without it, lists drafts (by default only unpublished ones, newest first). Read-only.",
             properties: [
                 "id": prop("string", "Draft id, for example FD-3"),
                 "status": prop("string", "draft, scheduled, published, or all; default draft", enum: ["draft", "scheduled", "published", "all"]),
                 "page": prop("string", "Only drafts of this Page (id or name)"),
                 "limit": prop("integer", "Maximum number of records, default 30"),
             ], required: [], readOnly: true),
        Tool(name: "fb_draft_save", title: "Save a Facebook draft",
             description: "Create or edit a Facebook post draft (without id it creates one). Only saves drafts and never publishes: publishing and scheduling can only be done by a person in omixor AI Automation > Pages. Scheduled or published drafts cannot be edited. When working as a role, pass agent and the draft is attributed to that role.",
             properties: [
                 "id": prop("string", "Id of the draft to edit; omitted creates a new one"),
                 "page": prop("string", "Which Page to post to (id or name); when creating, defaults to the first managed Page"),
                 "message": prop("string", "Post content (plain text)"),
                 "link": prop("string", "Link to attach (http or https)"),
                 "image_url": prop("string", "Image URL (http or https; when there is an image, the link is appended after the text)"),
                 "proposed_time": prop("string", "Suggested publish time (ISO 8601, for example 2026-11-20T20:00:00+08:00); only a suggestion, a person decides the actual schedule"),
                 "source": prop("string", "Where the draft came from, for example doc:DOC-3, task:T-5"),
                 "agent": prop("string", "When working as a virtual role, the role id or name; the draft is attributed to that role"),
             ], required: []),
    ]

    // MARK: - 工具實作

    private func call(_ name: String, _ a: MCPArgs) async throws -> Any {
        switch name {
        case "overview": return overview()
        case "list": return try list(a)
        case "get": return try get(a)
        case "navigate": return try navigate(a)
        case "alert_update": return try alertUpdate(a)
        case "alert_create": return try alertCreate(a)
        case "work_item_create": return try workItemCreate(a)
        case "work_item_move": return try workItemMove(a)
        case "pipeline_update": return try pipelineUpdate(a)
        case "request_approval": return try requestApproval(a)
        case "log_append": return try logAppend(a)
        case "activity_post": return try activityPost(a)
        case "voice_submit": return try await voiceSubmit(a)
        case "voice_jobs": return try voiceJobs(a)
        case "site_visitors": return try await siteVisitors(a)
        case "agent_task_delegate": return try agentDelegate(a)
        case "orgs": return orgsInfo()
        case "server": return serverInfo()
        case "github": return try await githubSummary(a)
        case "github_runs": return try await githubRuns(a)
        case "github_run": return try await githubRun(a)
        case "github_pull": return try await githubPull(a)
        case "gcp": return try await gcpSummary(a)
        case "gcp_logs": return try await gcpLogs(a)
        case "gcp_metrics": return try await gcpMetrics(a)
        case "fb_pages": return try await fbPages(a)
        case "fb_posts": return try await fbPosts(a)
        case "fb_insights": return try await fbInsights(a)
        case "fb_drafts": return try fbDrafts(a)
        case "fb_draft_save": return try fbDraftSave(a)
        default: throw MCPToolError("Unknown tool: \(name)")
        }
    }

    private func items(_ collection: String) throws -> [Any] {
        if Self.collections.contains(collection), !collectionEnabled(collection) {
            let key = Self.collectionFeatures[collection] ?? ""
            throw MCPToolError(Self.featureOffText(collection, keys: [key]))
        }
        let o: Any
        switch collection {
        case "people": o = MCPJSON.object(app.people)
        case "services": o = MCPJSON.object(app.services)
        case "servers": o = MCPJSON.object(app.servers)
        case "repos": o = MCPJSON.object(app.repos)
        case "pull_requests": o = MCPJSON.object(app.pullRequests)
        case "work_items": o = MCPJSON.object(app.workItems)
        case "sprint": o = app.sprint.isEmpty ? [[String: Any]()] as [Any] : [MCPJSON.object(app.sprint)] as [Any]
        case "pipeline_runs": o = MCPJSON.object(app.pipelineRuns)
        case "deployments": o = MCPJSON.object(app.deployments)
        case "alerts": o = MCPJSON.object(app.alerts)
        case "alert_rules": o = MCPJSON.object(app.alertRules)
        case "logs": o = MCPJSON.object(app.logs)
        case "dashboards": o = MCPJSON.object(app.dashboards)
        case "incidents": o = MCPJSON.object(app.incidents)
        case "on_call": o = MCPJSON.object(app.onCall)
        case "automations": o = MCPJSON.object(app.automations)
        case "automation_runs": o = MCPJSON.object(app.automationRuns)
        case "approvals": o = MCPJSON.object(app.approvals)
        case "activity": o = MCPJSON.object(app.activity)
        case "integrations": o = MCPJSON.object(app.integrations)
        default: throw MCPToolError("Unknown collection: \(collection). Available: \(Self.collections.joined(separator: ", "))")
        }
        return o as? [Any] ?? []
    }

    private func overview() -> [String: Any] {
        let now = Date()
        return [
            "now": MCPJSON.iso(now),
            "ui": ["section": app.section.rawValue, "env": app.env.rawValue, "timeRange": app.timeRange.rawValue,
                   "focusId": app.focusId as Any? ?? NSNull()],
            "counts": [
                "services": app.services.count,
                "unhealthyServices": app.services.filter { $0.health != .healthy }.count,
                "firingAlerts": app.firingAlerts.count,
                "openIncidents": app.openIncidents.count,
                "pendingApprovals": app.pendingApprovals.count,
                "runningPipelines": app.runningPipelines.count,
                "failedPipelines24h": app.pipelineRuns.filter { $0.status == .failed && now.timeIntervalSince($0.startedAt) < 86400 }.count,
            ],
            "services": app.services.map {
                pick(MCPJSON.object($0), ["id", "name", "health", "tier", "errorRate", "latencyP95", "rps", "errorBudgetRemaining"])
            },
            "firingAlerts": app.firingAlerts.map {
                pick(MCPJSON.object($0), ["id", "title", "severity", "service", "value", "startedAt", "incidentId"])
            },
            "openIncidents": app.openIncidents.map {
                pick(MCPJSON.object($0), ["id", "title", "sev", "status", "services", "commander", "startedAt"])
            },
            "pendingApprovals": app.pendingApprovals.map {
                pick(MCPJSON.object($0), ["id", "title", "risk", "automation", "action", "expiresAt"])
            },
            "runningPipelines": app.runningPipelines.map {
                pick(MCPJSON.object($0), ["id", "pipeline", "branch", "status", "startedAt"])
            },
            "activeDeployments": app.deployments.filter { $0.status == .running || $0.status == .waitingApproval }.map {
                pick(MCPJSON.object($0), ["id", "service", "version", "env", "status", "strategy", "progress"])
            },
            "onCall": app.onCall.filter { $0.start <= now && $0.end > now }.map { s in
                ["role": s.role, "person": s.person, "name": app.person(s.person)?.name ?? s.person, "until": MCPJSON.iso(s.end)]
            },
            "recentActivity": app.activity.prefix(10).map { item in
                ["date": MCPJSON.iso(item.date), "by": app.name(of: item.actor), "text": item.text, "section": item.section.rawValue]
            },
        ]
    }

    private func list(_ a: MCPArgs) throws -> Any {
        let collection = try a.require("collection")
        var rows = try items(collection)
        if let conds = a.raw["where"] as? [String: Any] {
            rows = rows.filter { row in conds.allSatisfy { k, v in matches(value(at: k, in: row), v) } }
        }
        if let q = a.string("search")?.lowercased(), !q.isEmpty {
            rows = rows.filter { MCPJSON.string($0).lowercased().contains(q) }
        }
        let total = rows.count
        let offset = max(0, a.int("offset") ?? 0)
        let limit = min(200, max(1, a.int("limit") ?? 30))
        let page = Array(rows.dropFirst(offset).prefix(limit)).map(compact)
        return ["collection": collection, "total": total, "offset": offset, "count": page.count, "items": page]
    }

    private func get(_ a: MCPArgs) throws -> Any {
        let collection = try a.require("collection")
        let id = try a.require("id")
        let rows = try items(collection)
        guard let row = rows.first(where: { r in
            (value(at: "id", in: r)).flatMap(scalarString).map { $0.caseInsensitiveCompare(id) == .orderedSame } ?? false
        }) else { throw MCPToolError("No record with id \"\(id)\" in \(collection)") }
        return row
    }

    private func navigate(_ a: MCPArgs) throws -> Any {
        if let env = try a.optionalEnum("env", DeployEnv.self) { app.env = env }
        if let range = try a.optionalEnum("time_range", TimeRange.self) { app.timeRange = range }
        var notice: String?
        if let section = try a.optionalEnum("section", AppSection.self) {
            // 關掉的頁面(系統層沒開放或組織層關了):改去總覽(回傳 notice 說明)
            if !app.go(section, focus: a.string("focus_id")) { notice = "The \(section.rawValue) page is turned off (by the system or in the organization's Settings > Features), so the app went to Overview instead" }
        } else if let focus = a.string("focus_id") {
            app.focusId = focus
        }
        var out: [String: Any] = ["section": app.section.rawValue, "focusId": app.focusId as Any? ?? NSNull(),
                                  "env": app.env.rawValue, "timeRange": app.timeRange.rawValue]
        if let notice { out["notice"] = notice }
        return out
    }

    private func alertUpdate(_ a: MCPArgs) throws -> Any {
        let id = try a.require("alert_id")
        guard let i = app.alerts.firstIndex(where: { $0.id == id }) else { throw MCPToolError.notFound("Alert not found: \(id)") }
        let action = a.string("action")
        let note = a.string("ai_note")
        if action == nil && note == nil { throw MCPToolError("Give at least one of action and ai_note") }
        if let note { app.alerts[i].aiNote = note }
        switch action {
        case "acknowledge": app.acknowledge(alertId: id, by: .mcp)
        case "resolve": app.resolve(alertId: id, by: .mcp)
        case "silence": app.silence(alertId: id, by: .mcp)
        case nil: break
        case let other?: throw MCPToolError("action must be acknowledge, resolve, or silence; got \"\(other)\"")
        }
        return MCPJSON.object(app.alerts[i])
    }

    private func alertCreate(_ a: MCPArgs) throws -> Any {
        let service = try serviceId(a.require("service"))
        let num = (app.alerts.compactMap { Int($0.id.filter(\.isNumber)) }.max() ?? 5000) + 1
        let alert = Alert(id: "ALT-\(num)", title: try a.require("title"), severity: try a.requireEnum("severity", Severity.self),
                          service: service, state: .firing, rule: try a.require("rule"), value: try a.require("value"),
                          startedAt: .now, acknowledgedBy: nil, incidentId: nil, aiNote: a.string("ai_note"))
        app.alerts.insert(alert, at: 0)
        app.log(.mcp, "bell.badge.fill", "新告警「\(alert.title)」(\(alert.severity.label))", .monitoring)
        return MCPJSON.object(alert)
    }

    private func workItemCreate(_ a: MCPArgs) throws -> Any {
        let title = try a.require("title")
        let type = try a.requireEnum("type", WorkType.self)
        let status = try a.optionalEnum("status", WorkStatus.self) ?? .todo
        let service = try a.string("service").map(serviceId)
        let assignee = a.string("assignee")
        if let assignee, app.person(assignee) == nil { throw MCPToolError.notFound("Person not found: \(assignee)") }
        var incidentIndex: Int?
        if let incId = a.string("incident_id") {
            guard let i = app.incidents.firstIndex(where: { $0.id == incId }) else { throw MCPToolError.notFound("Incident not found: \(incId)") }
            incidentIndex = i
        }
        let num = (app.workItems.compactMap { Int($0.id.filter(\.isNumber)) }.max() ?? 100) + 1
        let item = WorkItem(id: "OPS-\(num)", title: title, type: type, status: status,
                            assignee: assignee.map { .person($0) }, points: a.int("points") ?? 3,
                            service: service, createdByAI: true)
        app.workItems.append(item)
        app.log(.mcp, "checklist", "建立 \(item.id)「\(title)」", .development)
        if let i = incidentIndex {
            let incId = app.incidents[i].id
            do { _ = try app.startIncidents().update(id: incId, addFollowUps: [item.id], by: .mcp) } catch {
                throw MCPToolError("Created \(item.id), but could not link it to incident \(incId): \(error.localizedDescription)")
            }
        }
        return MCPJSON.object(item)
    }

    private func workItemMove(_ a: MCPArgs) throws -> Any {
        let id = try a.require("id")
        guard let i = app.workItems.firstIndex(where: { $0.id == id }) else { throw MCPToolError.notFound("Work item not found: \(id)") }
        app.moveWorkItem(id: id, to: try a.requireEnum("status", WorkStatus.self), by: .mcp)
        return MCPJSON.object(app.workItems[i])
    }

    private func pipelineUpdate(_ a: MCPArgs) throws -> Any {
        let id = try a.requireInt("run_id")
        guard let i = app.pipelineRuns.firstIndex(where: { $0.id == id }) else { throw MCPToolError.notFound("Pipeline run not found: #\(id)") }
        let action = a.string("action"), diagnosis = a.string("ai_diagnosis")
        if action == nil && diagnosis == nil { throw MCPToolError("Give at least one of action and ai_diagnosis") }
        if let diagnosis { app.pipelineRuns[i].aiDiagnosis = diagnosis }
        switch action {
        case "retry":
            app.retry(runId: id, by: .mcp)
            return ["retried": id, "newRun": compact(MCPJSON.object(app.pipelineRuns[0]))]
        case "cancel":
            let s = app.pipelineRuns[i].status
            guard s == .running || s == .queued || s == .waitingApproval else {
                throw MCPToolError("#\(id) is currently \"\(s.label)\" and cannot be cancelled")
            }
            app.cancel(runId: id, by: .mcp)
        case nil: break
        case let other?: throw MCPToolError("action must be retry or cancel; got \"\(other)\"")
        }
        return compact(MCPJSON.object(app.pipelineRuns[i]))
    }

    private func requestApproval(_ a: MCPArgs) throws -> Any {
        let automation = a.string("automation") ?? Actor.mcpId
        if automation != Actor.mcpId && app.automation(automation) == nil { throw MCPToolError.notFound("Automation not found: \(automation)") }
        let minutes = max(1, a.int("expires_in_minutes") ?? 60)
        let num = (app.approvals.compactMap { Int($0.id.filter(\.isNumber)) }.max() ?? 300) + 1
        let req = ApprovalRequest(id: "APV-\(num)", title: try a.require("title"), automation: automation,
                                  risk: try a.requireEnum("risk", RiskLevel.self), action: try a.require("action"),
                                  rationale: try a.require("rationale"), evidence: a.strings("evidence") ?? [],
                                  rollbackPlan: try a.require("rollback_plan"), createdAt: .now,
                                  expiresAt: .now.addingTimeInterval(Double(minutes) * 60), state: .pending, decidedBy: nil)
        app.approvals.insert(req, at: 0)
        app.log(.mcp, "hand.raised", "送出待核准請求「\(req.title)」", .automation)
        return MCPJSON.object(req)
    }

    private func logAppend(_ a: MCPArgs) throws -> Any {
        let entry = LogEntry(id: UUID(), date: .now, level: try a.requireEnum("level", LogLevel.self),
                             service: try serviceId(a.require("service")), host: a.string("host") ?? "mcp",
                             message: try a.require("message"), traceId: a.string("trace_id"))
        app.logs.insert(entry, at: 0)
        return MCPJSON.object(entry)
    }

    private func activityPost(_ a: MCPArgs) throws -> Any {
        let section = try a.optionalEnum("section", AppSection.self) ?? .overview
        app.log(.mcp, a.string("icon") ?? "sparkles", try a.require("text"), section)
        return MCPJSON.object(app.activity[0])
    }

    private func voiceSubmit(_ a: MCPArgs) async throws -> Any {
        guard let voice = app.voice else { throw MCPToolError("The narration voice feature has not started") }
        let all = (a.raw["all"] as? Bool) ?? false
        let scripts = a.strings("scripts") ?? []
        let limit = a.int("limit")
        do {
            if (a.raw["dry_run"] as? Bool) == true {
                return ["dryRun": try await voice.dryRun(scripts: scripts, all: all, limit: limit)]
            }
            let job = try voice.submit(scripts: scripts, all: all, limit: limit, by: .mcp)
            let ahead = app.voiceJobs.filter { $0.state == .queued && $0.id != job.id }.count
                + app.voiceJobs.filter(\.state.holdsBatch).count
            return ["job": voiceJSON(job, full: false),
                    "note": ahead > 0 ? "\(ahead) batch(es) ahead; it is submitted automatically when its turn comes" : "Submitted right away. When it finishes, a person auditions and publishes it in omixor CI/CD > Narration publishing; check progress with voice_jobs"]
        } catch {
            throw MCPToolError(error.localizedDescription)
        }
    }

    private func voiceJobs(_ a: MCPArgs) throws -> Any {
        if let id = a.string("id") {
            guard let j = app.voiceJobs.first(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else {
                throw MCPToolError.notFound("Narration batch not found: \(id)")
            }
            return voiceJSON(j, full: true)
        }
        return ["jobs": app.voiceJobs.prefix(20).map { voiceJSON($0, full: false) }]
    }

    /// 批次給 MCP 看的樣子:不含比對用的 baseline;摘要模式不列段落文字,紀錄只留最後幾行
    private func voiceJSON(_ job: VoiceJob, full: Bool) -> Any {
        guard var d = MCPJSON.object(job) as? [String: Any] else { return [:] }
        d["baseline"] = nil
        d["stateLabel"] = job.state.label
        if let log = d["log"] as? [Any] { d["log"] = Array(log.suffix(full ? 30 : 3)) }
        if !full, let changes = d["changes"] as? [[String: Any]] {
            d["changes"] = changes.map { c -> [String: Any] in
                var c = c
                c["addedCount"] = (c["added"] as? [Any])?.count ?? 0
                c["added"] = nil
                return c
            }
        }
        return d
    }

    // MARK: 虛擬角色(agent_task_delegate;其他角色、任務、組織鏈、文件櫃、Issue、頻道工具由 core server 處理)

    private var agentStore: AgentStore { app.startAgents() }

    private func taskJSON(_ t: AgentTask) -> [String: Any] {
        var d = MCPJSON.object(t) as? [String: Any] ?? [:]
        d["agentName"] = agentStore.agent(t.agentId)?.name ?? t.agentId
        d["createdBy"] = agentStore.authorName(t.createdBy)
        d["notes"] = t.notes.map { ["author": agentStore.authorName($0.author), "text": $0.text,
                                    "at": ISO8601DateFormatter().string(from: $0.at)] }
        return d
    }

    private func agentDelegate(_ a: MCPArgs) throws -> Any {
        let me = try a.require("agent")
        let to = try a.require("to")
        let title = try a.require("title")
        do {
            let t = try agentStore.delegate(from: me, to: to, title: title, instructions: a.raw["instructions"] as? String ?? "",
                                            priority: try a.optionalEnum("priority", IssuePriority.self) ?? .normal, sourceTask: a.string("task"))
            return taskJSON(t)
        } catch let e as MCPToolError { throw e } catch { throw MCPToolError(error.localizedDescription) }
    }

    // MARK: 組織

    /// server:連線資訊與版本(對應 web 的 internal/mcp/tools_server.go;不含網址與密鑰)
    private func serverInfo() -> Any {
        var out: [String: Any] = ["mode": app.mode.mode, "org": ["id": app.currentOrgId, "name": app.currentOrg?.name ?? ""]]
        var ver: [String: Any] = ["app": ["version": Self.serverVersion]]
        if let s = app.remoteServer {
            var login: [String: Any] = ["loggedIn": false]
            if let a = app.omixorLogin.account {
                login = ["loggedIn": true, "email": a.email, "name": a.name, "role": a.role]
                if let e = a.expiresAt { login["tokenExpiresAt"] = ISO8601DateFormatter().string(from: e) }
            }
            out["remote"] = ["server": s.name, "serverId": s.id, "login": login]
            do {
                let v = try IssueRemote(config: IssueServerConfig(url: s.url, token: nil)).version()
                ver["server"] = ["version": v.version, "commit": v.commit, "buildTime": v.buildTime]
            } catch {
                ver["server"] = ["error": "Could not get the remote server version (unreachable, timed out, or unsupported)"]
            }
        }
        out["version"] = ver
        return out
    }

    private func orgsInfo() -> Any {
        app.ensureOrgs()
        return ["current": app.currentOrgId,
                "orgs": app.orgs.map { ["id": $0.id, "name": $0.name, "note": $0.note, "current": $0.id == app.currentOrgId] }]
    }

    // MARK: 署名

    /// 署名(fb_draft_save 用):有給 agent 就是該角色,否則是 MCP 用戶端:有給 agent 就是該角色,否則是 MCP 用戶端
    private func issueAuthor(_ a: MCPArgs) throws -> String {
        guard let name = a.string("agent"), !name.isEmpty else { return IssueStore.mcpAuthor }
        guard let g = agentStore.agent(name) else { throw MCPToolError.notFound("Role not found: \(name)") }
        return IssueStore.agentPrefix + g.id
    }

    private func siteVisitors(_ a: MCPArgs) async throws -> Any {
        let r = a.string("range") ?? "1d"
        let range = r == "all" ? SiteRange.all : SiteRange(rawValue: r)
        guard let range else { throw MCPToolError("range must be one of all, 30m, 12h, 1d, 7d") }
        let d: SiteOverview
        do {
            d = try await SiteVisitors.fetch(app, connection: a.string("connection"), range: range)
        } catch {
            throw MCPToolError("Could not read visitor statistics: \(error.localizedDescription) (a person must sign in to the matching connection in omixor)")
        }
        let f = d.funnel
        let g = d.guestStats
        var out = MCPJSON.object(d) as? [String: Any] ?? [:]
        out["range"] = range.id
        out["funnel"] = ["tracked": f.tracked, "ready": f.ready, "played": f.played, "failed": f.failed, "bots": f.bots]
        // 只留有紀錄的課,依訪客打開次數排序
        out["gates"] = d.gates.map { ["path": $0.name, "views": $0.count] }
        out["lessons"] = d.playerLessons.filter { $0.guestTracked + $0.learners + $0.guestBots > 0 }
            .sorted { $0.guestTracked > $1.guestTracked }.map { MCPJSON.object($0) }
        if var gs = out["guestStats"] as? [String: Any] {
            gs["homePaths"] = Array((gs["homePaths"] as? [Any] ?? []).prefix(10))
            gs["homeStays"] = Array((gs["homeStays"] as? [Any] ?? []).prefix(20))
            gs["idleVisits"] = Array((gs["idleVisits"] as? [Any] ?? []).prefix(15))
            gs["homeStaysTotal"] = g.homeStays.count
            gs["idleVisitsTotal"] = g.idleVisits.count
            out["guestStats"] = gs
        }
        return out
    }

    // MARK: GitHub

    private func ghStore() throws -> GitHubStore {
        let g = app.startGitHub(timer: false)
        guard g.connection != nil, g.isLoggedIn else {
            throw MCPToolError("omixor is not signed in to GitHub. Ask a person to set it up and sign in under omixor Settings > Repositories")
        }
        return g
    }

    private func ghRepo(_ a: MCPArgs) throws -> String {
        if let r = a.string("repo") {
            guard let n = GitHub.normalizeRepo(r) else { throw MCPToolError("repo must be written as owner/name") }
            return n
        }
        let repos = app.githubSettings.repos
        guard repos.count == 1 else {
            throw MCPToolError(repos.isEmpty ? "No repo is tracked yet; give repo (owner/name)" : "Give repo; currently tracked: \(repos.joined(separator: ", "))")
        }
        return repos[0]
    }

    private func ghError(_ e: Error) -> MCPToolError {
        MCPToolError("GitHub returned an error: \(e.localizedDescription)")
    }

    private func runJSON(_ r: GHRun) -> [String: Any] {
        var o = MCPJSON.object(r) as? [String: Any] ?? [:]
        o["state"] = r.stateLabel
        if let d = r.duration { o["durationSeconds"] = Int(d) }
        return o
    }

    private func githubSummary(_ a: MCPArgs) async throws -> Any {
        let g = try ghStore()
        if a.bool("refresh") == true || app.github.fetchedAt == nil { await g.refresh() }
        if let e = app.github.error { throw MCPToolError(e) }
        let only = try a.string("repo").map { r -> String in
            guard let n = GitHub.normalizeRepo(r) else { throw MCPToolError("repo must be written as owner/name") }
            return n
        }
        let limit = max(1, a.int("limit") ?? 10)
        let repos = app.githubSettings.repos.filter { only == nil || $0.caseInsensitiveCompare(only!) == .orderedSame }
        return [
            "account": app.connectionStates[g.connectionId]?.account as Any,
            "fetchedAt": app.github.fetchedAt.map(MCPJSON.iso) as Any,
            "repos": repos.map { r -> [String: Any] in
                let info = app.github.repos.first { $0.id == r }
                let runs = app.github.runs.filter { $0.repo == r }
                return [
                    "repo": r,
                    "info": info.map { MCPJSON.object($0) } as Any,
                    "error": app.github.errors[r] as Any,
                    "openPulls": app.github.pulls.filter { $0.repo == r }.map { MCPJSON.object($0) },
                    "recentRuns": runs.prefix(limit).map(runJSON),
                    "failedRuns": runs.filter(\.isFailure).count,
                ]
            },
        ]
    }

    private func githubRuns(_ a: MCPArgs) async throws -> Any {
        let g = try ghStore()
        let repo = try ghRepo(a)
        var q = ["per_page=\(min(100, max(1, a.int("limit") ?? 20)))"]
        if let b = a.string("branch") { q.append("branch=" + (b.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? b)) }
        if let s = a.string("status") { q.append("status=\(s)") }
        do {
            let obj = try await g.get("/repos/\(ConnHTTP.escapePath(repo))/actions/runs?" + q.joined(separator: "&"))
            let runs = ((obj as? [String: Any])?["workflow_runs"] as? [[String: Any]] ?? []).map { GitHub.run($0, repo: repo) }
            return ["repo": repo, "total": (obj as? [String: Any])?["total_count"] as Any, "runs": runs.map(runJSON)]
        } catch { throw ghError(error) }
    }

    private func githubRun(_ a: MCPArgs) async throws -> Any {
        let g = try ghStore()
        let repo = try ghRepo(a)
        guard let id = a.int("id") else { throw MCPToolError("id is required") }
        do {
            guard let d = try await g.get("/repos/\(ConnHTTP.escapePath(repo))/actions/runs/\(id)") as? [String: Any] else {
                throw MCPToolError("Unexpected run data format")
            }
            let run = GitHub.run(d, repo: repo)
            let jobs = try await g.jobs(repo: repo, run: id)
            var out = runJSON(run)
            out["jobs"] = jobs.map { MCPJSON.object($0) }
            let failed = jobs.filter { $0.status == "completed" && !["success", "skipped", "cancelled"].contains($0.conclusion ?? "") }
            out["failedSteps"] = failed.flatMap { j in
                j.steps.filter { $0.status == "completed" && !["success", "skipped", "cancelled"].contains($0.conclusion ?? "") }
                    .map { "\(j.name) › \($0.name)" }
            }
            if a.bool("log") == true {
                let lines = min(400, max(10, a.int("lines") ?? 80))
                var logs: [[String: Any]] = []
                for j in failed.prefix(3) {
                    do { logs.append(["job": j.name, "tail": try await g.jobLogTail(repo: repo, job: j.id, lines: lines)]) }
                    catch { logs.append(["job": j.name, "error": error.localizedDescription]) }
                }
                out["logs"] = logs
            }
            return out
        } catch let e as MCPToolError { throw e } catch { throw ghError(error) }
    }

    private func githubPull(_ a: MCPArgs) async throws -> Any {
        let g = try ghStore()
        let repo = try ghRepo(a)
        guard let n = a.int("number") else { throw MCPToolError("number is required") }
        do { return MCPJSON.object(try await g.pullDetail(repo: repo, number: n)) } catch { throw ghError(error) }
    }

    // MARK: GCP

    private func gcpStore() throws -> GCPStore {
        let g = app.startGCP(timer: false)
        guard g.connection != nil, g.isLoggedIn else {
            throw MCPToolError("omixor is not signed in to Google Cloud. Ask a person to set it up and sign in under omixor Servers and Services > GCP")
        }
        guard g.isConfigured else { throw MCPToolError("No GCP project is configured; ask a person to set it up under omixor Servers and Services > GCP") }
        return g
    }

    private func gcpFail(_ e: Error) -> MCPToolError {
        guard let c = e as? ConnectionError else { return MCPToolError("GCP returned an error: \(e.localizedDescription)") }
        let hint = switch c.status {
        case 401: "the Google sign-in has expired; a person must sign in again"
        case 403: "permission denied: the signed-in account needs viewer access (roles/viewer) on the project, and the Cloud Run / Logging / Monitoring APIs must be enabled"
        case 404: "not found: check the project id and region"
        default: ""
        }
        return MCPToolError("GCP returned an error: " + (hint.isEmpty ? c.localizedDescription : "\(hint) (\(c.message))"))
    }

    private func gcpSummary(_ a: MCPArgs) async throws -> Any {
        let g = try gcpStore()
        if a.bool("refresh") == true || app.gcp.fetchedAt == nil { await g.refresh() }
        if let e = app.gcp.error { throw MCPToolError(e) }
        var out: [String: Any] = [
            "project": app.gcpSettings.project, "region": app.gcpSettings.region,
            "account": app.connectionStates[g.connectionId]?.account as Any,
            "fetchedAt": app.gcp.fetchedAt.map(MCPJSON.iso) as Any,
            "services": app.gcp.services.map { s -> Any in
                var o = MCPJSON.object(s) as? [String: Any] ?? [:]; o["state"] = s.stateLabel; return o
            },
            "jobs": app.gcp.jobs.map { j -> [String: Any] in
                ["name": j.id, "latest": j.latest.map { e -> Any in
                    var o = MCPJSON.object(e) as? [String: Any] ?? [:]; o["status"] = e.statusLabel; return o } as Any]
            },
        ]
        do {
            if let svc = a.string("service") { out["revisions"] = MCPJSON.object(try await g.revisions(service: svc)) }
            if let job = a.string("job") {
                out["executions"] = try await g.executions(job: job).map { e -> Any in
                    var o = MCPJSON.object(e) as? [String: Any] ?? [:]; o["status"] = e.statusLabel
                    if let d = e.duration { o["durationSeconds"] = Int(d) }
                    return o
                }
            }
        } catch { throw gcpFail(error) }
        return out
    }

    private func gcpLogs(_ a: MCPArgs) async throws -> Any {
        let g = try gcpStore()
        let sev = (a.string("severity") ?? "ERROR").uppercased()
        guard GCP.severities.contains(sev) else { throw MCPToolError("severity must be one of \(GCP.severities.joined(separator: ", "))") }
        let hours = min(720, max(1, a.int("hours") ?? 24))
        do {
            let logs = try await g.logs(service: a.string("service"), job: a.string("job"), severity: sev, hours: hours,
                                        contains: a.string("contains"), limit: min(500, max(1, a.int("limit") ?? 50)))
            return ["count": logs.count, "hours": hours, "severity": sev, "entries": MCPJSON.object(logs)]
        } catch { throw gcpFail(error) }
    }

    private func gcpMetrics(_ a: MCPArgs) async throws -> Any {
        let g = try gcpStore()
        var service = a.string("service")
        if service == nil {
            if app.gcp.fetchedAt == nil { await g.refresh() }
            guard app.gcp.services.count == 1 else {
                throw MCPToolError("Give service; available: \(app.gcp.services.map(\.id).joined(separator: ", "))")
            }
            service = app.gcp.services[0].id
        }
        let hours = min(720, max(1, a.int("hours") ?? 24))
        do {
            let m = try await g.metrics(service: service!, hours: hours)
            return ["service": service!, "hours": hours,
                    "totals": ["requests": m.totalRequests, "2xx": m.total("2xx"), "4xx": m.total("4xx"), "5xx": m.total("5xx"),
                               "errorRate5xx": (m.errorRate * 10000).rounded() / 10000, "maxP95ms": m.maxP95.rounded(),
                               "maxInstances": Int(m.instances.map(\.value).max() ?? 0)],
                    "requests": MCPJSON.object(m.requests), "latencyP95": MCPJSON.object(m.latencyP95),
                    "instances": MCPJSON.object(m.instances)]
        } catch { throw gcpFail(error) }
    }

    // MARK: - Facebook 粉絲專頁(只讀與草稿;沒有發佈工具)

    private var fb: FacebookStore { app.startFacebook() }

    /// 還沒連上粉絲專頁時回給用戶端的錯誤(App 畫面用的中文版在 FacebookStore.notConnected)
    private static let fbNotConnected = "omixor is not connected to any Facebook Page. Ask a person to set up the connection in omixor AI Automation > Pages (Meta sign-in or paste a Page access token) and tick the Pages to manage."

    private func fbConnected() throws -> FacebookStore {
        let f = fb
        guard f.isConnected else { throw MCPToolError(Self.fbNotConnected) }
        return f
    }

    private func fbFail(_ e: Error) -> MCPToolError {
        if let m = e as? MCPToolError { return m }
        return MCPToolError((e as? LocalizedError)?.errorDescription ?? "\(e)")
    }

    /// page 參數 → 要看的專頁(管理中且有權杖)
    private func fbTargets(_ f: FacebookStore, _ a: MCPArgs) throws -> [FBPage] {
        if let ref = a.string("page"), !ref.isEmpty {
            guard let p = f.page(ref) else {
                throw MCPToolError.notFound("Page not found: \(ref). Available: \(app.fbSettings.pages.map { "\($0.name) (\($0.id))" }.joined(separator: ", "))")
            }
            guard FacebookStore.pageToken(p.id) != nil else { throw MCPToolError("Page \"\(p.name)\" has no token. " + Self.fbNotConnected) }
            return [p]
        }
        return app.fbSettings.managedPages.filter { FacebookStore.pageToken($0.id) != nil }
    }

    private func fbPageJSON(_ p: FBPage) -> [String: Any] {
        ["id": p.id, "name": p.name, "category": p.category, "link": p.link, "picture": p.picture,
         "fanCount": p.fanCount as Any, "followersCount": p.followersCount as Any, "managed": p.managed,
         "hasToken": FacebookStore.pageToken(p.id) != nil]
    }

    private func fbMetricJSON(_ m: FBMetric) -> [String: Any] {
        ["key": m.key, "label": m.label, "metric": m.metric as Any, "status": m.status.rawValue, "total": m.total as Any,
         "values": m.values.map { ["date": MCPJSON.iso($0.date), "value": $0.value] },
         "breakdown": m.breakdown as Any, "note": m.note as Any]
    }

    private func fbPostJSON(_ p: FBPost) -> [String: Any] {
        ["id": p.id, "pageId": p.pageId, "message": p.message, "createdTime": p.createdTime.map(MCPJSON.iso) as Any,
         "scheduledTime": p.scheduledTime.map(MCPJSON.iso) as Any, "permalink": p.permalink, "picture": p.picture,
         "isPublished": p.isPublished, "statusType": p.statusType, "reactions": p.reactions as Any,
         "comments": p.comments as Any, "shares": p.shares as Any, "engagement": p.engagement as Any,
         "insights": p.insights.map(fbMetricJSON)]
    }

    private func fbDraftJSON(_ d: FBDraft) -> [String: Any] {
        ["id": d.id, "pageId": d.pageId, "pageName": fb.page(d.pageId)?.name as Any, "message": d.message, "link": d.link,
         "imageURL": d.imageURL, "hasLocalImage": !d.localImage.isEmpty,
         "localImageName": d.localImage.isEmpty ? NSNull() : URL(fileURLWithPath: d.localImage).lastPathComponent as Any,
         "proposedTime": d.proposedTime.map(MCPJSON.iso) as Any, "source": d.source, "author": d.author,
         "authorName": fb.authorName(d.author), "status": d.status.rawValue, "postId": d.postId as Any,
         "scheduledFor": d.scheduledFor.map(MCPJSON.iso) as Any, "publishedAt": d.publishedAt.map(MCPJSON.iso) as Any,
         "publishedBy": d.publishedBy as Any, "createdAt": MCPJSON.iso(d.createdAt), "updatedAt": MCPJSON.iso(d.updatedAt)]
    }

    private func fbPages(_ a: MCPArgs) async throws -> Any {
        let f = try fbConnected()
        var errors: [String: String] = [:]
        if a.bool("refresh") == true {
            for p in app.fbSettings.managedPages where FacebookStore.pageToken(p.id) != nil {
                do { _ = try await f.refreshPage(p.id) } catch { errors[p.id] = fbFail(error).message }
            }
        }
        return ["connected": true, "mode": app.fbSettings.mode, "apiVersion": app.fbSettings.apiVersion,
                "pages": app.fbSettings.pages.map { p -> [String: Any] in
                    var o = fbPageJSON(p)
                    if let e = errors[p.id] { o["error"] = e }
                    return o
                }]
    }

    private func fbPosts(_ a: MCPArgs) async throws -> Any {
        let f = try fbConnected()
        let limit = min(50, max(1, a.int("limit") ?? 10))
        let withInsights = a.bool("insights") ?? true
        let withScheduled = a.bool("scheduled") ?? true
        var out: [[String: Any]] = []
        for p in try fbTargets(f, a) {
            var o: [String: Any] = ["pageId": p.id, "pageName": p.name]
            do {
                o["posts"] = try await f.posts(p.id, limit: limit, withInsights: withInsights).map(fbPostJSON)
                o["scheduled"] = withScheduled ? try await f.scheduled(p.id).map(fbPostJSON) : []
            } catch {
                o["posts"] = o["posts"] ?? []
                o["scheduled"] = o["scheduled"] ?? []
                o["error"] = fbFail(error).message
            }
            out.append(o)
        }
        return ["pages": out]
    }

    private func fbInsights(_ a: MCPArgs) async throws -> Any {
        let f = try fbConnected()
        let period = a.string("period") ?? "day"
        guard FB.periods.contains(period) else { throw MCPToolError("period must be one of \(FB.periods.joined(separator: ", "))") }
        let days = min(90, max(1, a.int("days") ?? 28))
        var out: [[String: Any]] = []
        for p in try fbTargets(f, a) {
            var o: [String: Any] = ["pageId": p.id, "pageName": p.name, "period": period,
                                    "fanCount": p.fanCount as Any, "followersCount": p.followersCount as Any]
            do {
                let r = try await f.pageInsights(p.id, period: period, days: days)
                o["since"] = MCPJSON.iso(r.since)
                o["until"] = MCPJSON.iso(r.until)
                o["metrics"] = r.metrics.map(fbMetricJSON)
            } catch {
                o["metrics"] = []
                o["error"] = fbFail(error).message
            }
            out.append(o)
        }
        return ["pages": out]
    }

    private func fbDrafts(_ a: MCPArgs) throws -> Any {
        let f = fb
        if let id = a.string("id"), !id.isEmpty {
            guard let d = f.draft(id) else { throw MCPToolError.notFound("Draft not found: \(id)") }
            return fbDraftJSON(d)
        }
        let status = a.string("status") ?? "draft"
        guard ["draft", "scheduled", "published", "all"].contains(status) else {
            throw MCPToolError("status must be one of draft, scheduled, published, all")
        }
        var list = app.fbSettings.drafts.filter { status == "all" || $0.status.rawValue == status }
        if let ref = a.string("page"), !ref.isEmpty {
            guard let p = f.page(ref) else { throw MCPToolError.notFound("Page not found: \(ref)") }
            list = list.filter { $0.pageId == p.id }
        }
        list.sort { $0.updatedAt > $1.updatedAt }
        let limit = max(1, a.int("limit") ?? 30)
        return ["drafts": list.prefix(limit).map(fbDraftJSON), "total": list.count]
    }

    private func fbDraftSave(_ a: MCPArgs) throws -> Any {
        let f = fb
        let author = try issueAuthor(a)
        for k in ["link", "image_url"] {
            if let u = a.string(k), !u.isEmpty, !(u.lowercased().hasPrefix("https://") || u.lowercased().hasPrefix("http://")) {
                throw MCPToolError("\(k) must be an http or https URL")
            }
        }
        var proposed: Date?
        if let t = a.string("proposed_time"), !t.isEmpty {
            let iso = ISO8601DateFormatter()
            guard let d = iso.date(from: t) else { throw MCPToolError("proposed_time must be ISO 8601, for example 2026-11-20T20:00:00+08:00") }
            proposed = d
        }
        var pageId: String?
        if let ref = a.string("page"), !ref.isEmpty {
            guard let p = f.page(ref) else {
                throw MCPToolError.notFound("Page not found: \(ref). Available: \(app.fbSettings.pages.map { "\($0.name) (\($0.id))" }.joined(separator: ", "))")
            }
            pageId = p.id
        }
        var d: FBDraft
        let isNew: Bool
        if let id = a.string("id"), !id.isEmpty {
            guard let old = f.draft(id) else { throw MCPToolError.notFound("Draft not found: \(id)") }
            guard old.status == .draft else { throw MCPToolError("Draft \(old.id) is \(old.status.rawValue) and cannot be edited") }
            d = old
            isNew = false
        } else {
            d = FBDraft(id: f.newDraftId(), author: author)
            d.pageId = app.fbSettings.managedPages.first?.id ?? ""
            isNew = true
        }
        if let v = a.raw["message"] as? String { d.message = v }
        if let v = a.string("link") { d.link = v }
        if let v = a.string("image_url") { d.imageURL = v }
        if let proposed { d.proposedTime = proposed }
        if let v = a.string("source") { d.source = v }
        if let pageId { d.pageId = pageId }
        if d.isEmpty { throw MCPToolError("A draft needs at least one of message, link, or image_url") }
        let saved = f.saveDraft(d, by: author)
        app.log(f.actor(author), "square.and.pencil", "\(isNew ? "新增" : "修改") Facebook 草稿 \(saved.id)「\(saved.titleLine)」", .automation)
        var o = fbDraftJSON(saved)
        o["note"] = "Saved as a draft only. Publishing or scheduling must be done by a person in omixor AI Automation > Pages."
        return o
    }

    private func serviceId(_ id: String) throws -> String {
        guard app.service(id) != nil else {
            throw MCPToolError.notFound("Service not found: \(id). Available: \(app.services.map(\.id).joined(separator: ", "))")
        }
        return id
    }
}
