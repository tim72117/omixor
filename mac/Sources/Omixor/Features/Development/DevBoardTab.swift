import SwiftUI
import UniformTypeIdentifiers

// 開發 › 看板:依狀態分欄顯示目前組織真的 Issue。分欄、排序、篩選、搜尋的規則只在 core(model/issue_query.go):
// 畫面向 core 要 GET /api/orgs/<組織>/issues/board(IssueRemote.board,在背景執行),只負責顯示。
//   待處理 = open 且沒指派角色;進行中 = open 且指派給角色;已完成 = closed(每欄依優先度 → 最近更新 → 編號)
// 拖曳:欄的 dropStatus 和 Issue 現在的狀態不同才改(拖到「已完成」= 關閉,從「已完成」拖出 = 重新打開);
// 待處理 ↔ 進行中 狀態相同(要改指派,會牽動角色的任務),看板上不做,請到 Issue 分頁改。
// 拖曳游標:能放的欄是「移動」,不能放的欄(原欄、待處理 ↔ 進行中)是「禁止」(IssueBoardResult.dropCheck);
// NSItemProvider 在拖曳途中讀不到內容,所以開始拖時把 id 記在 draggingId。
// app.issues(IssueStore 定時輪詢的全量清單)有變、或篩選條件改了,就重新向 core 查一次(搜尋輸入等 250ms 再查)。

/// 看板各欄的顯示(圖示、顏色、說明);欄的順序、標題與內容照 core 回傳
enum IssueBoardColumn: String, CaseIterable, Identifiable {
    case todo, doing, done
    var id: String { rawValue }
    var icon: String {
        switch self {
        case .todo: return "circle.dotted"
        case .doing: return "circle.lefthalf.filled"
        case .done: return "checkmark.circle.fill"
        }
    }
    var color: Color {
        switch self {
        case .todo: return .secondary
        case .doing: return .blue
        case .done: return .green
        }
    }
    var help: String {
        switch self {
        case .todo: return "未解決、還沒指派給角色的 Issue"
        case .doing: return "未解決、已指派給角色的 Issue"
        case .done: return "已關閉的 Issue;拖到這裡會關閉,拖回去會重新打開"
        }
    }

    /// core 回傳的欄 key 對應的顯示;不認得的 key 用待處理的樣式
    static func look(_ key: String) -> IssueBoardColumn { IssueBoardColumn(rawValue: key) ?? .todo }
}

