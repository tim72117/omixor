import AppKit
import CryptoKit
import Foundation
import LocalAuthentication

/// 旁白語音批次的流程:
///   送出   npm run voice -- <腳本…> --batch    (Gemini Batch API,一次只能有一批)
///   輪詢   npm run voice -- --collect          (沒跑完不會動任何東西;跑完就下載、轉 mp3、上傳 GCS、改寫 .voice.json)
///   待發佈 比對收結果前後的 .voice.json,列出新增的段落,可試聽
///   發佈   人按下後(Touch ID)PUT /api/admin/scripts/<路徑>,線上立即生效,不用重新部署
/// 音檔上傳到 GCS 時檔名是內容雜湊,網站要等 .voice.json 換新才會引用,所以收結果可以自動做,發佈一定要人按。
/// 不改 shuttle 專案的任何程式:只執行它既有的 npm run voice 指令,讀它寫出的檔案。
@MainActor
final class VoicePipeline {
    unowned let app: AppState
    private var timer: Timer?
    private var busy = false
    private var activity: NSObjectProtocol?

    init(app: AppState) { self.app = app }

    // MARK: - 設定

    static let defaults = UserDefaults.standard

    var shuttleRoot: URL {
        if let p = ProcessInfo.processInfo.environment["OPS_SHUTTLE_PATH"] { return URL(fileURLWithPath: p) }
        let p = Self.defaults.string(forKey: "voice.shuttlePath") ?? NSHomeDirectory() + "/Documents/shuttle"
        return URL(fileURLWithPath: (p as NSString).expandingTildeInPath)
    }
    var webDir: URL { shuttleRoot.appendingPathComponent("web") }
    var liveDir: URL { webDir.appendingPathComponent("src/content/live") }
    var batchFile: URL { webDir.appendingPathComponent("recordings/voice-batch.json") }
    var seedFile: URL { shuttleRoot.appendingPathComponent("server/internal/store/courses_seed.json") }

    var pollMinutes: Int {
        get { max(1, Self.defaults.integer(forKey: "voice.pollMinutes").nonZero ?? 10) }
        set { Self.defaults.set(newValue, forKey: "voice.pollMinutes") }
    }

    /// 旁白批次是全部組織共用的,放在資料夾根目錄
    private var storeURL: URL { OrgFiles.root.appendingPathComponent("voice-jobs.json") }

    // MARK: - 啟動與儲存

