import SwiftUI
import AppKit

/// AI 自動化 › 虛擬角色:角色設定、例行任務、指派任務、看任務進度
struct AutoAgentsView: View {
    @EnvironmentObject var app: AppState
    @Binding var selection: String?
    @State private var editing: Agent?

    private var store: AgentStore { app.startAgents() }

    var body: some View {
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                List(selection: $selection) {
                    ForEach(app.agents) { a in AgentRow(agent: a).tag(a.id) }
                }
                .listStyle(.inset)
                Divider()
                HStack {
                    Button {
                        editing = Agent(id: "", name: "", symbol: "person.crop.circle", color: "blue")
                    } label: { Label("新增角色", systemImage: "plus") }
                    .buttonStyle(.borderless)
                    Spacer()
                }
                .padding(8)
            }
            .frame(width: 250)
            Divider()
            Group {
                if let id = selection, let a = app.agents.first(where: { $0.id == id }) {
                    AgentDetail(agent: a, edit: { editing = a }).id(a.id)
                } else {
                    EmptyStateView(title: "選一個角色", icon: "person.2",
                                   message: "每個角色有自己的角色設定與例行任務。任務指派後,在 Claude Code 請 AI 以角色身分透過 MCP 領取並回報。")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear { if selection == nil { selection = app.agents.first?.id } }
        .onChange(of: app.currentOrgId) { selection = app.agents.first?.id }
        .sheet(item: $editing) { a in
            AgentEditSheet(original: a) { saved in selection = saved.id }
        }
    }
}

private struct AgentRow: View {
    @EnvironmentObject var app: AppState
    let agent: Agent
    var body: some View {
        let open = app.agentTasks.filter { $0.agentId == agent.id && $0.status.isOpen }
        HStack(spacing: 10) {
            AgentAvatar(agent: agent, size: 28)
            VStack(alignment: .leading, spacing: 1) {
                Text(agent.name).lineLimit(1)
                Text(agent.role).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            if open.contains(where: { $0.status == .blocked }) {
                Image(systemName: "hand.raised.fill").foregroundStyle(.orange)
            }
            if !open.isEmpty { Text("\(open.count)").font(.caption.monospacedDigit()).foregroundStyle(.secondary) }
        }
        .opacity(agent.active ? 1 : 0.5)
        .padding(.vertical, 2)
    }
}

struct AgentAvatar: View {
    let agent: Agent
    var size: CGFloat = 32
    var body: some View {
        Image(systemName: agent.symbol)
            .font(.system(size: size * 0.48, weight: .semibold))
            .foregroundStyle(.white)
            .frame(width: size, height: size)
            .background(AgentPalette.color(agent.color).gradient, in: Circle())
    }
}

// MARK: - 角色內容

private struct AgentDetail: View {
    @EnvironmentObject var app: AppState
    let agent: Agent
    let edit: () -> Void
    @State private var title = ""
    @State private var instructions = ""
    @State private var priority: IssuePriority = .normal
    @State private var selectedTask: String?
    @State private var copied: String?

