import Foundation

// GitHub:用「GitHub」連線(網頁 OAuth,也可以改貼個人存取權杖)讀追蹤中 repo 的 PR、檢查與 Actions。
// 只讀不寫:Ops 不會開 PR、留言、重跑 workflow。
// 追蹤哪些 repo 是組織的設定,存在 core server(IntegrationRemote.swift;成員讀、組織管理員改)。
// 這台的 github.json 只再寫用哪一個連線(connection;token 在鑰匙圈);檔案裡原本的 repos 保留不刪,
// 核心還沒存過時第一次連上會把它匯入(要組織管理員)。讀 PR 與 Actions 仍在這台用鑰匙圈的 token 直接打 GitHub。

struct GHSettings: Codable, Equatable {
    var repos: [String] = []
    var connection = GitHub.connectionId

    init(repos: [String] = [], connection: String = GitHub.connectionId) {
        self.repos = repos
        self.connection = connection
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        repos = (try? c.decode([String].self, forKey: .repos)) ?? []
        connection = (try? c.decode(String.self, forKey: .connection)) ?? GitHub.connectionId
    }
}

struct GHRepo: Identifiable, Hashable, Encodable {
    var id: String            // owner/name
    var description: String
    var defaultBranch: String
    var isPrivate: Bool
    var openIssues: Int
    var stars: Int
    var pushedAt: Date?
    var url: String
}

struct GHChecks: Hashable, Encodable {
    var total = 0
    var success = 0
    var failure = 0
    var pending = 0
    var skipped = 0

    var state: String { failure > 0 ? "failure" : pending > 0 ? "pending" : total == 0 ? "none" : "success" }
    var label: String {
        switch state {
        case "failure": return "\(failure) 個失敗"
        case "pending": return "\(pending) 個執行中"
        case "success": return "\(success) 個通過"
        default: return "沒有檢查"
        }
    }
}

struct GHPull: Identifiable, Hashable, Encodable {
    var id: String { "\(repo)#\(number)" }
    var repo: String
    var number: Int
    var title: String
    var author: String
    var branch: String
    var base: String
    var draft: Bool
    var labels: [String]
    var reviewers: [String]
    var headSHA: String
    var createdAt: Date?
    var updatedAt: Date?
    var url: String
    var checks: GHChecks?
}

struct GHRun: Identifiable, Hashable, Encodable {
    var id: Int
    var repo: String
    var workflow: String
    var title: String
    var branch: String
    var event: String
    var status: String        // queued、in_progress、completed…
    var conclusion: String?   // success、failure、cancelled、skipped…
    var sha: String
    var actor: String
    var runNumber: Int
    var attempt: Int
    var createdAt: Date?
    var startedAt: Date?
    var updatedAt: Date?
    var url: String

    var isFailure: Bool { ["failure", "timed_out", "startup_failure"].contains(conclusion ?? "") }
    var isRunning: Bool { status != "completed" }
    var duration: TimeInterval? {
        guard !isRunning, let s = startedAt ?? createdAt, let e = updatedAt else { return nil }
        return e.timeIntervalSince(s)
    }
    var stateLabel: String {
        if isRunning { return status == "queued" || status == "waiting" || status == "pending" ? "排隊中" : "執行中" }
        switch conclusion ?? "" {
        case "success": return "成功"
        case "failure": return "失敗"
        case "cancelled": return "已取消"
        case "skipped": return "略過"
        case "timed_out": return "逾時"
        case "action_required": return "待處理"
        default: return conclusion ?? status
        }
    }
}

struct GHStep: Hashable, Encodable {
    var number: Int
    var name: String
    var status: String
    var conclusion: String?
}

struct GHJob: Identifiable, Hashable, Encodable {
    var id: Int
    var name: String
    var status: String
    var conclusion: String?
    var startedAt: Date?
    var completedAt: Date?
    var url: String
    var steps: [GHStep]
}

struct GHFile: Hashable, Encodable {
    var filename: String
    var status: String
    var additions: Int
    var deletions: Int
}

struct GHPullDetail: Hashable, Encodable {
    var pull: GHPull
    var body: String
    var additions: Int
    var deletions: Int
    var changedFiles: Int
    var mergeableState: String
    var files: [GHFile]
}

/// 畫面上的 GitHub 狀態
struct GHSnapshot: Equatable {
    var repos: [GHRepo] = []
    var pulls: [GHPull] = []
    var runs: [GHRun] = []
    var fetchedAt: Date?
    var busy = false
    /// 每個 repo 讀不到時的原因
    var errors: [String: String] = [:]
    var error: String?
    /// 讀寫組織設定(core server)的錯誤(nil = 正常)
    var settingsError: String?
}

