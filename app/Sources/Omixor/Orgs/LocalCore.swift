import Foundation
import Security
import SwiftUI

// 本機守護服務(omixor-local,Go 版核心,見 server/local.go):本機模式的 Issue、角色與任務、組織鏈、訊息頻道、文件櫃
// 一律存在它那裡,App 把它當成一台在 127.0.0.1 的 omixor server,和遠端模式走同一套用戶端(IssueRemote 與各 Store 的伺服器路徑、
// MCP 核心工具轉送)。連哪台、用哪把 token 只在 IssueServerConfig.current 決定(遠端 → 設定裡的伺服器;本機 → 這個守護服務)。
//
//   執行檔:omixor.app/Contents/MacOS/omixor-local(scripts/build-app.sh 編進去);開發時可用 OMIXOR_LOCAL_BIN 指定
//   資料夾:<App 資料夾>/core(store.json、附件、app-import.json)
//   位址:127.0.0.1 的隨機埠(守護服務自己挑,印在標準輸出的 OMIXOR_LOCAL_READY 那一行),不會撞到 8080、3765、3767
//   token:每次啟動用亂數產生一把 omx_…,只放在記憶體、用環境變數傳給守護服務(不進命令列、不寫檔);守護服務會作廢上一把
//   使用者:本機使用者的 email 是 App 的使用者 id(app.me,例如 tim),頻道與組織鏈的節點才會和原本的本機資料一樣是 user:tim
//   結束:App 結束時關掉它;App 當掉時守護服務讀到標準輸入關閉也會自己結束
//
// 守護服務起不來時不退回 Swift 的本機實作:畫面停在 LocalCoreGate(說明原因、可以重試),可用後自動進入主畫面。
// 守護服務中途意外結束(被砍、當掉):AppState.localCore 切回「無法使用」,畫面回到 LocalCoreGate 顯示原因,
// 並自動重啟(次數與間隔見 AppState.localCoreRestartDelays;穩定跑一陣子後才重新計算次數);重啟成功回主畫面、重讀資料。
// 第一次可用時,把 App 原本存在組織資料夾的 JSON(issues.json、agents.json、cabinet*.json、org-chain.json、channels.json)
// 匯入守護服務(/local/import;每一類只匯入一次,紀錄在守護服務的 app-import.json)。原本的 JSON 不刪,留著當備份。

enum LocalCoreState: Equatable {
    case idle
    case starting
    case ready
    case failed(String)

    var error: String? { if case .failed(let m) = self { return m }; return nil }
}

/// 守護服務的程序與連線資訊(執行緒安全;IssueServerConfig.current 會在任何執行緒讀)
final class LocalCore: @unchecked Sendable {
    static let shared = LocalCore()

    private let lock = NSLock()
    private var _config: IssueServerConfig?
    private var _state: LocalCoreState = .idle
    private var process: Process?
    private var stdin: Pipe?
    private var _onUnexpectedExit: ((String) -> Void)?

    /// 可用中的守護服務意外結束時呼叫(在背景執行緒;參數是原因)。stop() 停掉的不算
    var onUnexpectedExit: ((String) -> Void)? {
        get { lock.lock(); defer { lock.unlock() }; return _onUnexpectedExit }
        set { lock.lock(); _onUnexpectedExit = newValue; lock.unlock() }
    }

    /// 目前程序的 pid(沒在跑是 nil;測試用來模擬意外結束)
    var pid: Int32? {
        lock.lock(); defer { lock.unlock() }
        guard let p = process, p.isRunning else { return nil }
        return p.processIdentifier
    }

    /// 可用時的連線(網址與這次的 token);還沒好是 nil
    var config: IssueServerConfig? { lock.lock(); defer { lock.unlock() }; return _config }
    var state: LocalCoreState { lock.lock(); defer { lock.unlock() }; return _state }

    /// 還沒好時給各 Store 的連線:每個請求直接回這個錯誤(不會退回本機 JSON)
    var unavailableConfig: IssueServerConfig {
        let msg: String
        switch state {
        case .failed(let m): msg = "本機服務沒有啟動:\(m)"
        default: msg = "本機服務啟動中,請稍候"
        }
        return IssueServerConfig(url: URL(string: "http://127.0.0.1")!, token: nil, unavailable: msg)
    }

