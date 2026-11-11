import Foundation

// GCP:用「Google Cloud」連線(Google 網頁 OAuth,cloud-platform.read-only)讀 Cloud Run 服務、版本、Job 執行、
// Cloud Logging 的錯誤與 Cloud Monitoring 的請求量與延遲。只讀:Ops 不會部署、改流量、執行 Job。
// 專案、區域、追蹤哪些服務與 Job 存在目前組織的 gcp.json。

struct GCPSettings: Codable, Equatable {
    var project = ""
    var region = "asia-east1"
    /// 追蹤的 Cloud Run 服務;空的表示全部
    var services: [String] = []
    /// 追蹤的 Cloud Run Job;空的表示全部
    var jobs: [String] = []
    var connection = GCP.connectionId

    init(project: String = "", region: String = "asia-east1", services: [String] = [], jobs: [String] = [],
         connection: String = GCP.connectionId) {
        self.project = project; self.region = region; self.services = services; self.jobs = jobs; self.connection = connection
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        project = (try? c.decode(String.self, forKey: .project)) ?? ""
        region = (try? c.decode(String.self, forKey: .region)) ?? "asia-east1"
        services = (try? c.decode([String].self, forKey: .services)) ?? []
        jobs = (try? c.decode([String].self, forKey: .jobs)) ?? []
        connection = (try? c.decode(String.self, forKey: .connection)) ?? GCP.connectionId
    }
}

struct GCPTraffic: Hashable, Encodable {
    var revision: String
    var percent: Int
    var tag: String
}

struct GCPService: Identifiable, Hashable, Encodable {
    var id: String            // 服務名稱
    var url: String
    var ready: Bool
    var state: String         // CONDITION_SUCCEEDED、CONDITION_FAILED、CONDITION_RECONCILING…
    var message: String
    var latestReady: String
    var latestCreated: String
    var image: String
    var maxInstances: Int
    var updatedAt: Date?
    var lastModifier: String
    var traffic: [GCPTraffic]

    /// 最新建立的版本還沒好(部署中或失敗)
    var rolloutPending: Bool { !latestCreated.isEmpty && latestCreated != latestReady }
    var stateLabel: String {
        if state == "CONDITION_RECONCILING" || state == "CONDITION_PENDING" { return "部署中" }
        if !ready { return "異常" }
        return rolloutPending ? "新版本未就緒" : "正常"
    }
}

struct GCPRevision: Identifiable, Hashable, Encodable {
    var id: String
    var createdAt: Date?
    var image: String
    var ready: Bool
    var message: String
}

struct GCPExecution: Identifiable, Hashable, Encodable {
    var id: String
    var createdAt: Date?
    var startedAt: Date?
    var completedAt: Date?
    var succeeded: Int
    var failed: Int
    var running: Int
    var cancelled: Int
    var tasks: Int
    var message: String

    var status: String {
        if completedAt == nil { return running > 0 || startedAt != nil ? "running" : "pending" }
        if cancelled > 0 && failed == 0 && succeeded < tasks { return "cancelled" }
        return failed > 0 || succeeded < tasks ? "failed" : "succeeded"
    }
    var statusLabel: String {
        switch status {
        case "running": return "執行中"
        case "pending": return "等待中"
        case "cancelled": return "已取消"
        case "failed": return "失敗"
        default: return "成功"
        }
    }
    var duration: TimeInterval? {
        guard let s = startedAt ?? createdAt, let e = completedAt else { return nil }
        return e.timeIntervalSince(s)
    }
}

struct GCPJob: Identifiable, Hashable, Encodable {
    var id: String
    var latest: GCPExecution?
    var executions: [GCPExecution]
}

struct GCPLogEntry: Identifiable, Hashable, Encodable {
    var id: String
    var timestamp: Date?
    var severity: String
    var text: String
    var resource: String      // 服務或 Job 名稱
    var revision: String
    var httpStatus: Int?
    var method: String
    var requestUrl: String
    var latency: String
}

