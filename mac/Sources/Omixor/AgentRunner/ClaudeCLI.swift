import Foundation

// 在這台 Mac 上啟動 Claude Code CLI(`claude -p --output-format stream-json`)做一次推論,逐行解析事件。
// 做法同 core/claude(claude-go):用登入 Claude Code 的訂閱額度,執行時拿掉 ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN;
// 只給自己使用(Anthropic 不允許把訂閱登入提供給第三方產品)。
// 角色自動執行(AgentRunner.swift)用它:工具只開給這次任務的 MCP 端點(omixor 工具),有設工作資料夾時再加檔案工具。

struct ClaudeRunOptions {
    var model = ""                 // opus、sonnet、haiku 或完整 id;空白用 CLI 預設
    var effort = ""                // low、medium、high、xhigh、max
    var appendSystemPrompt = ""
    var builtinTools: [String] = []        // 內建工具(空 = 一個都不給)
    var allowedTools: [String] = []        // 不用詢問就能執行的工具
    var mcpConfigFile: URL?                // --mcp-config 的檔案(內含角色憑證:權限 600,用完刪掉;不放命令列,免得 ps 看得到)
    var workdir: URL?                      // 工作目錄(沒給就用暫存資料夾)
    var timeout: TimeInterval = 30 * 60
}

struct ClaudeRunResult {
    var text: String          // 最後的回覆
    var isError: Bool
    var sessionId: String?
    var turns: Int
    var costUSD: Double?      // 換算成 API 價格的估計值(訂閱不另外收費)
    var stderr: String
}

enum ClaudeCLI {
    /// 找 claude 執行檔:環境變數 OMIXOR_CLAUDE_BIN > 常見安裝位置 > PATH
    static func binary() -> URL? {
        let fm = FileManager.default
        var candidates: [String] = []
        if let b = ProcessInfo.processInfo.environment["OMIXOR_CLAUDE_BIN"], !b.isEmpty { candidates.append(b) }
        let home = fm.homeDirectoryForCurrentUser.path
        candidates += ["/opt/homebrew/bin/claude", "/usr/local/bin/claude", "\(home)/.local/bin/claude", "\(home)/.claude/local/claude"]
        for dir in (ProcessInfo.processInfo.environment["PATH"] ?? "").split(separator: ":") { candidates.append("\(dir)/claude") }
        return candidates.first { fm.isExecutableFile(atPath: $0) }.map { URL(fileURLWithPath: $0) }
    }

    /// 組出 CLI 參數(不含執行檔)
    static func arguments(prompt: String, _ o: ClaudeRunOptions) -> [String] {
        var a = ["-p", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
                 // 不載入使用者的設定、hooks、slash 指令與其他 MCP:只用這裡給的
                 "--setting-sources", "", "--disable-slash-commands", "--strict-mcp-config"]
        func add(_ flag: String, _ v: String) { if !v.isEmpty { a += [flag, v] } }
        add("--model", o.model)
        add("--effort", o.effort)
        add("--append-system-prompt", o.appendSystemPrompt)
        a += ["--tools", o.builtinTools.joined(separator: ",")]
        add("--allowed-tools", o.allowedTools.joined(separator: ","))
        if let m = o.mcpConfigFile { a += ["--mcp-config", m.path] }
        return a + ["--", prompt]
    }

