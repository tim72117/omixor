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
            return MCPJSON.data(errorResponse(id: NSNull(), code: -32700, message: "JSON 解析失敗"))
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
            return errorResponse(id: NSNull(), code: -32600, message: "不是有效的 JSON-RPC 請求")
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
                return errorResponse(id: id, code: -32602, message: "缺少工具名稱")
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
                return result(id: id, ["content": [["type": "text", "text": Self.agentTaskCreateAsAgentError]], "isError": true])
            }
            if let proxied = await callOnServer(id: id, raw, params["arguments"] as? [String: Any] ?? [:]) { return proxied }
            let name = Self.legacyToolName(raw)
            // 核心工具一律由 core server 執行(App 沒有自己的實作):server 連不上,或連得上但工具清單沒有這個工具時回錯誤
            if Self.coreTools.contains(name) {
                app.mcp.lastTool = name
                let why: String
                if await serverTools() == nil {
                    why = "現在連不上:" + (serverToolError ?? "讀不到 core server 的工具清單")
                } else {
                    why = "但 core server 的工具清單裡沒有這個工具(server 版本可能比 App 舊,請更新 server)"
                }
                return result(id: id, ["content": [["type": "text", "text": "\(name) 由 omixor core server 處理,\(why)"]], "isError": true])
            }
            guard Self.tools.contains(where: { $0.name == name }) else {
                return errorResponse(id: id, code: -32602, message: "沒有這個工具:\(name)")
            }
            let args = MCPArgs(raw: params["arguments"] as? [String: Any] ?? [:])
            app.mcp.lastTool = name
            if !toolEnabled(name) {
                return result(id: id, ["content": [["type": "text", "text": featureOffMessage(name)]], "isError": true])
            }
            do {
                let out = try await call(name, args)
                return result(id: id, ["content": [["type": "text", "text": MCPJSON.string(out)]], "isError": false])
            } catch let e as MCPToolError {
                return result(id: id, ["content": [["type": "text", "text": e.message]], "isError": true])
            } catch {
                return result(id: id, ["content": [["type": "text", "text": "\(error)"]], "isError": true])
            }
        case "resources/list":
            // 轉給 core server(MCP Apps 的 ui:// 畫面);連不上或 server 沒有 resources(舊版)時回空清單
            return result(id: id, ["resources": await serverResources() ?? []])
        case "resources/read":
            guard let uri = params["uri"] as? String, !uri.isEmpty else {
                return errorResponse(id: id, code: -32602, message: "缺少資源 uri")
            }
            return await readOnServer(id: id, uri: uri)
        case "prompts/list":
            return result(id: id, ["prompts": []])
        default:
            return errorResponse(id: id, code: -32601, message: "不支援的方法:\(method)")
        }
    }

    // MARK: - 轉給 omixor server

    /// agent_task_create 帶了 agent(以角色身分派任務)時的錯誤
    static let agentTaskCreateAsAgentError = "agent_task_create 不能帶 agent 以角色身分派任務(派任務一律署名登入的人;要指定接任務的角色用 assign_to)。"
        + "角色要把工作交給別的角色,請用 agent_task_delegate(agent = 委派的角色、to = 接任務的角色,組織鏈上要有 agent:<委派者> → agent:<接任務者>)"

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

    private func featureName(_ key: String) -> String {
        FeatureFallback.entry(key)?.name ?? key
    }

    /// 工具的功能關了時回給用戶端的錯誤
    func featureOffMessage(_ name: String) -> String {
        let keys = Self.sampleToolFeatures[name] ?? []
        let which = keys.count == 1 ? "(\(featureName(keys[0])),\(keys[0]))" : ""
        return "\(name):這個功能已關閉\(which)。目前組織沒有開啟對應的範例功能,請在 omixor 的 設定 › 功能(或系統頁)打開後再用"
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
            return errorResponse(id: id, code: -32002, message: "找不到資源:\(uri)(\(error.localizedDescription))")
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
            return result(id: id, ["content": [["type": "text", "text": error.localizedDescription]], "isError": true])
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
    omixor 是維運線上服務的 macOS app。這些工具直接讀寫 app 裡的即時資料,變更會立刻出現在畫面上,並以「MCP 用戶端」記在動態牆。
    先用 overview 掌握整體狀況,再用 list / get 查細節。
    核准 AI 請求、核准部署、回滾、調整自動化自主程度只能由人在 app 裡決定;需要這類變更時,用 request_approval 建立待核准請求,附上理由、依據與回滾方式。
    GCP(gcp、gcp_logs、gcp_metrics)只讀:看 Cloud Run 狀態、錯誤日誌、請求量與延遲;omixor 不會部署、改流量或執行 Job。
    GitHub(github、github_runs、github_run、github_pull)只讀:看 PR、檢查與 Actions 失敗原因;omixor 不會開 PR、留言或重跑 workflow。
    Issue、虛擬角色與任務、文件櫃、連線都屬於目前的組織(orgs 看是哪一個);切換組織只能由人在 app 裡做。
    omixor 有虛擬角色(agents)。使用者請你以某角色身分工作時:可先用 agent_context 取得該角色能讀取的資訊,再用 agent_task_claim 領任務,照回傳的角色設定與任務說明做,過程與結果用 agent_task_report 回報;需要人決定時回報 blocked。要以角色身分寫 Issue、留言、文件或頻道訊息,必須改用 agent_task_claim 回傳的角色憑證連角色端點(/mcp/agent);這裡(一般 omixor 連線)的 issue_create、issue_update、文件櫃與頻道工具不能帶 agent 參數指定角色署名(會被拒絕),署名一律是你登入的帳號。
    文件櫃(cabinet、docs、doc_save)依目前組織的文件櫃結構(區塊與頁籤)分類;角色只能讀寫它 docAccess 範圍內的文件(在角色端點上由憑證決定)。組織底下可以分專案(projects、project_save),文件櫃工具帶 project(專案 id)讀寫該專案的文件櫃,省略 = 未分專案。
    發現值得追蹤的問題(例如從 site_visitors 的數字看到異常)時,用 issue_create 存成 Issue;先用 issues 搜尋避免重複,已有就用 issue_update 加留言。
    旁白語音(shuttle 專案的 npm run voice)可以用 voice_submit 分派成批次:omixor 會定時查、收完結果,等人在 app 裡試聽後按發佈。發佈沒有 MCP 工具。
    訊息頻道(channels、channel_messages、channel_post、channel_create)把人與角色拉進同一個對話;@ 角色只通知、不排任務。以角色身分工作時,agent_context 的 mentions 列出提到它的未讀訊息;以角色身分讀(channel_messages)與回覆(channel_post)要在角色端點上做。
    Facebook 粉絲專頁(fb_pages、fb_posts、fb_insights、fb_drafts)只讀;fb_draft_save 只能新增或修改草稿(以角色身分寫時帶 agent),發佈與排程只能由人在 app 的 AI 自動化 › 粉絲專頁 按。
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
        Tool(name: "overview", title: "整體狀況",
             description: "目前畫面狀態、服務健康、觸發中的告警、進行中的事件、執行中的 CI/CD 與部署、待核准的 AI 請求、值班人員與最近動態。",
             properties: [:], required: [], readOnly: true),
        Tool(name: "list", title: "列出資料",
             description: "列出某個資料集合。where 依欄位篩選(欄位可用 a.b 路徑;陣列欄位只要包含該值即符合;執行者欄位比對 id),search 對整筆資料做不分大小寫的文字搜尋。時間序列與日誌會裁短,完整內容用 get。",
             properties: [
                 "collection": prop("string", "資料集合", enum: collections),
                 "where": ["type": "object", "description": "欄位 = 值,例如 {\"state\":\"firing\",\"service\":\"payments\"}", "additionalProperties": true],
                 "search": prop("string", "文字搜尋"),
                 "limit": prop("integer", "最多幾筆,預設 30,上限 200"),
                 "offset": prop("integer", "略過前幾筆"),
             ], required: ["collection"], readOnly: true),
        Tool(name: "get", title: "取得一筆資料",
             description: "依 id 取得一筆完整資料(含時間序列、工作日誌、事件時間軸)。",
             properties: [
                 "collection": prop("string", "資料集合", enum: collections.filter { $0 != "sprint" }),
                 "id": prop("string", "id,例如 ALT-5121、INC-2041、OPS-142;PR 與 pipeline run 用編號"),
             ], required: ["collection", "id"], readOnly: true),
        Tool(name: "navigate", title: "切換畫面",
             description: "讓 app 切到某個頁面,可選中某個項目(例如告警或事件 id)。也可以切換環境與時間範圍。",
             properties: [
                 "section": prop("string", "頁面", enum: cases(AppSection.self)),
                 "focus_id": prop("string", "要選中的項目 id"),
                 "env": prop("string", "環境篩選", enum: cases(DeployEnv.self)),
                 "time_range": prop("string", "時間範圍", enum: cases(TimeRange.self)),
             ], required: []),
        Tool(name: "alert_update", title: "處理告警",
             description: "確認、解除或靜音告警,可附上 AI 判讀。",
             properties: [
                 "alert_id": prop("string", "告警 id"),
                 "action": prop("string", "動作", enum: ["acknowledge", "resolve", "silence"]),
                 "ai_note": prop("string", "寫進告警的 AI 判讀(選填)"),
             ], required: ["alert_id"]),
        Tool(name: "alert_create", title: "建立告警",
             description: "從外部監控送進一筆觸發中的告警。",
             properties: [
                 "title": prop("string", "標題"),
                 "severity": prop("string", "嚴重度", enum: cases(Severity.self)),
                 "service": prop("string", "服務 id"),
                 "rule": prop("string", "觸發條件,例如 p95 latency > 800ms for 5m"),
                 "value": prop("string", "目前數值"),
                 "ai_note": prop("string", "AI 初步判讀"),
             ], required: ["title", "severity", "service", "rule", "value"]),
        Tool(name: "work_item_create", title: "建立工作項目",
             description: "在看板建立工作項目,可掛到事件的後續工作。",
             properties: [
                 "title": prop("string", "標題"),
                 "type": prop("string", "類型", enum: cases(WorkType.self)),
                 "status": prop("string", "欄位,預設 todo", enum: cases(WorkStatus.self)),
                 "points": prop("integer", "點數,預設 3"),
                 "service": prop("string", "服務 id"),
                 "assignee": prop("string", "負責人 person id"),
                 "incident_id": prop("string", "加到這個事件的後續工作"),
             ], required: ["title", "type"]),
        Tool(name: "work_item_move", title: "移動工作項目",
             description: "把工作項目移到看板的另一欄。",
             properties: [
                 "id": prop("string", "工作項目 id,例如 OPS-142"),
                 "status": prop("string", "目標欄位", enum: cases(WorkStatus.self)),
             ], required: ["id", "status"]),
        Tool(name: "pipeline_update", title: "操作 CI/CD 執行",
             description: "重新執行或取消 pipeline run,或寫入 AI 失敗診斷。",
             properties: [
                 "run_id": prop("integer", "run 編號"),
                 "action": prop("string", "動作", enum: ["retry", "cancel"]),
                 "ai_diagnosis": prop("string", "寫入的 AI 失敗診斷"),
             ], required: ["run_id"]),
        Tool(name: "request_approval", title: "送出待核准請求",
             description: "建立一筆待人核准的 AI 請求(出現在 AI 自動化 › 待核准與總覽)。用在任何需要人決定的變更:擴縮容、回滾、資料庫操作等。回傳請求 id;可之後用 get approvals 查核准結果。",
             properties: [
                 "title": prop("string", "標題"),
                 "action": prop("string", "要執行的指令或變更"),
                 "rationale": prop("string", "理由"),
                 "evidence": strings("依據"),
                 "rollback_plan": prop("string", "回滾方式"),
                 "risk": prop("string", "風險", enum: cases(RiskLevel.self)),
                 "automation": prop("string", "歸屬的自動化 id,預設為 mcp"),
                 "expires_in_minutes": prop("integer", "多久後過期,預設 60"),
             ], required: ["title", "action", "rationale", "rollback_plan", "risk"]),
        Tool(name: "log_append", title: "寫入日誌",
             description: "把日誌寫進監控 › 日誌。",
             properties: [
                 "service": prop("string", "服務 id"),
                 "level": prop("string", "等級", enum: cases(LogLevel.self)),
                 "message": prop("string", "內容"),
                 "host": prop("string", "主機,預設 mcp"),
                 "trace_id": prop("string", "trace id"),
             ], required: ["service", "level", "message"]),
        Tool(name: "activity_post", title: "發到動態牆",
             description: "在總覽的動態牆發一則以「MCP 用戶端」署名的動態。",
             properties: [
                 "text": prop("string", "內容"),
                 "section": prop("string", "點擊後前往的頁面,預設 overview", enum: cases(AppSection.self)),
                 "icon": prop("string", "SF Symbol 名稱,預設 sparkles"),
             ], required: ["text"]),
        Tool(name: "voice_submit", title: "分派旁白語音批次",
             description: "把 shuttle 專案的旁白語音產生分派給 omixor:執行 npm run voice -- <腳本> --batch(Gemini Batch API,約一半價格,最長 24 小時)。立刻回傳工作 id,不用等;omixor 每隔幾分鐘查一次,完成後自動收結果(下載、轉 mp3、上傳 GCS、改寫本機 .voice.json),再由人在 omixor 試聽、按發佈上線。一次只能有一批,其餘排隊。dry_run 只列出會送哪些段落,不呼叫 API、不花錢。",
             properties: [
                 "scripts": strings("web/src/content/live/ 底下的腳本路徑,例如 launch/2.1.json"),
                 "all": prop("boolean", "所有腳本"),
                 "limit": prop("integer", "每份腳本只送前幾段(試做用)"),
                 "dry_run": prop("boolean", "只試算,不送出"),
             ], required: []),
        Tool(name: "voice_jobs", title: "查旁白批次",
             description: "查旁白語音批次的狀態(排隊中、產生中、待發佈、已發佈、失敗…)、變動的旁白清單與紀錄。不給 id 時列出最近的批次。",
             properties: ["id": prop("string", "工作 id,例如 VJ-3")], required: [], readOnly: true),
        Tool(name: "site_visitors", title: "網站訪客統計",
             description: "讀 evoleap 網站的會員與訪客統計(後台「會員」頁同一份資料,GET /api/admin/overview):會員數、訪客數、訪客漏斗(打開 → 載入完成 → 有播放,和後台一樣不含登入門檻與一般頁面)、各課概況、登入門檻、裝置/來源/載入錯誤、從首頁進來的路徑、首頁停留、登入頁、最近沒播放的造訪足跡。需要 omixor 已登入該連線。",
             properties: [
                 "range": prop("string", "時間範圍,預設 1d", enum: ["all", "30m", "12h", "1d", "7d"]),
                 "connection": prop("string", "連線 id,預設 omixor 網站訪客頁選的那個(evoleap)"),
             ], required: [], readOnly: true),
        Tool(name: "server", title: "伺服器與連線資訊",
             description: "目前連線資訊與版本:模式(本機/遠端)、遠端 server 名稱(不含網址)、登入狀態(帳號、角色、token 到期時間)與目前組織,以及 omixor app 與遠端 server 的版本(version、commit、buildTime)。不會顯示任何密鑰。",
             properties: [:], required: [], readOnly: true),
        Tool(name: "orgs", title: "組織",
             description: "列出 omixor 的組織與目前使用中的組織。Issue、虛擬角色與任務、文件櫃、連線都屬於目前的組織;切換組織只能由人在 app 裡做。",
             properties: [:], required: [], readOnly: true),
        Tool(name: "agent_task_delegate", title: "依組織鏈委派任務",
             description: "以角色身分把工作委派給組織鏈上的下游角色(鏈上要有 agent:<自己> → agent:<to>)。任務署名你的角色,links 帶上委派它的那筆任務與其 links,label(目標、任務…)寫在任務說明開頭;對方做完會自動回報給你(替你排一筆「檢視回報」任務)。可以派給誰見 agent_context 的 chain.downstream。",
             properties: [
                 "agent": prop("string", "你自己正在扮演的角色 id 或名稱(委派者)"),
                 "to": prop("string", "接任務的角色 id 或名稱(要是鏈上的下游)"),
                 "title": prop("string", "標題"),
                 "instructions": prop("string", "任務說明(Markdown)"),
                 "priority": prop("string", "優先度,預設 normal", enum: cases(IssuePriority.self)),
                 "task": prop("string", "委派它的來源任務 id(選填;不給就用你的角色進行中的任務)"),
             ], required: ["agent", "to", "title"]),
        Tool(name: "github", title: "GitHub 概況",
             description: "列出 omixor 追蹤的 GitHub repo(目前組織的)、開著的 PR(含檢查結果)與最近的 Actions 執行。資料是 omixor 最近一次讀到的(約 5 分鐘更新一次);refresh=true 先重新讀。只讀。需要人先在 omixor 的 設定 › 程式碼倉庫 登入 GitHub。",
             properties: [
                 "refresh": prop("boolean", "先向 GitHub 重新讀一次"),
                 "repo": prop("string", "只看這個 repo(owner/name)"),
                 "limit": prop("integer", "每個 repo 最多幾筆執行,預設 10"),
             ], required: [], readOnly: true),
        Tool(name: "github_runs", title: "查 GitHub Actions 執行",
             description: "直接向 GitHub 查某個 repo 的 Actions 執行紀錄,可依分支、狀態、workflow 篩選。只讀。",
             properties: [
                 "repo": prop("string", "owner/name;只追蹤一個 repo 時可省略"),
                 "branch": prop("string", "分支"),
                 "status": prop("string", "狀態篩選", enum: ["failure", "success", "in_progress", "queued", "completed", "cancelled"]),
                 "limit": prop("integer", "筆數,預設 20,最多 100"),
             ], required: [], readOnly: true),
        Tool(name: "github_run", title: "GitHub Actions 執行詳情",
             description: "一筆 Actions 執行的工作與步驟;log=true 時附上失敗工作紀錄的最後幾行(找失敗原因用)。只讀。",
             properties: [
                 "id": prop("integer", "執行 id(github / github_runs 的 id)"),
                 "repo": prop("string", "owner/name;只追蹤一個 repo 時可省略"),
                 "log": prop("boolean", "附上失敗工作紀錄的最後幾行"),
                 "lines": prop("integer", "紀錄行數,預設 80,最多 400"),
             ], required: ["id"], readOnly: true),
        Tool(name: "github_pull", title: "GitHub PR 詳情",
             description: "一個 PR 的說明、分支、檢查結果與變更的檔案清單。只讀;要改程式請照工程師角色的流程,在新分支做並等人核准。",
             properties: [
                 "number": prop("integer", "PR 編號"),
                 "repo": prop("string", "owner/name;只追蹤一個 repo 時可省略"),
             ], required: ["number"], readOnly: true),
        Tool(name: "gcp", title: "GCP 概況",
             description: "列出 omixor 設定的 GCP 專案(目前組織的)裡 Cloud Run 服務的狀態、目前版本、流量、網址,以及 Cloud Run Job 最近一次執行。給 service 時另外回傳最近的版本;給 job 時回傳最近的執行。只讀。需要人先在 omixor 的 伺服器與服務 › GCP 登入。",
             properties: [
                 "refresh": prop("boolean", "先重新讀一次"),
                 "service": prop("string", "Cloud Run 服務名稱,例如 shuttle-server"),
                 "job": prop("string", "Cloud Run Job 名稱,例如 shuttle-course-sync"),
             ], required: [], readOnly: true),
        Tool(name: "gcp_logs", title: "查 GCP 日誌",
             description: "從 Cloud Logging 讀 Cloud Run 服務或 Job 的日誌(新的在前)。預設只看 ERROR 以上、最近 24 小時。只讀。",
             properties: [
                 "service": prop("string", "Cloud Run 服務名稱"),
                 "job": prop("string", "Cloud Run Job 名稱(和 service 擇一;都不給就是全部 Cloud Run)"),
                 "severity": prop("string", "最低嚴重度,預設 ERROR", enum: GCP.severities),
                 "hours": prop("integer", "往前幾小時,預設 24,最多 720"),
                 "contains": prop("string", "日誌內容要包含的文字"),
                 "limit": prop("integer", "筆數,預設 50,最多 500"),
             ], required: [], readOnly: true),
        Tool(name: "gcp_metrics", title: "GCP 服務指標",
             description: "從 Cloud Monitoring 讀 Cloud Run 服務的請求量(依 2xx/4xx/5xx)、p95 延遲與執行個體數,含總數與時間序列。只讀。",
             properties: [
                 "service": prop("string", "Cloud Run 服務名稱;只有一個服務時可省略"),
                 "hours": prop("integer", "往前幾小時,預設 24,最多 720"),
             ], required: [], readOnly: true),
        Tool(name: "fb_pages", title: "Facebook 粉絲專頁",
             description: "列出 omixor 連上的 Facebook 粉絲專頁:id、名稱、分類、網址、按讚數、追蹤數、是否在 omixor 管理中、有沒有權杖。只讀。需要人先在 omixor 的 AI 自動化 › 粉絲專頁 設定連線。",
             properties: ["refresh": prop("boolean", "先向 Facebook 重新讀一次專頁資料")], required: [], readOnly: true),
        Tool(name: "fb_posts", title: "Facebook 貼文與成效",
             description: "管理中粉絲專頁的最近貼文(新的在前)與已排程貼文。每篇含心情、留言、分享數與互動合計;insights=true 時附上前 10 篇的觸及、點擊與心情分類(Meta 已淘汰或取不到的指標 status 是 deprecated / unavailable)。只讀。",
             properties: [
                 "page": prop("string", "專頁 id 或名稱;不給就是全部管理中的專頁"),
                 "limit": prop("integer", "每個專頁最多幾篇最近貼文,預設 10,最多 50"),
                 "insights": prop("boolean", "附上每篇的成效指標,預設 true"),
                 "scheduled": prop("boolean", "附上已排程貼文,預設 true"),
             ], required: [], readOnly: true),
        Tool(name: "fb_insights", title: "Facebook 專頁成效",
             description: "粉絲專頁的成效:觸及人數、瀏覽次數、貼文互動、追蹤數、新增追蹤,含期間合計與每期的時間序列。Meta 淘汰或取不到的指標 status 是 deprecated / unavailable,total 為 null。只讀。",
             properties: [
                 "page": prop("string", "專頁 id 或名稱;不給就是全部管理中的專頁"),
                 "period": prop("string", "每個資料點的期間,預設 day", enum: FB.periods),
                 "days": prop("integer", "往前幾天,預設 28,1–90"),
             ], required: [], readOnly: true),
        Tool(name: "fb_drafts", title: "Facebook 草稿",
             description: "給 id 回傳一份貼文草稿;不給則列出草稿(預設只列還沒發佈的,新的在前)。只讀。",
             properties: [
                 "id": prop("string", "草稿 id,例如 FD-3"),
                 "status": prop("string", "draft、scheduled、published 或 all,預設 draft", enum: ["draft", "scheduled", "published", "all"]),
                 "page": prop("string", "只列這個專頁(id 或名稱)的草稿"),
                 "limit": prop("integer", "最多幾筆,預設 30"),
             ], required: [], readOnly: true),
        Tool(name: "fb_draft_save", title: "存 Facebook 草稿",
             description: "新增或修改 Facebook 貼文草稿(不給 id 是新增)。只存草稿,不會發佈:發佈與排程只能由人在 omixor 的 AI 自動化 › 粉絲專頁 按。已排程或已發佈的草稿不能修改。以角色身分工作時帶 agent,草稿會署名該角色。",
             properties: [
                 "id": prop("string", "要修改的草稿 id;不給就是新增"),
                 "page": prop("string", "要發到哪個專頁(id 或名稱);新增時不給就用第一個管理中的專頁"),
                 "message": prop("string", "貼文內容(純文字)"),
                 "link": prop("string", "附上的連結(http 或 https)"),
                 "image_url": prop("string", "圖片網址(http 或 https;有圖片時連結會接在內文後面)"),
                 "proposed_time": prop("string", "建議的發佈時間(ISO 8601,例如 2026-11-20T20:00:00+08:00);只是建議,實際排程由人決定"),
                 "source": prop("string", "草稿從哪裡來,例如 doc:DOC-3、task:T-5"),
                 "agent": prop("string", "以虛擬角色身分工作時填角色 id 或名稱,草稿會署名該角色"),
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
        default: throw MCPToolError("沒有這個工具:\(name)")
        }
    }

    private func items(_ collection: String) throws -> [Any] {
        if Self.collections.contains(collection), !collectionEnabled(collection) {
            let key = Self.collectionFeatures[collection] ?? ""
            throw MCPToolError("\(collection):這個功能已關閉(\(featureName(key)),\(key))。請在 omixor 的 設定 › 功能(或系統頁)打開後再用")
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
        default: throw MCPToolError("沒有這個集合:\(collection)。可用:\(Self.collections.joined(separator: "、"))")
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
        }) else { throw MCPToolError("\(collection) 裡找不到 id「\(id)」") }
        return row
    }

    private func navigate(_ a: MCPArgs) throws -> Any {
        if let env = try a.optionalEnum("env", DeployEnv.self) { app.env = env }
        if let range = try a.optionalEnum("time_range", TimeRange.self) { app.timeRange = range }
        var notice: String?
        if let section = try a.optionalEnum("section", AppSection.self) {
            // 關掉的頁面(系統層沒開放或組織層關了):改去總覽(回傳 notice 說明)
            if !app.go(section, focus: a.string("focus_id")) { notice = app.featureOffText(section.rawValue, name: section.title) + ",已改去總覽" }
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
        guard let i = app.alerts.firstIndex(where: { $0.id == id }) else { throw MCPToolError("找不到告警 \(id)") }
        let action = a.string("action")
        let note = a.string("ai_note")
        if action == nil && note == nil { throw MCPToolError("action 與 ai_note 至少要給一項") }
        if let note { app.alerts[i].aiNote = note }
        switch action {
        case "acknowledge": app.acknowledge(alertId: id, by: .mcp)
        case "resolve": app.resolve(alertId: id, by: .mcp)
        case "silence": app.silence(alertId: id, by: .mcp)
        case nil: break
        case let other?: throw MCPToolError("action 必須是 acknowledge、resolve 或 silence,收到「\(other)」")
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
        if let assignee, app.person(assignee) == nil { throw MCPToolError("找不到 person \(assignee)") }
        var incidentIndex: Int?
        if let incId = a.string("incident_id") {
            guard let i = app.incidents.firstIndex(where: { $0.id == incId }) else { throw MCPToolError("找不到事件 \(incId)") }
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
                throw MCPToolError("已建立 \(item.id),但沒有關聯到事件 \(incId):\(error.localizedDescription)")
            }
        }
        return MCPJSON.object(item)
    }

    private func workItemMove(_ a: MCPArgs) throws -> Any {
        let id = try a.require("id")
        guard let i = app.workItems.firstIndex(where: { $0.id == id }) else { throw MCPToolError("找不到工作項目 \(id)") }
        app.moveWorkItem(id: id, to: try a.requireEnum("status", WorkStatus.self), by: .mcp)
        return MCPJSON.object(app.workItems[i])
    }

    private func pipelineUpdate(_ a: MCPArgs) throws -> Any {
        let id = try a.requireInt("run_id")
        guard let i = app.pipelineRuns.firstIndex(where: { $0.id == id }) else { throw MCPToolError("找不到 pipeline run #\(id)") }
        let action = a.string("action"), diagnosis = a.string("ai_diagnosis")
        if action == nil && diagnosis == nil { throw MCPToolError("action 與 ai_diagnosis 至少要給一項") }
        if let diagnosis { app.pipelineRuns[i].aiDiagnosis = diagnosis }
        switch action {
        case "retry":
            app.retry(runId: id, by: .mcp)
            return ["retried": id, "newRun": compact(MCPJSON.object(app.pipelineRuns[0]))]
        case "cancel":
            let s = app.pipelineRuns[i].status
            guard s == .running || s == .queued || s == .waitingApproval else {
                throw MCPToolError("#\(id) 目前是「\(s.label)」,不能取消")
            }
            app.cancel(runId: id, by: .mcp)
        case nil: break
        case let other?: throw MCPToolError("action 必須是 retry 或 cancel,收到「\(other)」")
        }
        return compact(MCPJSON.object(app.pipelineRuns[i]))
    }

    private func requestApproval(_ a: MCPArgs) throws -> Any {
        let automation = a.string("automation") ?? Actor.mcpId
        if automation != Actor.mcpId && app.automation(automation) == nil { throw MCPToolError("找不到自動化 \(automation)") }
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
        guard let voice = app.voice else { throw MCPToolError("旁白功能還沒啟動") }
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
                    "note": ahead > 0 ? "前面還有 \(ahead) 批,輪到時自動送出" : "馬上送出。完成後由人在 omixor › CI/CD › 旁白發佈 試聽並發佈;可用 voice_jobs 查進度"]
        } catch {
            throw MCPToolError(error.localizedDescription)
        }
    }

    private func voiceJobs(_ a: MCPArgs) throws -> Any {
        if let id = a.string("id") {
            guard let j = app.voiceJobs.first(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else {
                throw MCPToolError("找不到旁白批次 \(id)")
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
                ver["server"] = ["error": "查不到遠端 server 的版本(連不上、逾時或不支援)"]
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
        guard let g = agentStore.agent(name) else { throw MCPToolError("找不到角色 \(name)") }
        return IssueStore.agentPrefix + g.id
    }

    private func siteVisitors(_ a: MCPArgs) async throws -> Any {
        let r = a.string("range") ?? "1d"
        let range = r == "all" ? SiteRange.all : SiteRange(rawValue: r)
        guard let range else { throw MCPToolError("range 必須是 all、30m、12h、1d、7d 之一") }
        let d: SiteOverview
        do {
            d = try await SiteVisitors.fetch(app, connection: a.string("connection"), range: range)
        } catch {
            throw MCPToolError("讀不到訪客統計:\(error.localizedDescription)(請在 omixor 登入對應的連線)")
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
            throw MCPToolError("omixor 還沒登入 GitHub。請人在 omixor 的 設定 › 程式碼倉庫 設定並登入")
        }
        return g
    }

    private func ghRepo(_ a: MCPArgs) throws -> String {
        if let r = a.string("repo") {
            guard let n = GitHub.normalizeRepo(r) else { throw MCPToolError("repo 要寫成 owner/name") }
            return n
        }
        let repos = app.githubSettings.repos
        guard repos.count == 1 else {
            throw MCPToolError(repos.isEmpty ? "還沒追蹤任何 repo,請給 repo(owner/name)" : "請給 repo,目前追蹤:\(repos.joined(separator: "、"))")
        }
        return repos[0]
    }

    private func ghError(_ e: Error) -> MCPToolError {
        MCPToolError("GitHub 回應錯誤:\(e.localizedDescription)")
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
            guard let n = GitHub.normalizeRepo(r) else { throw MCPToolError("repo 要寫成 owner/name") }
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
        guard let id = a.int("id") else { throw MCPToolError("要給 id") }
        do {
            guard let d = try await g.get("/repos/\(ConnHTTP.escapePath(repo))/actions/runs/\(id)") as? [String: Any] else {
                throw MCPToolError("執行資料格式不對")
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
        guard let n = a.int("number") else { throw MCPToolError("要給 number") }
        do { return MCPJSON.object(try await g.pullDetail(repo: repo, number: n)) } catch { throw ghError(error) }
    }

    // MARK: GCP

    private func gcpStore() throws -> GCPStore {
        let g = app.startGCP(timer: false)
        guard g.connection != nil, g.isLoggedIn else {
            throw MCPToolError("omixor 還沒登入 Google Cloud。請人在 omixor 的 伺服器與服務 › GCP 設定並登入")
        }
        guard g.isConfigured else { throw MCPToolError("還沒設定 GCP 專案,請人在 omixor 的 伺服器與服務 › GCP 設定") }
        return g
    }

    private func gcpFail(_ e: Error) -> MCPToolError { MCPToolError("GCP 回應錯誤:" + GCPStore.explain(e)) }

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
        guard GCP.severities.contains(sev) else { throw MCPToolError("severity 必須是 \(GCP.severities.joined(separator: "、")) 之一") }
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
                throw MCPToolError("請給 service,可用:\(app.gcp.services.map(\.id).joined(separator: "、"))")
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

    private func fbConnected() throws -> FacebookStore {
        let f = fb
        guard f.isConnected else { throw MCPToolError(FacebookStore.notConnected) }
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
                throw MCPToolError("找不到專頁 \(ref)。可用:\(app.fbSettings.pages.map { "\($0.name)(\($0.id))" }.joined(separator: "、"))")
            }
            guard FacebookStore.pageToken(p.id) != nil else { throw MCPToolError("專頁「\(p.name)」沒有權杖。" + FacebookStore.notConnected) }
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
        guard FB.periods.contains(period) else { throw MCPToolError("period 必須是 \(FB.periods.joined(separator: "、")) 之一") }
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
            guard let d = f.draft(id) else { throw MCPToolError("找不到草稿 \(id)") }
            return fbDraftJSON(d)
        }
        let status = a.string("status") ?? "draft"
        guard ["draft", "scheduled", "published", "all"].contains(status) else {
            throw MCPToolError("status 必須是 draft、scheduled、published、all 之一")
        }
        var list = app.fbSettings.drafts.filter { status == "all" || $0.status.rawValue == status }
        if let ref = a.string("page"), !ref.isEmpty {
            guard let p = f.page(ref) else { throw MCPToolError("找不到專頁 \(ref)") }
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
                throw MCPToolError("\(k) 必須是 http 或 https 網址")
            }
        }
        var proposed: Date?
        if let t = a.string("proposed_time"), !t.isEmpty {
            let iso = ISO8601DateFormatter()
            guard let d = iso.date(from: t) else { throw MCPToolError("proposed_time 要用 ISO 8601,例如 2026-11-20T20:00:00+08:00") }
            proposed = d
        }
        var pageId: String?
        if let ref = a.string("page"), !ref.isEmpty {
            guard let p = f.page(ref) else {
                throw MCPToolError("找不到專頁 \(ref)。可用:\(app.fbSettings.pages.map { "\($0.name)(\($0.id))" }.joined(separator: "、"))")
            }
            pageId = p.id
        }
        var d: FBDraft
        let isNew: Bool
        if let id = a.string("id"), !id.isEmpty {
            guard let old = f.draft(id) else { throw MCPToolError("找不到草稿 \(id)") }
            guard old.status == .draft else { throw MCPToolError("草稿 \(old.id) \(old.status.label),不能修改") }
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
        if d.isEmpty { throw MCPToolError("草稿至少要有 message、link 或 image_url") }
        let saved = f.saveDraft(d, by: author)
        app.log(f.actor(author), "square.and.pencil", "\(isNew ? "新增" : "修改") Facebook 草稿 \(saved.id)「\(saved.titleLine)」", .automation)
        var o = fbDraftJSON(saved)
        o["note"] = "只存成草稿。發佈或排程要由人在 omixor 的 AI 自動化 › 粉絲專頁 按。"
        return o
    }

    private func serviceId(_ id: String) throws -> String {
        guard app.service(id) != nil else {
            throw MCPToolError("找不到服務 \(id)。可用:\(app.services.map(\.id).joined(separator: "、"))")
        }
        return id
    }
}