struct GCPPoint: Hashable, Encodable {
    var time: Date
    var series: String        // 2xx、4xx、5xx、p95…
    var value: Double
}

struct GCPMetrics: Hashable, Encodable {
    var hours: Int
    var requests: [GCPPoint] = []
    var latencyP95: [GCPPoint] = []
    var instances: [GCPPoint] = []

    func total(_ cls: String) -> Int { Int(requests.filter { $0.series == cls }.reduce(0) { $0 + $1.value }) }
    var totalRequests: Int { Int(requests.reduce(0) { $0 + $1.value }) }
    var errorRate: Double { totalRequests > 0 ? Double(total("5xx")) / Double(totalRequests) : 0 }
    var maxP95: Double { latencyP95.map(\.value).max() ?? 0 }
}

struct GCPSnapshot: Equatable {
    var services: [GCPService] = []
    var jobs: [GCPJob] = []
    var fetchedAt: Date?
    var busy = false
    var error: String?
}

enum GCP {
    static let connectionId = "gcp"
    static let readOnlyScope = "https://www.googleapis.com/auth/cloud-platform.read-only"

    static var presetConnection: Connection {
        var c = Connection(id: connectionId, name: "Google Cloud", baseURL: "https://www.googleapis.com", isProduction: true,
                           testPath: "/oauth2/v3/userinfo", auth: .oauth,
                           note: "伺服器與服務 › GCP 讀 Cloud Run、日誌與監控(只讀)")
        c.oauth = OAuthSettings(issuer: "https://accounts.google.com",
                                authorizationEndpoint: "https://accounts.google.com/o/oauth2/v2/auth",
                                tokenEndpoint: "https://oauth2.googleapis.com/token",
                                scopes: "openid email \(readOnlyScope)",
                                extraParams: "access_type=offline&prompt=consent")
        return c
    }

    /// Google Cloud Console 建 OAuth 用戶端(桌面應用程式)的頁面
    static func createClientURL(project: String) -> URL {
        var comps = URLComponents(string: "https://console.cloud.google.com/auth/clients/create")!
        if !project.isEmpty { comps.queryItems = [URLQueryItem(name: "project", value: project)] }
        return comps.url!
    }

    static func consoleURL(project: String, region: String, service: String? = nil, job: String? = nil) -> URL? {
        if let service { return URL(string: "https://console.cloud.google.com/run/detail/\(region)/\(service)/metrics?project=\(project)") }
        if let job { return URL(string: "https://console.cloud.google.com/run/jobs/details/\(region)/\(job)/executions?project=\(project)") }
        return URL(string: "https://console.cloud.google.com/run?project=\(project)")
    }

    static func lastPart(_ s: String) -> String { s.split(separator: "/").last.map(String.init) ?? s }

    // MARK: 解析

