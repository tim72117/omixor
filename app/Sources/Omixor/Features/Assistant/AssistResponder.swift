import SwiftUI

/// 規則式回應器:只從 AppState 的資料組出固定格式的文字,不連網、不呼叫模型。
/// 之後接上真正的模型時,替換 `reply(to:)` 即可,面板其他部分不用改。
@MainActor
struct AssistResponder {
    let app: AppState

    // MARK: - 共用查詢

    private func svcName(_ id: String) -> String { app.service(id)?.name ?? id }

    /// 告警的 service 欄位可能是 id 也可能是名稱,兩種都比對
    private func alerts(for s: Service) -> [Alert] {
        app.firingAlerts.filter { $0.service == s.id || $0.service == s.name }
    }

    private var worstLatency: Service? { app.services.max { $0.latencyP95 < $1.latencyP95 } }

    private var failedRuns: [PipelineRun] {
        app.pipelineRuns.filter { $0.status == .failed }.sorted { $0.startedAt > $1.startedAt }
    }

    private var openPRs: [PullRequest] {
        app.pullRequests.filter { [.open, .approved, .changesRequested].contains($0.status) }
    }

    private var hotServers: [Server] {
        app.servers.filter { $0.cpu > 80 || $0.memory > 85 || $0.disk > 85 }.sorted { $0.cpu > $1.cpu }
    }

    private var topIncident: Incident? {
        app.openIncidents.min { ($0.sev, $1.startedAt) < ($1.sev, $0.startedAt) }
    }

    private func since(_ date: Date) -> String {
        let m = max(0, Int(Date().timeIntervalSince(date) / 60))
        if m < 60 { return "\(m) 分鐘" }
        if m < 1440 { return "\(m / 60) 小時 \(m % 60) 分" }
        return "\(m / 1440) 天"
    }

    /// 在使用者的問題裡找到提及的服務(名稱最長者優先,避免 api 吃掉 api-server)
    private func mentionedService(in q: String) -> Service? {
        app.services
            .sorted { $0.name.count > $1.name.count }
            .first { q.contains($0.name.lowercased()) || q.contains($0.id.lowercased()) }
    }

    /// 目前畫面正在看的項目名稱(給情境標籤用)
    var focusLabel: String? {
        guard let f = app.focusId, !f.isEmpty else { return nil }
        if let a = app.alerts.first(where: { $0.id == f }) { return a.title }
        if let i = app.incident(f) { return "\(i.id) \(i.title)" }
        if let s = app.service(f) { return s.name }
        if let s = app.server(f) { return s.name }
        if let a = app.approvals.first(where: { $0.id == f }) { return a.title }
        if let r = app.pipelineRuns.first(where: { String($0.id) == f }) { return "\(r.pipeline) #\(r.id)" }
        if let a = app.automation(f) { return a.name }
        if let d = app.deployments.first(where: { $0.id == f }) { return "\(svcName(d.service)) \(d.version)" }
        if let p = app.pullRequests.first(where: { String($0.id) == f }) { return "PR #\(p.id)" }
        return f
    }

    // MARK: - 目前狀況

    func briefing() -> AssistBriefing {
        switch app.section {
        case .overview: return overviewBrief()
        case .development: return developmentBrief()
        case .pipelines: return pipelinesBrief()
        case .infrastructure: return infraBrief()
        case .monitoring: return monitoringBrief()
        case .incidents: return incidentsBrief()
        case .automation: return automationBrief()
        case .cabinet: return AssistBriefing(headline: "文件櫃", tone: .secondary, lines: [.init(icon: "archivebox", color: .secondary,
                                                            text: "文件櫃有 \(app.cabinetDocs.count) 份文件")])
        case .settings: return settingsBrief()
        case .channels:
            let unread = app.channels.reduce(0) { $0 + $1.unread }
            return AssistBriefing(headline: "對話", tone: .secondary, lines: [.init(icon: "bubble.left.and.bubble.right", color: .secondary,
                                                            text: "\(app.channels.count) 個頻道" + (unread > 0 ? ",\(unread) 則未讀" : ""))])
        }
    }