struct DevBoardTab: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    @State private var query = ""
    @State private var label: String?
    @State private var priority: IssuePriority?
    /// 依所屬倉庫篩選:"" = 全部倉庫;IssueRepoOptions.none = 未標記;其他 = 只列這個倉庫(同 Issue 分頁)
    @State private var repoFilter = ""
    @State private var targeted: String?
    /// 拖曳中的 Issue id(卡片開始拖時記下,放下、看板消失時清掉;取消拖曳時留著,下次拖曳會覆寫)
    @State private var draggingId: String?
    /// 拖曳被拒絕時的提示(例如待處理 ↔ 進行中)
    @State private var notice: String?
    /// core 回傳的看板(第一次讀到前是 nil)
    @State private var result: IssueBoardResult?
    @State private var loadError: String?
    /// 上一次送出查詢時的搜尋字(搜尋字變了才等 250ms)
    @State private var lastQ = ""

    private var store: IssueStore { app.startIssues() }
    private var usedLabels: [String] { result?.labels ?? [] }
    /// Issue 用到的倉庫(去重:不分大小寫),篩選選單用(同 Issue 分頁)
    private var usedRepos: [String] { IssueRepoOptions.used(app.issues) }
    private var hasFilter: Bool { !query.isEmpty || label != nil || priority != nil || !repoFilter.isEmpty }
    private var total: Int { result?.total ?? app.issues.count }

    /// 重新查詢的條件:篩選、組織、全量清單(輪詢讀到新資料或寫入後就會變)
    private struct LoadKey: Equatable {
        var q: IssueQuery
        var org: String
        var issues: [Issue]
    }
    private var issueQuery: IssueQuery { DevBoardTab.query(q: query, priority: priority, label: label, repo: repoFilter) }

    /// 看板送給 core 的條件(status、sort 看板不用;repo 給 IssueRepoOptions.none = 未標記)
    static func query(q: String, priority: IssuePriority?, label: String?, repo: String) -> IssueQuery {
        IssueQuery(q: q, priority: priority?.rawValue ?? "", label: label ?? "", repo: repo)
    }

    var body: some View {
        VStack(spacing: 0) {
            filterBar
                .padding(.horizontal, 20)
                .padding(.vertical, 10)
            Divider()
            if let result, result.total == 0 {
                EmptyStateView(title: "這個組織還沒有 Issue", icon: "rectangle.split.3x1",
                               message: "到「Issue」分頁新增,或讓 AI 透過 MCP 的 issue_create 建立;建立後會依狀態出現在看板上。")
            } else if let result {
                board(result)
            } else if let loadError {
                EmptyStateView(title: "讀不到 Issue", icon: "exclamationmark.triangle", message: loadError)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .onAppear { _ = app.startIssues() }
        .onDisappear { draggingId = nil; targeted = nil }
        .onChange(of: app.currentOrgId) { label = nil; repoFilter = ""; notice = nil; result = nil }
        .task(id: LoadKey(q: issueQuery, org: app.currentOrgId, issues: app.issues)) { await load() }
    }

    /// 向 core 讀看板(背景執行;搜尋字變了先等 250ms,期間再變就取消這次)
    private func load() async {
        let q = issueQuery
        if q.q != lastQ {
            try? await Task.sleep(for: .milliseconds(250))
            if Task.isCancelled { return }
        }
        lastQ = q.q
        do {
            let b = try await store.board(q)
            if Task.isCancelled { return }
            if b != result { result = b }
            loadError = nil
        } catch is CancellationError {
        } catch {
            if !Task.isCancelled { loadError = error.localizedDescription }
        }
    }

    // MARK: 篩選列

    private var filterBar: some View {
        HStack(spacing: 10) {
            TextField("搜尋編號、標題、內文、標籤、留言", text: $query)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 140, maxWidth: 240)

            Picker("標籤", selection: $label) {
                Text("全部標籤").tag(String?.none)
                if !usedLabels.isEmpty { Divider() }
                ForEach(usedLabels, id: \.self) { Text($0).tag(String?.some($0)) }
                // 篩選中的標籤已經沒有 Issue 用了:仍列出來,選單才不會是空白
                if let l = label, !usedLabels.contains(where: { $0.caseInsensitiveCompare(l) == .orderedSame }) {
                    Text(l).tag(String?.some(l))
                }
            }
            .labelsHidden()
            .fixedSize()
            .help("依標籤篩選")

            Picker("優先度", selection: $priority) {
                Text("全部優先度").tag(IssuePriority?.none)
                Divider()
                ForEach(IssuePriority.allCases) { p in Text(p.label).tag(IssuePriority?.some(p)) }
            }
            .labelsHidden()
            .fixedSize()
            .help("依優先度篩選")

            IssueRepoFilterPicker(selection: $repoFilter, repos: usedRepos)
                .fixedSize()

            if hasFilter {
                Button("清除") { query = ""; label = nil; priority = nil; repoFilter = "" }
                    .buttonStyle(.link)
            }
            if let e = store.remoteError ?? (result != nil ? loadError : nil) {
                Label(e, systemImage: "exclamationmark.triangle")
                    .font(.caption).foregroundStyle(.orange)
                    .lineLimit(1).truncationMode(.tail)
                    .help(e)
            } else if let notice {
                Text(notice).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.tail)
            }
            Spacer(minLength: 0)
            Text(hasFilter ? "\(result?.matched ?? 0) / \(total) 個 Issue" : "\(total) 個 Issue")
                .font(.callout.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize()
        }
    }

    // MARK: 看板

    private func board(_ result: IssueBoardResult) -> some View {
        GeometryReader { geo in
            let spacing: CGFloat = 12
            let n = CGFloat(max(1, result.columns.count))
            let colWidth = max(240, (geo.size.width - 40 - spacing * (n - 1)) / n)
            ScrollView(.horizontal) {
                HStack(alignment: .top, spacing: spacing) {
                    ForEach(result.columns) { c in
                        column(c)
                            .frame(width: colWidth)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 14)
                .frame(minHeight: geo.size.height, alignment: .top)
            }
        }
    }

    private func column(_ col: IssueBoardResult.Column) -> some View {
        let isTarget = targeted == col.key
        let look = IssueBoardColumn.look(col.key)
        let items = col.issues
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Image(systemName: look.icon).foregroundStyle(look.color)
                Text(col.title).font(.headline)
                Text("\(items.count)")
                    .font(.caption.weight(.semibold).monospacedDigit())
                    .padding(.horizontal, 6).padding(.vertical, 1)
                    .background(Color.primary.opacity(0.08), in: Capsule())
                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 9)
            .help(look.help)

            ScrollView(.vertical) {
                LazyVStack(spacing: 8) {
                    ForEach(items, id: \.stableId) { issue in
                        IssueBoardCard(issue: issue, onStatus: { setStatus(issue, $0) }, onDragStart: { draggingId = issue.id })
                    }
                    if items.isEmpty {
                        Text(isTarget ? "放開以移到「\(col.title)」" : (hasFilter ? "沒有符合的 Issue" : "沒有 Issue"))
                            .font(.caption).foregroundStyle(.tertiary)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 24)
                            .overlay(RoundedRectangle(cornerRadius: 8)
                                .strokeBorder(style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
                                .foregroundStyle(.quaternary))
                    }
                }
                .padding(.horizontal, 8)
                .padding(.bottom, 10)
            }
        }
        .frame(maxHeight: .infinity, alignment: .top)
        .background(Color.primary.opacity(isTarget ? 0.08 : 0.035), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12)
            .strokeBorder(isTarget ? Color.accentColor : .clear, lineWidth: 2))
        .animation(.easeOut(duration: 0.15), value: isTarget)
        .onDrop(of: [.utf8PlainText, .plainText], delegate: BoardDropDelegate(
            check: { result?.dropCheck(dragging: draggingId, onto: col) ?? .unknown },
            onTarget: { on in
                if on { targeted = col.key } else if targeted == col.key { targeted = nil }
            },
            onBlocked: { notice = blockedNotice },
            onDrop: { dropped in
                // 實際拖進來的字串要等於拖曳開始時記下的 id 才改(擋掉取消拖曳後殘留的 id 配上外部拖進來的文字)
                defer { draggingId = nil; targeted = nil }
                guard let id = draggingId, dropped == id else { return }
                _ = drop([id], on: col)
            }))
    }

    private let blockedNotice = "待處理 ↔ 進行中 由指派角色決定,請到 Issue 分頁改指派"

    /// 拖放:欄的 dropStatus 和 Issue 現在的狀態不同才改;待處理 ↔ 進行中(要改指派)不做
    private func drop(_ ids: [String], on col: IssueBoardResult.Column) -> Bool {
        var moved = false
        for id in ids {
            guard let from = result?.column(of: id), let issue = from.issues.first(where: { $0.id == id }) else { continue }
            guard let s = col.status(forDropping: issue) else {
                if from.key != col.key { notice = blockedNotice }
                continue
            }
            setStatus(issue, s)
            moved = true
        }
        return moved
    }

    /// 改狀態:走和 Issue 分頁同一條路(IssueStore.update → core API),動態牆由 store 記
    private func setStatus(_ issue: Issue, _ s: IssueStatus) {
        guard issue.status != s else { return }
        notice = nil
        withAnimation(.snappy) {
            _ = store.update(issue.id, by: app.me) { $0.status = s }
        }
    }
}