    private nonisolated(unsafe) static let isoFull: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]; return f
    }()
    private nonisolated(unsafe) static let iso = ISO8601DateFormatter()

    /// Google 的時間常有 9 位小數,ISO8601DateFormatter 只吃 3 位,先截掉
    static func date(_ v: Any?) -> Date? {
        guard var s = v as? String, !s.isEmpty else { return nil }
        if let dot = s.firstIndex(of: "."), let z = s[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
            let frac = s[s.index(after: dot)..<z]
            s = String(s[..<dot]) + "." + String(frac.prefix(3)).padding(toLength: 3, withPad: "0", startingAt: 0) + String(s[z...])
        }
        return isoFull.date(from: s) ?? iso.date(from: s)
    }
    private static func str(_ v: Any?) -> String { v as? String ?? "" }
    private static func int(_ v: Any?) -> Int { (v as? NSNumber)?.intValue ?? Int(v as? String ?? "") ?? 0 }

    static func service(_ d: [String: Any]) -> GCPService {
        let cond = d["terminalCondition"] as? [String: Any] ?? [:]
        let tmpl = d["template"] as? [String: Any] ?? [:]
        let containers = tmpl["containers"] as? [[String: Any]] ?? []
        let statuses = d["trafficStatuses"] as? [[String: Any]] ?? []
        return GCPService(id: lastPart(str(d["name"])), url: str(d["uri"]),
                          ready: str(cond["state"]) == "CONDITION_SUCCEEDED", state: str(cond["state"]), message: str(cond["message"]),
                          latestReady: lastPart(str(d["latestReadyRevision"])), latestCreated: lastPart(str(d["latestCreatedRevision"])),
                          image: str(containers.first?["image"]),
                          maxInstances: int((tmpl["scaling"] as? [String: Any])?["maxInstanceCount"]),
                          updatedAt: date(d["updateTime"]), lastModifier: str(d["lastModifier"]),
                          traffic: statuses.map {
                              let rev = lastPart(str($0["revision"]))
                              return GCPTraffic(revision: rev.isEmpty ? "最新版本" : rev, percent: int($0["percent"]), tag: str($0["tag"]))
                          })
    }

    static func revision(_ d: [String: Any]) -> GCPRevision {
        let ready = (d["conditions"] as? [[String: Any]] ?? []).first { str($0["type"]) == "Ready" }
        return GCPRevision(id: lastPart(str(d["name"])), createdAt: date(d["createTime"]),
                           image: str((d["containers"] as? [[String: Any]])?.first?["image"]),
                           ready: ready.map { str($0["state"]) == "CONDITION_SUCCEEDED" } ?? false,
                           message: str(ready?["message"]))
    }

    static func execution(_ d: [String: Any]) -> GCPExecution {
        let failedCond = (d["conditions"] as? [[String: Any]] ?? []).first { str($0["state"]) == "CONDITION_FAILED" }
        return GCPExecution(id: lastPart(str(d["name"])), createdAt: date(d["createTime"]), startedAt: date(d["startTime"]),
                            completedAt: date(d["completionTime"]), succeeded: int(d["succeededCount"]), failed: int(d["failedCount"]),
                            running: int(d["runningCount"]), cancelled: int(d["cancelledCount"]), tasks: max(1, int(d["taskCount"])),
                            message: str(failedCond?["message"]))
    }

    static func logEntry(_ d: [String: Any]) -> GCPLogEntry {
        let labels = (d["resource"] as? [String: Any])?["labels"] as? [String: Any] ?? [:]
        let http = d["httpRequest"] as? [String: Any]
        var text = str(d["textPayload"])
        if text.isEmpty, let j = d["jsonPayload"] as? [String: Any] {
            text = str(j["message"]).isEmpty ? str(j["msg"]) : str(j["message"])
            if text.isEmpty, let data = try? JSONSerialization.data(withJSONObject: j, options: [.sortedKeys]) {
                text = String(decoding: data, as: UTF8.self)
            }
        }
        if text.isEmpty, let http {
            text = "\(str(http["requestMethod"])) \(str(http["requestUrl"])) → \(int(http["status"]))"
        }
        return GCPLogEntry(id: str(d["insertId"]).isEmpty ? UUID().uuidString : str(d["insertId"]), timestamp: date(d["timestamp"]),
                           severity: str(d["severity"]).isEmpty ? "DEFAULT" : str(d["severity"]), text: text,
                           resource: str(labels["service_name"]).isEmpty ? str(labels["job_name"]) : str(labels["service_name"]),
                           revision: str(labels["revision_name"]), httpStatus: http.map { int($0["status"]) },
                           method: str(http?["requestMethod"]), requestUrl: str(http?["requestUrl"]), latency: str(http?["latency"]))
    }

    /// Cloud Monitoring 的 timeSeries → 點;series 取 groupBy 的標籤值(沒有就用 fallback)
    static func points(_ obj: Any, label: String?, fallback: String) -> [GCPPoint] {
        var out: [GCPPoint] = []
        for ts in (obj as? [String: Any])?["timeSeries"] as? [[String: Any]] ?? [] {
            let labels = (ts["metric"] as? [String: Any])?["labels"] as? [String: Any] ?? [:]
            let name = label.map { str(labels[$0]) }.flatMap { $0.isEmpty ? nil : $0 } ?? fallback
            for p in ts["points"] as? [[String: Any]] ?? [] {
                guard let t = date((p["interval"] as? [String: Any])?["endTime"]), let v = p["value"] as? [String: Any] else { continue }
                let value = (v["doubleValue"] as? NSNumber)?.doubleValue ?? Double(int(v["int64Value"]))
                out.append(GCPPoint(time: t, series: name, value: value))
            }
        }
        return out.sorted { $0.time < $1.time }
    }

    /// Logging 的篩選字串:跳脫雙引號
    static func quote(_ s: String) -> String { "\"" + s.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\"" }

    static func logFilter(service: String? = nil, job: String? = nil, severity: String = "ERROR", since: Date, contains: String? = nil) -> String {
        var f: [String] = []
        if let service { f.append("resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\(quote(service))") }
        else if let job { f.append("resource.type=\"cloud_run_job\" AND resource.labels.job_name=\(quote(job))") }
        else { f.append("(resource.type=\"cloud_run_revision\" OR resource.type=\"cloud_run_job\")") }
        if !severity.isEmpty, severity != "DEFAULT" { f.append("severity>=\(severity)") }
        f.append("timestamp>=\(quote(ISO8601DateFormatter().string(from: since)))")
        if let contains, !contains.isEmpty { f.append(quote(contains)) }
        return f.joined(separator: " AND ")
    }

    static let severities = ["DEFAULT", "INFO", "WARNING", "ERROR", "CRITICAL"]
}

