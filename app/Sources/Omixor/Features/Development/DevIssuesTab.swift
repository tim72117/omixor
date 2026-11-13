import SwiftUI

/// 開發 › Issue:存在本機的真實 Issue(人或 AI 透過 MCP 建立)
struct DevIssuesTab: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel
    @State private var filter: IssueFilter = .open
    @State private var query = ""
    @State private var creating = false

    enum IssueFilter: String, CaseIterable, Identifiable {
        case open, closed, all
        var id: String { rawValue }
        var label: String { self == .open ? "未解決" : self == .closed ? "已關閉" : "全部" }
    }

    private var list: [OpsIssue] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return app.issues.filter { i in
            (filter == .all || i.status.rawValue == filter.rawValue)
                && (q.isEmpty || i.title.lowercased().contains(q) || i.id.lowercased().contains(q)
                    || i.labels.contains { $0.lowercased().contains(q) } || i.body.lowercased().contains(q))
        }
        .sorted { ($0.status == .open ? 0 : 1, $0.priority, $1.updatedAt) < ($1.status == .open ? 0 : 1, $1.priority, $0.updatedAt) }
    }

    var body: some View {
        HStack(spacing: 0) {
            VStack(spacing: 0) {
                VStack(spacing: 8) {
                    Picker("狀態", selection: $filter) {
                        ForEach(IssueFilter.allCases) { f in
                            Text(f.label + count(f)).tag(f)
                        }
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    HStack {
                        TextField("搜尋標題、標籤、內文", text: $query).textFieldStyle(.roundedBorder)
                        Button { creating = true } label: { Image(systemName: "plus") }
                            .help("新增 Issue")
                    }
                }
                .padding(10)
                Divider()
                if list.isEmpty {
                    EmptyStateView(title: app.issues.isEmpty ? "還沒有 Issue" : "沒有符合的 Issue", icon: "exclamationmark.bubble",
                                   message: app.issues.isEmpty ? "按 + 新增,或讓 AI 透過 MCP 的 issue_create 存進來。" : nil)
                } else {
                    List(selection: $model.selectedIssue) {
                        ForEach(list) { IssueRow(issue: $0).tag($0.id) }
                    }
                    .listStyle(.inset)
                }
            }
            .frame(width: 320)
            Divider()
            Group {
                if let id = model.selectedIssue, let issue = app.issues.first(where: { $0.id == id }) {
                    IssueDetail(issue: issue).id(issue.id)
                } else {
                    EmptyStateView(title: "選一個 Issue", icon: "exclamationmark.bubble",
                                   message: "Issue 存在這台 Mac 上(issues.json),重開 app 也還在。AI 可以用 MCP 建立、留言與關閉。")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear {
            app.startIssues()
            if model.selectedIssue == nil { model.selectedIssue = list.first?.id }
        }
        .onChange(of: app.currentOrgId) { model.selectedIssue = list.first?.id }
        .sheet(isPresented: $creating) {
            NewIssueSheet { model.selectedIssue = $0.id; filter = .open }
        }
    }

    private func count(_ f: IssueFilter) -> String {
        let n = f == .all ? app.issues.count : app.issues.filter { $0.status.rawValue == f.rawValue }.count
        return n > 0 ? " \(n)" : ""
    }
}

private struct IssueRow: View {
    @EnvironmentObject var app: AppState
    let issue: OpsIssue

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: issue.status == .open ? "circle.dotted" : "checkmark.circle.fill")
                    .foregroundStyle(issue.status == .open ? issue.priority.color : .secondary)
                Text(issue.title).lineLimit(2)
                    .foregroundStyle(issue.status == .open ? .primary : .secondary)
            }
            HStack(spacing: 6) {
                Text(issue.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                if IssueStore.isAI(issue.createdBy) {
                    AuthorIcon(author: issue.createdBy, size: 14).help(app.issueStore?.authorName(issue.createdBy) ?? "")
                }
                if issue.priority <= .high { Pill(text: issue.priority.label, color: issue.priority.color) }
                // 寬度不夠時依序少放標籤,最後指派只剩圖示
                ViewThatFits(in: .horizontal) {
                    tags(labels: 3, assigneeName: true)
                    tags(labels: 1, assigneeName: true)
                    tags(labels: 0, assigneeName: true)
                    tags(labels: 0, assigneeName: false)
                }
                Spacer(minLength: 4)
                if !issue.comments.isEmpty {
                    Label("\(issue.comments.count)", systemImage: "bubble.left").font(.caption).foregroundStyle(.secondary)
                }
                Text(issue.updatedAt, format: .relative(presentation: .named)).font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 3)
    }

    /// 指派與標籤(列表用)
    private func tags(labels n: Int, assigneeName: Bool) -> some View {
        HStack(spacing: 6) {
            if let a = issue.assignedAgent {
                let name = app.agents.first { $0.id == a }?.name ?? a
                Group {
                    if assigneeName {
                        Pill(text: name, color: .purple, icon: "person.crop.circle")
                    } else {
                        Image(systemName: "person.crop.circle").font(.caption).foregroundStyle(.purple)
                    }
                }
                .help("指派給角色:\(name)")
            }
            ForEach(issue.labels.prefix(n), id: \.self) { Pill(text: $0) }
        }
    }
}

// MARK: - 內容

private struct IssueDetail: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel
    let issue: OpsIssue
    @State private var editing = false
    @State private var draftTitle = ""
    @State private var draftBody = ""
    @State private var draftLabels = ""
    @State private var comment = ""
    @State private var confirmDelete = false
    @State private var channelError: String?

    private var store: IssueStore { app.startIssues() }

    /// 綁這個 Issue 的對話頻道:已有就跳過去,沒有就建立(名稱用 Issue 標題,成員含自己與指派的角色)再跳過去
    private func openChannel() {
        do {
            let c = try app.startChannels().openForIssue(issue)
            channelError = nil
            app.go(.channels, focus: c.id)
        } catch { channelError = "開不了對話頻道:\(error.localizedDescription)" }
    }

    /// 指派給角色(遠端模式:伺服器自動替角色排任務;列出啟用中的角色,已指派的停用角色也列出來)
    @ViewBuilder private var assigneeMenu: some View {
        Picker("指派給角色(會自動排任務)", selection: Binding(get: { issue.assignedAgent ?? "" }, set: { v in
            store.update(issue.id, by: app.me) { $0.assignee = v.isEmpty ? nil : v }
        })) {
            Text("未指派").tag("")
            ForEach(app.agents.filter { $0.active || $0.id == issue.assignedAgent }) { a in
                Text(a.active ? a.name : "\(a.name)(停用)").tag(a.id)
            }
        }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                Divider()
                if editing { editor } else { bodyView }
                if !issue.comments.isEmpty || !editing { comments }
            }
            .padding(20)
            .frame(maxWidth: 820, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .confirmationDialog("刪除 \(issue.id)?", isPresented: $confirmDelete) {
            Button("刪除(無法復原)", role: .destructive) {
                store.delete(issue.id)
                model.selectedIssue = nil
            }
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(issue.title).font(.title2.weight(.semibold)).textSelection(.enabled)
                Text(issue.id).font(.title3.monospaced()).foregroundStyle(.secondary)
                Spacer()
                Menu {
                    Picker("優先度", selection: Binding(get: { issue.priority }, set: { p in
                        store.update(issue.id, by: app.me) { $0.priority = p }
                    })) {
                        ForEach(IssuePriority.allCases) { Text($0.label).tag($0) }
                    }
                    assigneeMenu
                    Divider()
                    Button("編輯…") { startEdit() }
                    Button("刪除…", role: .destructive) { confirmDelete = true }
                } label: { Image(systemName: "ellipsis.circle") }
                .menuStyle(.borderlessButton)
                .fixedSize()
                Button { openChannel() } label: { Label("開對話頻道", systemImage: "bubble.left.and.bubble.right") }
                    .help(app.channels.contains { $0.channel.issueId == issue.id } ? "打開綁這個 Issue 的對話頻道" : "建立綁這個 Issue 的對話頻道(成員含你與指派的角色)")
                Button(issue.status == .open ? "關閉" : "重新打開") {
                    store.update(issue.id, by: app.me) { $0.status = issue.status == .open ? .closed : .open }
                }
            }
            HStack(spacing: 6) {
                Pill(text: issue.status.label, color: issue.status == .open ? .green : .secondary,
                     icon: issue.status == .open ? "circle.dotted" : "checkmark.circle")
                Pill(text: "優先度:" + issue.priority.label, color: issue.priority.color)
                if let a = issue.assignedAgent {
                    Pill(text: "指派:" + (app.agents.first { $0.id == a }?.name ?? a), color: .purple, icon: "person.crop.circle")
                }
                ForEach(issue.labels, id: \.self) { Pill(text: $0, icon: "tag") }
                if let e = store.remoteError { Text(e).font(.caption).foregroundStyle(.orange).lineLimit(2) }
                if let e = channelError { Text(e).font(.caption).foregroundStyle(.red).lineLimit(2) }
            }
            if let a = issue.assignedAgent {
                let ts = app.agentTasks.filter { $0.agentId == a && $0.links.contains { $0.caseInsensitiveCompare(issue.id) == .orderedSame } }
                if let t = ts.first(where: { $0.status.isOpen }) ?? ts.first {
                    Button { app.go(.automation, focus: t.id) } label: {
                        Label("\(t.id) \(t.status.label)" + (t.leaseLabel.map { " · \($0)" } ?? ""), systemImage: t.status.icon)
                            .font(.caption).foregroundStyle(t.status.color)
                    }
                    .buttonStyle(.plain)
                    .help("這個 Issue 替角色排的任務")
                }
            }
            HStack(spacing: 12) {
                HStack(spacing: 4) {
                    AuthorIcon(author: issue.createdBy, size: 16)
                    Text("\(store.authorName(issue.createdBy)) 建立於 ") + Text(issue.createdAt, format: .dateTime.month().day().hour().minute())
                }
                if issue.updatedAt.timeIntervalSince(issue.createdAt) > 1 {
                    Text("更新 ") + Text(issue.updatedAt, format: .relative(presentation: .named))
                }
                if !issue.source.isEmpty { Label(issue.source, systemImage: "scope") }
                if let url = URL(string: issue.link), url.scheme?.hasPrefix("http") == true {
                    Link(destination: url) { Label(url.host ?? issue.link, systemImage: "link") }
                }
            }
            .font(.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var bodyView: some View {
        if issue.body.isEmpty {
            Text("沒有內文").foregroundStyle(.secondary)
        } else {
            MarkdownText(issue.body)
        }
    }

    private var editor: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("標題", text: $draftTitle).textFieldStyle(.roundedBorder).font(.title3)
            TextField("標籤(逗號分隔)", text: $draftLabels).textFieldStyle(.roundedBorder)
            MarkdownEditor(text: $draftBody, minHeight: 220)
            HStack {
                Text("內文支援 Markdown").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("取消") { editing = false }
                Button("儲存") {
                    store.update(issue.id, by: app.me) {
                        $0.title = draftTitle.trimmingCharacters(in: .whitespaces)
                        $0.body = draftBody
                        $0.labels = draftLabels.split(separator: ",").map(String.init)
                    }
                    editing = false
                }
                .buttonStyle(.borderedProminent)
                .disabled(draftTitle.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
    }

    private var comments: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("留言").font(.headline)
            ForEach(issue.comments) { c in
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        AuthorIcon(author: c.author, size: 18)
                        Text(store.authorName(c.author)).font(.caption.weight(.semibold))
                        Text(c.at, format: .relative(presentation: .named)).font(.caption).foregroundStyle(.secondary)
                    }
                    MarkdownText(c.text)
                }
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 8))
            }
            HStack(alignment: .bottom) {
                MarkdownEditor(text: $comment, placeholder: "寫留言…", minHeight: 44, monospaced: false)
                Button("送出") {
                    _ = store.comment(issue.id, comment, by: app.me)
                    comment = ""
                }
                .disabled(comment.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }

    private func startEdit() {
        draftTitle = issue.title
        draftBody = issue.body
        draftLabels = issue.labels.joined(separator: ", ")
        editing = true
    }
}

