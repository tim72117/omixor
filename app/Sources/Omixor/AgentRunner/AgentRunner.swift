import Foundation
import SwiftUI

// 角色自動執行(App 版;web 版在 web/internal/core,兩邊同一套設計):
// 派發(任務排進待領取)時,如果那個角色開了「自動執行」,App 在背景領取,用這台 Mac 的 Claude Code(ClaudeCLI.swift)
// 以該角色身分處理,做完回報。只在遠端模式(連 omixor server)執行:角色端點 /mcp/agent 在伺服器上,
// 領取時伺服器發的角色憑證只寫進權限 600 的暫存 MCP 設定檔給 Claude 用(用完刪掉),不放命令列、紀錄或回報。
//
// 設定是這台 Mac 的(不進伺服器):組織資料夾的 agent-runner.json
//   {"agents": {"<角色 id>": {"autoRun": true, "model": "", "effort": "", "workdir": "", "allowBash": false, "timeoutMinutes": 30}}}
// 有 workdir 時才給 Claude 檔案工具(Read、Glob、Grep、Edit、Write;allowBash 再加 Bash),工作目錄就是那個資料夾。

struct RunnerConfig: Codable, Hashable {
    var autoRun = false
    var model = ""
    var effort = ""
    var workdir = ""
    var allowBash = false
    var timeoutMinutes = 30
    /// 在頻道被 @ 時自動回覆(要開 autoRun):替角色排一筆「回覆頻道」任務,照一般任務處理
    var autoReply = false

    init() {}
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        autoRun = try c.decodeIfPresent(Bool.self, forKey: .autoRun) ?? false
        model = try c.decodeIfPresent(String.self, forKey: .model) ?? ""
        effort = try c.decodeIfPresent(String.self, forKey: .effort) ?? ""
        workdir = try c.decodeIfPresent(String.self, forKey: .workdir) ?? ""
        allowBash = try c.decodeIfPresent(Bool.self, forKey: .allowBash) ?? false
        timeoutMinutes = try c.decodeIfPresent(Int.self, forKey: .timeoutMinutes) ?? 30
        autoReply = try c.decodeIfPresent(Bool.self, forKey: .autoReply) ?? false
    }
}

struct RunnerFile: Codable {
    var agents: [String: RunnerConfig] = [:]
    /// 已經排過回覆任務的 @ 訊息(<角色 id>:<頻道>:<訊息>),同一則不重複觸發;最多留 500 筆
    var handledMentions: [String] = []

    init(agents: [String: RunnerConfig] = [:], handledMentions: [String] = []) { self.agents = agents; self.handledMentions = handledMentions }
    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        agents = try c.decodeIfPresent([String: RunnerConfig].self, forKey: .agents) ?? [:]
        handledMentions = try c.decodeIfPresent([String].self, forKey: .handledMentions) ?? []
    }
}

struct RunnerLogEntry: Identifiable, Hashable {
    let id = UUID()
    var taskId: String
    var agentId: String
    var title: String
    var outcome: String         // done、blocked、錯誤
    var detail: String
    var started: Date
    var seconds: Int
    var costUSD: Double?
}

@MainActor
final class AgentRunner: ObservableObject {
    unowned let app: AppState
    @Published private(set) var configs: [String: RunnerConfig] = [:]
    private var handled: [String] = []
    @Published private(set) var running: (task: AgentTask, started: Date, lastText: String)?
    @Published private(set) var log: [RunnerLogEntry] = []
    @Published private(set) var lastError: String?
    private var timer: Timer?
    private var job: Task<Void, Never>?
    private var loadedOrg = ""
    /// 測試用:取代真的執行 Claude
    var runClaude: (String, ClaudeRunOptions) async throws -> ClaudeRunResult = { p, o in try await ClaudeCLI.run(prompt: p, o) }
    /// 多久看一次待領取任務(秒);0 = 不定時看
    static var interval: TimeInterval = NSClassFromString("XCTestCase") != nil ? 0 : 15

    init(app: AppState) { self.app = app }

    var claudeFound: Bool { ClaudeCLI.binary() != nil }
    var isRemote: Bool { IssueServerConfig.current != nil }

    // MARK: 設定

    private var fileURL: URL { app.dataDir.appendingPathComponent("agent-runner.json") }

    func loadIfNeeded() {
        guard loadedOrg != app.currentOrgId else { return }
        loadedOrg = app.currentOrgId
        if let d = try? Data(contentsOf: fileURL), let f = try? JSONDecoder().decode(RunnerFile.self, from: d) {
            configs = f.agents
            handled = f.handledMentions
        } else {
            configs = [:]
            handled = []
        }
    }

