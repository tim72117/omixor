import SwiftUI

/// ⌘K 指令面板:跳到頁面、服務、伺服器、事件。
struct CommandPalette: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @FocusState private var focused: Bool

    private struct Entry: Identifiable {
        let id: String
        let title: String
        let detail: String
        let icon: String
        let action: () -> Void
    }

    private var entries: [Entry] {
        var out: [Entry] = AppSection.allCases.map { s in
            Entry(id: "s-\(s.rawValue)", title: s.title, detail: "頁面", icon: s.icon) { app.go(s) }
        }
        out += app.services.map { s in
            Entry(id: "svc-\(s.id)", title: s.name, detail: "服務", icon: "shippingbox") { app.go(.infrastructure, focus: s.id) }
        }
        out += app.servers.map { s in
            Entry(id: "srv-\(s.id)", title: s.name, detail: "伺服器 · \(s.region)", icon: "server.rack") { app.go(.infrastructure, focus: s.id) }
        }
        out += app.incidents.map { i in
            Entry(id: "inc-\(i.id)", title: "\(i.id) \(i.title)", detail: "事件", icon: "flame") { app.go(.incidents, focus: i.id) }
        }
        out += app.automations.map { a in
            Entry(id: "ai-\(a.id)", title: a.name, detail: "AI 自動化", icon: a.icon) { app.go(.automation, focus: a.id) }
        }
        let q = query.lowercased().trimmingCharacters(in: .whitespaces)
        return q.isEmpty ? out : out.filter { $0.title.lowercased().contains(q) || $0.detail.lowercased().contains(q) }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("搜尋頁面、服務、伺服器、事件…", text: $query)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($focused)
                    .onSubmit { entries.first.map(run) }
            }
            .padding(14)
            Divider()
            List(entries.prefix(40)) { e in
                Button { run(e) } label: {
                    HStack {
                        Image(systemName: e.icon).frame(width: 20).foregroundStyle(.secondary)
                        Text(e.title)
                        Spacer()
                        Text(e.detail).font(.caption).foregroundStyle(.secondary)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
            .listStyle(.plain)
        }
        .frame(width: 560, height: 420)
        .onAppear { focused = true }
        .onExitCommand { dismiss() }
    }

    private func run(_ e: Entry) {
        e.action()
        dismiss()
    }
}