/// 簡單的 Markdown 顯示:標題、清單、程式碼區塊逐行處理,行內語法(粗體、連結、`程式碼`)交給 AttributedString;
/// 段落裡的圖片 ![alt](網址) 拆成圖片區塊(本機附件或 https 圖片)
struct MarkdownText: View {
    let source: String
    init(_ source: String) { self.source = source }

    private enum Block: Hashable {
        case heading(Int, String), bullet(String, String), code(String), text(String), image(String, String)
    }

    /// ![alt](網址):網址不含空白(同 Go 版)
    private static let imageRe = try! NSRegularExpression(pattern: #"!\[([^\]]*)\]\(([^)\s]+)\)"#)

    /// 段落裡的圖片拆成獨立區塊(SwiftUI 的 Text 不能內嵌圖片)
    private static func splitImages(_ s: String) -> [Block] {
        let ns = s as NSString
        var out: [Block] = []
        var last = 0
        for m in imageRe.matches(in: s, range: NSRange(location: 0, length: ns.length)) {
            let before = ns.substring(with: NSRange(location: last, length: m.range.location - last))
            if !before.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                out.append(.text(before.trimmingCharacters(in: .newlines)))
            }
            out.append(.image(ns.substring(with: m.range(at: 1)), ns.substring(with: m.range(at: 2))))
            last = m.range.location + m.range.length
        }
        let rest = ns.substring(from: last)
        if !rest.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { out.append(.text(last == 0 ? rest : rest.trimmingCharacters(in: .newlines))) }
        return out
    }