// MARK: - 拖放

/// 看板一欄的放置判斷:能放 → 移動游標 + 高亮;不能放(原欄、待處理 ↔ 進行中)→ 禁止游標、不高亮。
/// 判斷交給 check(IssueBoardResult.dropCheck,用拖曳開始時記下的 id),所以拖曳途中不用讀 NSItemProvider;
/// 放下時才讀出實際拖進來的字串交給 onDrop(由它比對是不是拖曳中的 id)。
private struct BoardDropDelegate: DropDelegate {
    private static let types: [UTType] = [.text]
    let check: () -> IssueBoardResult.DropCheck
    /// 高亮開 / 關
    let onTarget: (Bool) -> Void
    /// 拖進不能放、但要提示的欄(待處理 ↔ 進行中)
    let onBlocked: () -> Void
    /// 放下(只有能放的欄會走到這裡):拖進來的字串(讀不到是 nil),在主執行緒呼叫
    let onDrop: (String?) -> Void

    func validateDrop(info: DropInfo) -> Bool { info.hasItemsConforming(to: Self.types) }

    func dropEntered(info: DropInfo) {
        let c = check()
        onTarget(c.canDrop)
        if c == .blocked { onBlocked() }
    }

    func dropUpdated(info: DropInfo) -> DropProposal? {
        guard info.hasItemsConforming(to: Self.types) else { return DropProposal(operation: .forbidden) }
        return DropProposal(operation: check().canDrop ? .move : .forbidden)
    }

    func dropExited(info: DropInfo) { onTarget(false) }

    func performDrop(info: DropInfo) -> Bool {
        guard check().canDrop, info.hasItemsConforming(to: Self.types),
              let provider = info.itemProviders(for: Self.types).first,
              provider.canLoadObject(ofClass: NSString.self) else {
            onTarget(false)
            onDrop(nil) // 不放,也清掉 draggingId
            return false
        }
        let onDrop = onDrop
        _ = provider.loadObject(ofClass: NSString.self) { obj, _ in
            let s = (obj as? NSString).map { $0 as String }
            DispatchQueue.main.async { onDrop(s) }
        }
        return true
    }
}

// MARK: - Issue 卡片

