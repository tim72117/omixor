import Foundation

/// 模擬資料:一家經營線上課程平台的團隊(course-web、api-server、payments…)。
/// 所有時間都相對於 `Date()`,內容固定(用固定種子的亂數),每次啟動看到的資料一致。
struct MockData {
    var people: [Person] = []
    var services: [Service] = []
    var servers: [Server] = []
    var repos: [Repository] = []
    var pullRequests: [PullRequest] = []
    var workItems: [WorkItem] = []
    var sprint = Sprint(name: "Sprint", start: .now, end: .now, goal: "", burndown: [], idealBurndown: [])
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

    static func make() -> MockData {
        var b = MockDataBuilder(now: Date())
        var d = MockData()
        d.people = b.makePeople()
        d.services = b.makeServices()
        d.servers = b.makeServers()
        d.repos = b.makeRepos()
        d.pullRequests = b.makePullRequests()
        d.workItems = b.makeWorkItems()
        d.sprint = b.makeSprint()
        d.pipelineRuns = b.makePipelineRuns()
        d.deployments = b.makeDeployments()
        d.alerts = b.makeAlerts()
        d.alertRules = b.makeAlertRules()
        d.logs = b.makeLogs()
        d.dashboards = b.makeDashboards()
        d.incidents = b.makeIncidents()
        d.onCall = b.makeOnCall()
        d.automations = b.makeAutomations()
        d.automationRuns = b.makeAutomationRuns()
        d.approvals = b.makeApprovals()
        d.activity = b.makeActivity()
        d.integrations = b.makeIntegrations()
        return d
    }
}

/// 產生模擬資料用的小工具:固定種子亂數、相對時間、可重現的 UUID。
struct MockDataBuilder {
    let now: Date
    private var state: UInt64 = 0x9E37_79B9_7F4A_7C15
    private var uuidCounter: UInt64 = 0

    init(now: Date) { self.now = now }

    /// 0..<1 的亂數(SplitMix64)
    mutating func random() -> Double {
        state &+= 0x9E37_79B9_7F4A_7C15
        var z = state
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        z = z ^ (z >> 31)
        return Double(z >> 11) / Double(1 << 53)
    }

    mutating func random(_ range: ClosedRange<Double>) -> Double {
        range.lowerBound + random() * (range.upperBound - range.lowerBound)
    }

    mutating func int(_ range: ClosedRange<Int>) -> Int {
        range.lowerBound + Int(random() * Double(range.upperBound - range.lowerBound + 1)) % (range.upperBound - range.lowerBound + 1)
    }

    mutating func pick<T>(_ items: [T]) -> T { items[int(0...(items.count - 1))] }

    /// 可重現的 UUID
    mutating func uuid() -> UUID {
        uuidCounter += 1
        let s = String(format: "0000A11C-0000-4000-8000-%012llX", uuidCounter)
        return UUID(uuidString: s) ?? UUID()
    }

    mutating func hex(_ length: Int) -> String {
        let chars = Array("0123456789abcdef")
        var s = ""
        for _ in 0..<length { s.append(chars[int(0...15)]) }
        return s
    }

    func ago(minutes m: Double) -> Date { now.addingTimeInterval(-m * 60) }
    func ago(hours h: Double) -> Date { now.addingTimeInterval(-h * 3600) }
    func ago(days d: Double) -> Date { now.addingTimeInterval(-d * 86400) }
    func ahead(hours h: Double) -> Date { now.addingTimeInterval(h * 3600) }

    /// 平滑波動的時間序列:基準值 + 日夜週期 + 雜訊,可加尖峰。
    mutating func series(count: Int, step: TimeInterval, base: Double, amplitude: Double, noise: Double,
                         min lo: Double = 0, max hi: Double = .greatestFiniteMagnitude,
                         spikeFrom: Int? = nil, spike: Double = 0) -> [MetricPoint] {
        var points: [MetricPoint] = []
        points.reserveCapacity(count)
        var drift = 0.0
        for i in 0..<count {
            let date = now.addingTimeInterval(-Double(count - 1 - i) * step)
            let phase = date.timeIntervalSince1970 / 86400 * 2 * .pi
            drift = drift * 0.8 + random(-1...1) * noise
            var v = base + sin(phase - 1.2) * amplitude + drift
            if let s = spikeFrom, i >= s {
                let ramp = min(1, Double(i - s + 1) / 4)
                v += spike * ramp
            }
            points.append(MetricPoint(date: date, value: Swift.min(hi, Swift.max(lo, v))))
        }
        return points
    }
}
