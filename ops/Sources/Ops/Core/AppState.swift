import Foundation
import SwiftUI

/// app 的全域狀態。所有畫面透過 @EnvironmentObject 讀寫這裡;動作也集中在這裡,
/// 之後接真實 API 時只要改這些方法。
@MainActor
final class AppState: ObservableObject {
    // 導覽與全域篩選
    @Published var section: AppSection = .overview
    @Published var env: DeployEnv = .production
    @Published var timeRange: TimeRange = .hours24
    @Published var search = ""
    @Published var showAssistant = true
    @Published var showCommandPalette = false
    /// 跨頁跳轉時要選中的項目(例如從總覽點告警 → 監控頁選中該告警)
    @Published var focusId: String?

    // 資料
    @Published var people: [Person]
    @Published var services: [Service]
    @Published var servers: [Server]
    @Published var repos: [Repository]
    @Published var pullRequests: [PullRequest]
    @Published var workItems: [WorkItem]
    @Published var sprint: Sprint
    @Published var pipelineRuns: [PipelineRun]
    @Published var deployments: [Deployment]
    @Published var alerts: [Alert]
    @Published var alertRules: [AlertRule]
    @Published var logs: [LogEntry]
    @Published var dashboards: [Dashboard]
    @Published var incidents: [Incident]
    @Published var onCall: [OnCallShift]
    @Published var automations: [Automation]
    @Published var automationRuns: [AutomationRun]
    @Published var approvals: [ApprovalRequest]
    @Published var activity: [ActivityItem]
    @Published var integrations: [Integration]

    /// 內建 MCP 伺服器的狀態(MCP/MCPServer.swift)
    @Published var mcp = MCPStatus()
    var mcpServer: MCPServer?

    /// 旁白語音批次(Voice/VoicePipeline.swift)
    @Published var voiceJobs: [VoiceJob] = []

    /// 外部服務的連線(Connections/)
    @Published var connections: [Connection] = []
    @Published var connectionStates: [String: ConnectionState] = [:]
    private(set) var connStore: ConnectionStore?

    func startConnections() {
        guard connStore == nil else { return }
        let s = ConnectionStore(app: self)
        connStore = s
        s.load()
    }
    private(set) var voice: VoicePipeline?

    /// 虛擬角色與任務(Agents/Agents.swift):存在 agents.json,AI 透過 MCP 以角色身分領取
    @Published var agents: [Agent] = []
    @Published var agentTasks: [AgentTask] = []
    private(set) var agentStore: AgentStore?

    @discardableResult
    func startAgents(timer: Bool = true) -> AgentStore {
        if let s = agentStore { return s }
        let s = AgentStore(app: self)
        agentStore = s
        s.load()
        s.start(timer: timer)
        return s
    }

    /// GitHub(GitHub/GitHub.swift):追蹤中 repo 的 PR 與 Actions,只讀
    @Published var github = GHSnapshot()
    @Published var githubSettings = GHSettings()
    private(set) var ghStore: GitHubStore?

    @discardableResult
    func startGitHub(timer: Bool = true) -> GitHubStore {
        if let s = ghStore { return s }
        startConnections()
        let s = GitHubStore(app: self)
        ghStore = s
        s.load()
        s.start(timer: timer)
        return s
    }

    /// GCP(GCP/GCP.swift):Cloud Run、日誌與監控,只讀
    @Published var gcp = GCPSnapshot()
    @Published var gcpSettings = GCPSettings()
    private(set) var gcpStore: GCPStore?

    @discardableResult
    func startGCP(timer: Bool = true) -> GCPStore {
        if let s = gcpStore { return s }
        startConnections()
        let s = GCPStore(app: self)
        gcpStore = s
        s.load()
        s.start(timer: timer)
        return s
    }

    /// LINE 官方帳號(Line/):推播、在 LINE 核准、在 LINE 問狀況
    @Published var lineSettings = LineSettings()
    @Published var line = LineStatus()
    private(set) var lineStore: LineStore?

    @discardableResult
    func startLine(observe: Bool = true, server: Bool = true) -> LineStore {
        if let s = lineStore { return s }
        let s = LineStore(app: self)
        lineStore = s
        s.load()
        s.start(observe: observe, server: server)
        return s
    }

    /// Facebook 粉絲專頁(Facebook/):發文、排程、成效;設定與草稿存在 facebook.json
    @Published var fbSettings = FBSettings()
    @Published var facebook = FBSnapshot()
    private(set) var fbStore: FacebookStore?