    private func overviewBrief() -> AssistBriefing {
        var lines: [AssistBriefLine] = []
        let incidents = app.openIncidents, firing = app.firingAlerts
        let bad = app.services.filter { $0.health != .healthy }
        if app.services.isEmpty {
            lines.append(.init(icon: "server.rack", color: .secondary, text: "尚未載入任何服務資料"))
        } else if bad.isEmpty {
            lines.append(.init(icon: "checkmark.circle.fill", color: .green, text: "\(app.services.count) 個服務全部正常"))
        } else {
            let names = bad.prefix(3).map(\.name).joined(separator: "、")
            lines.append(.init(icon: "exclamationmark.triangle.fill", color: .orange,
                               text: "\(bad.count)/\(app.services.count) 個服務異常:\(names)"))
        }
        if !incidents.isEmpty {
            lines.append(.init(icon: "flame.fill", color: .red, text: "\(incidents.count) 起事件處理中"))
        }
        if !firing.isEmpty {
            let crit = firing.filter { $0.severity == .critical }.count
            lines.append(.init(icon: "bell.badge.fill", color: crit > 0 ? .red : .orange,
                               text: "\(firing.count) 則告警觸發中" + (crit > 0 ? "(\(crit) 則 Critical)" : "")))
        }
        if !app.runningPipelines.isEmpty {
            lines.append(.init(icon: "arrow.triangle.2.circlepath", color: .blue, text: "\(app.runningPipelines.count) 條流程執行中"))
        }
        if !app.pendingApprovals.isEmpty {
            lines.append(.init(icon: "hand.raised.fill", color: .purple, text: "\(app.pendingApprovals.count) 個 AI 請求等你核准"))
        }
        if let shift = app.currentOnCall {
            lines.append(.init(icon: "person.badge.clock", color: .secondary,
                               text: "值班:\(app.person(shift.person)?.name ?? shift.person)"))
        }
        let headline: String, tone: Color
        if !incidents.isEmpty { headline = "有事件正在處理"; tone = .red }
        else if !firing.isEmpty || !bad.isEmpty { headline = "有些地方需要留意"; tone = .orange }
        else { headline = "一切正常"; tone = .green }
        return AssistBriefing(headline: headline, tone: tone, lines: lines)
    }

    private func developmentBrief() -> AssistBriefing {
        var lines: [AssistBriefLine] = []
        let prs = openPRs
        let failing = prs.filter { $0.checks == .failed }
        let highRisk = prs.filter { $0.aiReview?.risk == .high }
        lines.append(.init(icon: "arrow.triangle.pull", color: .blue, text: "\(prs.count) 個 PR 尚未合併"))
        if !failing.isEmpty {
            lines.append(.init(icon: "xmark.circle.fill", color: .red, text: "\(failing.count) 個 PR 檢查失敗"))
        }
        if !highRisk.isEmpty {
            lines.append(.init(icon: "exclamationmark.shield.fill", color: .orange,
                               text: "AI 審查標記 \(highRisk.count) 個高風險 PR"))
        }
        let total = app.workItems.reduce(0) { $0 + $1.points }
        let done = app.workItems.filter { $0.status == .done }.reduce(0) { $0 + $1.points }
        if total > 0 {
            lines.append(.init(icon: "chart.bar.fill", color: .indigo,
                               text: "\(app.sprint.name) 完成 \(done)/\(total) 點(\(done * 100 / total)%)"))
        }
        let ai = app.workItems.filter(\.createdByAI).count
        if ai > 0 { lines.append(.init(icon: "sparkles", color: .purple, text: "\(ai) 個工作項目由 AI 建立")) }
        let tone: Color = failing.isEmpty && highRisk.isEmpty ? .green : .orange
        return AssistBriefing(headline: failing.isEmpty ? "開發進度正常" : "有 PR 卡在檢查", tone: tone, lines: lines)
    }

    private func pipelinesBrief() -> AssistBriefing {
        var lines: [AssistBriefLine] = []
        let failed = failedRuns.filter { Date().timeIntervalSince($0.startedAt) < 86400 }
        let waiting = app.deployments.filter { $0.status == .waitingApproval }
        let deploysToday = app.deployments.filter { Calendar.current.isDateInToday($0.startedAt) }
        lines.append(.init(icon: "arrow.triangle.2.circlepath", color: .blue, text: "\(app.runningPipelines.count) 條流程執行中或排隊"))
        if !failed.isEmpty {
            lines.append(.init(icon: "xmark.circle.fill", color: .red, text: "24 小時內 \(failed.count) 次執行失敗"))
            if let f = failed.first {
                lines.append(.init(icon: "sparkles", color: .purple,
                                   text: "最近失敗:\(f.pipeline) #\(f.id)" + (f.aiDiagnosis.map { "— \($0)" } ?? "")))
            }
        }
        if !waiting.isEmpty {
            lines.append(.init(icon: "hand.raised.fill", color: .purple, text: "\(waiting.count) 個部署等待核准"))
        }
        lines.append(.init(icon: "shippingbox.fill", color: .secondary, text: "今天已部署 \(deploysToday.count) 次"))
        return AssistBriefing(headline: failed.isEmpty ? "CI/CD 運作順利" : "有執行失敗待處理",
                              tone: failed.isEmpty ? .green : .red, lines: lines)
    }

