import SwiftUI

/// 文件櫃:依組織文件櫃結構(CabinetLayout)的區塊/頁籤放純文字文件;每個位置標出哪些角色可以讀寫
struct CabinetView: View {
    @EnvironmentObject var app: AppState
    /// 選中的位置:區塊 id 或頁籤網址(/ 開頭)
    @State private var node: String = "overview"
    @State private var selectedDoc: String?
    @State private var query = ""
    @State private var creating = false

    @State private var editingLayout = false
    /// 專案:新增或改名的視窗(nil = 沒開;id 空 = 新增)、要刪除的專案、專案動作的錯誤
    @State private var projectSheet: ProjectSheet.Target?
    @State private var deletingProject: Project?
    @State private var projectError: String?

    private var section: CabinetSection? {
        _ = app.cabinetLayoutVersion
        return CabinetLayout.sections.first { $0.id == node } ?? CabinetLayout.sections.first { $0.tabs.contains { $0.path == node } }
    }
    private var tab: CabinetTab? { node.hasPrefix("/") ? CabinetLayout.tab(node) : nil }

    private var docs: [CabinetDoc] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return app.cabinetDocs.filter { d in
            (tab != nil ? d.tab == node : d.section == section?.id)
                && (q.isEmpty || d.title.lowercased().contains(q) || d.body.lowercased().contains(q))
        }
        .sorted { $0.updatedAt > $1.updatedAt }
    }

    var body: some View {
        VStack(spacing: 0) {
            if let e = app.cabinetRemoteError {
                Label("讀不到 omixor server 的文件櫃(畫面是上次讀到的):\(e)", systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.orange).lineLimit(2).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 12).padding(.vertical, 6)
                Divider()
            }
            content
        }
    }

    private var content: some View {
        HStack(spacing: 0) {
            tree.frame(width: 250)
            Divider()
            docList.frame(width: 300)
            Divider()
            Group {
                if let id = selectedDoc, let d = app.cabinetDocs.first(where: { $0.id == id }) {
                    CabinetDocDetail(doc: d) { selectedDoc = nil }.id(d.id)
                } else {
                    EmptyStateView(title: "選一份文件", icon: "doc.text",
                                   message: "文件櫃依區塊與頁籤分類(左下「編輯結構」可調整)。每個角色可以在「AI 自動化 › 虛擬角色 › 編輯」設定能讀寫哪些區塊。")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear {
            app.startAgents(); app.startCabinet()
            if section == nil { node = CabinetLayout.sections.first?.id ?? "" }
        }
        .onChange(of: app.currentOrgId) { node = CabinetLayout.sections.first?.id ?? ""; selectedDoc = nil }
        .onChange(of: app.cabinetProject) { node = CabinetLayout.sections.first?.id ?? ""; selectedDoc = nil; query = "" }
        .onChange(of: app.cabinetLayoutVersion) { if section == nil { node = CabinetLayout.sections.first?.id ?? "" } }
        .sheet(isPresented: $editingLayout) { CabinetLayoutEditor() }
        .sheet(item: $projectSheet) { ProjectSheet(target: $0) }
        .confirmationDialog("刪除專案「\(deletingProject?.name ?? "")」?", isPresented: Binding(get: { deletingProject != nil }, set: { if !$0 { deletingProject = nil } }),
                            titleVisibility: .visible, presenting: deletingProject) { p in
            Button("刪除", role: .destructive) {
                do { try Cabinet.deleteProject(app, p.id) } catch { projectError = error.localizedDescription }
            }
        } message: { _ in
            Text("連同這個專案的文件櫃結構一起刪除。專案裡還有文件時不能刪,要先把文件搬走或刪除。")
        }
        .alert("專案操作失敗", isPresented: Binding(get: { projectError != nil }, set: { if !$0 { projectError = nil } })) {
            Button("好") { projectError = nil }
        } message: { Text(projectError ?? "") }
        .sheet(isPresented: $creating) {
            if let s = section {
                NewDocSheet(section: s, tab: tab) { selectedDoc = $0.id }
            }
        }
    }

    // MARK: 位置樹

    // MARK: 專案切換

    /// 文件櫃上方的專案選單:未分專案 + 各專案;旁邊的選單新增、改名、刪除專案
    private var projectBar: some View {
        HStack(spacing: 6) {
            Picker(selection: Binding(get: { app.cabinetProject }, set: { id in Task { await Cabinet.selectProject(app, id) } })) {
                Text("未分專案").tag("")
                if !app.cabinetProjects.isEmpty { Divider() }
                ForEach(app.cabinetProjects) { p in Text(p.name).tag(p.id) }
            } label: {
                Image(systemName: "square.stack.3d.up").foregroundStyle(.secondary)
            }
            .help("切換專案:每個專案各自一份文件櫃結構與文件")
            Menu {
                Button { projectSheet = .new } label: { Label("新增專案…", systemImage: "plus") }
                if let p = currentProject {
                    Divider()
                    Button { projectSheet = .edit(p) } label: { Label("改名「\(p.name)」…", systemImage: "pencil") }
                    Button(role: .destructive) { deletingProject = p } label: { Label("刪除「\(p.name)」…", systemImage: "trash") }
                }
            } label: { Image(systemName: "ellipsis.circle") }
            .menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize()
            .help("新增、改名、刪除專案")
        }
        .padding(.horizontal, 8).padding(.vertical, 6)
    }

    private var currentProject: Project? { app.cabinetProjects.first { $0.id == app.cabinetProject } }

    private var tree: some View {
        VStack(spacing: 0) {
        projectBar
        Divider()
        List(selection: Binding(get: { node }, set: { if let n = $0 { node = n; selectedDoc = nil } })) {
            ForEach(layout) { s in
                if s.tabs.isEmpty {
                    nodeRow(s.label, icon: "folder", count: app.cabinetDocs.filter { $0.section == s.id }.count,
                            readers: readers(s.id, nil)).tag(s.id)
                } else {
                    DisclosureGroup {
                        ForEach(s.tabs) { t in
                            nodeRow(t.title, icon: "doc.text", count: app.cabinetDocs.filter { $0.tab == t.path }.count,
                                    readers: readers(s.id, t.path)).tag(t.path)
                        }
                    } label: {
                        nodeRow(s.label, icon: "folder", count: app.cabinetDocs.filter { $0.section == s.id }.count,
                                readers: readers(s.id, nil)).tag(s.id)
                    }
                }
            }
        }
        .listStyle(.sidebar)
            Divider()
            HStack {
                Button { editingLayout = true } label: { Label("編輯結構", systemImage: "slider.horizontal.3") }
                    .buttonStyle(.borderless)
                Spacer()
            }
            .padding(8)
        }
    }

    private var layout: [CabinetSection] {
        _ = app.cabinetLayoutVersion
        return CabinetLayout.sections
    }

    private func readers(_ s: String, _ t: String?) -> [(Agent, DocAccess)] {
        app.agents.compactMap { a in a.access(section: s, tab: t, project: app.cabinetProject).map { (a, $0) } }
    }

    private func nodeRow(_ title: String, icon: String, count: Int, readers: [(Agent, DocAccess)]) -> some View {
        HStack(spacing: 6) {
            Image(systemName: icon).foregroundStyle(.secondary)
            Text(title).lineLimit(1)
            Spacer()
            HStack(spacing: -4) {
                ForEach(readers.prefix(4), id: \.0.id) { a, acc in
                    AgentAvatar(agent: a, size: 14)
                        .opacity(acc == .write ? 1 : 0.45)
                        .help("\(a.name):\(acc.label)")
                }
            }
            if count > 0 { Text("\(count)").font(.caption.monospacedDigit()).foregroundStyle(.secondary) }
        }
    }

    // MARK: 文件清單

    private var docList: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Text(tab?.title ?? section?.label ?? "").font(.headline).lineLimit(1)
                    Spacer()
                    Button { creating = true } label: { Image(systemName: "plus") }.help("新增文件")
                }
                Text("位置 " + (tab?.path ?? section?.path ?? "")).font(.caption.monospaced()).foregroundStyle(.secondary)
                let rs = readers(section?.id ?? "", tab?.path)
                if !rs.isEmpty {
                    Text(rs.map { "\($0.0.name)(\($0.1 == .write ? "讀寫" : "讀"))" }.joined(separator: "、"))
                        .font(.caption).foregroundStyle(.secondary).lineLimit(2)
                } else {
                    Text("沒有角色可以存取這裡").font(.caption).foregroundStyle(.secondary)
                }
                TextField("搜尋標題、內文", text: $query).textFieldStyle(.roundedBorder)
            }
            .padding(10)
            Divider()
            if docs.isEmpty {
                EmptyStateView(title: "沒有文件", icon: "doc", message: "按 + 新增,或讓有權限的角色用 MCP 的 doc_save 寫進來。")
                    .frame(maxHeight: .infinity)
            } else {
                List(selection: $selectedDoc) {
                    ForEach(docs) { d in
                        VStack(alignment: .leading, spacing: 3) {
                            Text(d.title).lineLimit(2)
                            HStack(spacing: 6) {
                                Text(d.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                                AuthorIcon(author: d.author, size: 14)
                                if tab == nil, let t = CabinetLayout.tab(d.tab) {
                                    Text(t.title).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                                }
                                Spacer()
                                Text(d.updatedAt, format: .relative(presentation: .named)).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        .padding(.vertical, 2)
                        .tag(d.id)
                    }
                }
                .listStyle(.inset)
            }
        }
        .frame(maxHeight: .infinity, alignment: .top)
    }
}

private struct CabinetDocDetail: View {
    @EnvironmentObject var app: AppState
    let doc: CabinetDoc
    let onDelete: () -> Void
    @State private var editing = false
    @State private var draftTitle = ""
    @State private var draftBody = ""
    @State private var error: String?
    @State private var moving = false

    private var html: String? { editing ? nil : DocTemplates.html(for: doc, in: Cabinet.templateDocs(app)) }

    var body: some View {
        if let html {
            // 有範本:標題列固定,內文用網頁顯示(網頁自己捲動)
            VStack(alignment: .leading, spacing: 0) {
                header.padding(20)
                Divider()
                DocHTMLView(html: html).frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    header
                    Divider()
                    if editing {
                        TextEditor(text: $draftBody)
                            .font(.system(size: 13, design: .monospaced))
                            .scrollContentBackground(.hidden)
                            .frame(minHeight: 360)
                            .inputBox()
                        if let error { Text(error).font(.caption).foregroundStyle(.orange) }
                    } else if doc.body.isEmpty {
                        Text("沒有內文").foregroundStyle(.secondary)
                    } else {
                        let fm = DocTemplates.frontMatter(doc.body)
                        if !fm.meta.isEmpty {
                            Text(fm.meta.sorted { $0.key < $1.key }.map { "\($0.key): \($0.value)" }.joined(separator: " · "))
                                .font(.caption.monospaced()).foregroundStyle(.secondary).textSelection(.enabled)
                        }
                        ReadingMarkdown(fm.rest).padding(.top, 6)
                    }
                }
                .padding(.horizontal, 28).padding(.vertical, 22)
                .frame(maxWidth: ReadingMarkdown.maxWidth + 56, alignment: .leading)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                if editing {
                    TextField("標題", text: $draftTitle).textFieldStyle(.roundedBorder).font(.title3)
                } else {
                    Text(doc.title).font(.title2.weight(.semibold)).textSelection(.enabled)
                }
                Spacer()
                if editing {
                    Button("取消") { editing = false; error = nil }
                    Button("儲存") {
                        if let e = DocTemplates.validate(draftBody, in: Cabinet.templateDocs(app)) { error = e; return }
                        do {
                            try Cabinet.update(app, doc.id) { $0.title = draftTitle; $0.body = draftBody }
                            editing = false; error = nil
                        } catch { self.error = error.localizedDescription }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(draftTitle.trimmingCharacters(in: .whitespaces).isEmpty)
                } else {
                    Button("編輯") { draftTitle = doc.title; draftBody = doc.body; editing = true }
                    Button("搬到…") { moving = true }.help("搬到其他專案或位置")
                    Button("刪除", role: .destructive) {
                        do { try Cabinet.delete(app, doc.id); onDelete() } catch { self.error = error.localizedDescription }
                    }
                }
            }
            HStack(spacing: 8) {
                if !app.cabinetProjects.isEmpty || doc.project != nil {
                    Pill(text: Cabinet.projectName(app, doc.project ?? ""), icon: "square.stack.3d.up")
                }
                Pill(text: doc.location, icon: "folder")
                ForEach(doc.tags, id: \.self) { Pill(text: $0, icon: "tag") }
                AuthorIcon(author: doc.author, size: 16)
                Text(Cabinet.authorName(app, doc.author)).font(.caption).foregroundStyle(.secondary)
                Text(doc.updatedAt, format: .relative(presentation: .named)).font(.caption).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .sheet(isPresented: $moving) { MoveDocSheet(doc: doc, onMoved: onDelete) }
    }
}

/// 新增或改名專案(組織管理員;伺服器檢查名稱必填、組織內不重複)
private struct ProjectSheet: View {
    enum Target: Identifiable {
        case new
        case edit(Project)
        var id: String { if case .edit(let p) = self { return p.id }; return "" }
    }

    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let target: Target
    @State private var name = ""
    @State private var desc = ""
    @State private var error: String?

    private var editing: Project? { if case .edit(let p) = target { return p }; return nil }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(editing.map { "編輯專案 \($0.id)" } ?? "新增專案").font(.title3.weight(.semibold))
            TextField("名稱", text: $name).textFieldStyle(.roundedBorder)
            TextField("說明(選填)", text: $desc).textFieldStyle(.roundedBorder)
            Text("專案只分文件櫃(各自的結構與文件);權限沿用組織成員。").font(.caption).foregroundStyle(.secondary)
            HStack {
                if let error { Text(error).font(.caption).foregroundStyle(.orange).lineLimit(3).textSelection(.enabled) }
                Spacer()
                Button("取消") { dismiss() }
                Button(editing == nil ? "建立" : "儲存") { save() }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
        .onAppear { if let p = editing { name = p.name; desc = p.description } }
    }

    private func save() {
        do {
            if let p = editing {
                try Cabinet.updateProject(app, p.id, name: name, description: desc)
            } else {
                let p = try Cabinet.createProject(app, name: name, description: desc)
                Task { await Cabinet.selectProject(app, p.id) }   // 建好就切過去
            }
            dismiss()
        } catch { self.error = error.localizedDescription }
    }
}

/// 把文件搬到其他專案(或同專案的其他位置):選專案,讀那個專案的結構,再選區塊或頁籤
private struct MoveDocSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let doc: CabinetDoc
    let onMoved: () -> Void
    @State private var project = ""
    @State private var layout: [CabinetSection] = []
    /// 選中的位置:區塊 id 或頁籤網址(/ 開頭)
    @State private var place = ""
    @State private var loading = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("搬移「\(doc.title)」").font(.title3.weight(.semibold))
            Picker("專案", selection: $project) {
                Text("未分專案").tag("")
                ForEach(app.cabinetProjects) { p in Text(p.name).tag(p.id) }
            }
            Picker("位置", selection: $place) {
                ForEach(layout) { s in
                    Text(s.label).tag(s.id)
                    ForEach(s.tabs) { t in Text("\(s.label) › \(t.title)").tag(t.path) }
                }
            }
            .disabled(loading || layout.isEmpty)
            HStack {
                if loading { ProgressView().controlSize(.small) }
                if let error { Text(error).font(.caption).foregroundStyle(.orange).lineLimit(3).textSelection(.enabled) }
                Spacer()
                Button("取消") { dismiss() }
                Button("搬移") { move() }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(loading || place.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 460)
        .onAppear { project = doc.project ?? "" }
        .task(id: project) { await loadLayout() }
    }

    private func loadLayout() async {
        loading = true
        defer { loading = false }
        do {
            layout = try await Cabinet.layout(app, project: project)
            error = nil
            let current = doc.tab ?? doc.section
            let all = layout.flatMap { [$0.id] + $0.tabs.map(\.path) }
            place = all.contains(current) ? current : (layout.first?.id ?? "")
        } catch {
            layout = []
            place = ""
            self.error = error.localizedDescription
        }
    }

    private func move() {
        let section = layout.first { $0.id == place || $0.tabs.contains { $0.path == place } }
        guard let section else { return }
        let tab = place.hasPrefix("/") ? place : nil
        do {
            let saved = try Cabinet.move(app, doc.id, toProject: project, section: section.id, tab: tab)
            if (saved?.project ?? "") != app.cabinetProject { onMoved() }   // 搬到別的專案:這裡看不到了
            dismiss()
        } catch { self.error = error.localizedDescription }
    }
}

private struct NewDocSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let section: CabinetSection
    let tab: CabinetTab?
    let onCreate: (CabinetDoc) -> Void
    @State private var title = ""
    @State private var bodyText = ""
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("新增文件到「\(section.label)\(tab.map { " › \($0.title)" } ?? "")」").font(.title3.weight(.semibold))
            TextField("標題", text: $title).textFieldStyle(.roundedBorder)
            TextEditor(text: $bodyText)
                .font(.system(size: 13, design: .monospaced))
                .scrollContentBackground(.hidden)
                .frame(minHeight: 260)
                .inputBox()
            HStack {
                if let error {
                    Text(error).font(.caption).foregroundStyle(.orange).lineLimit(3).textSelection(.enabled)
                } else {
                    Text("純文字,支援 Markdown").font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button("取消") { dismiss() }
                Button("建立") {
                    do {
                        onCreate(try Cabinet.create(app, title: title.trimmingCharacters(in: .whitespaces), body: bodyText,
                                                    section: section.id, tab: tab?.path, tags: [], by: app.me))
                        dismiss()
                    } catch { self.error = error.localizedDescription }
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(title.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 600)
    }
}

/// 角色的文件櫃存取範圍編輯器(用在角色編輯視窗)
struct DocAccessEditor: View {
    @Binding var access: [String: DocAccess]

    var body: some View {
        ForEach(CabinetLayout.sections) { s in
            if s.tabs.isEmpty {
                picker(s.label, key: s.id, inherited: nil)
            } else {
                DisclosureGroup {
                    ForEach(s.tabs) { t in picker(t.title, key: t.path, inherited: access[s.id]) }
                } label: {
                    picker(s.label + "(整個區塊)", key: s.id, inherited: nil)
                }
            }
        }
    }

    private func picker(_ title: String, key: String, inherited: DocAccess?) -> some View {
        Picker(title, selection: Binding(get: { access[key].map { $0.rawValue } ?? "" },
                                         set: { access[key] = $0.isEmpty ? nil : DocAccess(rawValue: $0) })) {
            Text(inherited.map { "跟區塊(\($0.label))" } ?? (key.hasPrefix("/") ? "跟區塊(不能存取)" : "不能存取")).tag("")
            if key.hasPrefix("/"), inherited != nil, inherited != DocAccess.none { Text("不能存取").tag(DocAccess.none.rawValue) }
            Text("可讀").tag(DocAccess.read.rawValue)
            Text("可讀寫").tag(DocAccess.write.rawValue)
        }
    }
}