    private var store: AgentStore { app.startAgents() }
    private var tasks: [AgentTask] {
        store.tasks(for: agent.id).sorted { ($0.status.isOpen ? 0 : 1, $1.updatedAt) < ($1.status.isOpen ? 0 : 1, $0.updatedAt) }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                Card("角色設定", subtitle: "AI 領取任務時會一起拿到這段") {
                    if agent.instructions.isEmpty {
                        Text("還沒寫角色設定").foregroundStyle(.secondary)
                    } else {
                        MarkdownText(agent.instructions).frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                routines
                docScopeCard
                assignCard
                taskList
            }
            .padding(20)
            .frame(maxWidth: 900, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var header: some View {
        HStack(spacing: 12) {
            AgentAvatar(agent: agent, size: 44)
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(agent.name).font(.title2.weight(.semibold))
                    Text(agent.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                    if !agent.active { Pill(text: "停用", color: .gray) }
                }
                Text(agent.role).foregroundStyle(.secondary)
            }
            Spacer()
            Button { copy(store.prompt(agent: agent), "all") } label: {
                Label(copied == "all" ? "已複製" : "複製給 Claude Code", systemImage: "doc.on.clipboard")
            }
            .help("複製一段話,貼到 Claude Code 就會以這個角色領取所有待領取任務")
            Button("編輯…", action: edit)
        }
    }

    private var routines: some View {
        Card("例行任務", subtitle: "到時間 omixor 會替角色排一筆任務;上一筆沒做完不會重複排") {
            if agent.routines.isEmpty {
                Text("沒有例行任務,按「編輯…」新增").foregroundStyle(.secondary)
            }
            ForEach(agent.routines) { r in
                HStack {
                    Image(systemName: "clock.arrow.circlepath").foregroundStyle(r.enabled ? .blue : .secondary)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(r.title)
                        Text(r.scheduleLabel + (r.enabled ? " · 下次 " : " · 已停用")
                             + (r.enabled ? r.nextRun().formatted(.dateTime.month().day().hour().minute()) : ""))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("現在排一筆") { store.runNow(agent: agent.id, routine: r.id) }
                        .buttonStyle(.borderless)
                }
                .padding(.vertical, 2)
            }
        }
    }

    private var docScopeCard: some View {
        Card("文件櫃存取範圍", subtitle: "按「編輯…」調整") {
            let items = agent.docAccess.sorted { $0.key < $1.key }.compactMap { k, v -> (String, DocAccess)? in
                if let s = CabinetLayout.section(k) { return (s.label, v) }
                if let t = CabinetLayout.tab(k) {
                    let sec = CabinetLayout.sections.first { $0.tabs.contains(t) }?.label ?? ""
                    return ("\(sec) › \(t.title)", v)
                }
                return nil
            }
            if items.isEmpty {
                Text("不能存取文件櫃").foregroundStyle(.secondary)
            } else {
                FlowRow(items: items)
            }
        }
    }

    private struct FlowRow: View {
        let items: [(String, DocAccess)]
        var body: some View {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(items, id: \.0) { label, acc in
                    HStack(spacing: 6) {
                        Image(systemName: acc == .write ? "pencil.circle.fill" : acc == .read ? "eye.circle" : "nosign")
                            .foregroundStyle(acc == .write ? .blue : .secondary)
                        Text(label)
                        Text(acc.label).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    private var assignCard: some View {
        Card("指派任務") {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    TextField("要\(agent.name)做什麼?", text: $title).textFieldStyle(.roundedBorder)
                    Picker("優先度", selection: $priority) {
                        ForEach(IssuePriority.allCases) { Text($0.label).tag($0) }
                    }
                    .labelsHidden()
                    .frame(width: 90)
                }
                TextField("補充說明(選填,Markdown)", text: $instructions, axis: .vertical)
                    .lineLimit(2...6).textFieldStyle(.roundedBorder)
                HStack {
                    Spacer()
                    Button("指派") {
                        if let t = store.assign(to: agent.id, title: title.trimmingCharacters(in: .whitespaces),
                                                instructions: instructions, priority: priority, by: app.me) {
                            selectedTask = t.id
                        }
                        title = ""; instructions = ""; priority = .normal
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(title.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }
    }

    private var taskList: some View {
        Card("任務", subtitle: tasks.isEmpty ? nil : "\(tasks.filter { $0.status.isOpen }.count) 件未完成") {
            if tasks.isEmpty { Text("還沒有任務").foregroundStyle(.secondary) }
            ForEach(tasks) { t in
                VStack(alignment: .leading, spacing: 8) {
                    Button {
                        selectedTask = selectedTask == t.id ? nil : t.id
                    } label: {
                        HStack {
                            Image(systemName: t.status.icon).foregroundStyle(t.status.color)
                            Text(t.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                            Text(t.title).foregroundStyle(t.status.isOpen ? .primary : .secondary)
                            if t.routineId != nil { Image(systemName: "clock.arrow.circlepath").font(.caption).foregroundStyle(.secondary) }
                            Spacer()
                            if t.priority <= .high { Pill(text: t.priority.label, color: t.priority.color) }
                            Pill(text: t.status.label, color: t.status.color)
                            Text(t.updatedAt, format: .relative(presentation: .named)).font(.caption).foregroundStyle(.secondary)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    if selectedTask == t.id { taskDetail(t) }
                }
                .padding(.vertical, 4)
                Divider()
            }
        }
    }

    @ViewBuilder private func taskDetail(_ t: AgentTask) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("\(store.authorName(t.createdBy)) 建立 · ") .font(.caption).foregroundStyle(.secondary)
                + Text(t.createdAt, format: .dateTime.month().day().hour().minute()).font(.caption).foregroundStyle(.secondary)
            if !t.instructions.isEmpty { MarkdownText(t.instructions) }
            ForEach(t.notes) { n in
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(store.authorName(n.author)).font(.caption.weight(.semibold))
                    Text(n.at, format: .dateTime.hour().minute()).font(.caption).foregroundStyle(.secondary)
                    Text(n.text).font(.callout).textSelection(.enabled)
                }
            }
            if !t.result.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    Label("結果", systemImage: "checkmark.seal").font(.caption.weight(.semibold)).foregroundStyle(.green)
                    MarkdownText(t.result)
                }
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.green.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
            }
            if !t.links.isEmpty {
                HStack {
                    ForEach(t.links, id: \.self) { l in
                        if app.issues.contains(where: { $0.id == l }) {
                            Button(l) { app.go(.development, focus: l) }.buttonStyle(.link)
                        } else {
                            Text(l).font(.caption).textSelection(.enabled)
                        }
                    }
                }
            }
            HStack {
                if t.status.isOpen {
                    Button { copy(store.prompt(agent: agent, task: t), t.id) } label: {
                        Label(copied == t.id ? "已複製" : "複製給 Claude Code", systemImage: "doc.on.clipboard")
                    }
                    Button("取消任務", role: .destructive) { store.update(t.id) { $0.status = .canceled } }
                    if t.status != .queued { Button("退回待領取") { store.update(t.id) { $0.status = .queued } } }
                } else {
                    Button("重新排入") { store.update(t.id) { $0.status = .queued } }
                }
            }
            .controlSize(.small)
        }
        .padding(.leading, 24)
    }

    private func copy(_ s: String, _ key: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
        copied = key
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { if copied == key { copied = nil } }
    }
}

// MARK: - 編輯角色

private struct AgentEditSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let original: Agent
    let onSave: (Agent) -> Void
    @State private var draft: Agent
    @State private var confirmDelete = false

    init(original: Agent, onSave: @escaping (Agent) -> Void) {
        self.original = original
        self.onSave = onSave
        _draft = State(initialValue: original)
    }

    private var isNew: Bool { original.id.isEmpty }
    private static let symbols = ["person.crop.circle", "chart.line.uptrend.xyaxis", "waveform", "tray.full", "magnifyingglass",
                                  "wrench.and.screwdriver", "shield.lefthalf.filled", "doc.text", "megaphone", "brain.head.profile"]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(isNew ? "新增角色" : "編輯「\(original.name)」").font(.title3.weight(.semibold))
            Form {
                TextField("名稱", text: $draft.name, prompt: Text("例如 訪客分析師"))
                TextField("職責", text: $draft.role, prompt: Text("一句話說明"))
                Picker("圖示", selection: $draft.symbol) {
                    ForEach(Self.symbols, id: \.self) { Label($0, systemImage: $0).labelStyle(.iconOnly).tag($0) }
                }
                Picker("顏色", selection: $draft.color) {
                    ForEach(AgentPalette.names, id: \.self) { n in
                        Text(n).foregroundStyle(AgentPalette.color(n)).tag(n)
                    }
                }
                Toggle("啟用(停用時例行任務不會排)", isOn: $draft.active)
                Section("角色設定(給 AI 的說明,Markdown)") {
                    TextEditor(text: $draft.instructions)
                        .font(.system(size: 13, design: .monospaced))
                        .frame(minHeight: 140)
                }
                Section {
                    DocAccessEditor(access: $draft.docAccess)
                } header: { Text("文件櫃存取範圍") } footer: {
                    Text("對應研發基地的區塊與頁籤。頁籤沒設定時跟著區塊。角色用 MCP 讀寫文件時只能碰這些範圍。").font(.caption)
                }
                Section {
                    ForEach($draft.routines) { $r in RoutineEditor(routine: $r) {
                        draft.routines.removeAll { $0.id == r.id }
                    } }
                    Button { draft.routines.append(AgentRoutine(title: "")) } label: { Label("新增例行任務", systemImage: "plus") }
                } header: { Text("例行任務") }
            }
            .formStyle(.grouped)
            HStack {
                if !isNew { Button("刪除角色", role: .destructive) { confirmDelete = true } }
                Spacer()
                Button("取消") { dismiss() }
                Button("儲存") {
                    let store = app.startAgents()
                    var a = draft
                    if isNew { a.id = store.newId(for: a.name); a.createdAt = .now }
                    a.routines = a.routines.filter { !$0.title.trimmingCharacters(in: .whitespaces).isEmpty }
                    store.upsert(a)
                    onSave(a)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(draft.name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 640, height: 680)
        .confirmationDialog("刪除「\(original.name)」?未完成的任務會被取消。", isPresented: $confirmDelete) {
            Button("刪除", role: .destructive) { app.startAgents().delete(original.id); dismiss() }
        }
    }
}

private struct RoutineEditor: View {
    @Binding var routine: AgentRoutine
    let remove: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Toggle("", isOn: $routine.enabled).labelsHidden()
                TextField("例行任務標題", text: $routine.title)
                Button(role: .destructive, action: remove) { Image(systemName: "trash") }.buttonStyle(.borderless)
            }
            HStack {
                Picker("", selection: $routine.kind) { ForEach(RoutineKind.allCases) { Text($0.label).tag($0) } }
                    .labelsHidden().frame(width: 120)
                if routine.kind == .weekly {
                    Picker("", selection: $routine.weekday) {
                        ForEach(1...7, id: \.self) { Text("週" + AgentRoutine.weekdayNames[$0 - 1]).tag($0) }
                    }
                    .labelsHidden().frame(width: 80)
                }
                if routine.kind == .hourly {
                    Stepper("每 \(routine.everyHours) 小時", value: $routine.everyHours, in: 1...48)
                } else {
                    Stepper(String(format: "%02d", routine.hour), value: $routine.hour, in: 0...23).fixedSize()
                    Text(":")
                    Stepper(String(format: "%02d", routine.minute), value: $routine.minute, in: 0...55, step: 5).fixedSize()
                }
            }
            TextField("要做什麼(給 AI 的任務說明)", text: $routine.instructions, axis: .vertical).lineLimit(1...4)
        }
        .padding(.vertical, 4)
    }
}