    func start(timer useTimer: Bool = true) {
        load()
        // 重開 app 時,做到一半的步驟回到可以重試的狀態
        for i in app.voiceJobs.indices {
            switch app.voiceJobs[i].state {
            case .submitting:
                app.voiceJobs[i].state = FileManager.default.fileExists(atPath: batchFile.path) ? .processing : .queued
                app.voiceJobs[i].nextCheckAt = .now
            case .collecting:
                app.voiceJobs[i].state = .processing
                app.voiceJobs[i].nextCheckAt = .now
            case .publishing:
                app.voiceJobs[i].state = .ready
            default: break
            }
        }
        save()
        guard useTimer else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 30, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.tick() }
        }
        Task { await tick() }
    }

    private func load() {
        guard let data = try? Data(contentsOf: storeURL) else { return }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        app.voiceJobs = (try? d.decode([VoiceJob].self, from: data)) ?? []
    }

    func save() {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? e.encode(app.voiceJobs) { try? data.write(to: storeURL, options: .atomic) }
        updateBadge()
    }

    private func update(_ id: String, _ change: (inout VoiceJob) -> Void) {
        guard let i = app.voiceJobs.firstIndex(where: { $0.id == id }) else { return }
        change(&app.voiceJobs[i])
        app.voiceJobs[i].updatedAt = .now
        save()
    }

    func job(_ id: String) -> VoiceJob? { app.voiceJobs.first { $0.id == id } }

    // MARK: - 送出

    enum SubmitError: LocalizedError {
        case invalid(String)
        var errorDescription: String? { if case .invalid(let m) = self { return m }; return nil }
    }

    /// 建立一個批次工作(排隊,輪到時送出)。回傳工作。
    @discardableResult
    func submit(scripts: [String], all: Bool, limit: Int?, by actor: Actor) throws -> VoiceJob {
        let scripts = try normalize(scripts)
        if !all && scripts.isEmpty { throw SubmitError.invalid("至少要指定一份腳本,或選「全部腳本」") }
        let num = (app.voiceJobs.compactMap { Int($0.id.dropFirst(3)) }.max() ?? 0) + 1
        var job = VoiceJob(id: "VJ-\(num)", scripts: all ? [] : scripts, all: all, limit: limit, state: .queued,
                           createdAt: .now, updatedAt: .now, by: Self.actorId(actor))
        job.note("建立:\(job.title)")
        app.voiceJobs.insert(job, at: 0)
        save()
        app.log(actor, "waveform", "建立旁白批次 \(job.id):\(job.title)", .pipelines)
        if timer != nil { Task { await tick() } }
        return job
    }

    /// 試算:列出會送哪些段落,不呼叫 API、不花錢
    func dryRun(scripts: [String], all: Bool, limit: Int?) async throws -> String {
        let scripts = try normalize(scripts)
        if !all && scripts.isEmpty { throw SubmitError.invalid("至少要指定一份腳本,或選「全部腳本」") }
        let r = await npm(voiceArgs(scripts: scripts, all: all, limit: limit) + ["--batch", "--dry-run"])
        if !r.ok { throw SubmitError.invalid(r.message) }
        return r.out.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    func cancel(_ id: String) {
        guard let j = job(id), j.state == .queued || j.state == .failed else { return }
        update(id) { $0.state = .canceled; $0.note("已取消") }
    }

    /// 立刻查一次(不等下一輪)
    func checkNow(_ id: String) {
        update(id) { $0.nextCheckAt = .now }
        Task { await tick() }
    }

    private func normalize(_ scripts: [String]) throws -> [String] {
        var out: [String] = []
        for raw in scripts.flatMap({ $0.split(whereSeparator: { $0 == " " || $0 == "," || $0 == "\n" }) }) {
            var s = String(raw).trimmingCharacters(in: .whitespaces)
            if s.isEmpty { continue }
            if let r = s.range(of: "src/content/live/") { s = String(s[r.upperBound...]) }
            if !s.hasSuffix(".json") { s += ".json" }
            if s.hasSuffix(".voice.json") || s.hasSuffix(".mix.json") { throw SubmitError.invalid("\(s) 是產生出來的清單,請給腳本本身") }
            guard FileManager.default.fileExists(atPath: liveDir.appendingPathComponent(s).path) else {
                throw SubmitError.invalid("找不到腳本 src/content/live/\(s)")
            }
            if !out.contains(s) { out.append(s) }
        }
        return out
    }

    private func voiceArgs(scripts: [String], all: Bool, limit: Int?) -> [String] {
        var a = all ? ["--all"] : scripts
        if let limit, limit > 0 { a += ["--limit", "\(limit)"] }
        return a
    }

    private func npm(_ args: [String]) async -> Shell.Result {
        await Shell.run(["npm", "run", "--silent", "voice", "--"] + args, cwd: webDir)
    }

    // MARK: - 輪詢

    func tick() async {
        guard !busy else { return }
        busy = true
        defer { busy = false; updateActivity() }
        adoptExternalBatch()
        if let j = app.voiceJobs.first(where: { $0.state == .processing }), (j.nextCheckAt ?? .distantPast) <= .now {
            await collect(j.id)
        }
        // 收完就空出位置,同一輪接著送排隊的下一批
        if app.voiceJobs.contains(where: \.state.holdsBatch) { return }
        if let next = app.voiceJobs.last(where: { $0.state == .queued }) { await send(next.id) }
    }

    /// Ops 以外(終端機)送出的批次也接手追蹤
    private func adoptExternalBatch() {
        guard !app.voiceJobs.contains(where: \.state.holdsBatch),
              let saved = readBatchFile(), let name = saved["name"] as? String else { return }
        let num = (app.voiceJobs.compactMap { Int($0.id.dropFirst(3)) }.max() ?? 0) + 1
        var job = VoiceJob(id: "VJ-\(num)", scripts: saved["targets"] as? [String] ?? [], all: false, limit: nil,
                           state: .processing, createdAt: .now, updatedAt: .now, by: "external")
        job.batchName = name
        job.clipCount = (saved["items"] as? [Any])?.count
        job.baseline = snapshot()
        job.nextCheckAt = .now
        job.note("接手 Ops 以外送出的批次 \(name)(送出時的清單沒有記錄,改用現在的狀態比對)")
        app.voiceJobs.insert(job, at: 0)
        save()
    }

    private func readBatchFile() -> [String: Any]? {
        guard let d = try? Data(contentsOf: batchFile) else { return nil }
        return try? JSONSerialization.jsonObject(with: d) as? [String: Any]
    }

    private func send(_ id: String) async {
        guard let j = job(id) else { return }
        if FileManager.default.fileExists(atPath: batchFile.path) {
            update(id) { $0.note("已經有一批還沒收,等它收完再送") }
            return
        }
        update(id) {
            $0.state = .submitting
            $0.baseline = snapshot()
            $0.note("送出批次…")
        }
        let r = await npm(voiceArgs(scripts: j.scripts, all: j.all, limit: j.limit) + ["--batch"])
        guard r.ok else {
            update(id) { $0.state = .failed; $0.error = r.message; $0.note("送出失敗:\(r.message)") }
            notify(id, "旁白批次 \(id) 送出失敗")
            return
        }
        if r.out.contains("沒有缺的旁白") {
            update(id) { $0.state = .nothingToDo; $0.note("沒有缺的旁白,不用送出") }
            return
        }
        let saved = readBatchFile()
        update(id) {
            $0.state = .processing
            $0.batchName = saved?["name"] as? String
            $0.clipCount = (saved?["items"] as? [Any])?.count
            $0.nextCheckAt = Date().addingTimeInterval(Double(pollMinutes) * 60)
            $0.note(r.out.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        app.log(Self.actor(j.by), "paperplane", "送出旁白批次 \(id)(\(saved.flatMap { ($0["items"] as? [Any])?.count } ?? 0) 段)", .pipelines)
    }

    private func collect(_ id: String) async {
        update(id) { $0.state = .collecting; $0.lastCheckAt = .now; $0.checks += 1 }
        let r = await npm(["--collect"])
        let out = r.out.trimmingCharacters(in: .whitespacesAndNewlines)
        if r.ok && out.contains("還在處理") {
            let state = out.range(of: #"還在處理\(([^)]*)\)"#, options: .regularExpression)
                .map { String(out[$0]).replacingOccurrences(of: "還在處理(", with: "").replacingOccurrences(of: ")", with: "") }
            update(id) {
                $0.state = .processing
                $0.batchState = state
                $0.error = nil
                $0.nextCheckAt = Date().addingTimeInterval(Double(pollMinutes) * 60)
                if $0.checks == 1 || $0.checks % 6 == 0 { $0.note("還在處理(\(state ?? "?")),\(pollMinutes) 分鐘後再查") }
            }
            return
        }
        if r.ok {
            let failed = Self.failedClips(in: out)
            let baseline = job(id)?.baseline ?? [:]
            let changes = diff(from: baseline)
            update(id) {
                $0.state = changes.isEmpty ? .nothingToDo : .ready
                $0.changes = changes
                $0.failedClips = failed
                $0.batchState = "JOB_STATE_SUCCEEDED"
                $0.error = nil
                $0.note(out)
                $0.note(changes.isEmpty ? "收完了,但沒有任何旁白清單有變動" : "收完了:\(changes.count) 份旁白清單有變動,等你發佈")
            }
            if !changes.isEmpty {
                app.log(.system, "waveform.badge.checkmark", "旁白批次 \(id) 完成,\(changes.count) 份清單待發佈", .pipelines)
                notify(id, "旁白批次 \(id) 完成,可以發佈了")
            }
            return
        }
        let msg = r.message
        if msg.contains("沒有完成") || msg.contains("沒有送出中的批次") {
            // 批次失敗、取消或過期(voice.mjs 已清掉紀錄),或紀錄被手動刪掉
            update(id) { $0.state = .failed; $0.error = msg; $0.note(msg) }
            notify(id, "旁白批次 \(id) 失敗")
        } else {
            // 網路、額度、上傳失敗等:下一輪重試(voice.mjs 每段完成就寫入清單,重跑不會重複計費)
            update(id) {
                $0.state = .processing
                $0.error = msg
                $0.nextCheckAt = Date().addingTimeInterval(Double(pollMinutes) * 60)
                $0.note("收結果時出錯,\(pollMinutes) 分鐘後重試:\(msg)")
            }
        }
    }

    /// voice.mjs --collect 的最後一段:「收到 N 段,M 段失敗(…):」後面縮排的每一行是一段失敗的旁白
    static func failedClips(in out: String) -> [String] {
        let lines = out.components(separatedBy: "\n")
        guard let i = lines.lastIndex(where: { $0.contains("段失敗") }) else { return [] }
        return lines[(i + 1)...].filter { $0.hasPrefix("  ") }.map { $0.trimmingCharacters(in: .whitespaces) }
    }

    // MARK: - 比對

    /// 所有 .voice.json 的內容雜湊與音檔清單
    func snapshot() -> [String: VoiceBaseline] {
        var out: [String: VoiceBaseline] = [:]
        for (path, data) in manifests() {
            out[path] = VoiceBaseline(sha: Self.sha(data), files: Self.clips(data).map(\.file))
        }
        return out
    }

    private func manifests() -> [(String, Data)] {
        guard let e = FileManager.default.enumerator(at: liveDir, includingPropertiesForKeys: nil) else { return [] }
        let base = liveDir.standardizedFileURL.path + "/"
        var out: [(String, Data)] = []
        for case let url as URL in e where url.lastPathComponent.hasSuffix(".voice.json") {
            guard let d = try? Data(contentsOf: url) else { continue }
            out.append((url.standardizedFileURL.path.replacingOccurrences(of: base, with: ""), d))
        }
        return out.sorted { $0.0 < $1.0 }
    }

    func diff(from baseline: [String: VoiceBaseline]) -> [VoiceFileChange] {
        let lessons = lessonMap()
        return manifests().compactMap { path, data in
            let sha = Self.sha(data)
            let old = baseline[path]
            if old?.sha == sha { return nil }
            let clips = Self.clips(data)
            let oldFiles = Set(old?.files ?? [])
            let nowFiles = Set(clips.map(\.file))
            let script = String(path.dropLast(".voice.json".count)) + ".json"
            let owner = lessons[script]
            return VoiceFileChange(path: path, isNew: old == nil, clips: clips.count, totalMs: clips.reduce(0) { $0 + $1.ms },
                                   added: clips.filter { !oldFiles.contains($0.file) }, removed: oldFiles.subtracting(nowFiles).count,
                                   course: owner?.0, lesson: owner?.1, sha: sha, publish: nil)
        }
    }

    /// 腳本路徑 → (課程 id, 課 id),從 courses_seed.json 的 script / files 查
    func lessonMap() -> [String: (String, String)] {
        guard let d = try? Data(contentsOf: seedFile),
              let courses = try? JSONSerialization.jsonObject(with: d) as? [[String: Any]] else { return [:] }
        var map: [String: (String, String)] = [:]
        for c in courses {
            guard let cid = c["id"] as? String else { continue }
            for u in c["units"] as? [[String: Any]] ?? [] {
                for l in u["lessons"] as? [[String: Any]] ?? [] {
                    guard let lid = l["id"] as? String else { continue }
                    for p in [l["script"] as? String].compactMap({ $0 }) + (l["files"] as? [String] ?? []) {
                        map[p] = (cid, lid)
                    }
                }
            }
        }
        return map
    }

    static func clips(_ data: Data) -> [VoiceClip] {
        guard let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let clips = obj["clips"] as? [String: Any] else { return [] }
        return clips.compactMap { text, v in
            guard let c = v as? [String: Any], let file = c["file"] as? String else { return nil }
            return VoiceClip(text: text, file: file, ms: (c["ms"] as? NSNumber)?.intValue ?? 0)
        }.sorted { $0.file < $1.file }
    }

    static func sha(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    // MARK: - 發佈(只能由人在介面上觸發)

    /// 發佈到哪個連線(設定 › 連線 裡的 id),預設 evoleap 正式站
    var targetId: String {
        get { Self.defaults.string(forKey: "voice.connection") ?? "evoleap" }
        set { Self.defaults.set(newValue, forKey: "voice.connection"); app.objectWillChange.send() }
    }

    var target: Connection? { app.connections.first { $0.id == targetId } }

    func publish(_ id: String) async {
        guard let j = job(id), j.state == .ready else { return }
        guard let target, let store = app.connStore else {
            update(id) { $0.note("找不到發佈目的地的連線,請在設定 › 連線 確認") }
            return
        }
        guard store.hasToken(target) else {
            update(id) { $0.note("發佈前要先登入「\(target.name)」") }
            return
        }
        guard await confirmOwner("發佈 \(j.changes.count) 份旁白清單到「\(target.name)」") else {
            update(id) { $0.note("取消發佈(未通過身分確認)") }
            return
        }
        update(id) { $0.state = .publishing; $0.note("發佈到「\(target.name)」(\(target.baseURL))…") }
        var allOK = true
        for (i, c) in j.changes.enumerated() where c.publish != "ok" {
            let url = liveDir.appendingPathComponent(c.path)
            var result = "ok"
            if let data = try? Data(contentsOf: url) {
                if Self.sha(data) != c.sha {
                    result = "收結果後這個檔又被改過,請重新檢查後再發佈"
                } else {
                    do {
                        var path = "/api/admin/scripts/" + ConnHTTP.escapePath(c.path)
                        if let course = c.course, let lesson = c.lesson {
                            path += "?" + ConnHTTP.formEncode(["course": course, "lesson": lesson])
                        }
                        _ = try await store.request(target.id, "PUT", path, body: data)
                        // 用 API 讀回來比對,確認線上拿到的就是這份內容
                        let online = try await store.request(target.id, "GET", "/api/scripts/" + ConnHTTP.escapePath(c.path))
                        let local = try JSONSerialization.jsonObject(with: data)
                        if !(online as AnyObject).isEqual(local) {
                            result = "寫入成功,但讀回來的內容和本機不同"
                        }
                    } catch {
                        result = error.localizedDescription
                    }
                }
            } else {
                result = "讀不到本機檔案"
            }
            if result != "ok" { allOK = false }
            update(id) {
                $0.changes[i].publish = result
                $0.note("\(c.path):\(result == "ok" ? "已發佈,讀回比對一致" : result)")
            }
        }
        update(id) {
            if allOK {
                $0.state = .published
                $0.publishedAt = .now
                $0.publishedTo = target.name
                $0.note("全部發佈完成。記得把這些 .voice.json commit 進 shuttle 的 repo")
            } else {
                $0.state = .ready
                $0.note("有檔案發佈失敗,修好後可以再按一次發佈(已成功的不會重送)")
            }
        }
        app.log(.person(app.me), allOK ? "checkmark.seal" : "exclamationmark.triangle",
                "\(allOK ? "發佈" : "部分發佈")旁白 \(id) 到「\(target.name)」(\(j.changes.count) 份)", .pipelines)
    }

    /// Touch ID(或電腦密碼)確認是本人
    private func confirmOwner(_ reason: String) async -> Bool {
        let ctx = LAContext()
        var err: NSError?
        guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: &err) else { return true }
        return (try? await ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)) ?? false
    }

    // MARK: - 通知與省電

    private func notify(_ id: String, _ text: String) {
        NSApp?.requestUserAttention(.informationalRequest)
    }

    private func updateBadge() {
        let n = app.voiceJobs.filter { $0.state == .ready }.count
        NSApp?.dockTile.badgeLabel = n > 0 ? "\(n)" : nil
    }

    /// 有批次在跑時不讓 App Nap 把輪詢拖慢
    private func updateActivity() {
        let active = app.voiceJobs.contains { $0.state.holdsBatch || $0.state == .queued }
        if active, activity == nil {
            activity = ProcessInfo.processInfo.beginActivity(options: [.userInitiatedAllowingIdleSystemSleep],
                                                             reason: "等待旁白批次完成")
        } else if !active, let a = activity {
            ProcessInfo.processInfo.endActivity(a)
            activity = nil
        }
    }

    // MARK: - 執行者

    static func actorId(_ actor: Actor) -> String {
        switch actor {
        case .person(let id): return id
        case .ai(let id): return id
        case .system: return "system"
        }
    }

    static func actor(_ id: String) -> Actor {
        switch id {
        case Actor.mcpId: return .mcp
        case "system", "external": return .system
        default: return .person(id)
        }
    }
}

private extension Int {
    var nonZero: Int? { self == 0 ? nil : self }
}