    static var dataDir: URL { OrgFiles.root.appendingPathComponent("core", isDirectory: true) }

    /// 找執行檔:OMIXOR_LOCAL_BIN、App 執行檔旁邊(omixor.app/Contents/MacOS)、Resources
    static func binary() -> URL? {
        let fm = FileManager.default
        var cands: [URL] = []
        if let p = ProcessInfo.processInfo.environment["OMIXOR_LOCAL_BIN"], !p.isEmpty { cands.append(URL(fileURLWithPath: p)) }
        if let u = Bundle.main.url(forAuxiliaryExecutable: "omixor-local") { cands.append(u) }
        if let exe = Bundle.main.executableURL { cands.append(exe.deletingLastPathComponent().appendingPathComponent("omixor-local")) }
        if let r = Bundle.main.resourceURL { cands.append(r.appendingPathComponent("omixor-local")) }
        return cands.first { fm.isExecutableFile(atPath: $0.path) }
    }

    /// 亂數 token(omx_ + 32 bytes base64url)
    static func newToken() -> String {
        var b = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, b.count, &b)
        let s = Data(b).base64EncodedString().replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        return "omx_" + s
    }

    private func set(_ s: LocalCoreState, _ c: IssueServerConfig? = nil) {
        lock.lock(); _state = s; _config = c; lock.unlock()
    }

    /// 啟動守護服務並等它可用(會擋住呼叫的執行緒,不要在主執行緒呼叫)。已經在跑就直接回傳
    func start(binary: URL? = LocalCore.binary(), dataDir: URL = LocalCore.dataDir, user: String, name: String = "",
               timeout: TimeInterval = 20) throws -> IssueServerConfig {
        lock.lock()
        if let c = _config, process?.isRunning == true { lock.unlock(); return c }
        _state = .starting; _config = nil
        lock.unlock()
        do {
            let c = try launch(binary: binary, dataDir: dataDir, user: user, name: name, timeout: timeout)
            set(.ready, c)
            return c
        } catch {
            set(.failed(error.localizedDescription))
            throw error
        }
    }

    private func launch(binary: URL?, dataDir: URL, user: String, name: String, timeout: TimeInterval) throws -> IssueServerConfig {
        guard let bin = binary else {
            throw IssueRemoteError(message: "找不到 omixor-local(本機守護服務)。請用 app/scripts/build-app.sh 打包,或設 OMIXOR_LOCAL_BIN 指向編好的執行檔")
        }
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        let token = Self.newToken()
        let p = Process()
        p.executableURL = bin
        p.arguments = []
        // 環境變數只給需要的(token 不放命令列)
        var env: [String: String] = [
            "OMIXOR_DATA_DIR": dataDir.path,
            "OMIXOR_LOCAL_TOKEN": token,
            "OMIXOR_LOCAL_USER": user,
            "OMIXOR_LOCAL_NAME": name,
            "OMIXOR_EXIT_ON_STDIN_EOF": "1",
        ]
        for k in ["HOME", "TMPDIR", "PATH"] { if let v = ProcessInfo.processInfo.environment[k] { env[k] = v } }
        p.environment = env
        let input = Pipe(), output = Pipe(), errors = Pipe()
        p.standardInput = input
        p.standardOutput = output
        p.standardError = errors

        final class Box: @unchecked Sendable {
            let lock = NSLock()
            var out = "", err = "", url: URL?
            let ready = DispatchSemaphore(value: 0)
        }
        let box = Box()
        output.fileHandleForReading.readabilityHandler = { h in
            let d = h.availableData
            guard !d.isEmpty else { h.readabilityHandler = nil; return }
            box.lock.lock()
            box.out += String(decoding: d, as: UTF8.self)
            var found = false
            if box.url == nil, let line = box.out.split(separator: "\n").first(where: { $0.hasPrefix("OMIXOR_LOCAL_READY ") }) {
                box.url = URL(string: String(line.dropFirst("OMIXOR_LOCAL_READY ".count)).trimmingCharacters(in: .whitespaces))
                found = true
            }
            box.lock.unlock()
            if found { box.ready.signal() }
        }
        errors.fileHandleForReading.readabilityHandler = { h in
            let d = h.availableData
            guard !d.isEmpty else { h.readabilityHandler = nil; return }
            let s = String(decoding: d, as: UTF8.self)
            NSLog("omixor-local:%@", s.trimmingCharacters(in: .whitespacesAndNewlines))
            box.lock.lock()
            box.err = String((box.err + s).suffix(2000))
            box.lock.unlock()
        }
        p.terminationHandler = { [weak self] proc in
            box.ready.signal()
            guard let self else { return }
            self.lock.lock()
            let mine = self.process === proc
            var died: String?
            if mine {
                self.process = nil
                self.stdin = nil
                if self._state == .ready || self._state == .starting {
                    let how = proc.terminationReason == .uncaughtSignal ? "訊號 \(proc.terminationStatus)" : "代碼 \(proc.terminationStatus)"
                    let msg = "本機服務意外結束(\(how))"
                    // 啟動中結束由 start() 丟錯誤;可用中結束才通知 App(回到等待畫面、自動重啟)
                    if self._state == .ready { died = msg }
                    self._config = nil
                    self._state = .failed(msg)
                }
            }
            let notify = died != nil ? self._onUnexpectedExit : nil
            self.lock.unlock()
            if let died, let notify { notify(died) }
        }
        lock.lock(); process = p; stdin = input; lock.unlock()
        try p.run()
        if box.ready.wait(timeout: .now() + timeout) == .timedOut {
            p.terminate()
            throw IssueRemoteError(message: "本機服務 \(Int(timeout)) 秒內沒有就緒")
        }
        box.lock.lock()
        let url = box.url, tail = box.err
        box.lock.unlock()
        guard let url, p.isRunning else {
            let last = tail.split(separator: "\n").last.map(String.init) ?? ""
            throw IssueRemoteError(message: last.isEmpty ? "本機服務啟動失敗" : "本機服務啟動失敗:\(last)")
        }
        guard url.host == "127.0.0.1" || url.host == "localhost" || url.host == "::1" || url.host == "[::1]" else {
            p.terminate()
            throw IssueRemoteError(message: "本機服務的位址不是 127.0.0.1:\(url.absoluteString)")
        }
        return IssueServerConfig(url: url, token: token, localCore: true)
    }

    /// 關閉標準輸入後最多等多久讓守護服務自己結束(秒;匯入進行中它最多等 30 秒才停),超過才 terminate
    static var stopGrace: TimeInterval = 31

    /// 停掉守護服務(App 結束時):先關標準輸入讓它自己收尾(一般 3 秒內結束;有匯入在進行時最多等 30 秒),
    /// 時間到還在跑才 terminate,不提前強制結束
    func stop() {
        lock.lock()
        let p = process, inp = stdin
        process = nil; stdin = nil; _config = nil; _state = .idle
        lock.unlock()
        try? inp?.fileHandleForWriting.close()
        guard let p, p.isRunning else { return }
        let deadline = Date().addingTimeInterval(Self.stopGrace)
        while p.isRunning, Date() < deadline { usleep(20_000) }
        if p.isRunning {
            p.terminate()
            let hard = Date().addingTimeInterval(3)
            while p.isRunning, Date() < hard { usleep(20_000) }
        }
    }
}