enum GitHub {
    static let connectionId = "github"
    /// 註冊 OAuth App 時要填的值
    static let homepage = "http://127.0.0.1"
    static let callback = "http://127.0.0.1/callback"

    /// 預設的 GitHub 連線:網頁 OAuth。GitHub 不支援自動註冊,要先建 OAuth App 拿 client ID 與 secret
    static var presetConnection: Connection {
        var c = Connection(id: connectionId, name: "GitHub", baseURL: "https://api.github.com", isProduction: false,
                           testPath: "/user", auth: .oauth,
                           note: "開發進度 › GitHub 讀 PR 與 Actions(只讀)")
        c.oauth = OAuthSettings(issuer: "https://github.com",
                                authorizationEndpoint: "https://github.com/login/oauth/authorize",
                                tokenEndpoint: "https://github.com/login/oauth/access_token",
                                scopes: "read:user")
        return c
    }

    /// 開 GitHub 的「新增 OAuth App」頁,先填好名稱與網址
    static var registerURL: URL {
        var comps = URLComponents(string: "https://github.com/settings/applications/new")!
        comps.queryItems = [URLQueryItem(name: "oauth_application[name]", value: "omixor"),
                            URLQueryItem(name: "oauth_application[url]", value: homepage),
                            URLQueryItem(name: "oauth_application[callback_url]", value: callback)]
        return comps.url!
    }

    /// owner/name;也接受 https://github.com/owner/name(.git)
    static func normalizeRepo(_ s: String) -> String? {
        var t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        for p in ["https://github.com/", "http://github.com/", "github.com/", "git@github.com:"] where t.hasPrefix(p) {
            t = String(t.dropFirst(p.count))
        }
        if t.hasSuffix(".git") { t = String(t.dropLast(4)) }
        if t.hasSuffix("/") { t = String(t.dropLast()) }
        let parts = t.split(separator: "/")
        guard parts.count == 2, parts.allSatisfy({ p in !p.isEmpty && p.allSatisfy { $0.isLetter || $0.isNumber || "-_.".contains($0) } })
        else { return nil }
        return parts.joined(separator: "/")
    }

    // MARK: 解析 GitHub 的 JSON

    private nonisolated(unsafe) static let isoFull: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]; return f
    }()
    private nonisolated(unsafe) static let iso: ISO8601DateFormatter = ISO8601DateFormatter()

    static func date(_ v: Any?) -> Date? {
        guard let s = v as? String else { return nil }
        return iso.date(from: s) ?? isoFull.date(from: s)
    }
    private static func str(_ v: Any?) -> String { v as? String ?? "" }
    private static func int(_ v: Any?) -> Int { (v as? NSNumber)?.intValue ?? 0 }
    private static func login(_ v: Any?) -> String { str((v as? [String: Any])?["login"]) }

    static func repo(_ d: [String: Any]) -> GHRepo {
        GHRepo(id: str(d["full_name"]), description: str(d["description"]), defaultBranch: str(d["default_branch"]),
               isPrivate: d["private"] as? Bool ?? false, openIssues: int(d["open_issues_count"]),
               stars: int(d["stargazers_count"]), pushedAt: date(d["pushed_at"]), url: str(d["html_url"]))
    }

    static func pull(_ d: [String: Any], repo: String) -> GHPull {
        let head = d["head"] as? [String: Any] ?? [:]
        let base = d["base"] as? [String: Any] ?? [:]
        return GHPull(repo: repo, number: int(d["number"]), title: str(d["title"]), author: login(d["user"]),
                      branch: str(head["ref"]), base: str(base["ref"]), draft: d["draft"] as? Bool ?? false,
                      labels: (d["labels"] as? [[String: Any]] ?? []).map { str($0["name"]) },
                      reviewers: (d["requested_reviewers"] as? [[String: Any]] ?? []).map { str($0["login"]) },
                      headSHA: str(head["sha"]), createdAt: date(d["created_at"]), updatedAt: date(d["updated_at"]),
                      url: str(d["html_url"]))
    }

    static func checks(_ obj: Any) -> GHChecks {
        var c = GHChecks()
        for r in (obj as? [String: Any])?["check_runs"] as? [[String: Any]] ?? [] {
            c.total += 1
            if str(r["status"]) != "completed" { c.pending += 1; continue }
            switch str(r["conclusion"]) {
            case "success", "neutral": c.success += 1
            case "skipped", "cancelled", "stale": c.skipped += 1
            default: c.failure += 1
            }
        }
        return c
    }

    static func run(_ d: [String: Any], repo: String) -> GHRun {
        GHRun(id: int(d["id"]), repo: repo, workflow: str(d["name"]),
              title: str(d["display_title"]).isEmpty ? str((d["head_commit"] as? [String: Any])?["message"]) : str(d["display_title"]),
              branch: str(d["head_branch"]), event: str(d["event"]), status: str(d["status"]),
              conclusion: d["conclusion"] as? String, sha: str(d["head_sha"]), actor: login(d["actor"]),
              runNumber: int(d["run_number"]), attempt: max(1, int(d["run_attempt"])),
              createdAt: date(d["created_at"]), startedAt: date(d["run_started_at"]), updatedAt: date(d["updated_at"]),
              url: str(d["html_url"]))
    }

    static func job(_ d: [String: Any]) -> GHJob {
        GHJob(id: int(d["id"]), name: str(d["name"]), status: str(d["status"]), conclusion: d["conclusion"] as? String,
              startedAt: date(d["started_at"]), completedAt: date(d["completed_at"]), url: str(d["html_url"]),
              steps: (d["steps"] as? [[String: Any]] ?? []).map {
                  GHStep(number: int($0["number"]), name: str($0["name"]), status: str($0["status"]), conclusion: $0["conclusion"] as? String)
              })
    }

    static func file(_ d: [String: Any]) -> GHFile {
        GHFile(filename: str(d["filename"]), status: str(d["status"]), additions: int(d["additions"]), deletions: int(d["deletions"]))
    }

    static func runEventLabel(_ e: String) -> String {
        switch e {
        case "push": return "push"
        case "pull_request": return "PR"
        case "schedule": return "排程"
        case "workflow_dispatch": return "手動"
        default: return e
        }
    }
}

