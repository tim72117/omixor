import Foundation

/// 在背景執行外部指令(npm、node、gcloud)。
/// GUI app 拿到的 PATH 很短,所以第一次執行時向登入 shell 要一份完整的 PATH(npm、gcloud 才找得到)。
enum Shell {
    struct Result {
        let code: Int32
        let out: String
        let err: String
        var ok: Bool { code == 0 }
        /// 錯誤訊息優先,沒有就用一般輸出
        var message: String {
            let e = err.trimmingCharacters(in: .whitespacesAndNewlines)
            return e.isEmpty ? out.trimmingCharacters(in: .whitespacesAndNewlines) : e
        }
    }

    private static let pathLock = NSLock()
    nonisolated(unsafe) private static var cachedPath: String?

    static func loginPath() -> String {
        pathLock.lock(); defer { pathLock.unlock() }
        if let cachedPath { return cachedPath }
        let fallback = ["/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin",
                        NSHomeDirectory() + "/.local/bin"].joined(separator: ":")
        let p = Process()
        p.executableURL = URL(fileURLWithPath: ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh")
        p.arguments = ["-lc", "printf %s \"$PATH\""]
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = FileHandle.nullDevice
        var path = fallback
        if (try? p.run()) != nil {
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            let s = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            if p.terminationStatus == 0, !s.isEmpty { path = s + ":" + fallback }
        }
        cachedPath = path
        return path
    }

    /// 執行指令並等它結束。輸出寫到暫存檔,避免輸出很多時管線塞住。
    static func run(_ args: [String], cwd: URL, timeout: TimeInterval = 1800) async -> Result {
        await withCheckedContinuation { cont in
            DispatchQueue.global(qos: .userInitiated).async {
                cont.resume(returning: runSync(args, cwd: cwd, timeout: timeout))
            }
        }
    }

    private static func runSync(_ args: [String], cwd: URL, timeout: TimeInterval) -> Result {
        let tmp = FileManager.default.temporaryDirectory
        let outURL = tmp.appendingPathComponent("ops-\(UUID().uuidString).out")
        let errURL = tmp.appendingPathComponent("ops-\(UUID().uuidString).err")
        FileManager.default.createFile(atPath: outURL.path, contents: nil)
        FileManager.default.createFile(atPath: errURL.path, contents: nil)
        defer {
            try? FileManager.default.removeItem(at: outURL)
            try? FileManager.default.removeItem(at: errURL)
        }
        guard let outH = try? FileHandle(forWritingTo: outURL), let errH = try? FileHandle(forWritingTo: errURL) else {
            return Result(code: -1, out: "", err: "無法建立暫存檔")
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        p.arguments = args
        p.currentDirectoryURL = cwd
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = loginPath()
        env["NO_COLOR"] = "1"
        env["FORCE_COLOR"] = "0"
        p.environment = env
        p.standardOutput = outH
        p.standardError = errH
        p.standardInput = FileHandle.nullDevice
        do { try p.run() } catch {
            return Result(code: -1, out: "", err: "無法執行 \(args.first ?? ""):\(error.localizedDescription)")
        }
        let deadline = Date().addingTimeInterval(timeout)
        while p.isRunning {
            if Date() > deadline { p.terminate(); break }
            Thread.sleep(forTimeInterval: 0.2)
        }
        p.waitUntilExit()
        try? outH.close(); try? errH.close()
        let out = (try? String(contentsOf: outURL, encoding: .utf8)) ?? ""
        let err = (try? String(contentsOf: errURL, encoding: .utf8)) ?? ""
        return Result(code: p.terminationStatus, out: out, err: err)
    }
}