    @discardableResult
    func startFacebook() -> FacebookStore {
        if let s = fbStore { return s }
        startConnections()
        let s = FacebookStore(app: self)
        fbStore = s
        s.load()
        return s
    }

    /// 組織(Orgs/Orgs.swift):每個組織有自己的資料夾
    @Published var orgs: [Organization] = []
    @Published var currentOrgId = OrgFiles.defaultId
    var orgsLoaded = false

    /// 文件櫃(Cabinet/Cabinet.swift):存在 cabinet.json
    @Published var cabinetDocs: [CabinetDoc] = []
    var cabinetLoaded = false
    /// 文件櫃結構改了就加一,讓畫面重畫
    @Published var cabinetLayoutVersion = 0
    func startCabinet() { Cabinet.load(self) }

    /// Issue(Issues/Issues.swift):真實資料,存在 issues.json
    @Published var issues: [OpsIssue] = []
    private(set) var issueStore: IssueStore?

    /// 開啟 Issue 功能(畫面與 MCP 第一次用到時呼叫;重複呼叫不會重新讀檔)
    @discardableResult
    func startIssues() -> IssueStore {
        if let s = issueStore { return s }
        let s = IssueStore(app: self)
        issueStore = s
        s.load()
        return s
    }

    func startVoice(timer: Bool = true) {
        guard voice == nil else { return }
        startConnections()
        let v = VoicePipeline(app: self)
        voice = v
        v.start(timer: timer)
    }

    /// 目前登入的使用者
    let me = "tim"

    private var timer: Timer?

    init(data: MockData = MockData.make()) {
        people = data.people
        services = data.services
        servers = data.servers
        repos = data.repos
        pullRequests = data.pullRequests
        workItems = data.workItems
        sprint = data.sprint
        pipelineRuns = data.pipelineRuns
        deployments = data.deployments
        alerts = data.alerts
        alertRules = data.alertRules
        logs = data.logs
        dashboards = data.dashboards
        incidents = data.incidents
        onCall = data.onCall
        automations = data.automations
        automationRuns = data.automationRuns
        approvals = data.approvals
        activity = data.activity
        integrations = data.integrations
    }

    // MARK: - 查詢

    func person(_ id: String) -> Person? { people.first { $0.id == id } }
    func service(_ id: String) -> Service? { services.first { $0.id == id } }
    func server(_ id: String) -> Server? { servers.first { $0.id == id } }
    func automation(_ id: String) -> Automation? { automations.first { $0.id == id } }
    func incident(_ id: String) -> Incident? { incidents.first { $0.id == id } }

    /// 顯示用名稱:人名或 AI 自動化名稱
    func name(of actor: Actor) -> String {
        switch actor {
        case .person(let id): return person(id)?.name ?? id
        case .ai(let id): return automation(id)?.name ?? agents.first { $0.id == id }?.name ?? (id == Actor.mcpId ? "MCP 用戶端" : "AI")
        case .system: return "系統"
        }
    }

    var firingAlerts: [Alert] { alerts.filter { $0.state == .firing }.sorted { $0.severity < $1.severity } }
    var openIncidents: [Incident] { incidents.filter { $0.status != .resolved } }
    var pendingApprovals: [ApprovalRequest] { approvals.filter { $0.state == .pending } }
    var runningPipelines: [PipelineRun] { pipelineRuns.filter { $0.status == .running || $0.status == .queued } }
    var currentOnCall: OnCallShift? { onCall.first { $0.role == "Primary" && $0.start <= .now && $0.end > .now } }

    /// 側邊欄徽章數字
    func badge(for section: AppSection) -> Int? {
        let n: Int
        switch section {
        case .monitoring: n = firingAlerts.count
        case .incidents: n = openIncidents.count
        case .automation: n = pendingApprovals.count + agentTasks.filter { $0.status == .blocked }.count
        case .pipelines: n = runningPipelines.count + voiceJobs.filter { $0.state == .ready }.count
        case .development: n = issues.filter { $0.status == .open && $0.priority <= .high }.count
        default: n = 0
        }
        return n > 0 ? n : nil
    }

    // MARK: - 導覽

    func go(_ section: AppSection, focus id: String? = nil) {
        self.section = section
        focusId = id
    }

    // MARK: - 動作(目前只改本機狀態,並寫進動態牆)