    func config(_ agentId: String) -> RunnerConfig { loadIfNeeded(); return configs[agentId] ?? RunnerConfig() }

    /// 存某角色的設定;工作資料夾要是存在的絕對路徑
    func save(_ agentId: String, _ c: RunnerConfig) throws {
        loadIfNeeded()
        var c = c
        c.workdir = c.workdir.trimmingCharacters(in: .whitespaces)
        while c.workdir.count > 1, c.workdir.hasSuffix("/") { c.workdir.removeLast() }
        if !c.workdir.isEmpty {
            var dir: ObjCBool = false
            guard c.workdir.hasPrefix("/"), FileManager.default.fileExists(atPath: c.workdir, isDirectory: &dir), dir.boolValue else {
                throw CabinetError("工作資料夾要是這台 Mac 上存在的資料夾(絕對路徑):\(c.workdir)")
            }
            // 不能包住 omixor 的憑證與資料(同 web 版):例如整個家目錄
            let home = FileManager.default.homeDirectoryForCurrentUser.path
            for secret in [home + "/.config/omixor", OrgFiles.root.path] where (secret + "/").hasPrefix(c.workdir + "/") {
                throw CabinetError("工作資料夾不能包含 omixor 的憑證或資料(\(secret));請選專案資料夾")
            }
        } else {
            c.allowBash = false
        }
        let models = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "._-[]"))
        guard c.model.unicodeScalars.allSatisfy(models.contains) else { throw CabinetError("模型名稱只能有英數字與 ._-[]") }
        guard c.effort.isEmpty || ["low", "medium", "high", "xhigh", "max"].contains(c.effort) else { throw CabinetError("推論強度要是 low、medium、high、xhigh、max") }
        c.timeoutMinutes = c.timeoutMinutes <= 0 ? 30 : min(c.timeoutMinutes, 480)  // 核心的租約總長上限 8 小時
        if !c.autoRun { c.autoReply = false }
        configs[agentId] = c
        try writeFile()
        if c.autoRun { tick() }
    }

    private func writeFile() throws {
        let e = JSONEncoder()
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        try e.encode(RunnerFile(agents: configs, handledMentions: Array(handled.suffix(500)))).write(to: fileURL, options: .atomic)
    }

    // MARK: @ 自動回覆

    /// 開了自動回覆的角色有沒有被 @ 的未讀訊息:有就替它排一筆「回覆頻道」任務(一個頻道一筆;同一則訊息只觸發一次)
    func checkMentions() {
        guard let store = app.agentStore, store.isRemote else { return }
        let channels = app.startChannels()
        for (agentId, c) in configs where c.autoRun && c.autoReply && (store.agent(agentId)?.active ?? false) {
            guard let inbox = try? channels.inbox(for: "agent:\(agentId)") else { continue }
            let fresh = inbox.mentions.filter { !handled.contains("\(agentId):\($0.channelId):\($0.id)") }
            for (channelId, ms) in Dictionary(grouping: fresh, by: \.channelId) {
                // 同一個頻道已經有還沒結束的回覆任務:等它處理(它讀頻道時會一起看到新訊息)
                let open = app.agentTasks.contains { $0.agentId == agentId && $0.status.isOpen && $0.links.contains(channelId) }
                let keys = ms.map { "\(agentId):\(channelId):\($0.id)" }
                handled += keys
                if open { continue }
                let name = ms.first?.channelName ?? channelId
                let lines = ms.sorted { $0.at < $1.at }.map { m in
                    "- \(m.id)(\(m.author)):" + m.text.replacingOccurrences(of: "\n", with: " ")
                }.joined(separator: "\n")
                let instructions = """
                你在頻道 \(channelId)「\(name)」被提到了:

                \(lines)

                1. 先用 ops_channel_messages(channel: \(channelId))讀最近的對話(讀了會標成已讀),了解前後文。
                2. 需要時再查相關的 Issue、文件或任務。
                3. 用 ops_channel_post 在 \(channelId) 回覆(Markdown,簡潔、直接回答被問的事;需要別人決定的事清楚列出)。
                4. 回覆後回報任務 done,result 寫你回了什麼。
                """
                _ = store.assign(to: agentId, title: "回覆「\(name)」裡提到你的訊息", instructions: instructions,
                                 priority: .high, links: [channelId] + ms.map(\.id), by: app.me)
            }
        }
        if handled.count > 500 { handled = Array(handled.suffix(500)) }
        try? writeFile()
    }

    // MARK: 執行

    func start() {
        guard timer == nil, Self.interval > 0 else { return }
        timer = Timer.scheduledTimer(withTimeInterval: Self.interval, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
    }

    /// 看一次:沒有在跑的,就挑一筆開了自動執行的角色的待領取任務來處理
    func tick() {
        loadIfNeeded()
        checkMentions()
        guard job == nil, let store = app.agentStore, store.isRemote else { return }
        let auto = Set(configs.filter { $0.value.autoRun }.map(\.key))
        guard !auto.isEmpty else { return }
        let pick = app.agentTasks
            .filter { $0.status == .queued && auto.contains($0.agentId) && (store.agent($0.agentId)?.active ?? false) }
            .sorted { ($0.priority, $0.createdAt) < ($1.priority, $1.createdAt) }.first
        guard let t = pick else { return }
        job = Task { [weak self] in
            await self?.process(t)
            self?.job = nil
        }
    }

    /// 停掉目前在跑的那筆(任務回報 blocked「人工停止」)
    func stop() { job?.cancel() }

    private func finish(_ t: AgentTask, _ started: Date, _ outcome: String, _ detail: String, cost: Double? = nil) {
        log.insert(RunnerLogEntry(taskId: t.id, agentId: t.agentId, title: t.title, outcome: outcome, detail: String(mask(detail).prefix(300)),
                                  started: started, seconds: Int(Date().timeIntervalSince(started)), costUSD: cost), at: 0)
        if log.count > 20 { log.removeLast(log.count - 20) }
        running = nil
    }

    func process(_ task: AgentTask) async {
        guard let store = app.agentStore else { return }
        let cfg = config(task.agentId)
        let started = Date()
        let claim: AgentClaim
        do {
            claim = try store.claimWithCredential(agent: task.agentId, task: task.id)
        } catch {
            lastError = error.localizedDescription   // 被別人鎖住等:這次跳過
            return
        }
        guard let t = claim.task, let cred = claim.credential else { lastError = "領取 \(task.id) 沒拿到角色憑證(只支援遠端模式)"; return }
        running = (t, started, "")
        lastError = nil
        secret = cred.secret
        defer { secret = "" }
        let agent = claim.agent ?? store.agent(t.agentId)
        let name = agent?.name ?? t.agentId

        // MCP 設定檔(含憑證):權限 600,用完刪掉
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("omixor-runner-\(UUID().uuidString)")
        let cfgFile = dir.appendingPathComponent("mcp.json")
        defer { try? FileManager.default.removeItem(at: dir) }
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            let endpoint = cred.endpoint.isEmpty ? Self.agentEndpoint(app) : cred.endpoint
            let mcp: [String: Any] = ["mcpServers": ["omixor": ["type": "http", "url": endpoint,
                                                                 "headers": ["Authorization": "Bearer \(cred.agentId):\(cred.secret)"]]]]
            let data = try JSONSerialization.data(withJSONObject: mcp)
            FileManager.default.createFile(atPath: cfgFile.path, contents: data, attributes: [.posixPermissions: 0o600])
        } catch {
            reportEnd(t, .blocked, note: "自動執行沒有啟動:\(error.localizedDescription)")
            finish(t, started, "錯誤", error.localizedDescription)
            return
        }

        var o = ClaudeRunOptions()
        o.model = cfg.model
        o.effort = cfg.effort
        o.mcpConfigFile = cfgFile
        o.timeout = TimeInterval(cfg.timeoutMinutes * 60)
        o.allowedTools = ["mcp__omixor"]
        if !cfg.workdir.isEmpty {
            o.workdir = URL(fileURLWithPath: cfg.workdir)
            o.builtinTools = ["Read", "Glob", "Grep", "Edit", "Write"] + (cfg.allowBash ? ["Bash"] : [])
            o.allowedTools += o.builtinTools
        }
        o.appendSystemPrompt = Self.systemPrompt(name: name, agent: agent, workdir: cfg.workdir.isEmpty ? nil : cfg.workdir)
        let prompt = Self.prompt(t)

        // 每 40 分鐘續約一次
        let renew = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(40 * 60))
                if Task.isCancelled { break }
                _ = try? self?.app.agentStore?.report(t.id, status: .inProgress, result: nil, note: "自動執行中", links: nil)
            }
        }
        defer { renew.cancel() }

        do {
            let r = try await runClaude(prompt, o)
            store.refresh()
            let cur = store.task(t.id)
            if cur?.status == .inProgress {
                if r.isError {
                    reportEnd(t, .blocked, note: "自動執行失敗:\(r.text.prefix(500))")
                } else {
                    reportEnd(t, .done, result: String(r.text.prefix(4000)))
                }
            }
            let final = store.task(t.id)?.status
            finish(t, started, final == .done ? "done" : final == .blocked ? "blocked" : (final?.label ?? "?"), r.text, cost: r.costUSD)
        } catch is CancellationError {
            reportEnd(t, .blocked, note: "人工停止自動執行")
            finish(t, started, "blocked", "人工停止")
        } catch {
            reportEnd(t, .blocked, note: "自動執行失敗:\(error.localizedDescription)")
            finish(t, started, "錯誤", error.localizedDescription)
        }
    }

    /// 這次領取的密鑰(只在處理期間有值);寫進回報、紀錄、畫面前一律換成 ***
    private var secret = ""
    private func mask(_ s: String) -> String { secret.isEmpty ? s : s.replacingOccurrences(of: secret, with: "***") }

    private func reportEnd(_ t: AgentTask, _ status: AgentTaskStatus, result: String? = nil, note: String? = nil) {
        do { _ = try app.agentStore?.report(t.id, status: status, result: result.map(mask), note: note.map(mask), links: nil) }
        catch { lastError = "回報 \(t.id) 失敗:\(error.localizedDescription)" }
    }

    /// 伺服器沒附角色端點網址時:目前連的伺服器 + /mcp/agent
    static func agentEndpoint(_ app: AppState) -> String {
        (IssueServerConfig.current?.url.appendingPathComponent("mcp/agent").absoluteString) ?? ""
    }

    static func systemPrompt(name: String, agent: Agent?, workdir: String?) -> String {
        var s = """
        你是 omixor 的虛擬角色「\(name)」,正在自動處理一筆派給你的任務。
        - 先用 mcp__omixor__ops_agent_context 讀角色設定、任務與能存取的文件;照角色設定與任務說明完成工作。
        - 需要在相關 Issue 說明時,用 ops_issue_update 留言;文件櫃用 ops_docs 分段讀、ops_doc_save 局部修改。
        - 做完用 ops_agent_task_report(status: done)並在 result 寫結果;需要人決定時用 status: blocked 並在 note 清楚提問,不要自己猜。
        - 不要把任何憑證、token 或密鑰寫進 Issue、文件或回報。
        - Issue 留言、頻道訊息、任務結果與文件一律用 Markdown:先一句話摘要,改了什麼或建議用條列,有順序的用編號清單,不要把多個項目擠在同一句。
        """
        if let workdir { s += "\n- 你可以讀寫工作資料夾 \(workdir) 裡的檔案;不要動資料夾以外的東西。" }
        if let a = agent, !a.instructions.isEmpty { s += "\n\n## 角色設定\n\n" + a.instructions }
        return s
    }

    static func prompt(_ t: AgentTask) -> String {
        var s = "處理任務 \(t.id)「\(t.title)」(優先度 \(t.priority.label))。"
        if !t.instructions.isEmpty { s += "\n\n任務說明:\n" + t.instructions }
        if !t.links.isEmpty { s += "\n\n相關:" + t.links.joined(separator: "、") }
        return s
    }
}