    /// 執行一次推論(在背景執行緒;onLine 收到每一則助理的文字,方便記錄進度)。取消 Task 會結束 CLI。
    static func run(prompt: String, _ o: ClaudeRunOptions, onText: (@Sendable (String) -> Void)? = nil) async throws -> ClaudeRunResult {
        guard let bin = binary() else { throw CabinetError("找不到 claude(Claude Code CLI);請先安裝並用訂閱帳號登入,或設 OMIXOR_CLAUDE_BIN") }
        let p = Process()
        p.executableURL = bin
        p.arguments = arguments(prompt: prompt, o)
        var env = ProcessInfo.processInfo.environment
        env["ANTHROPIC_API_KEY"] = nil
        env["ANTHROPIC_AUTH_TOKEN"] = nil
        if env["PATH"].map({ !$0.contains("/opt/homebrew/bin") }) ?? true { env["PATH"] = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:" + (env["PATH"] ?? "") }
        p.environment = env
        let dir = o.workdir ?? FileManager.default.temporaryDirectory.appendingPathComponent("omixor-agent-\(UUID().uuidString)")
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        p.currentDirectoryURL = dir
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err
        p.standardInput = FileHandle.nullDevice

        final class Box: @unchecked Sendable {
            var result: ClaudeRunResult?
            var turns = 0
            var stderr = Data()
            var buffer = Data()
            let lock = NSLock()
            /// 收尾(同步):把剩下的輸出逐行處理,回傳結果、回合數與 stderr
            func drain(_ rest: Data, _ handle: (Data) -> Void) -> (ClaudeRunResult?, Int, Data) {
                lock.lock()
                buffer.append(rest)
                let tail = buffer
                buffer = Data()
                lock.unlock()
                tail.split(separator: 0x0A).map { Data($0) }.forEach(handle)
                lock.lock()
                defer { lock.unlock() }
                return (result, turns, stderr)
            }
        }
        let box = Box()
        err.fileHandleForReading.readabilityHandler = { h in
            let d = h.availableData
            box.lock.lock(); box.stderr.append(d); box.lock.unlock()
        }
        @Sendable func handle(_ line: Data) {
            guard let obj = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any] else { return }
            switch obj["type"] as? String {
            case "assistant":
                box.lock.lock(); box.turns += 1; box.lock.unlock()
                let content = ((obj["message"] as? [String: Any])?["content"] as? [[String: Any]]) ?? []
                let text = content.compactMap { $0["type"] as? String == "text" ? $0["text"] as? String : nil }.joined()
                if !text.isEmpty { onText?(text) }
            case "result":
                let r = ClaudeRunResult(text: obj["result"] as? String ?? "", isError: obj["is_error"] as? Bool ?? false,
                                        sessionId: obj["session_id"] as? String, turns: obj["num_turns"] as? Int ?? 0,
                                        costUSD: obj["total_cost_usd"] as? Double, stderr: "")
                box.lock.lock(); box.result = r; box.lock.unlock()
            default:
                break
            }
        }
        out.fileHandleForReading.readabilityHandler = { h in
            let d = h.availableData
            box.lock.lock()
            box.buffer.append(d)
            var lines: [Data] = []
            while let i = box.buffer.firstIndex(of: 0x0A) {
                lines.append(box.buffer.subdata(in: box.buffer.startIndex..<i))
                box.buffer.removeSubrange(box.buffer.startIndex...i)
            }
            box.lock.unlock()
            lines.forEach(handle)
        }

        try p.run()
        let deadline = Date().addingTimeInterval(o.timeout)
        do {
            while p.isRunning {
                try Task.checkCancellation()
                if Date() > deadline { p.terminate(); throw CabinetError("Claude 執行超過 \(Int(o.timeout / 60)) 分鐘,已停止") }
                try await Task.sleep(for: .milliseconds(300))
            }
        } catch {
            if p.isRunning { p.terminate() }
            out.fileHandleForReading.readabilityHandler = nil
            err.fileHandleForReading.readabilityHandler = nil
            throw error
        }
        // 讀完剩下的輸出
        out.fileHandleForReading.readabilityHandler = nil
        err.fileHandleForReading.readabilityHandler = nil
        let rest = out.fileHandleForReading.readDataToEndOfFile()
        let errRest = err.fileHandleForReading.readDataToEndOfFile()
        let (result, turns, errData) = box.drain(rest, handle)
        let stderr = String(decoding: errData + errRest, as: UTF8.self)
        guard var r = result else {
            throw CabinetError("Claude 沒有回傳結果(結束碼 \(p.terminationStatus))" + (stderr.isEmpty ? "" : ":\(stderr.prefix(400))"))
        }
        r.stderr = stderr
        if r.turns == 0 { r.turns = turns }
        return r
    }
}
