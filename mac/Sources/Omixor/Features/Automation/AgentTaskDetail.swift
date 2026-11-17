import SwiftUI

/// 一筆角色任務的派發細節:誰派的、領取與租約、說明、過程記錄、結果、相關連結與動作。
/// 派發頁與角色頁的任務清單都用這個(展開在該列下方)。
struct AgentTaskDetail: View {
    @EnvironmentObject var app: AppState
    let task: AgentTask
    @State private var copied = false

    private var store: AgentStore { app.startAgents() }
    private var agent: Agent? { store.agent(task.agentId) }

    var body: some View {
        let t = task
        VStack(alignment: .leading, spacing: 8) {
            facts
            if let lease = t.leaseLabel {
                Label(lease, systemImage: "lock").font(.caption).foregroundStyle(.secondary)
                    .help("領取租約:鎖定期間不能被其他人領取;逾時沒回報會自動退回待領取")
            }
            if !t.instructions.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Label("說明", systemImage: "text.alignleft").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    MarkdownText(t.instructions).fixedSize(horizontal: false, vertical: true)
                }
            }
            if !t.notes.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Label("過程", systemImage: "list.bullet").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    ForEach(t.notes) { n in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Text(store.authorName(n.author)).font(.caption.weight(.semibold))
                            Text(n.at, format: .dateTime.month().day().hour().minute()).font(.caption).foregroundStyle(.secondary)
                            Text(n.text).font(.callout).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                        }
                    }
                }
            }
            if !t.result.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Label(t.status == .blocked ? "需要協助" : "結果", systemImage: t.status == .blocked ? "hand.raised" : "checkmark.seal")
                        .font(.caption.weight(.semibold)).foregroundStyle(t.status == .blocked ? .orange : .green)
                    MarkdownText(t.result).fixedSize(horizontal: false, vertical: true)
                }
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background((t.status == .blocked ? Color.orange : .green).opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
            }
            if !t.links.isEmpty { links }
            actions
        }
        .padding(.leading, 24)
        .padding(.vertical, 4)
    }

    /// 派發者、建立/領取/完成時間、領取者
    private var facts: some View {
        let t = task
        var parts = ["\(store.authorName(t.createdBy)) 派給 \(agent?.name ?? t.agentId)",
                     "建立 " + t.createdAt.formatted(.dateTime.month().day().hour().minute())]
        if let c = t.claimedAt { parts.append("領取 " + c.formatted(.dateTime.month().day().hour().minute())) }
        if let by = t.claimedBy, !by.isEmpty { parts.append("領取者 \(by)") }
        if let d = t.completedAt { parts.append("結束 " + d.formatted(.dateTime.month().day().hour().minute())) }
        if t.routineId != nil { parts.append("例行任務") }
        return Text(parts.joined(separator: " · ")).font(.caption).foregroundStyle(.secondary)
    }

    /// 相關連結:Issue、頻道可以直接打開,其他(訊息 id、網址)照原樣顯示
    private var links: some View {
        HStack(spacing: 8) {
            Text("相關").font(.caption).foregroundStyle(.secondary)
            ForEach(task.links, id: \.self) { l in
                let u = l.uppercased()
                if u.hasPrefix("ISS-") {
                    Button(l) { app.go(.development, focus: l) }.buttonStyle(.link).font(.caption)
                } else if u.hasPrefix("CH-") {
                    Button("頻道 \(l)") { app.go(.channels, focus: l) }.buttonStyle(.link).font(.caption)
                } else if let url = URL(string: l), url.scheme?.hasPrefix("http") == true {
                    Link(l, destination: url).font(.caption)
                } else {
                    Text(l).font(.caption).textSelection(.enabled)
                }
            }
        }
    }

    private var actions: some View {
        HStack {
            if task.status.isOpen {
                if let a = agent {
                    Button {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(store.prompt(agent: a, task: task), forType: .string)
                        copied = true
                        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { copied = false }
                    } label: {
                        Label(copied ? "已複製" : "複製給 Claude Code", systemImage: "doc.on.clipboard")
                    }
                }
                Button("取消任務", role: .destructive) { store.cancel(task.id) }
                if task.status != .queued { Button("退回待領取") { store.requeue(task.id) } }
            } else {
                Button("重新排入") { store.requeue(task.id) }
            }
        }
        .controlSize(.small)
    }
}
