import Foundation

// 範例資料分組織(和 Go 版 internal/core/org_mock.go 一樣):
//   - 預設組織(default)用 MockData 的範例資料
//   - 其他組織從空的開始,不帶任何範例資料(沒有 sprint)
//   - 切換組織時把目前組織的資料收起來、換成目標組織的;各組織的變更分開保留(只在記憶體,app 關掉就重來)
//   - 動態牆也分組織:新的動態帶 org(預設組織不帶)

/// 一個組織的範例資料
struct MockSnapshot {
    var people: [Person] = []
    var services: [Service] = []
    var servers: [Server] = []
    var repos: [Repository] = []
    var pullRequests: [PullRequest] = []
    var workItems: [WorkItem] = []
    var sprint: Sprint = .empty
    var pipelineRuns: [PipelineRun] = []
    var deployments: [Deployment] = []
    var alerts: [Alert] = []
    var alertRules: [AlertRule] = []
    var logs: [LogEntry] = []
    var dashboards: [Dashboard] = []
    var incidents: [Incident] = []
    var onCall: [OnCallShift] = []
    var automations: [Automation] = []
    var automationRuns: [AutomationRun] = []
    var approvals: [ApprovalRequest] = []
    var activity: [ActivityItem] = []
    var integrations: [Integration] = []
}

extension AppState {
    /// 把畫面上的範例資料收成一份
    private func captureMock() -> MockSnapshot {
        MockSnapshot(people: people, services: services, servers: servers, repos: repos, pullRequests: pullRequests,
                     workItems: workItems, sprint: sprint, pipelineRuns: pipelineRuns, deployments: deployments,
                     alerts: alerts, alertRules: alertRules, logs: logs, dashboards: dashboards, incidents: incidents,
                     onCall: onCall, automations: automations, automationRuns: automationRuns, approvals: approvals,
                     activity: activity, integrations: integrations)
    }

    private func applyMock(_ d: MockSnapshot) {
        people = d.people; services = d.services; servers = d.servers; repos = d.repos
        pullRequests = d.pullRequests; workItems = d.workItems; sprint = d.sprint
        pipelineRuns = d.pipelineRuns; deployments = d.deployments; alerts = d.alerts
        alertRules = d.alertRules; logs = d.logs; dashboards = d.dashboards; incidents = d.incidents
        onCall = d.onCall; automations = d.automations; automationRuns = d.automationRuns
        approvals = d.approvals; activity = d.activity; integrations = d.integrations
    }

    /// 把範例資料換成目前組織的(切換組織、啟動時讀到的目前組織不是預設組織時)
    func switchMock() {
        let target = currentOrgId
        guard target != mockOrg else { return }
        mockStash[mockOrg] = captureMock()
        let next = mockStash.removeValue(forKey: target) ?? MockSnapshot()
        applyMock(next)
        mockOrg = target
    }

    /// 刪除組織時丟掉它的範例資料
    func dropMock(_ id: String) {
        mockStash.removeValue(forKey: id)
    }
}