/// 讀 GitHub API。每 5 分鐘自動更新一次(有登入時);畫面、MCP 都用這裡
@MainActor
final class GitHubStore {
    let app: AppState
    private var timer: Timer?
    /// 條件請求的快取(連線 + 路徑 → 上次的 ETag 與內容),只在記憶體;換組織時清掉
    private var etags: [String: (etag: String, data: Data)] = [:]
    private static let etagLimit = 500

    init(app: AppState) { self.app = app }

    private var fileURL: URL { app.dataDir.appendingPathComponent("github.json") }

    var connectionId: String { app.githubSettings.connection }
    var connection: Connection? { app.connections.first { $0.id == connectionId } }
    var isLoggedIn: Bool { connection.map { app.connStore?.hasToken($0) == true } ?? false }

    /// 這台的 github.json(connection 與舊版的 repos)
    private func localFile() -> GHSettings {
        (try? OrgFiles.decoder().decode(GHSettings.self, from: Data(contentsOf: fileURL))) ?? GHSettings()
    }

    func load() {
        // 先顯示這台檔案裡的(連不到核心時當唯讀備援),再以組織(核心)的為準
        app.githubSettings = localFile()
        app.github = GHSnapshot()
        etags = [:]
        syncSettings()
    }

    /// 只寫這台的 connection;repos 是組織的設定(存在核心),檔案裡原本的 repos 保留
    func save() {
        var f = localFile()
        guard f.connection != app.githubSettings.connection || !FileManager.default.fileExists(atPath: fileURL.path) else { return }
        f.connection = app.githubSettings.connection
        if let d = try? OrgFiles.encoder().encode(f) { try? d.write(to: fileURL, options: .atomic) }
    }

    // MARK: 組織設定(core server)