    private func infraBrief() -> AssistBriefing {
        var lines: [AssistBriefLine] = []
        let down = app.servers.filter { $0.health == .down }
        let degraded = app.servers.filter { $0.health == .degraded }
        lines.append(.init(icon: "server.rack", color: .secondary,
                           text: "\(app.servers.count) 台伺服器、\(app.services.count) 個服務"))
        if !down.isEmpty {
            lines.append(.init(icon: "xmark.octagon.fill", color: .red, text: "中斷:" + down.prefix(3).map(\.name).joined(separator: "、")))
        }
        if !degraded.isEmpty {
            lines.append(.init(icon: "exclamationmark.triangle.fill", color: .orange,
                               text: "效能下降:" + degraded.prefix(3).map(\.name).joined(separator: "、")))
        }
        if let hot = hotServers.first {
            lines.append(.init(icon: "thermometer.high", color: .orange,
                               text: "資源最吃緊:\(hot.name) CPU \(Int(hot.cpu))%、記憶體 \(Int(hot.memory))%"))
        }
        let restarts = app.servers.flatMap(\.containers).filter { $0.restarts >= 3 }
        if !restarts.isEmpty {
            lines.append(.init(icon: "arrow.counterclockwise", color: .orange, text: "\(restarts.count) 個容器重啟 3 次以上"))
        }
        let ok = down.isEmpty && degraded.isEmpty
        return AssistBriefing(headline: ok ? "基礎設施穩定" : "有主機狀況異常", tone: down.isEmpty ? (ok ? .green : .orange) : .red, lines: lines)
    }

    private func monitoringBrief() -> AssistBriefing {
        let firing = app.firingAlerts
        var lines: [AssistBriefLine] = []
        if firing.isEmpty {
            lines.append(.init(icon: "checkmark.circle.fill", color: .green, text: "沒有觸發中的告警"))
        } else {
            for sev in Severity.allCases {
                let n = firing.filter { $0.severity == sev }.count
                if n > 0 { lines.append(.init(icon: "bell.fill", color: sev.color, text: "\(sev.label) \(n) 則")) }
            }
            let top = firing[0]
            lines.append(.init(icon: "exclamationmark.triangle.fill", color: top.severity.color,
                               text: "最嚴重:\(top.title)(\(svcName(top.service)),\(top.value),已 \(since(top.startedAt)))"))
            if let note = top.aiNote {
                lines.append(.init(icon: "sparkles", color: .purple, text: "AI 判讀:\(note)"))
            }
            let noIncident = firing.filter { $0.incidentId == nil && $0.severity == .critical }.count
            if noIncident > 0 {
                lines.append(.init(icon: "flame", color: .red, text: "\(noIncident) 則 Critical 尚未開事件"))
            }
        }
        let crit = firing.contains { $0.severity == .critical }
        return AssistBriefing(headline: firing.isEmpty ? "監控一切正常" : "\(firing.count) 則告警觸發中",
                              tone: firing.isEmpty ? .green : (crit ? .red : .orange), lines: lines)
    }

    private func incidentsBrief() -> AssistBriefing {
        guard let inc = topIncident else {
            let recent = app.incidents.filter { $0.status == .resolved }.count
            return AssistBriefing(headline: "沒有進行中的事件", tone: .green,
                                  lines: [.init(icon: "checkmark.seal.fill", color: .green, text: "已解決事件共 \(recent) 起")])
        }
        var lines: [AssistBriefLine] = [
            .init(icon: "flame.fill", color: inc.status.color, text: "\(inc.id) SEV\(inc.sev):\(inc.title)"),
            .init(icon: "clock", color: .secondary, text: "\(inc.status.label),已持續 \(since(inc.startedAt))"),
            .init(icon: "person.fill", color: .secondary, text: "指揮官:\(app.person(inc.commander)?.name ?? inc.commander)"),
        ]
        if !inc.services.isEmpty {
            lines.append(.init(icon: "square.stack.3d.up", color: .orange, text: "影響:" + inc.services.map(svcName).joined(separator: "、")))
        }
        if let s = inc.aiSummary { lines.append(.init(icon: "sparkles", color: .purple, text: s)) }
        let others = app.openIncidents.count - 1
        if others > 0 { lines.append(.init(icon: "ellipsis.circle", color: .secondary, text: "另有 \(others) 起事件處理中")) }
        return AssistBriefing(headline: "\(app.openIncidents.count) 起事件處理中", tone: .red, lines: lines)
    }

