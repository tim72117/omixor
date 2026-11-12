import SwiftUI

struct DevBoardTab: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel

    @State private var assignee = "all"          // all / me / ai / none / p:<id>
    @State private var type: WorkType?
    @State private var onlyAI = false
    @State private var showBacklog = true
    @State private var query = ""
    @State private var targeted: WorkStatus?

    private var columns: [WorkStatus] {
        showBacklog ? WorkStatus.allCases : WorkStatus.allCases.filter { $0 != .backlog }
    }

    private var filtered: [WorkItem] {
        app.workItems.filter { item in
            if let type, item.type != type { return false }
            if onlyAI && !item.createdByAI { return false }
            switch assignee {
            case "all": break
            case "me": if item.assignee != .person(app.me) { return false }
            case "ai": if !(item.assignee?.devIsAI ?? false) { return false }
            case "none": if item.assignee != nil { return false }
            default:
                if item.assignee?.devPersonId != String(assignee.dropFirst(2)) { return false }
            }
            if !query.isEmpty {
                let q = query.lowercased()
                if !item.title.lowercased().contains(q) && !item.id.lowercased().contains(q) { return false }
            }
            return true
        }
    }

    private var hasFilter: Bool { assignee != "all" || type != nil || onlyAI || !query.isEmpty }

    var body: some View {
        VStack(spacing: 0) {
            filterBar
                .padding(.horizontal, 20)
                .padding(.vertical, 10)
            Divider()
            if app.workItems.isEmpty {
                EmptyStateView(title: "看板是空的", icon: "rectangle.split.3x1", message: "這個 Sprint 還沒有工作項目")
            } else {
                board
            }
        }
    }

    // MARK: 篩選列

    private var filterBar: some View {
        let items = filtered
        let pts = items.reduce(0) { $0 + $1.points }
        return HStack(spacing: 10) {
            TextField("搜尋標題或編號", text: $query)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 120, maxWidth: 200)

            Picker("負責人", selection: $assignee) {
                Text("所有人").tag("all")
                Text("我").tag("me")
                Text("AI").tag("ai")
                Text("未指派").tag("none")
                if !app.people.isEmpty {
                    Divider()
                    ForEach(app.people) { p in Text(p.name).tag("p:\(p.id)") }
                }
            }
            .fixedSize()

            Picker("類型", selection: $type) {
                Text("全部類型").tag(WorkType?.none)
                Divider()
                ForEach(WorkType.allCases) { t in Text(t.label).tag(WorkType?.some(t)) }
            }
            .fixedSize()

            Toggle(isOn: $onlyAI) { Label("AI 建立", systemImage: "sparkles") }
                .toggleStyle(.button)
                .help("只顯示 AI 建立的項目")
            Toggle(isOn: $showBacklog) { Label("Backlog", systemImage: "tray") }
                .toggleStyle(.button)
                .help("顯示 Backlog 欄")

            if hasFilter {
                Button("清除") {
                    assignee = "all"; type = nil; onlyAI = false; query = ""
                }
                .buttonStyle(.link)
            }
            Spacer(minLength: 0)
            Text("\(items.count) 項・\(pts) 點")
                .font(.callout.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .fixedSize()
        }
    }

    // MARK: 看板

    private var board: some View {
        GeometryReader { geo in
            let spacing: CGFloat = 12
            let n = CGFloat(columns.count)
            let colWidth = max(220, (geo.size.width - 40 - spacing * (n - 1)) / n)
            let items = filtered
            ScrollView(.horizontal) {
                HStack(alignment: .top, spacing: spacing) {
                    ForEach(columns) { status in
                        column(status, items: items.filter { $0.status == status })
                            .frame(width: colWidth)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 14)
                .frame(minHeight: geo.size.height, alignment: .top)
            }
        }
    }

    private func column(_ status: WorkStatus, items: [WorkItem]) -> some View {
        let pts = items.reduce(0) { $0 + $1.points }
        let isTarget = targeted == status
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Image(systemName: status.devIcon).foregroundStyle(status.devColor)
                Text(status.label).font(.headline)
                Text("\(items.count)")
                    .font(.caption.weight(.semibold).monospacedDigit())
                    .padding(.horizontal, 6).padding(.vertical, 1)
                    .background(Color.primary.opacity(0.08), in: Capsule())
                Spacer()
                Text("\(pts) 點").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 9)

            ScrollView(.vertical) {
                LazyVStack(spacing: 8) {
                    ForEach(items) { item in
                        DevWorkCard(item: item)
                    }
                    if items.isEmpty {
                        Text(isTarget ? "放開以移到「\(status.label)」" : "沒有項目")
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
        .dropDestination(for: String.self) { ids, _ in
            var moved = false
            for id in ids {
                guard let item = app.workItems.first(where: { $0.id == id }), item.status != status else { continue }
                withAnimation(.snappy) { app.moveWorkItem(id: id, to: status) }
                app.log(.person(app.me), "rectangle.split.3x1", "把 \(id) 移到「\(status.label)」", .development)
                moved = true
            }
            return moved
        } isTargeted: { on in
            if on { targeted = status } else if targeted == status { targeted = nil }
        }
    }
}

// MARK: - 工作卡片

struct DevWorkCard: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel
    let item: WorkItem
    @State private var showDetail = false
    @State private var hover = false

    private var highlighted: Bool { model.highlightedItem == item.id }
    private var linkedPRs: [PullRequest] { app.pullRequests.filter { $0.linkedItem == item.id } }

    var body: some View {
        HStack(spacing: 0) {
            Rectangle().fill(item.type.color).frame(width: 3)
            VStack(alignment: .leading, spacing: 7) {
                HStack(spacing: 6) {
                    Text(item.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                    if item.createdByAI {
                        Image(systemName: "sparkles").font(.caption2).foregroundStyle(.purple)
                            .help("由 AI 建立")
                    }
                    Spacer(minLength: 0)
                    Text("\(item.points)")
                        .font(.caption.weight(.semibold).monospacedDigit())
                        .frame(minWidth: 18)
                        .padding(.horizontal, 4).padding(.vertical, 1)
                        .background(Color.primary.opacity(0.07), in: Capsule())
                        .help("\(item.points) 點")
                }
                Text(item.title)
                    .font(.callout)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 6) {
                    DevTypeTag(type: item.type)
                    if let svc = item.service {
                        Text(app.service(svc)?.name ?? svc)
                            .font(.caption2)
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 4))
                            .lineLimit(1)
                    }
                    Spacer(minLength: 0)
                    if !linkedPRs.isEmpty {
                        HStack(spacing: 2) {
                            Image(systemName: "arrow.triangle.pull")
                            Text("\(linkedPRs.count)")
                        }
                        .font(.caption2).foregroundStyle(.secondary)
                        .help("\(linkedPRs.count) 個相關 PR")
                    }
                    if let a = item.assignee {
                        ActorAvatar(actor: a, size: 20)
                    } else {
                        Circle().strokeBorder(style: StrokeStyle(lineWidth: 1, dash: [2, 2]))
                            .foregroundStyle(.secondary)
                            .frame(width: 20, height: 20)
                            .help("未指派")
                    }
                }
            }
            .padding(10)
        }
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8)
            .strokeBorder(highlighted ? Color.accentColor : Color.primary.opacity(hover ? 0.18 : 0.08),
                          lineWidth: highlighted ? 2 : 1))
        .shadow(color: .black.opacity(hover ? 0.12 : 0.05), radius: hover ? 4 : 1, y: 1)
        .onHover { hover = $0 }
        .contentShape(RoundedRectangle(cornerRadius: 8))
        .onTapGesture { showDetail = true }
        .draggable(item.id) {
            Text("\(item.id) \(item.title)")
                .lineLimit(1)
                .padding(8)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 6))
        }
        .contextMenu { menu }
        .popover(isPresented: $showDetail, arrowEdge: .trailing) {
            DevWorkDetail(item: item)
        }
        .onAppear {
            if highlighted {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { showDetail = true }
            }
        }
        .onChange(of: showDetail) { _, open in
            if !open && highlighted { model.highlightedItem = nil }
        }
    }

    @ViewBuilder private var menu: some View {
        Menu("移到…") {
            ForEach(WorkStatus.allCases) { s in
                Button {
                    withAnimation(.snappy) { app.moveWorkItem(id: item.id, to: s) }
                    app.log(.person(app.me), "rectangle.split.3x1", "把 \(item.id) 移到「\(s.label)」", .development)
                } label: {
                    Label(s.label, systemImage: s.devIcon)
                }
                .disabled(s == item.status)
            }
        }
        if item.assignee != .person(app.me) {
            Button("指派給我") { DevWorkActions.assign(app, item.id, to: .person(app.me)) }
        }
        if item.assignee != nil {
            Button("取消指派") { DevWorkActions.assign(app, item.id, to: nil) }
        }
        Divider()
        Button("查看詳細資料") { showDetail = true }
        if let svc = item.service {
            Button("前往服務「\(app.service(svc)?.name ?? svc)」") { app.go(.infrastructure, focus: svc) }
        }
        Button("複製編號") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(item.id, forType: .string)
        }
    }
}