    private var remoteClient: IssueRemote?
    /// 目前連的 core server 的用戶端(和 Issue 同一個;連線換了會換新的)
    var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.config == cfg { return c }
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }

    private func setSettingsError(_ msg: String?) {
        if let msg, msg != app.github.settingsError { NSLog("GitHub 設定(omixor server):%@", msg) }
        if app.github.settingsError != msg { app.github.settingsError = msg }
    }

    private func applyCore(_ x: CoreGitHubSettings) {
        if app.githubSettings.repos != x.repos { app.githubSettings.repos = x.repos }
    }

    /// 從核心讀組織的 GitHub 設定;核心還沒存過而這台的 github.json 有 repos → 匯入一次(原檔不刪不改)
    func syncSettings() {
        let org = app.currentOrgId
        applySettings(org, Result { try Self.fetchSettings(remote, org: org, legacy: localFile().repos) })
    }

    /// 同 syncSettings,但請求在背景跑,回主執行緒才更新畫面(不擋主執行緒)
    func syncSettingsAsync() async {
        let org = app.currentOrgId, r = remote, legacy = localFile().repos
        let res: Result<(CoreGitHubSettings, String?), Error>
        do { res = .success(try await r.run { try Self.fetchSettings($0, org: org, legacy: legacy) }) } catch { res = .failure(error) }
        applySettings(org, res)
    }

    /// 換組織:先顯示這台檔案裡的,再在背景讀核心的(同 load,不擋主執行緒)
    func reload() async {
        app.githubSettings = localFile()
        app.github = GHSnapshot()
        etags = [:]
        await syncSettingsAsync()
    }

    /// 讀核心的設定(背景執行緒也可以呼叫);回傳設定與要顯示的問題
    private static func fetchSettings(_ r: IssueRemote, org: String, legacy: [String]) throws -> (CoreGitHubSettings, String?) {
        var x = try r.githubSettings(org: org)
        var problem: String?
        if !x.saved, !legacy.isEmpty {
            do {
                let res = try r.importGitHubSettings(org: org, repos: legacy)
                x = res.settings
                if !res.skipped.isEmpty { NSLog("GitHub 設定匯入時略過格式不對的 repo:%@", res.skipped.joined(separator: "、")) }
            } catch {
                // 不是組織管理員不能匯入:先照核心的(空的)顯示,等管理員連上時再匯入
                problem = "這台的舊 GitHub 設定還沒匯入組織(要組織管理員):\(error.localizedDescription)"
            }
        }
        return (x, problem)
    }

    private func applySettings(_ org: String, _ res: Result<(CoreGitHubSettings, String?), Error>) {
        switch res {
        case .success(let (x, problem)):
            guard app.currentOrgId == org else { return }
            applyCore(x)
            setSettingsError(problem)
        case .failure(let error):
            setSettingsError(error.localizedDescription)
        }
    }

    func start(timer useTimer: Bool) {
        guard useTimer, timer == nil else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 300, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self, self.isLoggedIn, !self.app.github.busy else { return }
                await self.refresh()
            }
        }
    }

    /// 建立預設的 GitHub 連線(沒有才建)
    @discardableResult
    func ensureConnection() -> Connection {
        app.startConnections()
        if let c = connection { return c }
        let c = GitHub.presetConnection
        app.connStore?.upsert(c)
        if app.githubSettings.connection != c.id { app.githubSettings.connection = c.id; save() }
        return c
    }

    /// 追蹤 repo(存到組織的設定,要組織管理員)
    func addRepo(_ s: String) throws {
        guard let r = GitHub.normalizeRepo(s) else { throw ConnectionError("repo 要寫成 owner/name,例如 tim72117/evoleap") }
        let x = try remote.saveGitHubSettings(org: app.currentOrgId, add: [r])
        applyCore(x)
        setSettingsError(nil)
    }

    /// 不再追蹤(存到組織的設定,要組織管理員);成功後把畫面上這個 repo 的資料拿掉
    func removeRepo(_ r: String) throws {
        let x = try remote.saveGitHubSettings(org: app.currentOrgId, remove: [r])
        applyCore(x)
        setSettingsError(nil)
        app.github.repos.removeAll { $0.id == r }
        app.github.pulls.removeAll { $0.repo == r }
        app.github.runs.removeAll { $0.repo == r }
        app.github.errors[r] = nil
    }

    // MARK: 讀 API

    func get(_ path: String) async throws -> Any {
        app.startConnections()
        guard let store = app.connStore else { throw ConnectionError("連線功能還沒啟動") }
        guard connection != nil else { throw ConnectionError("還沒設定 GitHub 連線(開發進度 › GitHub)") }
        // 條件請求:帶上次的 ETag,GitHub 回 304 表示沒變,直接用上次的內容(304 不扣 API 額度)
        let key = connectionId + " " + path
        let cached = etags[key]
        let r = try await store.requestRaw(connectionId, "GET", path, extra: cached.map { ["If-None-Match": $0.etag] } ?? [:])
        if r.status == 304, let cached {
            return ConnHTTP.Response(status: 200, data: cached.data).json
        }
        let ok = try ConnHTTP.check(r)
        if let etag = ok.header("ETag"), !etag.isEmpty {
            if etags.count >= Self.etagLimit { etags = [:] }
            etags[key] = (etag, ok.data)
        } else {
            etags[key] = nil
        }
        return ok.json
    }

    func getText(_ path: String) async throws -> String {
        app.startConnections()
        guard let store = app.connStore else { throw ConnectionError("連線功能還沒啟動") }
        let r = try ConnHTTP.check(try await store.requestRaw(connectionId, "GET", path))
        return String(decoding: r.data, as: UTF8.self)
    }

    private func repoPath(_ r: String) -> String { "/repos/" + ConnHTTP.escapePath(r) }

    /// 重新讀全部追蹤中的 repo
    func refresh() async {
        guard !app.github.busy else { return }
        app.github.busy = true
        defer { app.github.busy = false }
        await syncSettingsAsync()   // 追蹤的 repo 以組織(核心)的為準,每輪先讀一次(背景讀,不擋主執行緒)
        app.github.error = nil
        guard isLoggedIn else { app.github.error = "還沒登入 GitHub"; return }
        var repos: [GHRepo] = [], pulls: [GHPull] = [], runs: [GHRun] = []
        var errors: [String: String] = [:]
        for r in app.githubSettings.repos {
            do {
                let (repo, ps, rs) = try await fetchRepo(r)
                repos.append(repo); pulls += ps; runs += rs
            } catch let e as ConnectionError where e.isUnauthorized {
                app.github.error = e.message
                return
            } catch let e as ConnectionError where e.status == 404 {
                errors[r] = "找不到(私有 repo 要在 GitHub 連線的 scopes 加 repo,或 OAuth App 沒有權限)"
            } catch {
                errors[r] = error.localizedDescription
            }
        }
        app.github = GHSnapshot(repos: repos, pulls: pulls.sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) },
                                runs: runs.sorted { ($0.createdAt ?? .distantPast) > ($1.createdAt ?? .distantPast) },
                                fetchedAt: .now, busy: true, errors: errors, error: nil, settingsError: app.github.settingsError)
    }

    func fetchRepo(_ r: String) async throws -> (GHRepo, [GHPull], [GHRun]) {
        let p = repoPath(r)
        guard let rd = try await get(p) as? [String: Any] else { throw ConnectionError("repo 資料格式不對") }
        let repo = GitHub.repo(rd)
        var pulls = (try await get(p + "/pulls?state=open&per_page=50") as? [[String: Any]] ?? []).map { GitHub.pull($0, repo: r) }
        for i in pulls.indices where !pulls[i].headSHA.isEmpty {
            pulls[i].checks = (try? await get(p + "/commits/\(pulls[i].headSHA)/check-runs?per_page=100")).map(GitHub.checks)
        }
        let runs = ((try await get(p + "/actions/runs?per_page=30") as? [String: Any])?["workflow_runs"] as? [[String: Any]] ?? [])
            .map { GitHub.run($0, repo: r) }
        return (repo, pulls, runs)
    }

    func jobs(repo: String, run: Int) async throws -> [GHJob] {
        ((try await get(repoPath(repo) + "/actions/runs/\(run)/jobs?per_page=50") as? [String: Any])?["jobs"] as? [[String: Any]] ?? [])
            .map(GitHub.job)
    }

    /// 失敗工作的紀錄最後幾行(GitHub 會轉址到暫時的下載網址)
    func jobLogTail(repo: String, job: Int, lines: Int = 120) async throws -> String {
        let text = try await getText(repoPath(repo) + "/actions/jobs/\(job)/logs")
        return text.split(separator: "\n", omittingEmptySubsequences: false).suffix(lines).joined(separator: "\n")
    }

    func pullDetail(repo: String, number: Int) async throws -> GHPullDetail {
        let p = repoPath(repo) + "/pulls/\(number)"
        guard let d = try await get(p) as? [String: Any] else { throw ConnectionError("PR 資料格式不對") }
        var pull = GitHub.pull(d, repo: repo)
        if !pull.headSHA.isEmpty {
            pull.checks = (try? await get(repoPath(repo) + "/commits/\(pull.headSHA)/check-runs?per_page=100")).map(GitHub.checks)
        }
        let files = (try await get(p + "/files?per_page=100") as? [[String: Any]] ?? []).map(GitHub.file)
        return GHPullDetail(pull: pull, body: d["body"] as? String ?? "",
                            additions: (d["additions"] as? NSNumber)?.intValue ?? 0,
                            deletions: (d["deletions"] as? NSNumber)?.intValue ?? 0,
                            changedFiles: (d["changed_files"] as? NSNumber)?.intValue ?? files.count,
                            mergeableState: d["mergeable_state"] as? String ?? "", files: files)
    }
}
