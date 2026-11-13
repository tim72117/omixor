import SwiftUI

/// AI 自動化 › 派發:各角色目前的負載(待領取/進行中/需要協助),以及跨角色的任務總表(對應 web 的 AutoDispatch.tsx)。
/// 點任務在列下方展開派發細節(AgentTaskDetail)。
struct AutoDispatchView: View {
    @EnvironmentObject var app: AppState
    /// 展開細節的任務(其他頁跳過來時也用這個)
    @Binding var selected: String?

    enum Scope: String, CaseIterable, Identifiable {
        case open, all
        var id: String { rawValue }
        var label: String { self == .open ? "只看未完成" : "全部(含完成、取消)" }
    }

    @State private var scope: Scope = .open
    @State private var agentId = ""

    private var store: AgentStore { app.startAgents() }

    /// 符合篩選的任務:未完成的在前,同一組裡最近更新的在前
    static func rows(_ tasks: [AgentTask], scope: Scope, agent: String) -> [AgentTask] {
        tasks.filter { (scope == .all || $0.status.isOpen) && (agent.isEmpty || $0.agentId == agent) }
            .sorted { ($0.status.isOpen ? 0 : 1, $1.updatedAt) < ($1.status.isOpen ? 0 : 1, $0.updatedAt) }
    }

    /// 某角色某狀態的任務數
    static func count(_ tasks: [AgentTask], agent: String, _ status: AgentTaskStatus) -> Int {
        tasks.filter { $0.agentId == agent && $0.status == status }.count
    }

    var body: some View {
        let rows = Self.rows(app.agentTasks, scope: scope, agent: agentId)
        VStack(spacing: 0) {
            if let e = store.remoteError { RemoteErrorBar(message: e) }
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        load
                        Card("派發列表", subtitle: "\(rows.count) 筆") {
                            filters
                            if rows.isEmpty { Text("沒有符合的任務").foregroundStyle(.secondary) }
                            ForEach(rows) { t in
                                VStack(alignment: .leading, spacing: 4) {
                                    row(t)
                                    if selected == t.id { AgentTaskDetail(task: t) }
                                }
                                .id(t.id)
                                Divider()
                            }
                        }
                    }
                    .padding(20)
                    .frame(maxWidth: 1000, alignment: .leading)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .onAppear { reveal(proxy) }
                .onChange(of: selected) { reveal(proxy) }
            }
        }
        .task { await store.reload() }   // 背景重讀(不擋主執行緒)
    }

    /// 從其他頁跳到某筆任務:已完成的要切到「全部」、篩選掉的角色要清掉,再捲過去
    private func reveal(_ proxy: ScrollViewProxy) {
        guard let id = selected, let t = app.agentTasks.first(where: { $0.id == id }) else { return }
        if !t.status.isOpen { scope = .all }
        if !agentId.isEmpty && agentId != t.agentId { agentId = "" }
        DispatchQueue.main.async { withAnimation { proxy.scrollTo(id, anchor: .top) } }
    }

    private var load: some View {
        Card("各角色目前的派發", subtitle: "點角色可只看他被派的任務") {
            if app.agents.isEmpty { Text("還沒有角色").foregroundStyle(.secondary) }
            ForEach(app.agents) { a in
                let open = app.agentTasks.filter { $0.agentId == a.id && $0.status.isOpen }.count
                let blocked = Self.count(app.agentTasks, agent: a.id, .blocked)
                Button {
                    agentId = agentId == a.id ? "" : a.id
                } label: {
                    HStack(spacing: 8) {
                        AgentAvatar(agent: a, size: 24)
                        Text(a.name).lineLimit(1)
                        Text("· \(a.role)").font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        Spacer()
                        if !a.active { Pill(text: "停用", color: .gray) }
                        Pill(text: "待領取 \(Self.count(app.agentTasks, agent: a.id, .queued))", color: .secondary)
                        Pill(text: "進行中 \(Self.count(app.agentTasks, agent: a.id, .inProgress))", color: .blue)
                        if blocked > 0 { Pill(text: "需要協助 \(blocked)", color: .orange, icon: "hand.raised.fill") }
                        Text("\(open) 件未完成").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            .frame(width: 70, alignment: .trailing)
                    }
                    .padding(.vertical, 4).padding(.horizontal, 6)
                    .background(agentId == a.id ? Color.accentColor.opacity(0.12) : .clear, in: RoundedRectangle(cornerRadius: 6))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .opacity(a.active ? 1 : 0.5)
            }
        }
    }

    private var filters: some View {
        HStack {
            Picker("範圍", selection: $scope) { ForEach(Scope.allCases) { Text($0.label).tag($0) } }
                .labelsHidden().fixedSize()
            Picker("角色", selection: $agentId) {
                Text("全部角色").tag("")
                ForEach(app.agents) { Text($0.name).tag($0.id) }
            }
            .labelsHidden().fixedSize()
            Spacer()
        }
    }

    private func row(_ t: AgentTask) -> some View {
        let a = store.agent(t.agentId)
        return HStack(spacing: 8) {
            HStack(spacing: 6) {
                if let a { AgentAvatar(agent: a, size: 20) } else { Image(systemName: "person.crop.circle").foregroundStyle(.secondary) }
                Text(a?.name ?? t.agentId).lineLimit(1)
            }
            .frame(width: 140, alignment: .leading)
            Button { selected = selected == t.id ? nil : t.id } label: {
                HStack(spacing: 6) {
                    Image(systemName: selected == t.id ? "chevron.down" : "chevron.right")
                        .font(.caption2).foregroundStyle(.tertiary).frame(width: 10)
                    Image(systemName: t.status.icon).foregroundStyle(t.status.color)
                    Text(t.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                    Text(t.title).lineLimit(1).foregroundStyle(t.status.isOpen ? .primary : .secondary)
                    if let lease = t.leaseLabel {
                        Text(lease).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                            .help("領取租約:鎖定期間不能被其他人領取")
                    }
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if let iss = t.issueId {
                Button(iss) { app.go(.development, focus: iss) }.buttonStyle(.link).font(.caption)
            }
            if t.priority <= .high { Pill(text: t.priority.label, color: t.priority.color) }
            Pill(text: t.status.label, color: t.status.color, icon: t.status == .blocked ? "hand.raised.fill" : nil)
            Text(store.authorName(t.createdBy)).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                .frame(width: 110, alignment: .leading).help("派發者")
            Text(t.updatedAt, format: .relative(presentation: .named)).font(.caption).foregroundStyle(.secondary)
                .frame(width: 80, alignment: .trailing)
        }
        .padding(.vertical, 3)
    }
}