    func acknowledge(alertId: String, by actor: Actor? = nil) {
        let who = actor ?? .person(me)
        guard let i = alerts.firstIndex(where: { $0.id == alertId }) else { return }
        alerts[i].state = .acknowledged
        alerts[i].acknowledgedBy = who
        log(who, "bell.badge", "確認告警「\(alerts[i].title)」", .monitoring)
    }

    func resolve(alertId: String, by actor: Actor? = nil) {
        let who = actor ?? .person(me)
        guard let i = alerts.firstIndex(where: { $0.id == alertId }) else { return }
        alerts[i].state = .resolved
        log(who, "checkmark.circle", "解除告警「\(alerts[i].title)」", .monitoring)
    }

    func silence(alertId: String, by actor: Actor? = nil) {
        let who = actor ?? .person(me)
        guard let i = alerts.firstIndex(where: { $0.id == alertId }) else { return }
        alerts[i].state = .silenced
        log(who, "bell.slash", "靜音告警「\(alerts[i].title)」", .monitoring)
    }

    /// 核准或拒絕 AI 請求。一律由人決定:`person` 是做決定的人(預設目前使用者),`via` 註明經由哪個管道(例如「LINE」)
    func decide(approvalId: String, approve: Bool, by person: String? = nil, via: String? = nil) {
        guard let i = approvals.firstIndex(where: { $0.id == approvalId }) else { return }
        let who = person ?? me
        approvals[i].state = approve ? .approved : .rejected
        approvals[i].decidedBy = who
        log(.person(who), approve ? "checkmark.seal" : "xmark.seal",
            "\(approve ? "核准" : "拒絕") AI 請求「\(approvals[i].title)」" + (via.map { "(經 \($0))" } ?? ""), .automation)
    }

    func setAutonomy(_ autonomy: Autonomy, automationId: String) {
        guard let i = automations.firstIndex(where: { $0.id == automationId }) else { return }
        automations[i].autonomy = autonomy
        log(.person(me), "slider.horizontal.3", "把「\(automations[i].name)」改為\(autonomy.label)", .automation)
    }

    func toggle(automationId: String) {
        guard let i = automations.firstIndex(where: { $0.id == automationId }) else { return }
        automations[i].enabled.toggle()
        log(.person(me), "power", "\(automations[i].enabled ? "啟用" : "停用")「\(automations[i].name)」", .automation)
    }

    func retry(runId: Int, by actor: Actor? = nil) {
        let who = actor ?? .person(me)
        guard let run = pipelineRuns.first(where: { $0.id == runId }) else { return }
        let newId = (pipelineRuns.map(\.id).max() ?? 0) + 1
        var stages = run.stages
        for s in stages.indices {
            stages[s].status = s == 0 ? .running : .queued
            for j in stages[s].jobs.indices {
                stages[s].jobs[j].status = s == 0 ? .running : .queued
                stages[s].jobs[j].duration = nil
            }
        }
        let new = PipelineRun(id: newId, pipeline: run.pipeline, repo: run.repo, branch: run.branch,
                              commitSHA: run.commitSHA, commitMessage: run.commitMessage, trigger: "手動重試",
                              triggeredBy: who, status: .running, stages: stages, startedAt: .now,
                              duration: nil, aiDiagnosis: nil, targetEnv: run.targetEnv)
        pipelineRuns.insert(new, at: 0)
        log(who, "arrow.clockwise", "重新執行 \(run.pipeline) #\(newId)", .pipelines)
    }

    func cancel(runId: Int, by actor: Actor? = nil) {
        let who = actor ?? .person(me)
        guard let i = pipelineRuns.firstIndex(where: { $0.id == runId }) else { return }
        pipelineRuns[i].status = .canceled
        log(who, "stop.circle", "取消 \(pipelineRuns[i].pipeline) #\(runId)", .pipelines)
    }

    func approveDeployment(id: String) {
        guard let i = deployments.firstIndex(where: { $0.id == id }) else { return }
        deployments[i].status = .running
        log(.person(me), "checkmark.seal", "核准部署 \(deployments[i].service) \(deployments[i].version) 到 \(deployments[i].env.label)", .pipelines)
    }