// MARK: - 第一次匯入 App 原本的本機資料

enum LocalCoreImport {
    /// 組織資料夾裡要匯入的檔案 → 匯入的類別(見 server/local.go 的 AppImport)
    static func bundle(orgDir d: URL) -> [String: Any] {
        func raw(_ name: String) -> Any? {
            guard let data = try? Data(contentsOf: d.appendingPathComponent(name)) else { return nil }
            return try? JSONSerialization.jsonObject(with: data)
        }
        var out: [String: Any] = [:]
        if let v = raw("issues.json") as? [Any] { out["issues"] = v }
        if let v = raw("agents.json") as? [String: Any] { out["agents"] = v }
        if let v = raw("cabinet.json") as? [Any] { out["docs"] = v }
        // 結構:沒有檔案時 App 原本用研發基地的結構(Cabinet.load),照舊
        if let v = raw("cabinet-layout.json") as? [Any] {
            out["cabinetLayout"] = v
        } else if let data = try? OrgFiles.encoder().encode(CabinetLayout.evoleapStudio), let v = try? JSONSerialization.jsonObject(with: data) {
            out["cabinetLayout"] = v
        }
        if let v = raw("org-chain.json") as? [String: Any] { out["chain"] = v }
        if let v = raw("channels.json") as? [String: Any] { out["channels"] = v }
        return out
    }