    private func automationBrief() -> AssistBriefing {
        let pending = app.pendingApprovals
        var lines: [AssistBriefLine] = []
        lines.append(.init(icon: "hand.raised.fill", color: pending.isEmpty ? .secondary : .purple,
                           text: pending.isEmpty ? "沒有等待核准的請求" : "\(pending.count) 個請求等你核准"))
        let high = pending.filter { $0.risk == .high }.count
        if high > 0 { lines.append(.init(icon: "exclamationmark.shield.fill", color: .red, text: "其中 \(high) 個高風險")) }
        let enabled = app.automations.filter(\.enabled)
        lines.append(.init(icon: "sparkles", color: .indigo, text: "\(enabled.count)/\(app.automations.count) 個自動化啟用中"))
        let runs = app.automations.reduce(0) { $0 + $1.runs30d }
        let saved = app.automations.reduce(0) { $0 + $1.timeSavedHours }
        if runs > 0 {
            lines.append(.init(icon: "clock.arrow.circlepath", color: .green,
                               text: "30 天執行 \(runs) 次,省下約 \(Int(saved)) 小時"))
        }
        let runningNow = app.automationRuns.filter { $0.status == .running }.count
        if runningNow > 0 { lines.append(.init(icon: "gearshape.2.fill", color: .blue, text: "\(runningNow) 個 AI 任務執行中")) }
        return AssistBriefing(headline: pending.isEmpty ? "AI 自動化運作中" : "有請求等你決定",
                              tone: pending.isEmpty ? .green : .purple, lines: lines)
    }

    private func settingsBrief() -> AssistBriefing {
        let connected = app.integrations.filter(\.connected).count
        var lines = [AssistBriefLine(icon: "puzzlepiece.extension.fill", color: .indigo,
                                     text: "\(connected)/\(app.integrations.count) 個整合已連接")]
        let off = app.integrations.filter { !$0.connected }.prefix(3).map(\.name)
        if !off.isEmpty { lines.append(.init(icon: "bolt.slash", color: .secondary, text: "未連接:" + off.joined(separator: "、"))) }
        return AssistBriefing(headline: "設定與整合", tone: .secondary, lines: lines)
    }

    // MARK: - 建議問題

    func suggestions() -> [String] {
        let svc = worstLatency?.name ?? "api-server"
        switch app.section {
        case .overview:
            return ["總結目前狀況", "今天有哪些部署?", "有什麼需要我決定?", "誰在值班?"]
        case .development:
            return ["哪些 PR 卡住了?", "這個 Sprint 進度如何?", "AI 審查標出哪些風險?"]
        case .pipelines:
            return ["最近哪些執行失敗?為什麼?", "總結今天的部署", "有部署在等核准嗎?"]
        case .infrastructure:
            return ["哪台伺服器資源最吃緊?", "\(svc) 狀況如何?", "有容器一直重啟嗎?"]
        case .monitoring:
            return ["為什麼 \(svc) 延遲變高?", "列出觸發中的告警", "哪些告警還沒開事件?"]
        case .incidents:
            return ["總結進行中的事件", "草擬事件公告", "這次事件和部署有關嗎?"]
        case .automation:
            return ["有什麼需要我核准?", "AI 自動化省了多少時間?", "哪個自動化最常失敗?"]
        case .cabinet:
            return ["總結目前狀況"]
        case .settings:
            return ["哪些整合還沒連接?", "總結目前狀況"]
        case .channels:
            return ["總結目前狀況"]
        }
    }

    // MARK: - 回答

    func reply(to raw: String) -> AssistReply {
        let q = raw.lowercased()
        func has(_ words: String...) -> Bool { words.contains { q.contains($0) } }

        let svc = mentionedService(in: q)
        if has("延遲", "慢", "latency", "p95", "錯誤率", "為什麼", "error") , let s = svc ?? (has("延遲", "慢", "latency", "p95") ? worstLatency : nil) {
            return diagnose(s)
        }
        if has("公告", "草擬", "status page", "對外") { return announcement() }
        if has("和部署有關", "跟部署有關", "部署有關") { return incidentDeployCorrelation() }
        if has("部署", "deploy", "上線", "release") { return deploymentsSummary() }
        if has("核准", "審核", "approval", "決定", "待辦請求") { return approvalsReply() }
        if has("失敗", "ci", "pipeline", "建置", "build", "流程") {
            if has("自動化") { return automationsReply() }
            return failedRunsReply()
        }
        if has("還沒開事件", "沒開事件") { return alertsReply(onlyWithoutIncident: true) }
        if has("告警", "alert", "警報") { return alertsReply(onlyWithoutIncident: false) }
        if has("事件", "incident", "inc-") { return incidentsReply() }
        if has("伺服器", "server", "cpu", "記憶體", "主機", "容器", "重啟", "資源") { return serversReply() }
        if has("pr", "審查", "review", "合併", "卡住") { return prsReply() }
        if has("sprint", "進度", "衝刺", "工作項目") { return sprintReply() }
        if has("值班", "on-call", "oncall", "on call") { return onCallReply() }
        if has("自動化", "automation", "省", "ai 任務") { return automationsReply() }
        if has("整合", "integration") { return integrationsReply() }
        if let s = svc { return diagnose(s) }
        if has("狀況", "總結", "摘要", "概況", "今天", "現在", "怎麼樣", "如何") { return statusReply() }
        return fallback()
    }