/// 讀 GCP API。有登入時每 5 分鐘更新服務與 Job 狀態;日誌、指標在打開詳情或 MCP 要的時候才讀
@MainActor
final class GCPStore {
    let app: AppState
    private var timer: Timer?

    init(app: AppState) { self.app = app }

    private var fileURL: URL { app.dataDir.appendingPathComponent("gcp.json") }
    var s: GCPSettings { app.gcpSettings }
    var connectionId: String { s.connection }
    var connection: Connection? { app.connections.first { $0.id == connectionId } }
    var isLoggedIn: Bool { connection.map { app.connStore?.hasToken($0) == true } ?? false }
    var isConfigured: Bool { !s.project.isEmpty && !s.region.isEmpty }

    func load() {
        app.gcpSettings = (try? OrgFiles.decoder().decode(GCPSettings.self, from: Data(contentsOf: fileURL))) ?? GCPSettings()
        app.gcp = GCPSnapshot()
    }

    func save() {
        if let d = try? OrgFiles.encoder().encode(app.gcpSettings) { try? d.write(to: fileURL, options: .atomic) }
    }

    func update(_ change: (inout GCPSettings) -> Void) {
        var n = app.gcpSettings
        change(&n)
        n.project = n.project.trimmingCharacters(in: .whitespacesAndNewlines)
        n.region = n.region.trimmingCharacters(in: .whitespacesAndNewlines)
        n.services = n.services.map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        n.jobs = n.jobs.map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        guard n != app.gcpSettings else { return }
        app.gcpSettings = n
        app.gcp = GCPSnapshot()
        save()
    }

