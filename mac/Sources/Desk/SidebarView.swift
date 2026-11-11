import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var model: AppModel
    @State private var collapsed: Set<String> = []

    /// 只有使用者點選才開啟對話;程式設定 selection(例如新對話拿到 id)不會重新載入內容
    private var selection: Binding<String?> {
        Binding(get: { model.selection }, set: { model.open(sessionId: $0) })
    }

    var body: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 2) {
                SidebarAction(title: "New", systemImage: "plus") { model.newChat() }
                    .disabled(model.isRunning)
                HStack(spacing: 8) {
                    Image(systemName: "magnifyingglass").foregroundStyle(.secondary).frame(width: 18)
                    TextField("Search", text: $model.search).textFieldStyle(.plain)
                }
                .padding(.horizontal, 10).padding(.vertical, 6)
            }
            .padding(.horizontal, 8).padding(.top, 8).padding(.bottom, 4)

            List(selection: selection) {
                ForEach(model.filteredProjects) { project in
                    Section(isExpanded: expanded(project.path)) {
                        ForEach(project.sessions) { session in
                            SessionRow(session: session).tag(session.id as String?)
                        }
                    } header: {
                        Text(project.name).help(project.path)
                    }
                }
            }
            .listStyle(.sidebar)
            .overlay {
                if model.projects.isEmpty {
                    ContentUnavailableView("沒有對話紀錄", systemImage: "bubble.left.and.bubble.right",
                                           description: Text("~/.claude/projects 裡還沒有 Claude Code 對話。"))
                }
            }
        }
    }

    private func expanded(_ path: String) -> Binding<Bool> {
        Binding(get: { !collapsed.contains(path) },
                set: { if $0 { collapsed.remove(path) } else { collapsed.insert(path) } })
    }
}

private struct SidebarAction: View {
    let title: String
    let systemImage: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 8) {
                Image(systemName: systemImage).frame(width: 18)
                Text(title)
                Spacer()
            }
            .contentShape(Rectangle())
            .padding(.horizontal, 10).padding(.vertical, 6)
        }
        .buttonStyle(.plain)
    }
}

private struct SessionRow: View {
    let session: SessionSummary

    var body: some View {
        HStack(spacing: 6) {
            Text(session.title).lineLimit(1)
            Spacer(minLength: 4)
            if let icon {
                Image(systemName: icon).font(.caption).foregroundStyle(.tertiary)
                    .help(session.entrypoint ?? "")
            }
        }
        .help(session.updatedAt.formatted(date: .abbreviated, time: .shortened))
    }

    /// 標出對話是從哪裡開始的
    private var icon: String? {
        switch session.entrypoint {
        case "claude-vscode": return "chevron.left.forwardslash.chevron.right"
        case "cli": return "terminal"
        default: return nil
        }
    }
}
