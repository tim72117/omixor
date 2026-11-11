import Foundation

/// 執行外部指令的小工具。GUI app 啟動時 PATH 很短,所以補上 Homebrew 等常見路徑。
enum Shell {
    static var environment: [String: String] {
        var env = ProcessInfo.processInfo.environment
        let extra = ["/opt/homebrew/bin", "/usr/local/bin", "\(NSHomeDirectory())/.local/bin", "/usr/bin", "/bin"]
        let current = env["PATH"].map { $0.split(separator: ":").map(String.init) } ?? []
        env["PATH"] = (extra + current).reduce(into: [String]()) { if !$0.contains($1) { $0.append($1) } }
            .joined(separator: ":")
        return env
    }

    /// 在 PATH 中找執行檔。
    static func which(_ name: String) -> String? {
        for dir in (environment["PATH"] ?? "").split(separator: ":") {
            let path = "\(dir)/\(name)"
            if FileManager.default.isExecutableFile(atPath: path) { return path }
        }
        return nil
    }

    /// 同步執行並回傳 stdout;失敗回傳 nil。
    static func run(_ exe: String, _ args: [String], cwd: String) -> String? {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = args
        p.currentDirectoryURL = URL(fileURLWithPath: cwd)
        p.environment = environment
        let out = Pipe()
        p.standardOutput = out
        p.standardError = Pipe()
        do { try p.run() } catch { return nil }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        guard p.terminationStatus == 0 else { return nil }
        return String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

/// 右側面板的 git 資訊。
struct GitInfo: Equatable {
    var branch: String?
    var added = 0
    var removed = 0
    var isRepo: Bool { branch != nil }

    static func load(cwd: String) -> GitInfo {
        guard FileManager.default.fileExists(atPath: cwd),
              let git = Shell.which("git"),
              let branch = Shell.run(git, ["rev-parse", "--abbrev-ref", "HEAD"], cwd: cwd)
        else { return GitInfo() }
        var info = GitInfo(branch: branch)
        // 未 commit 的變更(含已 stage),與 Changes 欄位一致
        if let stat = Shell.run(git, ["diff", "HEAD", "--numstat"], cwd: cwd) {
            for line in stat.split(separator: "\n") {
                let cols = line.split(separator: "\t")
                guard cols.count >= 2 else { continue }
                info.added += Int(cols[0]) ?? 0
                info.removed += Int(cols[1]) ?? 0
            }
        }
        return info
    }
}