    func start(timer useTimer: Bool) {
        guard useTimer, timer == nil else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 300, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self, self.isLoggedIn, self.isConfigured, !self.app.gcp.busy else { return }
                await self.refresh()
            }
        }
    }

    @discardableResult
    func ensureConnection() -> Connection {
        app.startConnections()
        if let c = connection { return c }
        let c = GCP.presetConnection
        app.connStore?.upsert(c)
        if app.gcpSettings.connection != c.id { app.gcpSettings.connection = c.id; save() }
        return c
    }

    // MARK: 請求

    private func call(_ method: String, _ url: String, body: [String: Any]? = nil) async throws -> Any {
        app.startConnections()
        guard let store = app.connStore else { throw ConnectionError("連線功能還沒啟動") }
        guard connection != nil else { throw ConnectionError("還沒設定 Google Cloud 連線(伺服器與服務 › GCP)") }
        let data = try body.map { try JSONSerialization.data(withJSONObject: $0) }
        return try await store.request(connectionId, method, url, body: data)
    }

    private var runBase: String {
        "https://run.googleapis.com/v2/projects/\(ConnHTTP.escapePath(s.project))/locations/\(ConnHTTP.escapePath(s.region))"
    }

    private func need() throws {
        guard isConfigured else { throw ConnectionError("還沒設定 GCP 專案與區域") }
    }

    // MARK: 讀

    func refresh() async {
        guard !app.gcp.busy else { return }
        app.gcp.busy = true
        app.gcp.error = nil
        defer { app.gcp.busy = false }
        guard isLoggedIn else { app.gcp.error = "還沒登入 Google Cloud"; return }
        do {
            let services = try await listServices()
            let jobs = try await listJobs()
            app.gcp = GCPSnapshot(services: services, jobs: jobs, fetchedAt: .now, busy: true, error: nil)
        } catch {
            app.gcp.error = Self.explain(error)
        }
    }

    /// 常見的錯誤講清楚一點
    static func explain(_ error: Error) -> String {
        guard let e = error as? ConnectionError else { return error.localizedDescription }
        switch e.status {
        case 401: return "Google 登入已失效,請重新登入"
        case 403: return "沒有權限:登入的帳號要有這個專案的檢視權限(roles/viewer),且專案要啟用 Cloud Run / Logging / Monitoring API。(\(e.message))"
        case 404: return "找不到:確認專案 id 和區域。(\(e.message))"
        default: return e.localizedDescription
        }
    }

    func listServices() async throws -> [GCPService] {
        try need()
        let all = ((try await call("GET", runBase + "/services?pageSize=100") as? [String: Any])?["services"] as? [[String: Any]] ?? [])
            .map(GCP.service)
        return (s.services.isEmpty ? all : all.filter { s.services.contains($0.id) }).sorted { $0.id < $1.id }
    }

    func listJobs() async throws -> [GCPJob] {
        try need()
        let raw = (try await call("GET", runBase + "/jobs?pageSize=100") as? [String: Any])?["jobs"] as? [[String: Any]] ?? []
        var jobs: [GCPJob] = []
        for d in raw {
            let name = GCP.lastPart(d["name"] as? String ?? "")
            guard s.jobs.isEmpty || s.jobs.contains(name) else { continue }
            let latest = (d["latestCreatedExecution"] as? [String: Any]).map { e -> GCPExecution in
                var x = GCP.execution(e)
                // 摘要裡只有 completionStatus
                let st = e["completionStatus"] as? String ?? ""
                if st == "EXECUTION_FAILED" { x.failed = max(1, x.failed) }
                if st == "EXECUTION_SUCCEEDED" { x.succeeded = max(x.tasks, x.succeeded) }
                if st == "EXECUTION_CANCELLED" { x.cancelled = max(1, x.cancelled) }
                return x
            }
            jobs.append(GCPJob(id: name, latest: latest, executions: []))
        }
        return jobs.sorted { $0.id < $1.id }
    }

    func revisions(service: String, limit: Int = 10) async throws -> [GCPRevision] {
        try need()
        let obj = try await call("GET", runBase + "/services/\(ConnHTTP.escapePath(service))/revisions?pageSize=\(limit)")
        return ((obj as? [String: Any])?["revisions"] as? [[String: Any]] ?? []).map(GCP.revision)
            .sorted { ($0.createdAt ?? .distantPast) > ($1.createdAt ?? .distantPast) }
    }

    func executions(job: String, limit: Int = 10) async throws -> [GCPExecution] {
        try need()
        let obj = try await call("GET", runBase + "/jobs/\(ConnHTTP.escapePath(job))/executions?pageSize=\(limit)")
        return ((obj as? [String: Any])?["executions"] as? [[String: Any]] ?? []).map(GCP.execution)
            .sorted { ($0.createdAt ?? .distantPast) > ($1.createdAt ?? .distantPast) }
    }

    func logs(service: String? = nil, job: String? = nil, severity: String = "ERROR", hours: Int = 24,
              contains: String? = nil, limit: Int = 50) async throws -> [GCPLogEntry] {
        try need()
        let filter = GCP.logFilter(service: service, job: job, severity: severity,
                                   since: Date().addingTimeInterval(-Double(hours) * 3600), contains: contains)
        let obj = try await call("POST", "https://logging.googleapis.com/v2/entries:list", body: [
            "resourceNames": ["projects/\(s.project)"], "filter": filter, "orderBy": "timestamp desc", "pageSize": min(1000, max(1, limit)),
        ])
        return ((obj as? [String: Any])?["entries"] as? [[String: Any]] ?? []).map(GCP.logEntry)
    }

    private func series(_ metric: String, service: String, hours: Int, aligner: String, reducer: String?, groupBy: String?) async throws -> Any {
        let end = Date(), start = end.addingTimeInterval(-Double(hours) * 3600)
        let period = hours <= 6 ? 300 : hours <= 48 ? 3600 : 6 * 3600
        let f = ISO8601DateFormatter()
        var q: [URLQueryItem] = [
            URLQueryItem(name: "filter", value: "metric.type=\"\(metric)\" AND resource.labels.service_name=\(GCP.quote(service))"),
            URLQueryItem(name: "interval.startTime", value: f.string(from: start)),
            URLQueryItem(name: "interval.endTime", value: f.string(from: end)),
            URLQueryItem(name: "aggregation.alignmentPeriod", value: "\(period)s"),
            URLQueryItem(name: "aggregation.perSeriesAligner", value: aligner),
        ]
        if let reducer { q.append(URLQueryItem(name: "aggregation.crossSeriesReducer", value: reducer)) }
        if let groupBy { q.append(URLQueryItem(name: "aggregation.groupByFields", value: groupBy)) }
        let query = q.map { "\($0.name)=" + ConnHTTP.formEncode(["v": $0.value ?? ""]).dropFirst(2) }.joined(separator: "&")
        let url = "https://monitoring.googleapis.com/v3/projects/\(ConnHTTP.escapePath(s.project))/timeSeries?" + query
        return try await call("GET", url)
    }

    func metrics(service: String, hours: Int = 24) async throws -> GCPMetrics {
        try need()
        var m = GCPMetrics(hours: hours)
        m.requests = GCP.points(try await series("run.googleapis.com/request_count", service: service, hours: hours,
                                                 aligner: "ALIGN_DELTA", reducer: "REDUCE_SUM",
                                                 groupBy: "metric.labels.response_code_class"),
                                label: "response_code_class", fallback: "其他")
        m.latencyP95 = GCP.points(try await series("run.googleapis.com/request_latencies", service: service, hours: hours,
                                                   aligner: "ALIGN_DELTA", reducer: "REDUCE_PERCENTILE_95", groupBy: nil),
                                  label: nil, fallback: "p95")
        m.instances = GCP.points(try await series("run.googleapis.com/container/instance_count", service: service, hours: hours,
                                                 aligner: "ALIGN_MAX", reducer: "REDUCE_SUM", groupBy: nil),
                                 label: nil, fallback: "執行個體")
        return m
    }
}