    private func statusReply() -> AssistReply {
        let b = briefing()
        let body = b.lines.map { "• \($0.text)" }.joined(separator: "\n")
        var actions: [AssistAction] = []
        if !app.openIncidents.isEmpty { actions.append(.go(.incidents, focus: topIncident?.id, label: "前往事件")) }
        if !app.firingAlerts.isEmpty { actions.append(.go(.monitoring, focus: app.firingAlerts.first?.id, label: "查看告警")) }
        if !app.pendingApprovals.isEmpty { actions.append(.go(.automation, focus: nil, label: "前往核准")) }
        return AssistReply(text: "**\(app.section.title):\(b.headline)**\n\(body.isEmpty ? "目前沒有資料可以總結。" : body)", actions: actions)
    }

    private func diagnose(_ s: Service) -> AssistReply {
        var parts: [String] = []
        var actions: [AssistAction] = []
        parts.append("**\(s.name)** 目前\(s.health.label):p95 延遲 \(Int(s.latencyP95)) ms、錯誤率 \(Fmt.percent(s.errorRate, digits: 2))、每秒 \(Fmt.number(s.rps)) 次請求。")
        parts.append("30 天可用率 \(Fmt.percent(s.uptime30d, digits: 2))(目標 \(Fmt.percent(s.sloTarget, digits: 2))),錯誤預算剩 \(Int(s.errorBudgetRemaining * 100))%。")

        var causes: [String] = []
        let related = alerts(for: s)
        if let a = related.first {
            causes.append("告警「\(a.title)」觸發中(\(a.rule),目前 \(a.value),已 \(since(a.startedAt)))")
            if let note = a.aiNote { causes.append("AI 初步判讀:\(note)") }
            actions.append(.acknowledge(alertId: a.id, title: a.title))
            actions.append(.go(.monitoring, focus: a.id, label: "查看告警"))
        }
        if let d = app.deployments.filter({ $0.service == s.id && Date().timeIntervalSince($0.startedAt) < 6 * 3600 })
            .max(by: { $0.startedAt < $1.startedAt }) {
            causes.append("\(since(d.startedAt))前部署了 \(d.version)(前一版 \(d.previousVersion),\(d.env.label)),時間點接近,可能相關")
            actions.append(.go(.pipelines, focus: d.id, label: "查看部署"))
        }
        let hosts = s.hostIds.compactMap(app.server).filter { $0.cpu > 80 || $0.memory > 85 }
        if let h = hosts.first { causes.append("主機 \(h.name) CPU \(Int(h.cpu))%、記憶體 \(Int(h.memory))%,資源吃緊") }
        let deps = s.dependencies.compactMap(app.service).filter { $0.health != .healthy }
        if !deps.isEmpty { causes.append("依賴的服務異常:" + deps.map { "\($0.name)(\($0.health.label))" }.joined(separator: "、")) }
        if let inc = app.openIncidents.first(where: { $0.services.contains(s.id) }) {
            causes.append("已有事件 \(inc.id) 在處理(\(inc.status.label))")
            actions.append(.go(.incidents, focus: inc.id, label: "前往事件"))
        }

        if causes.isEmpty {
            parts.append("目前沒有找到相關告警、近期部署或主機壓力,數字看起來在正常範圍內。")
        } else {
            parts.append("可能原因:\n" + causes.map { "• \($0)" }.joined(separator: "\n"))
        }
        actions.append(.go(.infrastructure, focus: s.id, label: "查看服務"))
        return AssistReply(text: parts.joined(separator: "\n"), actions: actions)
    }