    /// 同步組織清單(App 的 orgs.json 為準)並匯入還沒匯入的資料;回傳匯入失敗的說明(空 = 都好)
    static func sync(_ r: IssueRemote, orgs: [Organization]) -> [String] {
        r.timeout = 120   // 第一次匯入可能比較大
        var problems: [String] = []
        let list: [[String: Any]] = orgs.map { ["id": $0.id, "name": $0.name, "symbol": $0.symbol, "color": $0.color, "note": $0.note] }
        do {
            let res = try r.sendJSON("POST", r.config.url.appendingPathComponent("local/orgs"), ["orgs": list]) as? [String: Any] ?? [:]
            for (id, v) in res where (v as? String) != "ok" { problems.append("組織 \(id):\(v)") }
        } catch {
            return ["同步組織清單失敗:\(error.localizedDescription)"]
        }
        // 已經匯入過的類別不再送(檔案可能很大)
        let done = (try? r.sendJSON("GET", r.config.url.appendingPathComponent("local/import"), nil)) as? [String: Any] ?? [:]
        let sectionOf = ["issues": "issues", "agents": "agents", "docs": "cabinet", "cabinetLayout": "cabinet", "chain": "chain", "channels": "channels"]
        for o in orgs {
            let imported = Set((done[o.id] as? [String: Any])?.keys.map { $0 } ?? [])
            let b = bundle(orgDir: OrgFiles.dir(o.id)).filter { !imported.contains(sectionOf[$0.key] ?? $0.key) }
            guard !b.isEmpty else { continue }
            do {
                let res = try r.sendJSON("POST", r.config.url.appendingPathComponent("local/import").appendingPathComponent(o.id), b) as? [String: Any] ?? [:]
                // 每一類的 status:ok、skipped、existing(守護服務已經有這類資料,當作已完成)都算成功,只有 error 是失敗
                for (k, v) in res {
                    if let s = v as? [String: Any], s["status"] as? String == "error" {
                        problems.append("\(o.name) 的 \(k) 匯入失敗:\(s["error"] as? String ?? "")")
                    }
                }
            } catch {
                problems.append("\(o.name) 匯入失敗:\(error.localizedDescription)")
            }
        }
        return problems
    }
}

// MARK: - 刪除組織

extension IssueRemote {
    /// DELETE /local/orgs/{org}:請本機守護服務刪掉這個組織的全部資料與匯入紀錄(要伺服器管理員,本機守護服務的 token 就是)。
    /// 刪預設或最後一個組織伺服器回 400;失敗丟出原因
    func deleteLocalOrg(_ org: String) throws {
        if let m = config.unavailable { throw IssueRemoteError(message: m) }
        var req = URLRequest(url: config.url.appendingPathComponent("local/orgs").appendingPathComponent(org), timeoutInterval: timeout)
        req.httpMethod = "DELETE"
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let t = config.token { req.setValue("Bearer \(t)", forHTTPHeaderField: "Authorization") }
        let (status, data) = try perform(req)
        let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        // 守護服務回 JSON 的 404 = 它沒有這個組織(例如建立時本機服務沒在跑),當作已經刪掉;
        // 沒有 JSON 的 404/405 是舊版守護服務沒有這個路徑
        if status == 404, obj?["error"] != nil { return }
        if let e = obj?["error"] as? String { throw IssueRemoteError(message: e, status: status) }
        guard (200..<300).contains(status) else {
            let msg = status == 404 || status == 405
                ? "本機服務不支援刪除組織(HTTP \(status)),請更新 omixor"
                : "本機服務的回應不對(HTTP \(status))"
            throw IssueRemoteError(message: msg, status: status)
        }
    }
}