    func rollback(deploymentId: String) {
        guard let d = deployments.first(where: { $0.id == deploymentId }) else { return }
        let new = Deployment(id: UUID().uuidString, service: d.service, version: d.previousVersion,
                             previousVersion: d.version, env: d.env, status: .running, strategy: .rolling,
                             progress: 0.05, canaryPercent: nil, startedAt: .now, finishedAt: nil,
                             by: .person(me), changes: ["回滾到 \(d.previousVersion)"], healthGate: .pending)
        deployments.insert(new, at: 0)
        log(.person(me), "arrow.uturn.backward", "回滾 \(d.service) 到 \(d.previousVersion)", .pipelines)
    }

    func updateIncident(id: String, status: IncidentStatus, note: String? = nil, by actor: Actor? = nil) {
        let who = actor ?? .person(me)
        guard let i = incidents.firstIndex(where: { $0.id == id }) else { return }
        incidents[i].status = status
        if status == .resolved { incidents[i].resolvedAt = .now }
        incidents[i].timeline.append(IncidentEvent(id: UUID(), date: .now, actor: who, kind: .status,
                                                   text: note ?? "狀態改為「\(status.label)」"))
        log(who, "flame", "\(incidents[i].id) 狀態改為\(status.label)", .incidents)
    }

    func addIncidentNote(id: String, text: String, by actor: Actor? = nil) {
        let who = actor ?? .person(me)
        guard let i = incidents.firstIndex(where: { $0.id == id }), !text.isEmpty else { return }
        incidents[i].timeline.append(IncidentEvent(id: UUID(), date: .now, actor: who, kind: .note, text: text))
    }

    func moveWorkItem(id: String, to status: WorkStatus, by actor: Actor? = nil) {
        guard let i = workItems.firstIndex(where: { $0.id == id }) else { return }
        workItems[i].status = status
        if let actor { log(actor, "rectangle.stack", "把 \(id) 移到「\(status.label)」", .development) }
    }

    func setIntegration(id: String, connected: Bool) {
        guard let i = integrations.firstIndex(where: { $0.id == id }) else { return }
        integrations[i].connected = connected
    }

    func log(_ actor: Actor, _ icon: String, _ text: String, _ section: AppSection) {
        activity.insert(ActivityItem(id: UUID(), date: .now, actor: actor, icon: icon, text: text, section: section), at: 0)
    }

    // MARK: - 模擬即時資料

    /// 每 3 秒讓伺服器指標與執行中的流程往前走一點,讓畫面看起來是活的。
    func startLiveUpdates() {
        guard timer == nil else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
    }

    private var tickCount = 0

    private func tick() {
        tickCount += 1
        let now = Date()
        for i in servers.indices {
            let wobble = sin(Double(tickCount + i * 7) / 3) * 4
            servers[i].cpu = min(99, max(2, servers[i].cpu + wobble * 0.5))
            servers[i].memory = min(98, max(5, servers[i].memory + wobble * 0.15))
            servers[i].cpuSeries.append(MetricPoint(date: now, value: servers[i].cpu))
            servers[i].memorySeries.append(MetricPoint(date: now, value: servers[i].memory))
            if servers[i].cpuSeries.count > 120 { servers[i].cpuSeries.removeFirst() }
            if servers[i].memorySeries.count > 120 { servers[i].memorySeries.removeFirst() }
        }
        for i in deployments.indices where deployments[i].status == .running {
            deployments[i].progress = min(1, deployments[i].progress + 0.04)
            if deployments[i].progress >= 1 {
                deployments[i].status = .succeeded
                deployments[i].finishedAt = now
                deployments[i].healthGate = .passed
            }
        }
        for i in pipelineRuns.indices where pipelineRuns[i].status == .running {
            advance(runIndex: i)
        }
    }

    /// 把執行中的 run 往前推一個 job
    private func advance(runIndex i: Int) {
        var run = pipelineRuns[i]
        guard let s = run.stages.firstIndex(where: { $0.status == .running || $0.status == .queued }) else {
            run.status = .succeeded
            run.duration = Date().timeIntervalSince(run.startedAt)
            pipelineRuns[i] = run
            return
        }
        run.stages[s].status = .running
        if let j = run.stages[s].jobs.firstIndex(where: { $0.status == .running || $0.status == .queued }) {
            if run.stages[s].jobs[j].status == .queued {
                run.stages[s].jobs[j].status = .running
            } else {
                run.stages[s].jobs[j].status = .succeeded
                run.stages[s].jobs[j].duration = Double(20 + (j * 13) % 70)
            }
        }
        if run.stages[s].jobs.allSatisfy({ $0.status == .succeeded }) {
            run.stages[s].status = .succeeded
        }
        pipelineRuns[i] = run
    }
}
