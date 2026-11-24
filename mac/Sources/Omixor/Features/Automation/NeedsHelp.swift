import SwiftUI

// 「需要處理」:角色回報「需要協助」(blocked)的任務,看得到問題、能直接回覆(對應 web 的 NeedsHelp.tsx、TaskQuestion.tsx、needsHelpModel.ts)。
// 回覆:記成以你署名的備註、任務回到待領取;角色下次領取時會連同備註一起看到。
// AI 自動化的「需要處理」分頁與側邊欄的「需要協助」頁共用這裡的卡片;哪些任務算、回覆的檢查都在 NeedsHelpModel,不各算各的。

enum NeedsHelpModel {
    /// 回覆字數上限(同核心的 service.MaxTaskReplyRunes)
    static let replyLimit = 4000
    /// 摺起時顯示的開頭:最多幾行、幾字(同 needsHelpModel.ts 的 excerpt 預設)
    static let excerptLines = 2
    static let excerptChars = 160

    /// 所有需要處理的任務,等最久的排最前面(條件同 AgentTask.needsHelp,側邊欄與分頁上的數字也用它)
    static func tasks(_ all: [AgentTask]) -> [AgentTask] {
        all.filter(\.needsHelp).sorted { $0.updatedAt < $1.updatedAt }
    }

    /// 回覆的問題(空字串 = 可以送出)
    static func replyProblem(_ text: String) -> String {
        let n = text.trimmingCharacters(in: .whitespacesAndNewlines).count
        if n == 0 { return "要寫回覆內容" }
        return n > replyLimit ? "回覆太長(\(n) / \(replyLimit) 字)" : ""
    }
}

/// AI 自動化 › 需要處理
struct AutoHelpView: View {
    @EnvironmentObject var app: AppState
    /// 要標示並捲到的任務(從別頁跳來)
    let focusId: String?
    let openTask: (AgentTask) -> Void

    var body: some View {
        let list = NeedsHelpModel.tasks(app.agentTasks)
        if list.isEmpty {
            EmptyStateView(title: "沒有需要處理的任務", icon: "checkmark.circle", message: "角色回報卡住、需要你回覆的任務會出現在這裡。")
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    Card("需要處理", subtitle: "\(list.count) 件:角色卡住了,等你回覆") {
                        NeedsHelpList(tasks: list, focusId: focusId, openTask: openTask)
                    }
                    .padding(20)
                    .frame(maxWidth: 1000, alignment: .leading)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .onAppear { reveal(proxy) }
                .onChange(of: focusId) { reveal(proxy) }
            }
            .task { _ = await app.startAgents().reload() }   // 打開時在背景重讀(不擋主執行緒)
        }
    }

    private func reveal(_ proxy: ScrollViewProxy) {
        guard let id = focusId else { return }
        DispatchQueue.main.async { withAnimation { proxy.scrollTo(id, anchor: .center) } }
    }
}

/// 一組需要處理的任務卡;focusId 的那張標示出來(外層的 ScrollViewReader 用任務 id 捲過去)
struct NeedsHelpList: View {
    @EnvironmentObject var app: AppState
    let tasks: [AgentTask]
    var focusId: String?
    let openTask: (AgentTask) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ForEach(tasks) { t in
                NeedsHelpCard(task: t, agent: app.agents.first { $0.id == t.agentId }, focused: t.id == focusId, openTask: openTask)
                    .id(t.id)
            }
        }
    }
}

private struct NeedsHelpCard: View {
    @EnvironmentObject var app: AppState
    let task: AgentTask
    let agent: Agent?
    let focused: Bool
    let openTask: (AgentTask) -> Void
    @State private var reply = ""
    @State private var error: String?
    @State private var busy = false

    private var store: AgentStore { app.startAgents() }

    var body: some View {
        let problem = NeedsHelpModel.replyProblem(reply)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                if let agent { AgentAvatar(agent: agent, size: 22) } else { Image(systemName: "person.crop.circle").foregroundStyle(.secondary) }
                Button { openTask(task) } label: { Text(task.id).font(.body.monospaced()) + Text(" " + task.title) }
                    .buttonStyle(.link)
                Spacer()
                if let iss = task.issueKey(in: app.issues) {
                    Button(iss) { app.go(.development, focus: iss) }.buttonStyle(.link).font(.caption)
                }
                Text("\(agent?.name ?? task.agentId) · 等了 \(Fmt.relative(task.updatedAt))").font(.caption).foregroundStyle(.secondary)
                    .help("角色回報卡住至今")
            }
            TaskQuestion(task: task, expanded: true)
            TextField("回覆角色(會記成你署名的備註,任務回到待領取,角色下次領取時看得到)", text: $reply, axis: .vertical)
                .textFieldStyle(.roundedBorder)
                .lineLimit(1...6)
            HStack(spacing: 8) {
                Button(busy ? "送出中…" : "回覆並重新執行") { send() }
                    .buttonStyle(.borderedProminent).controlSize(.small)
                    .disabled(busy || !problem.isEmpty)
                    .help(problem.isEmpty ? "送出回覆,任務回到待領取;自動的角色會接著執行,手動的要在派發列表按「開始執行」" : problem)
                Button("取消任務", role: .destructive) { cancel() }.controlSize(.small).disabled(busy)
            }
            if let error { Text(error).font(.caption).foregroundStyle(.red).textSelection(.enabled) }
        }
        .padding(12)
        .background(Color.orange.opacity(focused ? 0.16 : 0.06), in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.orange.opacity(focused ? 0.7 : 0.2)))
    }

    /// 回覆:一次記成本人署名的備註並重新排入(遠端要組織管理員)。請求在背景跑,送出期間按鈕停用
    private func send() {
        guard !busy else { return }
        let text = reply.trimmingCharacters(in: .whitespacesAndNewlines), id = task.id
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do { _ = try await store.replyAsync(id, text: text) } catch {
                self.error = "回覆失敗:\(error.localizedDescription)(遠端要組織管理員)"
                return
            }
            reply = ""
        }
    }

    private func cancel() {
        guard !busy else { return }
        busy = true
        Task {
            defer { busy = false }
            let ok = await store.cancelAsync(task.id) != nil
            error = ok ? nil : "取消失敗:\(store.remoteError ?? "未知錯誤")(遠端要組織管理員)"
        }
    }
}

/// 角色卡住時留下的問題:摺起時只顯示開頭兩行,可展開全文
struct TaskQuestion: View {
    let task: AgentTask
    @State private var expanded: Bool

    init(task: AgentTask, expanded: Bool = false) {
        self.task = task
        _expanded = State(initialValue: expanded)
    }

    var body: some View {
        let q = task.blockQuestion
        if q.isEmpty {
            Text("角色沒有留下說明").font(.caption).foregroundStyle(.secondary)
        } else {
            let short = q.excerpt(lines: NeedsHelpModel.excerptLines, chars: NeedsHelpModel.excerptChars), truncated = short != q
            VStack(alignment: .leading, spacing: 4) {
                MarkdownText(truncated && !expanded ? short + "…" : q)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if truncated { Button(expanded ? "收起" : "展開全部") { expanded.toggle() }.buttonStyle(.link).font(.caption) }
            }
        }
    }
}