// MARK: - AppState:啟動、就緒、重試

extension AppState {
    /// 意外結束後自動重啟的間隔(秒);用完還起不來就停在等待畫面,等人按「重試」
    static var localCoreRestartDelays: [TimeInterval] = [1, 3, 10, 30, 60]
    /// 可用超過這麼久(秒)才掉,重啟次數重新計算
    static var localCoreStableAfter: TimeInterval = 120
    /// 怎麼啟動守護服務(測試換成暫存資料夾與測試編的執行檔);會擋住呼叫的執行緒,只在背景呼叫
    nonisolated(unsafe) static var localCoreLauncher: (_ user: String, _ name: String) throws -> IssueServerConfig = { user, name in
        try LocalCore.shared.start(user: user, name: name)
    }
    /// 測試用:允許在 XCTest 裡啟動守護服務(平常測試由 TestCore 自己啟動)
    nonisolated(unsafe) static var localCoreInTests = false

    /// 本機模式:啟動守護服務(背景),好了就同步組織、匯入舊資料,再讓各功能重讀。遠端模式不做事
    func startLocalCore() {
        guard mode.mode == OmixorMode.local, NSClassFromString("XCTestCase") == nil || Self.localCoreInTests else { return }
        watchLocalCore()
        if LocalCore.shared.config != nil { if localCore != .ready { localCoreBecameReady() }; return }
        guard localCore != .starting else { return }
        localCoreRestartTask?.cancel()
        localCoreRestartTask = nil
        localCore = .starting
        let user = me, name = person(me)?.name ?? ""
        let launch = Self.localCoreLauncher
        Task.detached(priority: .userInitiated) {
            do {
                _ = try launch(user, name)
                await MainActor.run { self.localCoreBecameReady() }
            } catch {
                let msg = error.localizedDescription
                await MainActor.run { self.localCoreStartFailed(msg) }
            }
        }
    }

    /// 監看守護服務:可用中意外結束就回到等待畫面並自動重啟
    private func watchLocalCore() {
        LocalCore.shared.onUnexpectedExit = { [weak self] msg in
            Task { @MainActor in self?.localCoreDied(msg) }
        }
    }

    /// 守護服務意外結束:切回「無法使用」(畫面回到 LocalCoreGate 顯示原因),排定自動重啟
    func localCoreDied(_ msg: String) {
        guard mode.mode == OmixorMode.local else { return }
        NSLog("本機服務:%@", msg)
        // 穩定跑了一陣子才掉:重新計算重啟次數(一直起來又馬上掉的,次數會用完而停下)
        if let at = localCoreReadyAt, Date().timeIntervalSince(at) > Self.localCoreStableAfter { localCoreRestarts = 0 }
        localCoreReadyAt = nil
        localCore = .failed(msg)
        scheduleLocalCoreRestart(msg)
    }

    private func localCoreStartFailed(_ msg: String) {
        localCore = .failed(msg)
        // 自動重啟中又失敗:排下一次(次數有上限);第一次啟動失敗等人按「重試」
        if localCoreRestarts > 0 { scheduleLocalCoreRestart(msg) }
    }