    private func announcement() -> AssistReply {
        guard let inc = topIncident else {
            return AssistReply(text: "目前沒有進行中的事件,不需要發布公告。如果要預告維護,可以告訴我時段和影響的服務。")
        }
        let svcs = inc.services.map(svcName).joined(separator: "、")
        let next: String
        switch inc.status {
        case .investigating: next = "我們正在調查原因"
        case .identified: next = "我們已找到原因,正在修復"
        case .monitoring: next = "修復已上線,正在觀察是否恢復穩定"
        case .resolved: next = "問題已解決"
        }
        let draft = """
        【服務狀態更新】\(inc.title)
        自 \(Fmt.time(inc.startedAt)) 起,\(svcs.isEmpty ? "部分服務" : svcs)出現異常。\(inc.impact)
        目前進度:\(next)。下一次更新預計在 30 分鐘內。造成不便,敬請見諒。
        """
        return AssistReply(text: "以下是 \(inc.id) 的公告草稿,發布前請再確認:\n\n\(draft)",
                           actions: [.copy(draft), .go(.incidents, focus: inc.id, label: "前往事件")])
    }

    private func incidentDeployCorrelation() -> AssistReply {
        guard let inc = topIncident else { return AssistReply(text: "目前沒有進行中的事件。") }
        let window = inc.startedAt.addingTimeInterval(-2 * 3600)...inc.startedAt.addingTimeInterval(600)
        let ds = app.deployments.filter { inc.services.contains($0.service) && window.contains($0.startedAt) }
        guard let d = ds.max(by: { $0.startedAt < $1.startedAt }) else {
            return AssistReply(text: "\(inc.id) 開始前 2 小時內,受影響的服務沒有部署紀錄,看起來和部署無關。",
                               actions: [.go(.incidents, focus: inc.id, label: "前往事件")])
        }
        let gap = Int(inc.startedAt.timeIntervalSince(d.startedAt) / 60)
        return AssistReply(
            text: "很可能有關:\(svcName(d.service)) 在事件開始前 \(max(gap, 0)) 分鐘部署了 \(d.version)(前一版 \(d.previousVersion))。\n變更:\(d.changes.prefix(3).joined(separator: "、"))\n建議先確認這次變更,必要時回滾到 \(d.previousVersion)。",
            actions: [.go(.pipelines, focus: d.id, label: "查看部署"), .go(.incidents, focus: inc.id, label: "前往事件")])
    }

    private func deploymentsSummary() -> AssistReply {
        let today = app.deployments.filter { Calendar.current.isDateInToday($0.startedAt) }
        guard !today.isEmpty else { return AssistReply(text: "今天還沒有部署紀錄。") }
        let ok = today.filter { $0.status == .succeeded }.count
        let failed = today.filter { $0.status == .failed }.count
        let running = today.filter { $0.status == .running }.count
        let waiting = today.filter { $0.status == .waitingApproval }
        var lines = ["今天共 \(today.count) 次部署:成功 \(ok)、失敗 \(failed)、進行中 \(running)、等待核准 \(waiting.count)。"]
        for d in today.sorted(by: { $0.startedAt > $1.startedAt }).prefix(5) {
            lines.append("• \(Fmt.time(d.startedAt)) \(svcName(d.service)) \(d.version) → \(d.env.short)(\(d.status.label),\(app.name(of: d.by)))")
        }
        var actions: [AssistAction] = [.go(.pipelines, focus: nil, label: "前往 CI/CD")]
        if let w = waiting.first { actions.insert(.go(.pipelines, focus: w.id, label: "查看待核准部署"), at: 0) }
        return AssistReply(text: lines.joined(separator: "\n"), actions: actions)
    }

    private func approvalsReply() -> AssistReply {
        let pending = app.pendingApprovals
        guard !pending.isEmpty else { return AssistReply(text: "目前沒有等你核准的 AI 請求。") }
        var lines = ["有 \(pending.count) 個 AI 請求等你決定:"]
        for p in pending.prefix(4) {
            lines.append("• \(p.title)(\(p.risk.label),\(Fmt.relative(p.expiresAt))到期)")
        }
        let top = pending.sorted { $0.expiresAt < $1.expiresAt }[0]
        lines.append("\n最快到期的是「\(top.title)」:\(top.rationale)\n回滾方式:\(top.rollbackPlan)")
        return AssistReply(text: lines.joined(separator: "\n"),
                           actions: [.approve(approvalId: top.id), .reject(approvalId: top.id),
                                     .go(.automation, focus: top.id, label: "查看詳情")])
    }