private struct IssueBoardCard: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel
    let issue: Issue
    let onStatus: (IssueStatus) -> Void
    /// 開始拖曳(看板記下拖曳中的 id,判斷每一欄能不能放)
    let onDragStart: () -> Void
    @State private var hover = false

    private var agentName: String? {
        issue.assignedAgent.map { a in app.agents.first { $0.id == a }?.name ?? a }
    }

    var body: some View {
        HStack(spacing: 0) {
            Rectangle().fill(issue.priority.color).frame(width: 3)
                .help("優先度:\(issue.priority.label)")
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    Text(issue.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                        .lineLimit(1).fixedSize()
                    if IssueStore.isAI(issue.createdBy) {
                        AuthorIcon(author: issue.createdBy, size: 14)
                            .help(app.issueStore?.authorName(issue.createdBy) ?? "")
                    }
                    Spacer(minLength: 4)
                    if !issue.comments.isEmpty {
                        Label("\(issue.comments.count)", systemImage: "bubble.left")
                            .font(.caption).foregroundStyle(.secondary).lineLimit(1).fixedSize()
                    }
                    priorityBadge
                }
                helpPill
                Text(issue.title)
                    .foregroundStyle(issue.status == .open ? .primary : .secondary)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if agentName != nil || issue.repoTag != nil {
                    VStack(alignment: .leading, spacing: 4) {
                        if let name = agentName {
                            BoardTag(text: name, icon: "person.crop.circle", color: .purple)
                                .help("指派給角色:\(name)")
                        }
                        if let r = issue.repoTag {
                            BoardTag(text: r, icon: "chevron.left.forwardslash.chevron.right", color: .teal, monospaced: true)
                                .help("倉庫:\(r)")
                        }
                    }
                }
                if !issue.labels.isEmpty {
                    // 寬度不夠時少放幾個標籤,剩下的用 +n
                    ViewThatFits(in: .horizontal) {
                        labelRow(3)
                        labelRow(2)
                        labelRow(1)
                        labelRow(0)
                    }
                    .help(issue.labels.joined(separator: "、"))
                }
            }
            .padding(10)
        }
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8)
            .strokeBorder(Color.primary.opacity(hover ? 0.18 : 0.08), lineWidth: 1))
        .shadow(color: .black.opacity(hover ? 0.12 : 0.05), radius: hover ? 4 : 1, y: 1)
        .onHover { hover = $0 }
        .contentShape(RoundedRectangle(cornerRadius: 8))
        .onTapGesture { model.showIssue(issue.stableId) }
        .help("按一下在 Issue 分頁打開")
        .onDrag {
            onDragStart()
            return NSItemProvider(object: issue.id as NSString)
        } preview: {
            Text("\(issue.id) \(issue.title)")
                .lineLimit(1)
                .padding(8)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 6))
        }
        .contextMenu { menu }
    }

    /// 有任務需要協助:橘色小標,按下去打開 Issue 詳情(那裡列出任務與問題)
    @ViewBuilder private var helpPill: some View {
        if !app.helpTasks(for: issue).isEmpty {
            Button { model.showIssue(issue.stableId) } label: { IssueHelpPill(issue: issue) }
                .buttonStyle(.plain).help("有任務需要協助,打開 Issue 查看並回覆")
        }
    }

    /// 優先度:緊急、高顯示標記;一般、低只用左邊的色條
    @ViewBuilder private var priorityBadge: some View {
        if issue.priority <= .high {
            Text(issue.priority.label)
                .font(.caption.weight(.medium))
                .lineLimit(1).fixedSize()
                .padding(.horizontal, 6).padding(.vertical, 1)
                .foregroundStyle(issue.priority.color)
                .background(issue.priority.color.opacity(0.13), in: Capsule())
        }
    }

    private func labelRow(_ n: Int) -> some View {
        HStack(spacing: 4) {
            ForEach(issue.labels.prefix(n), id: \.self) { BoardTag(text: $0, icon: "tag", color: .secondary, maxWidth: 120) }
            if issue.labels.count > n {
                Text("+\(issue.labels.count - n)").font(.caption).foregroundStyle(.secondary).fixedSize()
            }
        }
    }

    @ViewBuilder private var menu: some View {
        Button("打開詳細資料") { model.showIssue(issue.stableId) }
        Divider()
        if issue.status == .open {
            Button("關閉 Issue") { onStatus(.closed) }
        } else {
            Button("重新打開") { onStatus(.open) }
        }
        Divider()
        Button("複製編號") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(issue.id, forType: .string)
        }
    }
}

/// 卡片上的小標記(指派、倉庫、標籤):太長時截斷,不撐開卡片
private struct BoardTag: View {
    let text: String
    let icon: String
    var color: Color = .secondary
    var monospaced = false
    var maxWidth: CGFloat? = nil

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: icon).imageScale(.small)
            Text(text)
                .font(monospaced ? .caption.monospaced() : .caption)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .font(.caption)
        .foregroundStyle(color)
        .padding(.horizontal, 6).padding(.vertical, 2)
        .background(color.opacity(0.12), in: Capsule())
        .frame(maxWidth: maxWidth, alignment: .leading)
    }
}
