import SwiftUI

/// 左側事件列表:進行中在上、已解決在下,可依狀態、SEV、關鍵字篩選
struct IncListPane: View {
    @EnvironmentObject var app: AppState
    @Binding var selectedId: String?
    @State private var scope: Scope = .all
    @State private var sevFilter: Set<Int> = []
    @State private var query = ""

    enum Scope: String, CaseIterable, Identifiable {
        case all, active, resolved
        var id: String { rawValue }
        var label: String {
            switch self {
            case .all: return "全部"
            case .active: return "進行中"
            case .resolved: return "已解決"
            }
        }
    }

    private var filtered: [Incident] {
        app.incidents.filter { inc in
            if scope == .active && !inc.incIsActive { return false }
            if scope == .resolved && inc.incIsActive { return false }
            if !sevFilter.isEmpty && !sevFilter.contains(inc.sev) { return false }
            let q = query.trimmingCharacters(in: .whitespaces)
            if !q.isEmpty {
                let names = inc.services.map { app.service($0)?.name ?? $0 }.joined(separator: " ")
                let hay = "\(inc.id) \(inc.title) \(names)"
                if !hay.localizedCaseInsensitiveContains(q) { return false }
            }
            return true
        }
    }

    private var active: [Incident] {
        filtered.filter(\.incIsActive).sorted { ($0.sev, $0.startedAt) < ($1.sev, $1.startedAt) }
    }

    private var resolved: [Incident] {
        filtered.filter { !$0.incIsActive }.sorted { ($0.resolvedAt ?? $0.startedAt) > ($1.resolvedAt ?? $1.startedAt) }
    }

    var body: some View {
        VStack(spacing: 0) {
            filters.padding(10)
            Divider()
            if filtered.isEmpty {
                EmptyStateView(title: app.incidents.isEmpty ? "沒有事件" : "沒有符合的事件",
                               icon: app.incidents.isEmpty ? "checkmark.shield" : "line.3.horizontal.decrease.circle",
                               message: app.incidents.isEmpty ? "一切正常。" : "試著調整篩選條件。")
                    .frame(maxHeight: .infinity)
            } else {
                List(selection: $selectedId) {
                    if !active.isEmpty {
                        Section {
                            ForEach(active) { IncListRow(incident: $0).tag($0.id) }
                        } header: {
                            sectionHeader("進行中", count: active.count, color: .red)
                        }
                    }
                    if !resolved.isEmpty {
                        Section {
                            ForEach(resolved) { IncListRow(incident: $0).tag($0.id) }
                        } header: {
                            sectionHeader("已解決", count: resolved.count, color: .green)
                        }
                    }
                }
                .listStyle(.inset)
                .scrollContentBackground(.hidden)
            }
        }
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.4))
    }

    private var filters: some View {
        VStack(spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("搜尋事件、服務", text: $query).textFieldStyle(.plain)
                if !query.isEmpty {
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill") }
                        .buttonStyle(.plain).foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
            .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 7))

            HStack(spacing: 6) {
                Picker("狀態", selection: $scope) {
                    ForEach(Scope.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                Menu {
                    ForEach(IncSev.all, id: \.self) { s in
                        Toggle(isOn: Binding(get: { sevFilter.contains(s) },
                                             set: { on in if on { sevFilter.insert(s) } else { sevFilter.remove(s) } })) {
                            Text("\(IncSev.label(s)) — \(IncSev.meaning(s))")
                        }
                    }
                    if !sevFilter.isEmpty {
                        Divider()
                        Button("清除 SEV 篩選") { sevFilter.removeAll() }
                    }
                } label: {
                    Label(sevFilter.isEmpty ? "SEV" : sevFilter.sorted().map { "\($0)" }.joined(separator: ","),
                          systemImage: sevFilter.isEmpty ? "line.3.horizontal.decrease.circle" : "line.3.horizontal.decrease.circle.fill")
                }
                .menuStyle(.borderlessButton)
                .fixedSize()
                .help("依嚴重程度篩選")
            }
        }
    }

    private func sectionHeader(_ title: String, count: Int, color: Color) -> some View {
        HStack(spacing: 6) {
            Circle().fill(color).frame(width: 6, height: 6)
            Text(title)
            Text("\(count)").foregroundStyle(.secondary)
        }
        .font(.caption.weight(.semibold))
    }
}

struct IncListRow: View {
    @EnvironmentObject var app: AppState
    let incident: Incident

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                if incident.incIsActive {
                    StatusDot(color: incident.status.color, pulsing: true)
                } else {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).font(.caption)
                }
                IncSevBadge(sev: incident.sev)
                Text(incident.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                Spacer(minLength: 4)
                TimelineView(.periodic(from: .now, by: 30)) { ctx in
                    Text(IncFmt.short(incident.incDuration(now: ctx.date)))
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(incident.incIsActive ? Color.primary : Color.secondary)
                }
            }
            Text(incident.title)
                .font(.callout.weight(.medium))
                .lineLimit(2)
                .foregroundStyle(incident.incIsActive ? Color.primary : Color.secondary)
            HStack(spacing: 6) {
                Text(incident.status.label)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(incident.status.color)
                if !incident.services.isEmpty {
                    Text("·").foregroundStyle(.tertiary)
                    Text(incident.services.map { app.service($0)?.name ?? $0 }.joined(separator: "、"))
                        .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
                if incident.incIsActive {
                    ActorAvatar(actor: .person(incident.commander), size: 16)
                } else {
                    Text(Fmt.relative(incident.resolvedAt ?? incident.startedAt)).font(.caption2).foregroundStyle(.tertiary)
                }
            }
        }
        .padding(.vertical, 5)
        .contentShape(Rectangle())
    }
}
