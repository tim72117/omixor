import SwiftUI

/// 派發:角色任務清單(未完成/全部、依角色篩選),卡住的任務醒目標示
struct DispatchView: View {
    @ObservedObject var store: DispatchStore
    @EnvironmentObject var directory: Directory
    @State private var scope: DispatchStore.Scope = .open
    @State private var agent = ""
    @State private var path: [String] = []

    var body: some View {
        let now = Date()
        let rows = DispatchStore.rows(store.tasks, scope: scope, agent: agent, now: now)
        let stuck = store.stuckCount(now: now)
        NavigationStack(path: $path) {
            List {
                if let e = store.error { ErrorBanner(message: e).listRowInsets(EdgeInsets()) }
                Picker("範圍", selection: $scope) {
                    ForEach(DispatchStore.Scope.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .listRowSeparator(.hidden)
                if stuck > 0 {
                    Label("\(stuck) 筆任務卡住了(需要協助、租約過期或很久沒人領)", systemImage: "exclamationmark.triangle.fill")
                        .font(.footnote.weight(.semibold)).foregroundStyle(.orange)
                }
                if store.loaded && rows.isEmpty {
                    ContentUnavailableView("沒有符合的任務", systemImage: "tray")
                }
                ForEach(rows) { t in
                    NavigationLink(value: t.id) { TaskRow(task: t, now: now) }
                        .listRowBackground(t.stuckReason(now: now) != nil ? Color.orange.opacity(0.12) : nil)
                }
            }
            .listStyle(.plain)
            .navigationTitle("派發")
            .navigationDestination(for: String.self) { id in TaskDetailView(store: store, id: id) }
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Picker("角色", selection: $agent) {
                            Text("全部角色").tag("")
                            ForEach(directory.agents) { a in
                                let open = store.tasks.filter { $0.agentId == a.id && $0.status.isOpen }.count
                                Text(open > 0 ? "\(a.name)(\(open))" : a.name).tag(a.id)
                            }
                        }
                    } label: {
                        Label(agent.isEmpty ? "全部角色" : directory.name("agent:" + agent),
                              systemImage: agent.isEmpty ? "line.3.horizontal.decrease.circle" : "line.3.horizontal.decrease.circle.fill")
                    }
                }
            }
            .refreshable { await store.refresh() }
            .task {
                await store.refresh()
                if path.isEmpty, let id = DebugLaunch.openId, store.task(id) != nil { path = [id] }
            }
            .foregroundPolling(every: 15) { await store.refresh() }
            .overlay { if !store.loaded { ProgressView() } }
        }
    }
}

extension TaskStatus {
    var color: Color {
        switch self {
        case .queued: return .gray
        case .inProgress: return .blue
        case .blocked: return .orange
        case .done: return .green
        case .canceled: return .secondary
        }
    }
}

struct TaskRow: View {
    let task: AgentTask
    var now = Date()
    @EnvironmentObject var directory: Directory

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Text(task.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                Pill(text: task.status.label, color: task.status.color, icon: task.status == .blocked ? "hand.raised.fill" : nil)
                Spacer()
                if let u = task.updatedAt { Text(u.shortLabel).font(.caption2).foregroundStyle(.secondary) }
            }
            Text(task.title).lineLimit(2)
            HStack(spacing: 4) {
                Image(systemName: "sparkles").font(.caption2)
                Text(directory.name("agent:" + task.agentId)).font(.caption)
            }
            .foregroundStyle(.secondary)
            if let why = task.stuckReason(now: now) {
                Label("卡住:\(why)", systemImage: "exclamationmark.triangle.fill")
                    .font(.caption.weight(.semibold)).foregroundStyle(.orange)
            }
        }
        .padding(.vertical, 2)
    }
}