    /// 標題、清單裡的圖片只顯示替代文字
    private static func imagesAsText(_ s: String) -> String {
        imageRe.stringByReplacingMatches(in: s, range: NSRange(s.startIndex..., in: s), withTemplate: "🖼 $1")
    }

    private var blocks: [Block] {
        var out: [Block] = []
        var code: [String]?
        var para: [String] = []
        func flush() { if !para.isEmpty { out += Self.splitImages(para.joined(separator: "\n")); para = [] } }
        for raw in source.components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                if let c = code { out.append(.code(c.joined(separator: "\n"))); code = nil } else { flush(); code = [] }
                continue
            }
            if code != nil { code?.append(raw); continue }
            if line.isEmpty { flush(); continue }
            let hashes = line.prefix { $0 == "#" }.count
            if (1...4).contains(hashes), line.dropFirst(hashes).first == " " {
                flush(); out.append(.heading(hashes, Self.imagesAsText(line.dropFirst(hashes + 1).trimmingCharacters(in: .whitespaces)))); continue
            }
            if let first = line.first, "-*+".contains(first), line.dropFirst().first == " " {
                flush(); out.append(.bullet("•", Self.imagesAsText(String(line.dropFirst(2))))); continue
            }
            let digits = line.prefix { $0.isNumber }
            if !digits.isEmpty, line.dropFirst(digits.count).hasPrefix(". ") {
                flush(); out.append(.bullet(digits + ".", Self.imagesAsText(String(line.dropFirst(digits.count + 2))))); continue
            }
            para.append(raw)
        }
        if let c = code { out.append(.code(c.joined(separator: "\n"))) }
        flush()
        return out
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, b in
                switch b {
                case .heading(let level, let t):
                    inline(t).font(level <= 1 ? .title3.weight(.semibold) : level == 2 ? .headline : .subheadline.weight(.semibold))
                        .padding(.top, 4)
                case .bullet(let marker, let t):
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(marker).foregroundStyle(.secondary)
                        inline(t)
                    }
                case .code(let t):
                    Text(t).font(.system(size: 12, design: .monospaced))
                        .padding(8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
                case .text(let t):
                    inline(t)
                case .image(let alt, let src):
                    MarkdownImage(alt: alt, src: src)
                }
            }
        }
        .textSelection(.enabled)
    }

    private func inline(_ s: String) -> Text {
        let a = (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(s)
        return Text(a)
    }
}

