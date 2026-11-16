import Foundation

/// 事件的讀寫;畫面與 MCP 共用。事件存在 core server(IssueServerConfig.current:遠端 omixor 或本機守護服務),
/// app.incidents 是伺服器資料的副本(寫入後、切換組織、每隔幾秒重新讀)。
///
/// - 連線還不能用(本機守護服務還沒啟動好或中途掉了)時,宣告與更新一律丟出錯誤(畫面與 MCP 都看得到原因),
///   不會只改記憶體還當成成功;讀取保留畫面上目前的資料
/// - 關聯告警:核心只記告警 id(alertIds),讀回來時把 app.alerts 裡還沒標事件的告警補上 incidentId
/// - 範例資料(MockData+Incidents):本機守護服務的預設組織第一次讀到「伺服器沒有任何事件」時匯入一次當種子
///   (UserDefaults 記著做過了,之後不再匯入);遠端 omixor 與其他組織不匯入
/// - 執行者:App 以 actor 帶畫面上的人(.person(me))或 MCP(.ai("mcp"));核心只接受伺服器管理員指定,
///   遠端 omixor 的一般成員一律記成登入的 email
@MainActor
final class IncidentStore {
    let app: AppState
    init(app: AppState) { self.app = app }

    private var remoteClient: IssueRemote?
    /// 目前連的 core server 的用戶端(連線換了會換新的)
    var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.matches(cfg) { return c }   // 連線設定與組織的 token 範圍都相同才沿用
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }
    /// 連線可以用
    var useServer: Bool { IssueServerConfig.current.unavailable == nil }
    /// 連線不能用時丟出原因(不會只改記憶體)
    private func requireServer() throws {
        if let why = IssueServerConfig.current.unavailable {
            let e = IssueRemoteError(message: why)
            remoteFailed(e)
            throw e
        }
    }
    /// 最近一次和伺服器溝通的錯誤(nil = 正常)
    private(set) var remoteError: String?
    private var poller: Timer?
    /// 多久重新讀一次(秒);0 = 不定時讀
    static var pollInterval: TimeInterval = NSClassFromString("XCTestCase") != nil ? 0 : 5

    private func remoteFailed(_ e: Error) {
        let msg = e.localizedDescription
        if remoteError != msg { NSLog("事件(omixor server):%@", msg) }
        remoteError = msg
    }

    func start() {
        guard poller == nil, Self.pollInterval > 0 else { return }
        poller = Timer.scheduledTimer(withTimeInterval: Self.pollInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.poll() }
        }
    }

    private var polling = false
    /// 定時重讀:請求在背景跑,上一輪還沒回來就跳過這一輪
    private func poll() async {
        guard !polling else { return }
        polling = true
        defer { polling = false }
        await reload()
    }

    /// 在背景讀目前組織的事件,回主執行緒才更新畫面(定時重讀、切換組織用;不擋主執行緒)
    func reload() async {
        guard useServer else { return }
        let org = app.currentOrgId, r = remote, before = app.incidents
        do {
            let list = try await r.run { try $0.listIncidents(org: org) }
            guard app.currentOrgId == org else { return }
            remoteError = nil
            // 讀的期間畫面上的資料被寫入改過:這次讀到的可能比較舊,等下一輪
            guard app.incidents == before else { return }
            apply(list)
        } catch {
            if app.currentOrgId == org { remoteFailed(error) }
        }
    }

    /// 切換組織、模式後重新讀
    func load() { refresh() }

    // MARK: 讀

    /// 重新讀目前組織的事件(連線不能用時保留目前的資料)
    func refresh() {
        guard useServer else { return }
        let org = app.currentOrgId
        do {
            let list = try remote.listIncidents(org: org)
            remoteError = nil
            apply(list)
        } catch { remoteFailed(error) }
    }

    /// 伺服器的資料換進畫面:內容有變才更新;補上告警的 incidentId
    private func apply(_ list: [ServerIncident]) {
        let incs = list.map(\.incident)
        if incs != app.incidents { app.incidents = incs }
        for s in list {
            for aid in s.alertIds {
                if let i = app.alerts.firstIndex(where: { $0.id == aid }), app.alerts[i].incidentId == nil {
                    app.alerts[i].incidentId = s.incident.id
                }
            }
        }
    }

    private func apply(_ one: ServerIncident) {
        if let i = app.incidents.firstIndex(where: { $0.id == one.incident.id }) {
            app.incidents[i] = one.incident
        } else {
            app.incidents.insert(one.incident, at: 0)
        }
        for aid in one.alertIds {
            if let i = app.alerts.firstIndex(where: { $0.id == aid }) { app.alerts[i].incidentId = one.incident.id }
        }
    }

    // MARK: 寫

    /// 宣告事件。timeline 給了就當初始時間軸(否則由核心產生「宣告 SEVn 事件,指揮官:…」與關聯告警)
    @discardableResult
    func declare(title: String, sev: Int, services: [String], commander: String, impact: String,
                 startedAt: Date? = nil, aiSummary: String? = nil, alertIds: [String] = [],
                 timeline: [IncidentEvent]? = nil, by actor: Actor) throws -> Incident {
        let commanderName = app.person(commander)?.name ?? commander
        try requireServer()
        var b: [String: Any] = ["title": title, "sev": sev, "services": services, "commander": commander,
                                "commanderName": commanderName, "impact": impact, "actor": IncidentJSON.actor(actor)]
        if let startedAt { b["startedAt"] = IncidentJSON.iso(startedAt) }
        if let aiSummary, !aiSummary.isEmpty { b["aiSummary"] = aiSummary }
        if !alertIds.isEmpty {
            b["alertIds"] = alertIds
            var titles: [String: String] = [:]
            for id in alertIds { if let a = app.alerts.first(where: { $0.id == id }) { titles[id] = a.title } }
            b["alertTitles"] = titles
        }
        if let timeline, !timeline.isEmpty { b["timeline"] = timeline.map(IncidentJSON.event) }
        do {
            let s = try remote.declareIncident(org: app.currentOrgId, b)
            remoteError = nil
            apply(s)
            return s.incident
        } catch { remoteFailed(error); throw error }
    }

    /// 更新事件(同核心的 IncidentPatch;nil = 不改)。aiSummary 給 "" 是清掉
    @discardableResult
    func update(id: String, status: IncidentStatus? = nil, note: String? = nil, finding: String? = nil,
                aiSummary: String? = nil, impact: String? = nil, addFollowUps: [String] = [], by actor: Actor) throws -> Incident {
        try requireServer()
        var b: [String: Any] = ["actor": IncidentJSON.actor(actor)]
        if let status { b["status"] = status.rawValue }
        if let note { b["note"] = note }
        if let finding { b["finding"] = finding }
        if let aiSummary { b["aiSummary"] = aiSummary }
        if let impact { b["impact"] = impact }
        if !addFollowUps.isEmpty { b["addFollowUps"] = addFollowUps }
        do {
            let s = try remote.updateIncident(org: app.currentOrgId, id: id, b)
            remoteError = nil
            apply(s)
            return s.incident
        } catch { remoteFailed(error); throw error }
    }
}

extension AppState {
    /// 開啟事件功能(啟動時呼叫;重複呼叫不會重新讀)
    @discardableResult
    func startIncidents() -> IncidentStore {
        if let s = incidentStore { return s }
        let s = IncidentStore(app: self)
        incidentStore = s
        s.load()
        s.start()
        return s
    }
}