enum DevWorkActions {
    @MainActor
    static func assign(_ app: AppState, _ id: String, to actor: Actor?) {
        guard let i = app.workItems.firstIndex(where: { $0.id == id }) else { return }
        app.workItems[i].assignee = actor
        let who = actor.map { app.name(of: $0) } ?? "未指派"
        app.log(.person(app.me), "person.crop.circle.badge.checkmark", "把 \(id) 指派給 \(who)", .development)
    }
}

// MARK: - 卡片詳細(popover)

private struct DevWorkDetail: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var model: DevModel
    let item: WorkItem

    private var current: WorkItem { app.workItems.first { $0.id == item.id } ?? item }

    var body: some View {
        let w = current
        let prs = app.pullRequests.filter { $0.linkedItem == w.id }
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                Text(w.id).font(.callout.monospaced()).foregroundStyle(.secondary)
                Pill(text: w.type.label, color: w.type.color)
                if w.createdByAI { AIBadge(text: "AI 建立") }
                Spacer()
                Text("\(w.points) 點").font(.callout.monospacedDigit()).foregroundStyle(.secondary)
            }
            Text(w.title).font(.headline).fixedSize(horizontal: false, vertical: true)

            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 8) {
                GridRow {
                    Text("狀態").foregroundStyle(.secondary)
                    Picker("狀態", selection: Binding(
                        get: { w.status },
                        set: { s in
                            app.moveWorkItem(id: w.id, to: s)
                            app.log(.person(app.me), "rectangle.split.3x1", "把 \(w.id) 移到「\(s.label)」", .development)
                        })) {
                        ForEach(WorkStatus.allCases) { s in Text(s.label).tag(s) }
                    }
                    .labelsHidden()
                    .fixedSize()
                }
                GridRow {
                    Text("負責人").foregroundStyle(.secondary)
                    HStack {
                        if let a = w.assignee { ActorLabel(actor: a) } else { Text("未指派").foregroundStyle(.tertiary) }
                        if w.assignee != .person(app.me) {
                            Button("指派給我") { DevWorkActions.assign(app, w.id, to: .person(app.me)) }
                                .buttonStyle(.link).font(.caption)
                        }
                    }
                }
                if let svc = w.service {
                    GridRow {
                        Text("服務").foregroundStyle(.secondary)
                        Button {
                            app.go(.infrastructure, focus: svc)
                        } label: {
                            HStack(spacing: 4) {
                                if let s = app.service(svc) { HealthBadge(health: s.health, compact: true) }
                                Text(app.service(svc)?.name ?? svc)
                                Image(systemName: "arrow.up.forward").font(.caption2)
                            }
                        }
                        .buttonStyle(.link)
                    }
                }
            }
            .font(.callout)

            if !prs.isEmpty {
                Divider()
                Text("相關 Pull Request").font(.subheadline.weight(.medium))
                ForEach(prs) { pr in
                    Button { model.showPR(pr.id) } label: {
                        HStack(spacing: 6) {
                            Circle().fill(pr.status.color).frame(width: 7, height: 7)
                            Text("#\(pr.id)").monospacedDigit().foregroundStyle(.secondary)
                            Text(pr.title).lineLimit(1)
                            Spacer(minLength: 4)
                            DevChecksIcon(status: pr.checks)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .font(.callout)
                }
            }
        }
        .padding(16)
        .frame(width: 340)
    }
}