    private func failedRunsReply() -> AssistReply {
        let failed = failedRuns
        guard !failed.isEmpty else {
            return AssistReply(text: "最近沒有失敗的執行。\(app.runningPipelines.count) 條流程正在執行或排隊。",
                               actions: [.go(.pipelines, focus: nil, label: "前往 CI/CD")])
        }
        var lines = ["最近 \(failed.count) 次執行失敗:"]
        for r in failed.prefix(3) {
            let stage = r.stages.first { $0.status == .failed }?.name
            lines.append("• \(r.pipeline) #\(r.id)" + (stage.map { "(卡在 \($0))" } ?? "") + ":\(r.commitMessage)")
            if let d = r.aiDiagnosis { lines.append("  AI 分析:\(d)") }
        }
        let top = failed[0]
        return AssistReply(text: lines.joined(separator: "\n"),
                           actions: [.retry(runId: top.id), .go(.pipelines, focus: String(top.id), label: "查看執行")])
    }

    private func alertsReply(onlyWithoutIncident: Bool) -> AssistReply {
        var list = app.firingAlerts
        if onlyWithoutIncident { list = list.filter { $0.incidentId == nil } }
        guard !list.isEmpty else {
            return AssistReply(text: onlyWithoutIncident ? "所有觸發中的告警都已關聯到事件。" : "目前沒有觸發中的告警。")
        }
        var lines = [onlyWithoutIncident ? "\(list.count) 則告警還沒有關聯事件:" : "\(list.count) 則告警觸發中:"]
        for a in list.prefix(5) {
            lines.append("• [\(a.severity.label)] \(a.title) — \(svcName(a.service)),\(a.value),已 \(since(a.startedAt))")
        }
        if list.count > 5 { lines.append("…還有 \(list.count - 5) 則") }
        let top = list[0]
        return AssistReply(text: lines.joined(separator: "\n"),
                           actions: [.acknowledge(alertId: top.id, title: top.title), .go(.monitoring, focus: top.id, label: "查看告警")])
    }

    private func incidentsReply() -> AssistReply {
        let open = app.openIncidents
        guard !open.isEmpty else { return AssistReply(text: "目前沒有進行中的事件。") }
        var lines = ["\(open.count) 起事件處理中:"]
        for i in open.sorted(by: { $0.sev < $1.sev }) {
            lines.append("• \(i.id) SEV\(i.sev) \(i.title)(\(i.status.label),\(since(i.startedAt)),指揮官 \(app.person(i.commander)?.name ?? i.commander))")
        }
        if let top = topIncident {
            if let s = top.aiSummary { lines.append("\nAI 摘要:\(s)") }
            if let last = top.timeline.max(by: { $0.date < $1.date }) {
                lines.append("最新進度(\(Fmt.relative(last.date))):\(last.text)")
            }
            return AssistReply(text: lines.joined(separator: "\n"), actions: [.go(.incidents, focus: top.id, label: "前往事件")])
        }
        return AssistReply(text: lines.joined(separator: "\n"))
    }

    private func serversReply() -> AssistReply {
        guard !app.servers.isEmpty else { return AssistReply(text: "目前沒有伺服器資料。") }
        let hot = hotServers
        var lines: [String] = []
        if hot.isEmpty {
            let top = app.servers.max { $0.cpu < $1.cpu }!
            lines.append("所有 \(app.servers.count) 台伺服器資源都在安全範圍,CPU 最高的是 \(top.name)(\(Int(top.cpu))%)。")
        } else {
            lines.append("\(hot.count) 台伺服器資源吃緊:")
            for s in hot.prefix(4) {
                lines.append("• \(s.name)(\(s.env.short)):CPU \(Int(s.cpu))%、記憶體 \(Int(s.memory))%、磁碟 \(Int(s.disk))%")
            }
        }
        let restarting = app.servers.flatMap { srv in srv.containers.map { (srv, $0) } }.filter { $0.1.restarts >= 3 }
        if !restarting.isEmpty {
            lines.append("重啟次數偏高的容器:" + restarting.prefix(3).map { "\($0.1.name)@\($0.0.name)(\($0.1.restarts) 次)" }.joined(separator: "、"))
        }
        let focus = hot.first ?? app.servers.max { $0.cpu < $1.cpu }
        return AssistReply(text: lines.joined(separator: "\n"),
                           actions: [.go(.infrastructure, focus: focus?.id, label: "查看伺服器")])
    }

