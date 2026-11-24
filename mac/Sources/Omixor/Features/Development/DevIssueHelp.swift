import SwiftUI

// Issue 上的「需要協助」(對應網頁 IssueHelp.tsx):這張 Issue 的任務被角色回報卡住時,
// 看板卡、列表列、Issue 詳情標題都顯示同一個橘色小標,詳情另外列出卡住的任務與角色的問題。

extension AppState {
    /// 前往處理需要協助的任務:到 AI 自動化,由 AutomationView 依 focus 切到「需要處理」分頁並標示該任務
    func openHelp(_ task: AgentTask? = nil) { go(.automation, focus: task?.id) }
}

/// 橘色「需要協助 N」小標;沒有需要協助的任務就不顯示
struct IssueHelpPill: View {
    @EnvironmentObject var app: AppState
    let issue: Issue

    var body: some View {
        let n = app.helpTasks(for: issue).count
        if n > 0 { Pill(text: "需要協助 \(n)", color: .orange, icon: "hand.raised") }
    }
}

/// Issue 詳情的「需要協助」區塊:列出卡住的任務、角色的問題,按鈕前往回覆
struct IssueHelpSection: View {
    @EnvironmentObject var app: AppState
    let issue: Issue

    var body: some View {
        let tasks = app.helpTasks(for: issue)
        if !tasks.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Text("需要協助").font(.headline)
                ForEach(tasks) { t in
                    VStack(alignment: .leading, spacing: 6) {
                        HStack(spacing: 6) {
                            Text(t.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                            Text(app.agents.first { $0.id == t.agentId }?.name ?? t.agentId).font(.caption.weight(.semibold))
                            Text(t.title).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                            Spacer()
                            Button("前往處理") { app.openHelp(t) }.buttonStyle(.link).font(.caption)
                        }
                        let q = t.blockQuestion
                        Text(q.isEmpty ? "角色沒有留下說明" : q.excerpt(lines: 4, chars: 300))
                            .font(.callout).foregroundStyle(q.isEmpty ? .secondary : .primary)
                            .textSelection(.enabled)
                    }
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
                }
            }
        }
    }
}
