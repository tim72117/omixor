import SwiftUI

/// 任務細節:誰派的、領取與租約、說明、過程、結果(Markdown)、相關連結;組織管理員可以重新排入、取消
struct TaskDetailView: View {
    @ObservedObject var store: DispatchStore
    @EnvironmentObject var directory: Directory
    @EnvironmentObject var session: Session
    let id: String
    @State private var confirmCancel = false

    var body: some View {
        ScrollView {
            if let t = store.task(id) {
                VStack(alignment: .leading, spacing: 14) {
                    Text(t.title).font(.title3.bold())
                    HStack(spacing: 6) {
                        Pill(text: t.status.label, color: t.status.color, icon: t.status == .blocked ? "hand.raised.fill" : nil)
                        Pill(text: directory.name("agent:" + t.agentId), color: .indigo, icon: "sparkles")
                        if t.priority != "normal" { Pill(text: "優先:\(t.priorityLabel)", color: .orange) }
                    }
                    if let why = t.stuckReason() {
                        Label("這筆任務卡住了:\(why)", systemImage: "exclamationmark.triangle.fill")
                            .font(.callout.weight(.semibold)).foregroundStyle(.orange)
                            .padding(10).frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 10))
                    }
                    facts(t)
                    if !t.instructions.isEmpty { section("說明", icon: "text.alignleft") { MarkdownText(t.instructions, font: .callout) } }
                    if !t.notes.isEmpty {
                        section("過程", icon: "list.bullet") {
                            ForEach(t.notes) { n in
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack {
                                        Text(directory.name(n.author)).font(.caption.bold())
                                        if let at = n.at { Text(at.shortLabel).font(.caption2).foregroundStyle(.secondary) }
                                    }
                                    MarkdownText(n.text, font: .callout)
                                }
                            }
                        }
                    }
                    if !t.result.isEmpty {
                        section(t.status == .blocked ? "需要協助" : "結果", icon: t.status == .blocked ? "hand.raised" : "checkmark.seal") {
                            MarkdownText(t.result, font: .callout)
                        }
                        .padding(10)
                        .background((t.status == .blocked ? Color.orange : .green).opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
                    }
                    if !t.links.isEmpty {
                        section("相關", icon: "link") {
                            ForEach(t.links, id: \.self) { l in
                                if let u = SafeLink.url(l) { Link(l, destination: u).font(.callout) } else { Text(l).font(.callout).textSelection(.enabled) }
                            }
                        }
                    }
                    actions(t)
                }
                .padding()
            } else {
                ContentUnavailableView("找不到任務 \(id)", systemImage: "questionmark")
            }
        }
        .safeAreaInset(edge: .top) { if let e = store.error { ErrorBanner(message: e) } }
        .navigationTitle(id)
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await store.refresh() }
        .foregroundPolling(every: 10) { await store.refresh() }
        .confirmationDialog("取消這筆任務?", isPresented: $confirmCancel, titleVisibility: .visible) {
            Button("取消任務", role: .destructive) { Task { await store.cancel(id) } }
        }
    }

    private func facts(_ t: AgentTask) -> some View {
        var parts: [String] = []
        if !t.createdBy.isEmpty { parts.append("\(directory.name(t.createdBy)) 派的") }
        if let c = t.createdAt { parts.append("建立 " + c.shortLabel) }
        if let c = t.claimedAt { parts.append("領取 " + c.shortLabel) }
        if !t.claimedBy.isEmpty { parts.append("領取者 \(t.claimedBy)") }
        if t.status == .inProgress, let l = t.lockedUntil { parts.append((l <= Date() ? "租約已在 " : "鎖定到 ") + l.shortLabel + (l <= Date() ? " 過期" : "")) }
        if let d = t.completedAt { parts.append("結束 " + d.shortLabel) }
        return Text(parts.joined(separator: " · ")).font(.caption).foregroundStyle(.secondary)
    }

    private func section<C: View>(_ title: String, icon: String, @ViewBuilder _ content: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Label(title, systemImage: icon).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private func actions(_ t: AgentTask) -> some View {
        if session.org?.isAdmin == true {
            HStack {
                if t.status.isOpen {
                    if t.status != .queued {
                        Button("退回待領取") { Task { await store.requeue(id) } }.buttonStyle(.bordered)
                    }
                    Button("取消任務", role: .destructive) { confirmCancel = true }.buttonStyle(.bordered)
                } else {
                    Button("重新排入") { Task { await store.requeue(id) } }.buttonStyle(.borderedProminent)
                }
                if store.busy == id { ProgressView() }
            }
            .disabled(store.busy != nil)
        } else {
            Text("重新排入、取消要組織管理員才能做。").font(.caption).foregroundStyle(.secondary)
        }
    }
}