    private func scheduleLocalCoreRestart(_ why: String) {
        localCoreRestartTask?.cancel()
        let delays = Self.localCoreRestartDelays
        guard localCoreRestarts < delays.count else {
            localCoreRestartTask = nil
            localCoreNotice = "已自動重啟 \(localCoreRestarts) 次仍無法使用,請按「重試」。"
            return
        }
        let delay = delays[localCoreRestarts]
        localCoreRestarts += 1
        let n = localCoreRestarts
        localCoreNotice = "\(why)。\(Self.seconds(delay)) 後自動重啟(第 \(n)/\(delays.count) 次)…"
        localCoreRestartTask = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: UInt64(max(0, delay) * 1_000_000_000))
            guard !Task.isCancelled, let self, self.mode.mode == OmixorMode.local, self.localCore != .ready else { return }
            self.localCoreRestartTask = nil
            self.localCoreNotice = "\(why)。正在自動重啟(第 \(n)/\(delays.count) 次)…"
            self.startLocalCore()
        }
    }

    private static func seconds(_ t: TimeInterval) -> String {
        t == t.rounded() ? "\(Int(t)) 秒" : String(format: "%.1f 秒", t)
    }

    private func localCoreBecameReady() {
        ensureOrgs()
        let orgs = self.orgs
        Task.detached(priority: .userInitiated) {
            let problems = LocalCoreImport.sync(IssueRemote(config: IssueServerConfig.current), orgs: orgs)
            await MainActor.run {
                for p in problems { NSLog("本機服務:%@", p) }
                self.localCoreProblems = problems
                self.localCoreReadyAt = .now
                self.localCoreNotice = nil
                self.localCore = .ready
                self.reloadOrgData()
            }
        }
    }

    /// 本機模式的組織清單改了(新增、修改):在背景同步到守護服務並匯入新組織的初始檔案(角色、文件櫃結構)。
    /// 依序執行(後一次等前一次);切換組織、刪除組織前會先等它做完,新組織切過去時資料就在
    func syncLocalCoreOrgs() {
        let cfg = IssueServerConfig.current
        guard mode.mode == OmixorMode.local, cfg.localCore, cfg.unavailable == nil else { return }
        let orgs = self.orgs, prev = localOrgSync
        localOrgSync = Task { @MainActor [weak self] in
            await prev?.value
            let problems: [String]
            do { problems = try await IssueRemote.run(cfg) { LocalCoreImport.sync($0, orgs: orgs) } } catch {
                problems = [error.localizedDescription]
            }
            for p in problems { NSLog("本機服務:%@", p) }
            if !problems.isEmpty { self?.errorNotice = "組織清單沒有完整同步到本機服務:\(problems.joined(separator: ";"))" }
        }
    }

    /// 等待畫面的「重試」(重新計算自動重啟的次數)
    func retryLocalCore() {
        localCoreRestartTask?.cancel()
        localCoreRestartTask = nil
        localCoreRestarts = 0
        localCoreNotice = nil
        localCore = .idle
        startLocalCore()
    }
}

/// 本機模式、守護服務還沒可用時的畫面:說明原因、可以重試;仍可換連接對象或登出(同 NoOrgsView)
struct LocalCoreGate: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "internaldrive").font(.system(size: 44, weight: .semibold)).foregroundStyle(.tint)
            Text("本機服務").font(.title.weight(.bold))
            VStack(spacing: 10) {
                if let e = app.localCore.error {
                    Text("本機模式的資料由背景的 omixor 本機服務(127.0.0.1)提供,它現在沒有在跑,所以還不能進入主畫面。")
                        .font(.callout).multilineTextAlignment(.center).fixedSize(horizontal: false, vertical: true)
                    Text(e).font(.callout).foregroundStyle(.red).multilineTextAlignment(.center).textSelection(.enabled)
                    if let n = app.localCoreNotice {
                        Text(n).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    }
                    Button("重試") { app.retryLocalCore() }.buttonStyle(.borderedProminent)
                } else {
                    ProgressView()
                    Text(app.localCoreNotice ?? "正在啟動本機服務…").font(.callout).foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
            }
            .frame(width: 420)
            if !OmixorMode.servers.isEmpty {
                HStack(spacing: 16) {
                    Button("換連接對象") { app.connectChosen = false }.buttonStyle(.link)
                    Button("登出") { Task { await app.omixorLogout() } }.buttonStyle(.link)
                }
            }
        }
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        // 只在還沒開始時啟動;意外結束後由自動重啟依間隔處理(這裡不要立刻再啟動)
        .task { if app.localCore == .idle { app.startLocalCore() } }
    }
}
