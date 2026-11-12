import Foundation

/// MCP(Model Context Protocol)協定層:收 JSON-RPC 訊息、回傳回應,直接讀寫 AppState。
/// 傳輸(HTTP)在 MCPServer.swift;這裡不碰網路,方便測試。
///
/// 透過 MCP 做的變更都記為 `Actor.mcp`,會出現在動態牆與事件時間軸。
/// 依 app 的 AI 治理原則,核准 AI 請求、核准部署、回滾、改自主程度、切換整合
/// 這些決定只能由人在 app 裡做;MCP 用戶端要做這類變更時,用 ops_request_approval 送出待核准請求。
@MainActor
final class MCPHandler {
    let app: AppState

    static let serverName = "ops"
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
            return result(id: id, [
                "protocolVersion": version,
                "capabilities": ["tools": ["listChanged": false]],
                "serverInfo": ["name": Self.serverName, "title": "omixor", "version": Self.serverVersion],
                "instructions": Self.instructions,
            ])
        case "ping":
            return result(id: id, [:])
        case "tools/list":
            return result(id: id, ["tools": Self.tools.map(\.definition)])
        case "tools/call":
            guard let name = params["name"] as? String else {
                return errorResponse(id: id, code: -32602, message: "缺少工具名稱")
            }
            guard Self.tools.contains(where: { $0.name == name }) else {
                return errorResponse(id: id, code: -32602, message: "沒有這個工具:\(name)")
            }
            let args = MCPArgs(raw: params["arguments"] as? [String: Any] ?? [:])
            app.mcp.lastTool = name
            do {
                let out = try await call(name, args)
                return result(id: id, ["content": [["type": "text", "text": MCPJSON.string(out)]], "isError": false])
            } catch let e as MCPToolError {
                return result(id: id, ["content": [["type": "text", "text": e.message]], "isError": true])
            } catch {
                return result(id: id, ["content": [["type": "text", "text": "\(error)"]], "isError": true])
            }
        case "resources/list":
            return result(id: id, ["resources": []])
        case "prompts/list":
            return result(id: id, ["prompts": []])
        default:
            return errorResponse(id: id, code: -32601, message: "不支援的方法:\(method)")
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
    先用 ops_overview 掌握整體狀況,再用 ops_list / ops_get 查細節。
    核准 AI 請求、核准部署、回滾、調整自動化自主程度只能由人在 app 裡決定;需要這類變更時,用 ops_request_approval 建立待核准請求,附上理由、依據與回滾方式。
    GCP(ops_gcp、ops_gcp_logs、ops_gcp_metrics)只讀:看 Cloud Run 狀態、錯誤日誌、請求量與延遲;omixor 不會部署、改流量或執行 Job。
    GitHub(ops_github、ops_github_runs、ops_github_run、ops_github_pull)只讀:看 PR、檢查與 Actions 失敗原因;omixor 不會開 PR、留言或重跑 workflow。
    Issue、虛擬角色與任務、文件櫃、連線都屬於目前的組織(ops_orgs 看是哪一個);切換組織只能由人在 app 裡做。
    omixor 有虛擬角色(ops_agents)。使用者請你以某角色身分工作時:可先用 ops_agent_context 取得該角色能讀取的資訊,再用 ops_agent_task_claim 領任務,照回傳的角色設定與任務說明做,過程與結果用 ops_agent_task_report 回報;需要人決定時回報 blocked。以角色身分開 Issue 或留言時,ops_issue_create / ops_issue_update 帶上 agent(角色 id),才會署名該角色。
    文件櫃(ops_cabinet、ops_docs、ops_doc_save)依 evoleap 研發基地的區塊分類;以角色身分工作時一律帶 agent,只能讀寫該角色 docAccess 範圍內的文件。
    發現值得追蹤的問題(例如從 ops_site_visitors 的數字看到異常)時,用 ops_issue_create 存成 Issue;先用 ops_issues 搜尋避免重複,已有就用 ops_issue_update 加留言。
    旁白語音(shuttle 專案的 npm run voice)可以用 ops_voice_submit 分派成批次:omixor 會定時查、收完結果,等人在 app 裡試聽後按發佈。發佈沒有 MCP 工具。
    Facebook 粉絲專頁(ops_fb_pages、ops_fb_posts、ops_fb_insights、ops_fb_drafts)只讀;ops_fb_draft_save 只能新增或修改草稿(以角色身分寫時帶 agent),發佈與排程只能由人在 app 的 AI 自動化 › 粉絲專頁 按。
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