// MARK: - 新增

private struct NewIssueSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let onCreate: (OpsIssue) -> Void
    @State private var title = ""
    @State private var bodyText = ""
    @State private var labels = ""
    @State private var priority: IssuePriority = .normal

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("新增 Issue").font(.title3.weight(.semibold))
            TextField("標題", text: $title).textFieldStyle(.roundedBorder)
            HStack {
                Picker("優先度", selection: $priority) {
                    ForEach(IssuePriority.allCases) { Text($0.label).tag($0) }
                }
                .frame(width: 180)
                TextField("標籤(逗號分隔)", text: $labels).textFieldStyle(.roundedBorder)
            }
            MarkdownEditor(text: $bodyText, minHeight: 180)
            HStack {
                Text("內文支援 Markdown").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("取消") { dismiss() }
                Button("建立") {
                    let i = app.startIssues().create(title: title.trimmingCharacters(in: .whitespaces), body: bodyText,
                                                     priority: priority, labels: labels.split(separator: ",").map(String.init),
                                                     by: app.me)
                    onCreate(i)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .keyboardShortcut(.defaultAction)
                .disabled(title.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 560)
    }
}

/// 作者圖示:虛擬角色用角色頭像,MCP 用戶端用 sparkles,人用人像
struct AuthorIcon: View {
    @EnvironmentObject var app: AppState
    let author: String
    var size: CGFloat = 18

    var body: some View {
        if let a = app.issueStore?.agent(of: author) {
            AgentAvatar(agent: a, size: size)
        } else if author == IssueStore.mcpAuthor {
            Image(systemName: "sparkles").foregroundStyle(.purple).frame(width: size, height: size)
        } else {
            Image(systemName: "person.crop.circle").foregroundStyle(.secondary).frame(width: size, height: size)
        }
    }
}
