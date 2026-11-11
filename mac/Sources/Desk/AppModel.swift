import AppKit
import Foundation

/// app 的狀態:專案與對話列表、目前的對話、送出訊息。
@MainActor
final class AppModel: ObservableObject {
    @Published var projects: [Project] = []
    @Published var selection: String?          // 選中的 session id
    @Published var items: [TranscriptItem] = []
    @Published var draft = ""
    @Published var search = ""
    @Published var model: ModelChoice = .opus
    @Published var permission: PermissionMode = .default
    @Published var isRunning = false
    @Published var isLoading = false
    @Published var git = GitInfo()
    @Published var contextTokens: Int?
    @Published var showInspector = true

    /// 新對話時還沒有 session id,只有資料夾
    @Published var newChatFolder: String?

    /// context 上限:超過 200k 的對話一定是用 1M context 的模型
    static func contextWindow(for tokens: Int) -> Int { tokens > 200_000 ? 1_000_000 : 200_000 }

    private let runner = ClaudeRunner()

    var currentSession: SessionSummary? {
        guard let id = selection else { return nil }
        return projects.lazy.flatMap(\.sessions).first { $0.id == id }
    }

    var currentFolder: String? { currentSession?.cwd ?? newChatFolder }

    var title: String {
        if let s = currentSession { return s.title }
        return newChatFolder == nil ? "Desk" : "New session"
    }

    var filteredProjects: [Project] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return projects }
        return projects.compactMap { p in
            let hits = p.sessions.filter { $0.title.lowercased().contains(q) || p.name.lowercased().contains(q) }
            return hits.isEmpty ? nil : Project(path: p.path, sessions: hits)
        }
    }

    // MARK: - 載入

    func reloadProjects() {
        Task.detached(priority: .userInitiated) {
            let projects = SessionStore.loadProjects()
            await MainActor.run { self.projects = projects }
        }
    }

    func open(sessionId: String?) {
        guard !isRunning else { return }
        selection = sessionId
        newChatFolder = nil
        items = []
        contextTokens = nil
        guard let session = currentSession else { return }
        isLoading = true
        let file = session.file
        let cwd = session.cwd
        Task.detached(priority: .userInitiated) {
            let data = (try? Data(contentsOf: file, options: .mappedIfSafe)) ?? Data()
            let loaded = TranscriptParser.items(from: data)
            let notice = TranscriptItem.notice(id: "truncated", text: "對話很長,只顯示最近的部分。", isError: false)
            let items = loaded.truncated ? [notice] + loaded.items : loaded.items
            let tokens = TranscriptParser.contextTokens(from: data)
            let git = GitInfo.load(cwd: cwd)
            await MainActor.run {
                guard self.selection == session.id else { return }
                self.items = items
                self.contextTokens = tokens
                self.git = git
                self.isLoading = false
            }
        }
    }

    /// 開新對話:選資料夾。預設是目前對話的資料夾。
    func newChat() {
        guard !isRunning else { return }
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.prompt = "選擇資料夾"
        panel.message = "新對話要在哪個資料夾工作?"
        if let f = currentFolder { panel.directoryURL = URL(fileURLWithPath: f) }
        guard panel.runModal() == .OK, let url = panel.url else { return }
        selection = nil
        items = []
        contextTokens = nil
        newChatFolder = url.path
        refreshGit()
    }

    func refreshGit() {
        guard let cwd = currentFolder else { git = GitInfo(); return }
        Task.detached {
            let info = GitInfo.load(cwd: cwd)
            await MainActor.run { self.git = info }
        }
    }

    // MARK: - 送出

    func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !isRunning, let cwd = currentFolder else { return }
        draft = ""
        items.append(.user(id: UUID().uuidString, text: text))
        isRunning = true

        let options = ClaudeRunner.Options(cwd: cwd, resume: currentSession?.id, model: model, permission: permission)
        runner.send(text, options: options) { [weak self] event in
            guard let self else { return }
            switch event {
            case .started(let id):
                // 新對話第一次回應時拿到 session id
                if self.selection == nil { self.selection = id }
            case .record(let obj):
                // 自己送出的訊息已經先顯示,串流中的 user 事件只會是工具結果
                if obj["type"] as? String == "assistant" {
                    TranscriptParser.append(record: obj, to: &self.items)
                }
            case .finished(let isError, let message, let usage):
                if isError { self.items.append(.notice(id: UUID().uuidString, text: message ?? "發生錯誤", isError: true)) }
                if let usage { self.contextTokens = TranscriptParser.contextTokens(usage: usage) }
                self.finishRun()
            case .failed(let message):
                self.items.append(.notice(id: UUID().uuidString, text: message, isError: true))
                self.finishRun()
            }
        }
    }

    func stop() { runner.stop() }

    private func finishRun() {
        isRunning = false
        refreshGit()
        // 讓新對話出現在側邊欄;newChatFolder 保留到使用者切換對話,
        // 側邊欄重新載入前 currentFolder 才不會是空的
        reloadProjects()
    }
}
