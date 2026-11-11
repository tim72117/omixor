import Foundation

/// 用 claude CLI 的 print 模式送出一則訊息,逐行讀取 stream-json 事件。
/// 每則訊息啟動一次程序;接續既有對話時帶 --resume。
final class ClaudeRunner {
    struct Options {
        var cwd: String
        var resume: String?
        var model: ModelChoice
        var permission: PermissionMode
    }

    enum Event {
        case record([String: Any])                 // assistant / user 等可顯示的事件
        case started(sessionId: String)
        case finished(isError: Bool, message: String?, usage: [String: Any]?)
        case failed(String)
    }

    private var process: Process?
    var isRunning: Bool { process?.isRunning ?? false }

    func send(_ prompt: String, options: Options, onEvent: @escaping (Event) -> Void) {
        guard let exe = Shell.which("claude") else {
            onEvent(.failed("找不到 claude 指令。請先安裝 Claude Code,並確認在 /opt/homebrew/bin 或 ~/.local/bin。"))
            return
        }
        var args = ["-p", prompt, "--output-format", "stream-json", "--verbose",
                    "--model", options.model.rawValue,
                    "--permission-mode", options.permission.rawValue]
        if let id = options.resume { args += ["--resume", id] }

        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = args
        p.currentDirectoryURL = URL(fileURLWithPath: options.cwd)
        p.environment = Shell.environment
        p.standardInput = FileHandle.nullDevice
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err

        var buffer = Data()
        var sawResult = false
        let deliver: (Event) -> Void = { e in DispatchQueue.main.async { onEvent(e) } }

        out.fileHandleForReading.readabilityHandler = { handle in
            let chunk = handle.availableData
            if chunk.isEmpty { return }
            buffer.append(chunk)
            while let nl = buffer.firstIndex(of: 0x0A) {
                let line = buffer[buffer.startIndex..<nl]
                buffer.removeSubrange(buffer.startIndex...nl)
                guard let obj = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any] else { continue }
                switch obj["type"] as? String {
                case "system":
                    if obj["subtype"] as? String == "init", let id = obj["session_id"] as? String {
                        deliver(.started(sessionId: id))
                    }
                case "result":
                    sawResult = true
                    let isError = obj["is_error"] as? Bool ?? false
                    deliver(.finished(isError: isError,
                                      message: isError ? obj["result"] as? String : nil,
                                      usage: obj["usage"] as? [String: Any]))
                default:
                    deliver(.record(obj))
                }
            }
        }
        p.terminationHandler = { proc in
            out.fileHandleForReading.readabilityHandler = nil
            let errText = String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            if !sawResult {
                let msg = proc.terminationReason == .uncaughtSignal
                    ? "已停止。"
                    : (errText.isEmpty ? "claude 結束,代碼 \(proc.terminationStatus)。" : errText)
                deliver(.failed(msg.trimmingCharacters(in: .whitespacesAndNewlines)))
            }
        }

        do {
            try p.run()
            process = p
        } catch {
            onEvent(.failed("無法啟動 claude:\(error.localizedDescription)"))
        }
    }

    func stop() {
        process?.interrupt()
    }
}