    static let tools: [Tool] = [
        Tool(name: "ops_overview", title: "整體狀況",
             description: "目前畫面狀態、服務健康、觸發中的告警、進行中的事件、執行中的 CI/CD 與部署、待核准的 AI 請求、值班人員與最近動態。",
             properties: [:], required: [], readOnly: true),
        Tool(name: "ops_list", title: "列出資料",
             description: "列出某個資料集合。where 依欄位篩選(欄位可用 a.b 路徑;陣列欄位只要包含該值即符合;執行者欄位比對 id),search 對整筆資料做不分大小寫的文字搜尋。時間序列與日誌會裁短,完整內容用 ops_get。",
             properties: [
                 "collection": prop("string", "資料集合", enum: collections),
                 "where": ["type": "object", "description": "欄位 = 值,例如 {\"state\":\"firing\",\"service\":\"payments\"}", "additionalProperties": true],
                 "search": prop("string", "文字搜尋"),
                 "limit": prop("integer", "最多幾筆,預設 30,上限 200"),
                 "offset": prop("integer", "略過前幾筆"),
             ], required: ["collection"], readOnly: true),
        Tool(name: "ops_get", title: "取得一筆資料",
             description: "依 id 取得一筆完整資料(含時間序列、工作日誌、事件時間軸)。",
             properties: [
                 "collection": prop("string", "資料集合", enum: collections.filter { $0 != "sprint" }),
                 "id": prop("string", "id,例如 ALT-5121、INC-2041、OPS-142;PR 與 pipeline run 用編號"),
             ], required: ["collection", "id"], readOnly: true),
        Tool(name: "ops_navigate", title: "切換畫面",
             description: "讓 app 切到某個頁面,可選中某個項目(例如告警或事件 id)。也可以切換環境與時間範圍。",
             properties: [
                 "section": prop("string", "頁面", enum: cases(AppSection.self)),
                 "focus_id": prop("string", "要選中的項目 id"),
                 "env": prop("string", "環境篩選", enum: cases(DeployEnv.self)),
                 "time_range": prop("string", "時間範圍", enum: cases(TimeRange.self)),
             ], required: []),
        Tool(name: "ops_alert_update", title: "處理告警",
             description: "確認、解除或靜音告警,可附上 AI 判讀。",
             properties: [
                 "alert_id": prop("string", "告警 id"),
                 "action": prop("string", "動作", enum: ["acknowledge", "resolve", "silence"]),
                 "ai_note": prop("string", "寫進告警的 AI 判讀(選填)"),
             ], required: ["alert_id"]),
        Tool(name: "ops_alert_create", title: "建立告警",
             description: "從外部監控送進一筆觸發中的告警。",
             properties: [
                 "title": prop("string", "標題"),
                 "severity": prop("string", "嚴重度", enum: cases(Severity.self)),
                 "service": prop("string", "服務 id"),
                 "rule": prop("string", "觸發條件,例如 p95 latency > 800ms for 5m"),
                 "value": prop("string", "目前數值"),
                 "ai_note": prop("string", "AI 初步判讀"),
             ], required: ["title", "severity", "service", "rule", "value"]),
        Tool(name: "ops_incident_declare", title: "宣告事件",
             description: "宣告新事件,回傳事件 id。可關聯告警。",
             properties: [
                 "title": prop("string", "標題"),
                 "sev": prop("integer", "嚴重度 1–4,1 最嚴重"),
                 "services": strings("受影響的服務 id"),
                 "impact": prop("string", "對使用者的影響"),
                 "commander": prop("string", "指揮官 person id,預設為目前的 Primary 值班"),
                 "alert_ids": strings("關聯的告警 id"),
             ], required: ["title", "sev", "impact"]),
        Tool(name: "ops_incident_update", title: "更新事件",
             description: "更新事件狀態、在時間軸加註記或 AI 發現、或寫入 AI 摘要。至少給一項。",
             properties: [
                 "incident_id": prop("string", "事件 id"),
                 "status": prop("string", "新狀態", enum: cases(IncidentStatus.self)),
                 "note": prop("string", "時間軸註記"),
                 "finding": prop("string", "加到時間軸的 AI 發現"),
                 "ai_summary": prop("string", "取代事件的 AI 摘要"),
                 "impact": prop("string", "更新影響說明"),
             ], required: ["incident_id"]),
        Tool(name: "ops_work_item_create", title: "建立工作項目",
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
        Tool(name: "ops_work_item_move", title: "移動工作項目",
             description: "把工作項目移到看板的另一欄。",
             properties: [
                 "id": prop("string", "工作項目 id,例如 OPS-142"),
                 "status": prop("string", "目標欄位", enum: cases(WorkStatus.self)),
             ], required: ["id", "status"]),
        Tool(name: "ops_pipeline_update", title: "操作 CI/CD 執行",
             description: "重新執行或取消 pipeline run,或寫入 AI 失敗診斷。",
             properties: [
                 "run_id": prop("integer", "run 編號"),
                 "action": prop("string", "動作", enum: ["retry", "cancel"]),
                 "ai_diagnosis": prop("string", "寫入的 AI 失敗診斷"),
             ], required: ["run_id"]),
        Tool(name: "ops_request_approval", title: "送出待核准請求",
             description: "建立一筆待人核准的 AI 請求(出現在 AI 自動化 › 待核准與總覽)。用在任何需要人決定的變更:擴縮容、回滾、資料庫操作等。回傳請求 id;可之後用 ops_get approvals 查核准結果。",
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
        Tool(name: "ops_log_append", title: "寫入日誌",
             description: "把日誌寫進監控 › 日誌。",
             properties: [
                 "service": prop("string", "服務 id"),
                 "level": prop("string", "等級", enum: cases(LogLevel.self)),
                 "message": prop("string", "內容"),
                 "host": prop("string", "主機,預設 mcp"),
                 "trace_id": prop("string", "trace id"),
             ], required: ["service", "level", "message"]),
        Tool(name: "ops_activity_post", title: "發到動態牆",
             description: "在總覽的動態牆發一則以「MCP 用戶端」署名的動態。",
             properties: [
                 "text": prop("string", "內容"),
                 "section": prop("string", "點擊後前往的頁面,預設 overview", enum: cases(AppSection.self)),
                 "icon": prop("string", "SF Symbol 名稱,預設 sparkles"),
             ], required: ["text"]),
        Tool(name: "ops_voice_submit", title: "分派旁白語音批次",
             description: "把 shuttle 專案的旁白語音產生分派給 omixor:執行 npm run voice -- <腳本> --batch(Gemini Batch API,約一半價格,最長 24 小時)。立刻回傳工作 id,不用等;omixor 每隔幾分鐘查一次,完成後自動收結果(下載、轉 mp3、上傳 GCS、改寫本機 .voice.json),再由人在 omixor 試聽、按發佈上線。一次只能有一批,其餘排隊。dry_run 只列出會送哪些段落,不呼叫 API、不花錢。",
             properties: [
                 "scripts": strings("web/src/content/live/ 底下的腳本路徑,例如 launch/2.1.json"),
                 "all": prop("boolean", "所有腳本"),
                 "limit": prop("integer", "每份腳本只送前幾段(試做用)"),
                 "dry_run": prop("boolean", "只試算,不送出"),
             ], required: []),
        Tool(name: "ops_voice_jobs", title: "查旁白批次",
             description: "查旁白語音批次的狀態(排隊中、產生中、待發佈、已發佈、失敗…)、變動的旁白清單與紀錄。不給 id 時列出最近的批次。",
             properties: ["id": prop("string", "工作 id,例如 VJ-3")], required: [], readOnly: true),
        Tool(name: "ops_site_visitors", title: "網站訪客統計",
             description: "讀 evoleap 網站的會員與訪客統計(後台「會員」頁同一份資料,GET /api/admin/overview):會員數、訪客數、訪客漏斗(打開 → 載入完成 → 有播放,和後台一樣不含登入門檻與一般頁面)、各課概況、登入門檻、裝置/來源/載入錯誤、從首頁進來的路徑、首頁停留、登入頁、最近沒播放的造訪足跡。需要 omixor 已登入該連線。",
             properties: [
                 "range": prop("string", "時間範圍,預設 1d", enum: ["all", "30m", "12h", "1d", "7d"]),
                 "connection": prop("string", "連線 id,預設 omixor 網站訪客頁選的那個(evoleap)"),
             ], required: [], readOnly: true),
        Tool(name: "ops_issue_create", title: "建立 Issue",
             description: "把發現的問題或待辦存成 Issue(omixor 的真實資料,存在本機,重開 app 也還在;出現在 開發 › Issue)。內文用 Markdown,寫清楚現象、依據(數字、網址、時間範圍)與建議。建立前可先用 ops_issues 搜尋,避免重複。",
             properties: [
                 "title": prop("string", "標題,一句話說明問題"),
                 "body": prop("string", "內文(Markdown)"),
                 "priority": prop("string", "優先度,預設 normal", enum: cases(IssuePriority.self)),
                 "labels": strings("標籤,例如 [\"evoleap\", \"訪客\"]"),
                 "source": prop("string", "從哪裡發現的,例如「網站訪客 7d」"),
                 "link": prop("string", "相關網址"),
                 "agent": prop("string", "以虛擬角色身分工作時填角色 id 或名稱,Issue 會署名該角色"),
                 "assignee": prop("string", "指派給的虛擬角色 id;連 omixor server 時伺服器會自動替它排任務(角色必須存在)"),
             ], required: ["title"]),
        Tool(name: "ops_issue_update", title: "更新 Issue",
             description: "更新 Issue:改標題、內文、優先度、標籤、指派,加留言,或關閉/重新打開。只給要改的欄位。",
             properties: [
                 "id": prop("string", "Issue id,例如 ISS-3"),
                 "title": prop("string", "新標題"),
                 "body": prop("string", "取代內文(Markdown)"),
                 "append_body": prop("string", "接在內文後面的文字"),
                 "priority": prop("string", "優先度", enum: cases(IssuePriority.self)),
                 "labels": strings("取代全部標籤"),
                 "add_labels": strings("加上的標籤"),
                 "status": prop("string", "open 重新打開,closed 關閉", enum: cases(IssueStatus.self)),
                 "comment": prop("string", "加一則留言"),
                 "agent": prop("string", "以虛擬角色身分工作時填角色 id 或名稱,留言與變更會署名該角色"),
                 "assignee": prop("string", "改指派給的角色 id(空字串 = 取消指派);連 omixor server 時會自動替新角色排任務、取消舊角色還沒領取的任務"),
             ], required: ["id"]),
        Tool(name: "ops_issues", title: "查 Issue",
             description: "給 id 回傳完整內容(含留言);不給 id 則列出符合條件的 Issue(預設只列未解決,依優先度與時間排序)。",
             properties: [
                 "id": prop("string", "Issue id"),
                 "status": prop("string", "open、closed 或 all,預設 open", enum: ["open", "closed", "all"]),
                 "label": prop("string", "只列有這個標籤的"),
                 "query": prop("string", "標題、內文或留言包含這段文字"),
                 "limit": prop("integer", "最多幾筆,預設 30"),
             ], required: [], readOnly: true),
        Tool(name: "ops_agents", title: "列出虛擬角色",
             description: "列出 omixor 的虛擬角色:id、名稱、職責、角色設定(instructions)、例行任務,以及各狀態的任務數。以角色身分工作前先讀它的 instructions。",
             properties: ["id": prop("string", "只看這個角色")], required: [], readOnly: true),
        Tool(name: "ops_agent_context", title: "角色能讀取的資訊",
             description: "用角色 id 或名稱,一次取得這個角色工作時能讀取的資訊:角色設定、未完成的任務、文件櫃中能存取的區塊與頁籤(read / write),以及能讀的文件清單。include_bodies 為 true 時附上文件全文(最多 20 份,其餘只列清單)。文件權限依角色的 docAccess;Issue、範例資料等其他工具不分角色。",
             properties: [
                 "agent": prop("string", "角色 id 或名稱"),
                 "include_bodies": prop("boolean", "附上文件全文,預設 false"),
             ], required: ["agent"], readOnly: true),
        Tool(name: "ops_agent_tasks", title: "查角色任務",
             description: "給 id 回傳任務完整內容;否則列出任務(預設未完成的:待領取、進行中、需要協助)。",
             properties: [
                 "id": prop("string", "任務 id,例如 T-3"),
                 "agent": prop("string", "只列這個角色的"),
                 "status": prop("string", "open(預設)、all 或單一狀態",
                                enum: ["open", "all"] + cases(AgentTaskStatus.self)),
             ], required: [], readOnly: true),
        Tool(name: "ops_agent_task_claim", title: "以角色身分領取任務",
             description: "以某個虛擬角色的身分領取任務(狀態改成進行中)。給 id 就領那筆;不給就領該角色最優先的待領取任務。回傳角色設定與任務說明,照著做,做完用 ops_agent_task_report 回報。沒有任務時 task 為 null。連 omixor server 時另外回傳 credential(agentId、taskId、secret、expiresAt、endpoint):綁這筆任務的角色憑證,secret 只給這一次,不要寫進 Issue、留言或檔案。",
             properties: [
                 "agent": prop("string", "角色 id 或名稱"),
                 "id": prop("string", "任務 id(選填)"),
             ], required: ["agent"]),
        Tool(name: "ops_agent_task_report", title: "回報任務",
             description: "回報任務進度或結果。note 是過程紀錄;result 是最終結果(Markdown);做完設 status=done,卡住需要人決定設 blocked 並在 note 說明。",
             properties: [
                 "id": prop("string", "任務 id"),
                 "status": prop("string", "新狀態", enum: ["inProgress", "blocked", "done"]),
                 "note": prop("string", "過程紀錄"),
                 "result": prop("string", "結果(Markdown)"),
                 "links": strings("相關的 Issue id 或網址"),
             ], required: ["id"]),
        Tool(name: "ops_agent_upsert", title: "新增或修改角色",
             description: "新增虛擬角色,或修改既有角色(給 id 或名稱)。只給要改的欄位。routines 會取代全部例行任務;add_routines 是加在後面。例行任務格式:{title, instructions, kind: daily|weekly|hourly, hour, minute, weekday(1=週日…7=週六), every_hours, enabled}。",
             properties: [
                 "id": prop("string", "要修改的角色 id 或名稱;新增時可指定英文 id(例如 product-manager),不給就自動產生"),
                 "name": prop("string", "名稱(新增時必填)"),
                 "role": prop("string", "一句話職責"),
                 "instructions": prop("string", "角色設定(給 AI 的說明,Markdown)"),
                 "symbol": prop("string", "SF Symbol 名稱"),
                 "color": prop("string", "顏色", enum: AgentPalette.names),
                 "active": ["type": "boolean", "description": "啟用"],
                 "routines": ["type": "array", "items": ["type": "object"], "description": "取代全部例行任務"],
                 "add_routines": ["type": "array", "items": ["type": "object"], "description": "加上的例行任務"],
                 "doc_access": ["type": "object", "additionalProperties": ["type": "string", "enum": cases(DocAccess.self)],
                                "description": "文件櫃存取範圍,會合併進現有設定:key 是區塊 id(levels、dev、ideas、docs、overview、todo、members、tools)或頁籤網址(例如 /studio/tools、/docs/course-goals),值是 read、write 或 none(移除)。頁籤的設定優先於區塊。"],
             ], required: []),
        Tool(name: "ops_server", title: "伺服器與連線資訊",
             description: "目前連線資訊與版本:模式(本機/遠端)、遠端 server 名稱(不含網址)、登入狀態(帳號、角色、token 到期時間)與目前組織,以及 omixor app 與遠端 server 的版本(version、commit、buildTime)。不會顯示任何密鑰。",
             properties: [:], required: [], readOnly: true),
        Tool(name: "ops_orgs", title: "組織",
             description: "列出 omixor 的組織與目前使用中的組織。Issue、虛擬角色與任務、文件櫃、連線都屬於目前的組織;切換組織只能由人在 app 裡做。",
             properties: [:], required: [], readOnly: true),
        Tool(name: "ops_cabinet", title: "文件櫃結構",
             description: "列出文件櫃的區塊與頁籤(對應 evoleap 研發基地 /studio 的區塊),以及各處有幾份文件。給 agent 時只列該角色能存取的位置並標出權限(read / write)。",
             properties: ["agent": prop("string", "以角色身分查看時填角色 id 或名稱")], required: [], readOnly: true),
        Tool(name: "ops_docs", title: "查文件",
             description: "給 id 回傳文件全文;否則依區塊、頁籤、關鍵字列出文件。給 agent 時只看得到該角色有權限的文件。",
             properties: [
                 "id": prop("string", "文件 id,例如 DOC-3"),
                 "section": prop("string", "區塊 id 或名稱,例如 levels、課程"),
                 "tab": prop("string", "頁籤網址或名稱,例如 /studio/tools、L0、課程目標"),
                 "query": prop("string", "標題、內文或標籤包含這段文字"),
                 "agent": prop("string", "以角色身分查看時填角色 id 或名稱"),
             ], required: [], readOnly: true),
        Tool(name: "ops_doc_save", title: "存文件到文件櫃",
             description: "新增文件(不給 id)或修改文件(給 id)。內容是純文字/Markdown,放在研發基地對應的區塊與頁籤。以角色身分工作時一定要帶 agent:只能寫到該角色「可讀寫」的位置。文件存在本機,重開 omixor 也還在。內文開頭可以有欄位(--- 之間一行一個 key: value),kind 是文件類型:有對應範本(kind: template、for: <類型> 的文件,內含 ```html 版面與 ```css 樣式)時,畫面依範本顯示,存檔前也會檢查範本的 required 欄位。要新增一種類型,存一份範本文件就好。",
             properties: [
                 "id": prop("string", "要修改的文件 id;新增時不給"),
                 "title": prop("string", "標題(新增時必填)"),
                 "body": prop("string", "取代內文(Markdown)"),
                 "append_body": prop("string", "接在內文後面的文字"),
                 "section": prop("string", "區塊 id 或名稱(新增時必填,或只給 tab)"),
                 "tab": prop("string", "頁籤網址或名稱"),
                 "tags": strings("標籤"),
                 "agent": prop("string", "以角色身分工作時填角色 id 或名稱"),
             ], required: []),
        Tool(name: "ops_agent_task_create", title: "指派任務給角色",
             description: "建立一筆任務指派給虛擬角色,等它被領取。assign_to 是接任務的角色;agent 是你自己正在扮演的角色(用來署名,和其他工具的 agent 意思一樣)。",
             properties: [
                 "assign_to": prop("string", "接這個任務的角色 id 或名稱"),
                 "agent": prop("string", "以角色身分工作時填你自己的角色 id(署名用);不是接任務的角色"),
                 "title": prop("string", "標題"),
                 "instructions": prop("string", "任務說明(Markdown)"),
                 "priority": prop("string", "優先度,預設 normal", enum: cases(IssuePriority.self)),
             ], required: ["assign_to", "title"]),
        Tool(name: "ops_github", title: "GitHub 概況",
             description: "列出 omixor 追蹤的 GitHub repo(目前組織的)、開著的 PR(含檢查結果)與最近的 Actions 執行。資料是 omixor 最近一次讀到的(約 5 分鐘更新一次);refresh=true 先重新讀。只讀。需要人先在 omixor 的 開發進度 › GitHub 登入。",
             properties: [
                 "refresh": prop("boolean", "先向 GitHub 重新讀一次"),
                 "repo": prop("string", "只看這個 repo(owner/name)"),
                 "limit": prop("integer", "每個 repo 最多幾筆執行,預設 10"),
             ], required: [], readOnly: true),
        Tool(name: "ops_github_runs", title: "查 GitHub Actions 執行",
             description: "直接向 GitHub 查某個 repo 的 Actions 執行紀錄,可依分支、狀態、workflow 篩選。只讀。",
             properties: [
                 "repo": prop("string", "owner/name;只追蹤一個 repo 時可省略"),
                 "branch": prop("string", "分支"),
                 "status": prop("string", "狀態篩選", enum: ["failure", "success", "in_progress", "queued", "completed", "cancelled"]),
                 "limit": prop("integer", "筆數,預設 20,最多 100"),
             ], required: [], readOnly: true),
        Tool(name: "ops_github_run", title: "GitHub Actions 執行詳情",
             description: "一筆 Actions 執行的工作與步驟;log=true 時附上失敗工作紀錄的最後幾行(找失敗原因用)。只讀。",
             properties: [
                 "id": prop("integer", "執行 id(ops_github / ops_github_runs 的 id)"),
                 "repo": prop("string", "owner/name;只追蹤一個 repo 時可省略"),
                 "log": prop("boolean", "附上失敗工作紀錄的最後幾行"),
                 "lines": prop("integer", "紀錄行數,預設 80,最多 400"),
             ], required: ["id"], readOnly: true),
        Tool(name: "ops_github_pull", title: "GitHub PR 詳情",
             description: "一個 PR 的說明、分支、檢查結果與變更的檔案清單。只讀;要改程式請照工程師角色的流程,在新分支做並等人核准。",
             properties: [
                 "number": prop("integer", "PR 編號"),
                 "repo": prop("string", "owner/name;只追蹤一個 repo 時可省略"),
             ], required: ["number"], readOnly: true),
        Tool(name: "ops_gcp", title: "GCP 概況",
             description: "列出 omixor 設定的 GCP 專案(目前組織的)裡 Cloud Run 服務的狀態、目前版本、流量、網址,以及 Cloud Run Job 最近一次執行。給 service 時另外回傳最近的版本;給 job 時回傳最近的執行。只讀。需要人先在 omixor 的 伺服器與服務 › GCP 登入。",
             properties: [
                 "refresh": prop("boolean", "先重新讀一次"),
                 "service": prop("string", "Cloud Run 服務名稱,例如 shuttle-server"),
                 "job": prop("string", "Cloud Run Job 名稱,例如 shuttle-course-sync"),
             ], required: [], readOnly: true),
        Tool(name: "ops_gcp_logs", title: "查 GCP 日誌",
             description: "從 Cloud Logging 讀 Cloud Run 服務或 Job 的日誌(新的在前)。預設只看 ERROR 以上、最近 24 小時。只讀。",
             properties: [
                 "service": prop("string", "Cloud Run 服務名稱"),
                 "job": prop("string", "Cloud Run Job 名稱(和 service 擇一;都不給就是全部 Cloud Run)"),
                 "severity": prop("string", "最低嚴重度,預設 ERROR", enum: GCP.severities),
                 "hours": prop("integer", "往前幾小時,預設 24,最多 720"),
                 "contains": prop("string", "日誌內容要包含的文字"),
                 "limit": prop("integer", "筆數,預設 50,最多 500"),
             ], required: [], readOnly: true),
        Tool(name: "ops_gcp_metrics", title: "GCP 服務指標",
             description: "從 Cloud Monitoring 讀 Cloud Run 服務的請求量(依 2xx/4xx/5xx)、p95 延遲與執行個體數,含總數與時間序列。只讀。",
             properties: [
                 "service": prop("string", "Cloud Run 服務名稱;只有一個服務時可省略"),
                 "hours": prop("integer", "往前幾小時,預設 24,最多 720"),
             ], required: [], readOnly: true),
        Tool(name: "ops_fb_pages", title: "Facebook 粉絲專頁",
             description: "列出 omixor 連上的 Facebook 粉絲專頁:id、名稱、分類、網址、按讚數、追蹤數、是否在 omixor 管理中、有沒有權杖。只讀。需要人先在 omixor 的 AI 自動化 › 粉絲專頁 設定連線。",
             properties: ["refresh": prop("boolean", "先向 Facebook 重新讀一次專頁資料")], required: [], readOnly: true),
        Tool(name: "ops_fb_posts", title: "Facebook 貼文與成效",
             description: "管理中粉絲專頁的最近貼文(新的在前)與已排程貼文。每篇含心情、留言、分享數與互動合計;insights=true 時附上前 10 篇的觸及、點擊與心情分類(Meta 已淘汰或取不到的指標 status 是 deprecated / unavailable)。只讀。",
             properties: [
                 "page": prop("string", "專頁 id 或名稱;不給就是全部管理中的專頁"),
                 "limit": prop("integer", "每個專頁最多幾篇最近貼文,預設 10,最多 50"),
                 "insights": prop("boolean", "附上每篇的成效指標,預設 true"),
                 "scheduled": prop("boolean", "附上已排程貼文,預設 true"),
             ], required: [], readOnly: true),
        Tool(name: "ops_fb_insights", title: "Facebook 專頁成效",
             description: "粉絲專頁的成效:觸及人數、瀏覽次數、貼文互動、追蹤數、新增追蹤,含期間合計與每期的時間序列。Meta 淘汰或取不到的指標 status 是 deprecated / unavailable,total 為 null。只讀。",
             properties: [
                 "page": prop("string", "專頁 id 或名稱;不給就是全部管理中的專頁"),
                 "period": prop("string", "每個資料點的期間,預設 day", enum: FB.periods),
                 "days": prop("integer", "往前幾天,預設 28,1–90"),
             ], required: [], readOnly: true),
        Tool(name: "ops_fb_drafts", title: "Facebook 草稿",
             description: "給 id 回傳一份貼文草稿;不給則列出草稿(預設只列還沒發佈的,新的在前)。只讀。",
             properties: [
                 "id": prop("string", "草稿 id,例如 FD-3"),
                 "status": prop("string", "draft、scheduled、published 或 all,預設 draft", enum: ["draft", "scheduled", "published", "all"]),
                 "page": prop("string", "只列這個專頁(id 或名稱)的草稿"),
                 "limit": prop("integer", "最多幾筆,預設 30"),
             ], required: [], readOnly: true),
        Tool(name: "ops_fb_draft_save", title: "存 Facebook 草稿",
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
        case "ops_overview": return overview()
        case "ops_list": return try list(a)
        case "ops_get": return try get(a)
        case "ops_navigate": return try navigate(a)
        case "ops_alert_update": return try alertUpdate(a)
        case "ops_alert_create": return try alertCreate(a)
        case "ops_incident_declare": return try incidentDeclare(a)
        case "ops_incident_update": return try incidentUpdate(a)
        case "ops_work_item_create": return try workItemCreate(a)
        case "ops_work_item_move": return try workItemMove(a)
        case "ops_pipeline_update": return try pipelineUpdate(a)
        case "ops_request_approval": return try requestApproval(a)
        case "ops_log_append": return try logAppend(a)
        case "ops_activity_post": return try activityPost(a)
        case "ops_voice_submit": return try await voiceSubmit(a)
        case "ops_voice_jobs": return try voiceJobs(a)
        case "ops_site_visitors": return try await siteVisitors(a)
        case "ops_issue_create": return try issueCreate(a)
        case "ops_issue_update": return try issueUpdate(a)
        case "ops_issues": return try issuesQuery(a)
        case "ops_agents": return try agentsList(a)
        case "ops_agent_context": return try agentContext(a)
        case "ops_agent_tasks": return try agentTasks(a)
        case "ops_agent_task_claim": return try agentClaim(a)
        case "ops_agent_task_report": return try agentReport(a)
        case "ops_agent_task_create": return try agentCreate(a)
        case "ops_agent_upsert": return try agentUpsert(a)
        case "ops_orgs": return orgsInfo()
        case "ops_server": return serverInfo()
        case "ops_cabinet": return try cabinet(a)
        case "ops_docs": return try docs(a)
        case "ops_doc_save": return try docSave(a)
        case "ops_github": return try await githubSummary(a)
        case "ops_github_runs": return try await githubRuns(a)
        case "ops_github_run": return try await githubRun(a)
        case "ops_github_pull": return try await githubPull(a)
        case "ops_gcp": return try await gcpSummary(a)
        case "ops_gcp_logs": return try await gcpLogs(a)
        case "ops_gcp_metrics": return try await gcpMetrics(a)
        case "ops_fb_pages": return try await fbPages(a)
        case "ops_fb_posts": return try await fbPosts(a)
        case "ops_fb_insights": return try await fbInsights(a)
        case "ops_fb_drafts": return try fbDrafts(a)
        case "ops_fb_draft_save": return try fbDraftSave(a)
        default: throw MCPToolError("沒有這個工具:\(name)")
        }
    }

    private func items(_ collection: String) throws -> [Any] {
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
        if let section = try a.optionalEnum("section", AppSection.self) {
            app.go(section, focus: a.string("focus_id"))
        } else if let focus = a.string("focus_id") {
            app.focusId = focus
        }
        return ["section": app.section.rawValue, "focusId": app.focusId as Any? ?? NSNull(),
                "env": app.env.rawValue, "timeRange": app.timeRange.rawValue]
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

    private func incidentDeclare(_ a: MCPArgs) throws -> Any {
        let title = try a.require("title")
        let sev = try a.requireInt("sev")
        guard (1...4).contains(sev) else { throw MCPToolError("sev 必須是 1–4") }
        let services = try (a.strings("services") ?? []).map(serviceId)
        let commander = a.string("commander") ?? app.currentOnCall?.person ?? app.me
        guard app.person(commander) != nil else { throw MCPToolError("找不到 person \(commander)") }
        let alertIds = a.strings("alert_ids") ?? []
        for id in alertIds where !app.alerts.contains(where: { $0.id == id }) { throw MCPToolError("找不到告警 \(id)") }

        let num = (app.incidents.compactMap { Int($0.id.filter(\.isNumber)) }.max() ?? 2000) + 1
        let id = "INC-\(num)"
        var timeline = [IncidentEvent(id: UUID(), date: .now, actor: .mcp, kind: .status,
                                      text: "宣告 SEV\(sev) 事件,指揮官:\(app.person(commander)?.name ?? commander)")]
        for alertId in alertIds {
            if let i = app.alerts.firstIndex(where: { $0.id == alertId }) {
                app.alerts[i].incidentId = id
                timeline.append(IncidentEvent(id: UUID(), date: .now, actor: .mcp, kind: .alert, text: "關聯告警「\(app.alerts[i].title)」"))
            }
        }
        let inc = Incident(id: id, title: title, sev: sev, status: .investigating, services: services, commander: commander,
                           startedAt: .now, resolvedAt: nil, impact: try a.require("impact"), timeline: timeline,
                           aiSummary: nil, followUps: [])
        app.incidents.insert(inc, at: 0)
        app.log(.mcp, "flame.fill", "宣告 SEV\(sev) 事件 \(id)「\(title)」", .incidents)
        return MCPJSON.object(inc)
    }

    private func incidentUpdate(_ a: MCPArgs) throws -> Any {
        let id = try a.require("incident_id")
        guard let i = app.incidents.firstIndex(where: { $0.id == id }) else { throw MCPToolError("找不到事件 \(id)") }
        let status = try a.optionalEnum("status", IncidentStatus.self)
        let note = a.string("note"), finding = a.string("finding")
        let summary = a.string("ai_summary"), impact = a.string("impact")
        if status == nil && note == nil && finding == nil && summary == nil && impact == nil {
            throw MCPToolError("status、note、finding、ai_summary、impact 至少要給一項")
        }
        if let status {
            app.updateIncident(id: id, status: status, note: note, by: .mcp)
        } else if let note {
            app.addIncidentNote(id: id, text: note, by: .mcp)
        }
        if let finding {
            app.incidents[i].timeline.append(IncidentEvent(id: UUID(), date: .now, actor: .mcp, kind: .aiFinding, text: finding))
        }
        if let summary { app.incidents[i].aiSummary = summary }
        if let impact { app.incidents[i].impact = impact }
        return MCPJSON.object(app.incidents[i])
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
        if let i = incidentIndex { app.incidents[i].followUps.append(item.id) }
        app.log(.mcp, "checklist", "建立 \(item.id)「\(title)」", .development)
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
                    "note": ahead > 0 ? "前面還有 \(ahead) 批,輪到時自動送出" : "馬上送出。完成後由人在 omixor › CI/CD › 旁白發佈 試聽並發佈;可用 ops_voice_jobs 查進度"]
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

    // MARK: 虛擬角色

    private var agentStore: AgentStore { app.startAgents() }

    private func agentJSON(_ g: Agent) -> [String: Any] {
        var d = MCPJSON.object(g) as? [String: Any] ?? [:]
        let ts = agentStore.tasks(for: g.id)
        var counts: [String: Int] = [:]
        for t in ts where t.status.isOpen { counts[t.status.rawValue, default: 0] += 1 }
        d["openTasks"] = counts
        d["docAccess"] = docScope(g)
        d["routines"] = g.routines.map { r -> [String: Any] in
            ["title": r.title, "schedule": r.scheduleLabel, "enabled": r.enabled, "instructions": r.instructions]
        }
        return d
    }

    private func taskJSON(_ t: AgentTask) -> [String: Any] {
        var d = MCPJSON.object(t) as? [String: Any] ?? [:]
        d["agentName"] = agentStore.agent(t.agentId)?.name ?? t.agentId
        d["createdBy"] = agentStore.authorName(t.createdBy)
        d["notes"] = t.notes.map { ["author": agentStore.authorName($0.author), "text": $0.text,
                                    "at": ISO8601DateFormatter().string(from: $0.at)] }
        return d
    }

    private func agentsList(_ a: MCPArgs) throws -> Any {
        if let id = a.string("id") {
            guard let g = agentStore.agent(id) else { throw MCPToolError("找不到角色 \(id)") }
            return agentJSON(g)
        }
        return ["agents": app.agents.map(agentJSON)]
    }

    private func agentTasks(_ a: MCPArgs) throws -> Any {
        if let id = a.string("id") {
            guard let t = agentStore.task(id) else { throw MCPToolError("找不到任務 \(id)") }
            return taskJSON(t)
        }
        var list = app.agentTasks
        if let ag = a.string("agent") {
            guard let g = agentStore.agent(ag) else { throw MCPToolError("找不到角色 \(ag)") }
            list = list.filter { $0.agentId == g.id }
        }
        let st = a.string("status") ?? "open"
        if st == "open" { list = list.filter { $0.status.isOpen } }
        else if st != "all" {
            guard let s = AgentTaskStatus(rawValue: st) else { throw MCPToolError("status 不正確:\(st)") }
            list = list.filter { $0.status == s }
        }
        return ["total": list.count, "tasks": list.prefix(50).map { t -> [String: Any] in
            ["id": t.id, "title": t.title, "agent": t.agentId, "status": t.status.rawValue, "priority": t.priority.rawValue,
             "updatedAt": ISO8601DateFormatter().string(from: t.updatedAt)]
        }]
    }

    private func agentClaim(_ a: MCPArgs) throws -> Any {
        let ag = try a.require("agent")
        let c = try agentStore.claimWithCredential(agent: ag, task: a.string("id"))
        let t = c.task
        let g = agentStore.agent(ag) ?? c.agent
        var out: [String: Any] = ["agent": g.map(agentJSON) ?? [:], "task": t.map { taskJSON($0) as Any } ?? NSNull(),
                                  "message": t == nil ? "\(g?.name ?? ag) 目前沒有待領取的任務"
                                      : "已領取,做完用 ops_agent_task_report 回報。開 Issue 或留言時帶 agent: \"\(g?.id ?? ag)\",才會署名這個角色"]
        if let cr = c.credential {
            out["credential"] = ["agentId": cr.agentId, "taskId": cr.taskId, "secret": cr.secret,
                                 "expiresAt": cr.expiresAt, "endpoint": cr.endpoint]
            out["message"] = "已領取。用 credential 連角色端點(\(cr.endpoint),HTTP 標頭 Authorization: Bearer <agentId>:<secret>)以角色身分工作;secret 只給這一次,不要寫進 Issue、留言、任務結果或檔案。至少每小時用 ops_agent_task_report status=inProgress 回報以續約,做完回報 done、需要人決定回報 blocked(之後憑證失效)。"
        }
        return out
    }

    private func agentReport(_ a: MCPArgs) throws -> Any {
        let id = try a.require("id")
        var status: AgentTaskStatus?
        if let s = a.string("status") {
            guard let v = AgentTaskStatus(rawValue: s), [.inProgress, .blocked, .done].contains(v) else {
                throw MCPToolError("status 必須是 inProgress、blocked、done 之一")
            }
            status = v
        }
        let t = try agentStore.report(id, status: status, result: a.raw["result"] as? String, note: a.string("note"), links: a.strings("links"))
        return t.map(taskJSON) ?? [:]
    }

    private func agentUpsert(_ a: MCPArgs) throws -> Any {
        var g: Agent
        let isNew: Bool
        if let id = a.string("id"), let found = agentStore.agent(id) {
            g = found; isNew = false
        } else if let id = a.string("id") {
            // 新增並指定 id:只能用小寫英數與連字號
            guard let name = a.string("name"), !name.isEmpty else { throw MCPToolError("找不到角色 \(id);要新增就一起給 name") }
            guard !id.isEmpty, id.allSatisfy({ ($0.isASCII && ($0.isLowercase || $0.isNumber)) || $0 == "-" }) else {
                throw MCPToolError("新角色的 id 只能用小寫英文、數字與 -")
            }
            g = Agent(id: id, name: name); isNew = true
        } else {
            let name = try a.require("name")
            g = Agent(id: agentStore.newId(for: name), name: name); isNew = true
        }
        if let v = a.string("name"), !v.isEmpty { g.name = v }
        if let v = a.string("role") { g.role = v }
        if let v = a.raw["instructions"] as? String { g.instructions = v }
        if let v = a.string("symbol"), !v.isEmpty { g.symbol = v }
        if let v = a.string("color") {
            guard AgentPalette.names.contains(v) else { throw MCPToolError("color 必須是 \(AgentPalette.names.joined(separator: "、")) 之一") }
            g.color = v
        }
        if let v = a.raw["active"] as? Bool { g.active = v }
        if let list = a.raw["routines"] as? [[String: Any]] { g.routines = try list.map(routine) }
        if let list = a.raw["add_routines"] as? [[String: Any]] { g.routines += try list.map(routine) }
        if let m = a.raw["doc_access"] as? [String: Any] {
            for (k, v) in m {
                guard let level = (v as? String).flatMap(DocAccess.init) else { throw MCPToolError("doc_access 的值必須是 read、write 或 none") }
                let key: String
                if let sec = CabinetLayout.section(k) { key = sec.id }
                else if let (_, t) = try? CabinetLayout.resolve(section: nil, tab: k), let t { key = t.path }
                else { throw MCPToolError("文件櫃沒有「\(k)」這個區塊或頁籤") }
                g.docAccess[key] = level == .none ? nil : level
            }
        }
        do { g = try agentStore.save(agent: g) } catch { throw MCPToolError("角色沒有存起來:\(error.localizedDescription)") }
        app.log(.mcp, "person.badge.plus", "\(isNew ? "新增" : "修改")角色「\(g.name)」", .automation)
        return agentJSON(g)
    }

    private func routine(_ d: [String: Any]) throws -> AgentRoutine {
        let r = MCPArgs(raw: d)
        var x = AgentRoutine(title: try r.require("title"))
        x.instructions = r.raw["instructions"] as? String ?? ""
        x.kind = try r.optionalEnum("kind", RoutineKind.self) ?? .daily
        x.hour = min(23, max(0, r.int("hour") ?? 9))
        x.minute = min(59, max(0, r.int("minute") ?? 0))
        x.weekday = min(7, max(1, r.int("weekday") ?? 2))
        x.everyHours = max(1, r.int("every_hours") ?? 6)
        x.priority = try r.optionalEnum("priority", IssuePriority.self) ?? .normal
        if let e = r.raw["enabled"] as? Bool { x.enabled = e }
        return x
    }

    private func agentCreate(_ a: MCPArgs) throws -> Any {
        guard let ag = a.string("assign_to"), !ag.isEmpty else {
            throw MCPToolError("要用 assign_to 指定接任務的角色(agent 是你自己扮演的角色,用來署名)")
        }
        guard let target = agentStore.agent(ag) else { throw MCPToolError("找不到角色 \(ag)") }
        let by = try issueAuthor(a)
        if by == IssueStore.agentPrefix + target.id {
            throw MCPToolError("不能把任務派給自己(\(target.name));要派給別的角色請改 assign_to")
        }
        let t = agentStore.assign(to: ag, title: try a.require("title"), instructions: a.raw["instructions"] as? String ?? "",
                                  priority: try a.optionalEnum("priority", IssuePriority.self) ?? .normal, by: by)
        if t == nil, let e = agentStore.remoteError { throw MCPToolError("任務沒有派出去:\(e)") }
        return t.map(taskJSON) ?? [:]
    }

    // MARK: 組織

    /// ops_server:連線資訊與版本(對應 web 的 internal/mcp/tools_server.go;不含網址與密鑰)
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

    // MARK: 文件櫃

    /// 角色的存取範圍,給人看得懂的樣子
    private func docScope(_ g: Agent) -> [[String: Any]] {
        g.docAccess.sorted { $0.key < $1.key }.compactMap { k, v in
            if let sec = CabinetLayout.section(k) { return ["section": sec.id, "label": sec.label, "access": v.rawValue] }
            if let t = CabinetLayout.tab(k) { return ["tab": t.path, "label": t.title, "access": v.rawValue] }
            return nil
        }
    }

    private func docAgent(_ a: MCPArgs) throws -> Agent? {
        guard let name = a.string("agent"), !name.isEmpty else { return nil }
        guard let g = agentStore.agent(name) else { throw MCPToolError("找不到角色 \(name)") }
        return g
    }

    private func docJSON(_ d: CabinetDoc, full: Bool) -> [String: Any] {
        var o: [String: Any] = ["id": d.id, "title": d.title, "section": d.section, "location": d.location, "tags": d.tags,
                                "author": Cabinet.authorName(app, d.author),
                                "updatedAt": ISO8601DateFormatter().string(from: d.updatedAt)]
        if let t = d.tab { o["tab"] = t }
        if full { o["body"] = d.body } else { o["chars"] = d.body.count }
        return o
    }

    private func cabinet(_ a: MCPArgs) throws -> Any {
        app.startCabinet()
        let g = try docAgent(a)
        return ["organization": app.currentOrg?.name ?? app.currentOrgId, "sections": cabinetSections(g)]
    }

    /// 文件櫃的區塊與頁籤;給角色時只列它能存取的位置並標出權限
    private func cabinetSections(_ g: Agent?) -> [[String: Any]] {
        func count(_ s: String, _ t: String?) -> Int { app.cabinetDocs.filter { $0.section == s && $0.tab == t }.count }
        var out: [[String: Any]] = []
        for s in CabinetLayout.sections {
            let tabs: [[String: Any]] = s.tabs.compactMap { t in
                let acc = g.map { $0.access(section: s.id, tab: t.path)?.rawValue as Any } ?? "write"
                if g != nil, acc as? String == nil { return nil }
                return ["tab": t.path, "label": t.title, "docs": count(s.id, t.path), "access": acc]
            }
            let secAcc = g.map { $0.access(section: s.id, tab: nil)?.rawValue as Any } ?? "write"
            if g != nil, secAcc as? String == nil, tabs.isEmpty { continue }
            var o: [String: Any] = ["section": s.id, "label": s.label, "studioPath": s.path,
                                    "docs": count(s.id, nil), "tabs": tabs]
            o["access"] = secAcc as? String ?? "none"
            out.append(o)
        }
        return out
    }

    /// 角色能讀取的資訊:角色設定、未完成的任務、文件櫃權限與能讀的文件
    private func agentContext(_ a: MCPArgs) throws -> Any {
        let name = try a.require("agent")
        guard let g = agentStore.agent(name) else { throw MCPToolError("找不到角色 \(name)") }
        app.startCabinet()
        let tasks = app.agentTasks.filter { $0.agentId == g.id && $0.status.isOpen }
        let docs = app.cabinetDocs.filter { g.canRead(section: $0.section, tab: $0.tab) }
        let bodies = a.raw["include_bodies"] as? Bool ?? false
        let bodyLimit = 20
        return [
            "organization": app.currentOrg?.name ?? app.currentOrgId,
            "agent": agentJSON(g),
            "tasks": ["total": tasks.count, "tasks": tasks.prefix(50).map { t -> [String: Any] in
                ["id": t.id, "title": t.title, "status": t.status.rawValue, "priority": t.priority.rawValue,
                 "updatedAt": ISO8601DateFormatter().string(from: t.updatedAt)]
            }],
            "cabinet": cabinetSections(g),
            "docs": ["total": docs.count, "docs": docs.prefix(50).enumerated().map { i, d -> [String: Any] in
                var o = docJSON(d, full: bodies && i < bodyLimit)
                o["access"] = g.canWrite(section: d.section, tab: d.tab) ? "write" : "read"
                return o
            }],
        ]
    }

    private func docs(_ a: MCPArgs) throws -> Any {
        app.startCabinet()
        let g = try docAgent(a)
        func visible(_ d: CabinetDoc) -> Bool { g?.canRead(section: d.section, tab: d.tab) ?? true }
        if let id = a.string("id") {
            guard let d = Cabinet.doc(app, id) else { throw MCPToolError("找不到文件 \(id)") }
            guard visible(d) else { throw MCPToolError("\(g!.name)沒有權限讀「\(d.location)」的文件") }
            return docJSON(d, full: true)
        }
        var list = app.cabinetDocs.filter(visible)
        if a.string("section") != nil || a.string("tab") != nil {
            let (s, t) = try loc(a)
            list = list.filter { $0.section == s.id && (t == nil || $0.tab == t?.path) }
        }
        if let q = a.string("query")?.lowercased(), !q.isEmpty {
            list = list.filter { $0.title.lowercased().contains(q) || $0.body.lowercased().contains(q) || $0.tags.contains { $0.lowercased().contains(q) } }
        }
        return ["total": list.count, "docs": list.prefix(50).map { docJSON($0, full: false) }]
    }

    private func loc(_ a: MCPArgs) throws -> (CabinetSection, CabinetTab?) {
        do { return try CabinetLayout.resolve(section: a.string("section"), tab: a.string("tab")) }
        catch let e as CabinetError { throw MCPToolError(e.message) }
    }

    private func docSave(_ a: MCPArgs) throws -> Any {
        app.startCabinet()
        let g = try docAgent(a)
        let author = g.map { IssueStore.agentPrefix + $0.id } ?? IssueStore.mcpAuthor
        func checkWrite(_ s: String, _ t: String?) throws {
            if let g, !g.canWrite(section: s, tab: t) {
                let where_ = CabinetDoc(id: "", title: "", section: s, tab: t, author: "").location
                throw MCPToolError("\(g.name)沒有權限寫入「\(where_)」;它可以寫的範圍:\(docScope(g).filter { $0["access"] as? String == "write" }.compactMap { $0["label"] as? String }.joined(separator: "、"))")
            }
        }
        if let id = a.string("id") {
            guard let d = Cabinet.doc(app, id) else { throw MCPToolError("找不到文件 \(id)") }
            try checkWrite(d.section, d.tab)
            var newLoc: (CabinetSection, CabinetTab?)?
            if a.string("section") != nil || a.string("tab") != nil {
                newLoc = try loc(a)
                try checkWrite(newLoc!.0.id, newLoc!.1?.path)
            }
            if let t = a.string("title"), t.isEmpty { throw MCPToolError("標題不能是空的") }
            var newBody = a.raw["body"] as? String ?? d.body
            if let b = a.raw["append_body"] as? String, !b.isEmpty { newBody += (newBody.isEmpty ? "" : "\n\n") + b }
            if let e = DocTemplates.validate(newBody, in: app.cabinetDocs) { throw MCPToolError(e) }
            let updated = Cabinet.update(app, d.id) { x in
                if let t = a.string("title") { x.title = t }
                if let b = a.raw["body"] as? String { x.body = b }
                if let b = a.raw["append_body"] as? String, !b.isEmpty { x.body += (x.body.isEmpty ? "" : "\n\n") + b }
                if let l = newLoc { x.section = l.0.id; x.tab = l.1?.path }
                if let tags = a.strings("tags") { x.tags = tags }
            }
            return updated.map { docJSON($0, full: false) } ?? [:]
        }
        let title = try a.require("title")
        let (s, t) = try loc(a)
        try checkWrite(s.id, t?.path)
        let body = a.raw["body"] as? String ?? ""
        if let e = DocTemplates.validate(body, in: app.cabinetDocs) { throw MCPToolError(e) }
        let d = Cabinet.create(app, title: title, body: body, section: s.id, tab: t?.path,
                               tags: a.strings("tags") ?? [], by: author)
        return docJSON(d, full: false)
    }

    // MARK: Issue

    /// Issue 的署名:有給 agent 就是該角色,否則是 MCP 用戶端
    private func issueAuthor(_ a: MCPArgs) throws -> String {
        guard let name = a.string("agent"), !name.isEmpty else { return IssueStore.mcpAuthor }
        guard let g = agentStore.agent(name) else { throw MCPToolError("找不到角色 \(name)") }
        return IssueStore.agentPrefix + g.id
    }

    /// assignee 參數:nil = 沒給;"" = 取消指派;其他換成角色 id(找不到角色就報錯)
    private func issueAssignee(_ a: MCPArgs) throws -> String? {
        guard let v = a.raw["assignee"] as? String else { return nil }
        let s = v.trimmingCharacters(in: .whitespaces)
        if s.isEmpty { return "" }
        guard let g = agentStore.agent(s) else { throw MCPToolError("找不到角色 \(s)") }
        return g.id
    }

    private func issueCreate(_ a: MCPArgs) throws -> Any {
        let store = app.startIssues()
        let author = try issueAuthor(a)
        let issue = store.create(title: try a.require("title"), body: a.raw["body"] as? String ?? "",
                                 priority: try a.optionalEnum("priority", IssuePriority.self) ?? .normal,
                                 labels: a.strings("labels") ?? [], source: a.string("source") ?? "",
                                 link: a.string("link") ?? "", assignee: try issueAssignee(a), by: author)
        // Issue 伺服器模式連不上時,create 回傳沒存起來的草稿(id 是空的)
        if issue.id.isEmpty { throw MCPToolError("Issue 沒有存起來:\(store.remoteError ?? "Issue 伺服器沒有回應")") }
        return issueJSON(issue)
    }

    private func issueUpdate(_ a: MCPArgs) throws -> Any {
        let store = app.startIssues()
        let id = try a.require("id")
        guard store.issue(id) != nil else { throw MCPToolError("找不到 Issue \(id)") }
        let author = try issueAuthor(a)
        let priority = try a.optionalEnum("priority", IssuePriority.self)
        let status = try a.optionalEnum("status", IssueStatus.self)
        if let t = a.string("title"), t.isEmpty { throw MCPToolError("標題不能是空的") }
        let assignee = try issueAssignee(a)
        var updated = store.update(id, by: author) { i in
            if let v = assignee { i.assignee = v.isEmpty ? nil : v }
            if let t = a.string("title") { i.title = t }
            if let b = a.raw["body"] as? String { i.body = b }
            if let b = a.raw["append_body"] as? String, !b.isEmpty { i.body += (i.body.isEmpty ? "" : "\n\n") + b }
            if let p = priority { i.priority = p }
            if let l = a.strings("labels") { i.labels = l }
            if let l = a.strings("add_labels") { i.labels += l }
            if let s = status { i.status = s }
        }
        if let c = a.string("comment") { updated = store.comment(id, c, by: author) }
        if updated == nil, let e = store.remoteError { throw MCPToolError("Issue 沒有改成:\(e)") }
        return updated.map(issueJSON) ?? [:]
    }

    private func issuesQuery(_ a: MCPArgs) throws -> Any {
        let store = app.startIssues()
        if let id = a.string("id") {
            guard let i = store.issue(id) else { throw MCPToolError("找不到 Issue \(id)") }
            return issueJSON(i)
        }
        store.refreshIfStale(0)   // Issue 伺服器模式:清單也用最新的
        let status = a.string("status") ?? "open"
        guard ["open", "closed", "all"].contains(status) else { throw MCPToolError("status 必須是 open、closed、all 之一") }
        let label = a.string("label")?.lowercased()
        let q = a.string("query")?.lowercased() ?? ""
        let list = app.issues.filter { i in
            (status == "all" || i.status.rawValue == status)
                && (label == nil || i.labels.contains { $0.lowercased() == label })
                && (q.isEmpty || i.title.lowercased().contains(q) || i.body.lowercased().contains(q)
                    || i.comments.contains { $0.text.lowercased().contains(q) })
        }
        .sorted { ($0.priority, $1.updatedAt) < ($1.priority, $0.updatedAt) }
        let limit = max(1, a.int("limit") ?? 30)
        return [
            "total": list.count,
            "issues": list.prefix(limit).map { i -> [String: Any] in
                ["id": i.id, "title": i.title, "status": i.status.rawValue, "priority": i.priority.rawValue,
                 "labels": i.labels, "source": i.source, "comments": i.comments.count,
                 "updatedAt": ISO8601DateFormatter().string(from: i.updatedAt)]
            },
        ]
    }

    private func issueJSON(_ i: OpsIssue) -> Any {
        guard var d = MCPJSON.object(i) as? [String: Any] else { return [:] }
        d["createdBy"] = app.issueStore?.authorName(i.createdBy) ?? i.createdBy
        d["comments"] = i.comments.map { c -> [String: Any] in
            ["author": app.issueStore?.authorName(c.author) ?? c.author, "text": c.text,
             "at": ISO8601DateFormatter().string(from: c.at)]
        }
        return d
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
            throw MCPToolError("omixor 還沒登入 GitHub。請人在 omixor 的 開發進度 › GitHub 設定並登入")
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