    private func prsReply() -> AssistReply {
        let prs = openPRs
        guard !prs.isEmpty else { return AssistReply(text: "目前沒有未合併的 PR。") }
        let stuck = prs.filter { $0.checks == .failed || $0.status == .changesRequested || Date().timeIntervalSince($0.updatedAt) > 2 * 86400 }
        var lines = ["\(prs.count) 個 PR 未合併,其中 \(stuck.count) 個可能卡住:"]
        for p in (stuck.isEmpty ? prs : stuck).prefix(4) {
            var why: [String] = []
            if p.checks == .failed { why.append("檢查失敗") }
            if p.status == .changesRequested { why.append("需修改") }
            if Date().timeIntervalSince(p.updatedAt) > 2 * 86400 { why.append("\(Fmt.relative(p.updatedAt))更新") }
            if let r = p.aiReview, r.risk != .low { why.append("AI:\(r.risk.label)") }
            lines.append("• #\(p.id) \(p.title)" + (why.isEmpty ? "" : "(\(why.joined(separator: "、")))"))
        }
        return AssistReply(text: lines.joined(separator: "\n"), actions: [.go(.development, focus: nil, label: "前往開發進度")])
    }

    private func sprintReply() -> AssistReply {
        let items = app.workItems
        guard !items.isEmpty else { return AssistReply(text: "目前沒有工作項目資料。") }
        let total = items.reduce(0) { $0 + $1.points }
        let done = items.filter { $0.status == .done }.reduce(0) { $0 + $1.points }
        let daysLeft = max(0, Calendar.current.dateComponents([.day], from: .now, to: app.sprint.end).day ?? 0)
        var lines = ["**\(app.sprint.name)**" + (app.sprint.goal.isEmpty ? "" : ":\(app.sprint.goal)"),
                     "完成 \(done)/\(total) 點(\(total > 0 ? done * 100 / total : 0)%),剩 \(daysLeft) 天。"]
        for st in [WorkStatus.inProgress, .review, .todo] {
            let n = items.filter { $0.status == st }.count
            if n > 0 { lines.append("• \(st.label) \(n) 項") }
        }
        let bugs = items.filter { $0.type == .bug && $0.status != .done }.count
        if bugs > 0 { lines.append("• 未完成的 Bug \(bugs) 個") }
        return AssistReply(text: lines.joined(separator: "\n"), actions: [.go(.development, focus: nil, label: "前往開發進度")])
    }

    private func onCallReply() -> AssistReply {
        guard let s = app.currentOnCall else { return AssistReply(text: "目前查不到值班資料。") }
        let p = app.person(s.person)
        var text = "目前主要值班是 **\(p?.name ?? s.person)**" + (p.map { "(\($0.role))" } ?? "") + ",到 \(Fmt.time(s.end))。"
        if let sec = app.onCall.first(where: { $0.role != "Primary" && $0.start <= .now && $0.end > .now }) {
            text += "\n次要值班:\(app.person(sec.person)?.name ?? sec.person)。"
        }
        return AssistReply(text: text, actions: [.go(.incidents, focus: nil, label: "前往事件")])
    }

    private func automationsReply() -> AssistReply {
        let list = app.automations
        guard !list.isEmpty else { return AssistReply(text: "目前沒有設定任何 AI 自動化。") }
        let runs = list.reduce(0) { $0 + $1.runs30d }
        let saved = list.reduce(0) { $0 + $1.timeSavedHours }
        var lines = ["\(list.filter(\.enabled).count)/\(list.count) 個自動化啟用中,30 天共執行 \(runs) 次,省下約 \(Int(saved)) 小時。"]
        if let best = list.max(by: { $0.timeSavedHours < $1.timeSavedHours }) {
            lines.append("• 最省時:\(best.name)(\(Int(best.timeSavedHours)) 小時)")
        }
        if let worst = list.filter({ $0.runs30d > 0 }).min(by: { $0.successRate < $1.successRate }) {
            lines.append("• 成功率最低:\(worst.name)(\(Int(worst.successRate * 100))%,\(worst.autonomy.label))")
        }
        return AssistReply(text: lines.joined(separator: "\n"), actions: [.go(.automation, focus: nil, label: "前往 AI 自動化")])
    }

    private func integrationsReply() -> AssistReply {
        let off = app.integrations.filter { !$0.connected }
        let text = off.isEmpty ? "所有整合都已連接。"
            : "還沒連接的整合:" + off.map { "\($0.name)(\($0.category))" }.joined(separator: "、")
        return AssistReply(text: text, actions: [.go(.settings, focus: nil, label: "前往設定")])
    }

    private func fallback() -> AssistReply {
        let ideas = suggestions().prefix(3).map { "「\($0)」" }.joined(separator: "、")
        return AssistReply(text: "這個問題我還不會回答。我可以從目前的資料回答告警、事件、部署、CI 失敗、伺服器資源、PR、Sprint、值班和 AI 核准相關的問題,例如 \(ideas)。")
    }
}