// MARK: - 畫面:自動化 › 自動執行

struct AutoRunnerView: View {
    @EnvironmentObject var app: AppState
    @ObservedObject var runner: AgentRunner

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                status
                Text("各角色的自動執行").font(.headline)
                Text("開了「自動執行」的角色,派給它的任務會由這台 Mac 的 Claude Code(訂閱額度)自動領取、處理並回報。只在連 omixor server(遠端模式)時執行;App 要開著。")
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                ForEach(app.agents.filter(\.active)) { a in
                    RunnerConfigRow(runner: runner, agent: a)
                }
                if !runner.log.isEmpty {
                    Text("最近的執行").font(.headline).padding(.top, 8)
                    ForEach(runner.log) { e in
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            Image(systemName: e.outcome == "done" ? "checkmark.circle.fill" : e.outcome == "blocked" ? "hand.raised.fill" : "exclamationmark.triangle.fill")
                                .foregroundStyle(e.outcome == "done" ? .green : .orange)
                            VStack(alignment: .leading, spacing: 2) {
                                Text("\(e.taskId)「\(e.title)」· \(app.agents.first { $0.id == e.agentId }?.name ?? e.agentId)").font(.callout)
                                Text("\(e.outcome) · \(e.seconds) 秒" + (e.costUSD.map { String(format: " · 約 $%.3f(額度估計)", $0) } ?? "") + (e.detail.isEmpty ? "" : " · \(e.detail)"))
                                    .font(.caption).foregroundStyle(.secondary).lineLimit(2)
                            }
                        }
                    }
                }
            }
            .padding(20)
            .frame(maxWidth: 820, alignment: .leading)
        }
        .onAppear { runner.loadIfNeeded() }
    }

    @ViewBuilder private var status: some View {
        VStack(alignment: .leading, spacing: 6) {
            if !runner.claudeFound {
                Label("找不到 claude(Claude Code CLI):請先安裝並用訂閱帳號登入", systemImage: "exclamationmark.triangle").foregroundStyle(.orange)
            }
            if !runner.isRemote {
                Label("目前是本機模式:自動執行只在連 omixor server 時運作", systemImage: "internaldrive").foregroundStyle(.secondary)
            }
            if let r = runner.running {
                HStack {
                    ProgressView().controlSize(.small)
                    Text("正在處理 \(r.task.id)「\(r.task.title)」(\(app.agents.first { $0.id == r.task.agentId }?.name ?? r.task.agentId)),開始於 \(r.started.formatted(date: .omitted, time: .shortened))")
                    Spacer()
                    Button("停止") { runner.stop() }
                }
            } else {
                Label("目前沒有在處理的任務", systemImage: "pause.circle").foregroundStyle(.secondary)
            }
            if let e = runner.lastError { Text(e).font(.caption).foregroundStyle(.orange).textSelection(.enabled) }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct RunnerConfigRow: View {
    @ObservedObject var runner: AgentRunner
    let agent: Agent
    @State private var c = RunnerConfig()
    @State private var error: String?
    @State private var loaded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Toggle(isOn: Binding(get: { c.autoRun }, set: { c.autoRun = $0; save() })) {
                    Text(agent.name).font(.callout.weight(.semibold))
                }
                .toggleStyle(.switch)
                Text(agent.role).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            if c.autoRun {
                HStack {
                    Picker("模型", selection: Binding(get: { c.model }, set: { c.model = $0; save() })) {
                        Text("預設").tag(""); Text("Opus").tag("opus"); Text("Sonnet").tag("sonnet"); Text("Haiku").tag("haiku")
                    }.frame(width: 170)
                    Picker("推論強度", selection: Binding(get: { c.effort }, set: { c.effort = $0; save() })) {
                        Text("預設").tag(""); ForEach(["low", "medium", "high", "xhigh", "max"], id: \.self) { Text($0).tag($0) }
                    }.frame(width: 190)
                    Stepper("逾時 \(c.timeoutMinutes) 分", value: Binding(get: { c.timeoutMinutes }, set: { c.timeoutMinutes = $0; save() }), in: 5...480, step: 5)
                }
                HStack {
                    TextField("工作資料夾(絕對路徑;空白 = 不給檔案工具)", text: $c.workdir).textFieldStyle(.roundedBorder).onSubmit(save)
                    Button("選擇…") { pick() }
                    Toggle("允許 Bash", isOn: Binding(get: { c.allowBash }, set: { c.allowBash = $0; save() })).disabled(c.workdir.isEmpty)
                }
                Toggle("在頻道被 @ 時自動回覆", isOn: Binding(get: { c.autoReply }, set: { c.autoReply = $0; save() }))
                    .help("被 @ 時替這個角色排一筆「回覆頻道」任務,由自動執行處理後在頻道回覆")
            }
            if let error { Text(error).font(.caption).foregroundStyle(.orange) }
        }
        .padding(12)
        .background(.quaternary.opacity(0.25), in: RoundedRectangle(cornerRadius: 8))
        .onAppear { if !loaded { c = runner.config(agent.id); loaded = true } }
    }

    private func save() {
        do { try runner.save(agent.id, c); error = nil } catch { self.error = error.localizedDescription }
    }

    private func pick() {
        let p = NSOpenPanel()
        p.canChooseDirectories = true
        p.canChooseFiles = false
        p.allowsMultipleSelection = false
        if p.runModal() == .OK, let u = p.url { c.workdir = u.path; save() }
    }
}
