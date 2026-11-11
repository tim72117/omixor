import Foundation

/// 掃描 ~/.claude/projects 下的對話紀錄,依專案資料夾分組。
enum SessionStore {
    static var root: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".claude/projects")
    }

    static func loadProjects(root: URL = SessionStore.root) -> [Project] {
        let fm = FileManager.default
        guard let dirs = try? fm.contentsOfDirectory(at: root, includingPropertiesForKeys: nil) else { return [] }
        var byCwd: [String: [SessionSummary]] = [:]
        for dir in dirs {
            guard let files = try? fm.contentsOfDirectory(
                at: dir, includingPropertiesForKeys: [.contentModificationDateKey]
            ) else { continue }
            for file in files where file.pathExtension == "jsonl" {
                guard let s = summary(of: file) else { continue }
                byCwd[s.cwd, default: []].append(s)
            }
        }
        return byCwd
            .map { Project(path: $0.key, sessions: $0.value.sorted { $0.updatedAt > $1.updatedAt }) }
            .sorted { ($0.sessions.first?.updatedAt ?? .distantPast) > ($1.sessions.first?.updatedAt ?? .distantPast) }
    }

    static func summary(of file: URL) -> SessionSummary? {
        guard let data = try? Data(contentsOf: file, options: .mappedIfSafe) else { return nil }
        let h = TranscriptParser.header(of: data)
        // 沒有使用者訊息的紀錄(例如只有設定變更)不列出
        guard let cwd = h.cwd, let prompt = h.firstPrompt else { return nil }
        let date = (try? file.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
        let title = h.customTitle ?? h.aiTitle ?? String(prompt.prefix(60))
        return SessionSummary(
            id: h.sessionId ?? file.deletingPathExtension().lastPathComponent,
            file: file,
            cwd: cwd,
            title: title,
            updatedAt: date,
            entrypoint: h.entrypoint
        )
    }
}
