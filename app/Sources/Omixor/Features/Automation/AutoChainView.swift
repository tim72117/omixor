import SwiftUI

/// AI 自動化 › 組織鏈:誰可以派工作給誰(實線箭頭),做完回報給誰(綠色虛線)。
/// 由上而下分層畫圖;管理員(本機一定是;遠端要是組織管理員)可以新增、刪除邊並存檔,錯誤原文顯示。
/// 對應 web 的 AutoChain.tsx;規則見 Agents/OrgChain.swift 與 core/README.md。
struct AutoChainView: View {
    @EnvironmentObject var app: AppState
    @State private var draft: [ChainEdge] = []
    @State private var dirty = false
    @State private var newFrom = ""
    @State private var newTo = ""
    @State private var newLabel = ""
    @State private var saveError: String?
    @State private var saved = false
    @State private var members: [RemoteMember] = []
    @State private var canEdit = true
    @State private var reported: [AgentTask] = []

    private var store: AgentStore { app.startAgents() }

    var body: some View {
        VStack(spacing: 0) {
            if let e = app.orgChainError { RemoteErrorBar(message: "讀不到組織鏈:\(e)") }
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Card("組織鏈", subtitle: "實線箭頭:派工作的方向(上面的可以派給下面的,邊上是 label);綠色虛線:做完回報的方向") {
                        ChainGraph(chain: OrgChain(edges: draft), name: store.chainName, agent: agentOf)
                    }
                    editor
                    reportedCard
                }
                .padding(20)
                .frame(maxWidth: 1100, alignment: .leading)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .onAppear(perform: reload)
        .onChange(of: app.orgChain) { if !dirty { draft = app.orgChain.edges } }
    }

    private func agentOf(_ node: String) -> Agent? { ChainNode.agentId(node).flatMap { store.agent($0) } }

    private func reload() {
        store.loadChain()
        let r = store.remote
        members = (try? r.listMembers(org: app.currentOrgId)) ?? []
        canEdit = ((try? r.myRole(org: app.currentOrgId)) ?? nil) == "admin" || app.omixorLogin.account?.role == "admin"
        reported = (try? r.reportedTasks(org: app.currentOrgId, reportTo: "me")) ?? []
        draft = app.orgChain.edges
        dirty = false
    }

    /// 可以選的節點:角色、組織成員(server 上的),加上鏈上已經有的
    private var nodeOptions: [String] {
        var out = app.agents.map { ChainNode.agent($0.id) }
        out += members.map { ChainNode.user($0.email) }
        out += draft.flatMap { [$0.from, $0.to] }
        var seen = Set<String>()
        return out.filter { seen.insert($0).inserted }
    }

    private func nodeLabel(_ n: String) -> String {
        ChainNode.isAgent(n) ? "\(store.chainName(n))(角色)" : store.chainName(n)
    }

    private var editor: some View {
        Card("邊", subtitle: canEdit ? "改完按「存檔」才會生效;不能指向自己、不能重複、不能有循環" : "只有組織管理員可以修改") {
            if draft.isEmpty { Text("還沒有任何邊").foregroundStyle(.secondary) }
            ForEach(draft) { e in
                HStack(spacing: 8) {
                    Text(nodeLabel(e.from)).lineLimit(1)
                    Image(systemName: "arrow.right").foregroundStyle(.secondary)
                    Text(nodeLabel(e.to)).lineLimit(1)
                    Pill(text: e.labelText, color: .indigo)
                    Spacer()
                    if canEdit {
                        Button(role: .destructive) {
                            draft.removeAll { $0.id == e.id }
                            dirty = true; saved = false
                        } label: { Image(systemName: "trash") }
                        .buttonStyle(.borderless)
                        .help("刪除這條邊")
                    }
                }
                Divider()
            }
            if canEdit {
                HStack(spacing: 8) {
                    Picker("From", selection: $newFrom) {
                        Text("選擇派工的…").tag("")
                        ForEach(nodeOptions, id: \.self) { Text(nodeLabel($0)).tag($0) }
                    }
                    .frame(maxWidth: 220)
                    Image(systemName: "arrow.right").foregroundStyle(.secondary)
                    Picker("To", selection: $newTo) {
                        Text("選擇接工作的…").tag("")
                        ForEach(nodeOptions, id: \.self) { Text(nodeLabel($0)).tag($0) }
                    }
                    .frame(maxWidth: 220)
                    TextField("label(例如 目標、任務)", text: $newLabel)
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 180)
                    Button("加入") {
                        draft.removeAll { $0.from == newFrom && $0.to == newTo }
                        draft.append(ChainEdge(from: newFrom, to: newTo, label: newLabel.trimmingCharacters(in: .whitespaces)))
                        newLabel = ""
                        dirty = true; saved = false
                    }
                    .disabled(newFrom.isEmpty || newTo.isEmpty)
                }
                HStack {
                    Button("存檔") { save() }
                        .buttonStyle(.borderedProminent)
                        .disabled(!dirty)
                    Button("還原") { reload(); saveError = nil }
                        .disabled(!dirty)
                    if saved { Label("已存檔", systemImage: "checkmark.circle").foregroundStyle(.green).font(.caption) }
                    Spacer()
                }
            }
            if let saveError {
                Text(saveError).font(.callout).foregroundStyle(.red).textSelection(.enabled)
            }
        }
    }

    private func save() {
        do {
            let c = try store.saveChain(draft)
            draft = c.edges
            dirty = false
            saveError = nil
            saved = true
        } catch {
            saveError = error.localizedDescription
            saved = false
        }
    }

    private var reportedCard: some View {
        Card("回報給我的任務", subtitle: "我派出去、做完或需要協助時依組織鏈回報給我的") {
            if reported.isEmpty { Text("還沒有").foregroundStyle(.secondary) }
            ForEach(reported) { t in
                HStack(spacing: 6) {
                    Image(systemName: t.status.icon).foregroundStyle(t.status.color)
                    Text(t.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                    Text(t.title).lineLimit(1)
                    Spacer()
                    Text(store.agent(t.agentId)?.name ?? t.agentId).font(.caption).foregroundStyle(.secondary)
                    Pill(text: t.status.label, color: t.status.color)
                }
            }
        }
    }
}

/// 分層的組織鏈圖:節點由上而下(根在最上層),實線箭頭是派工方向、綠色虛線是回報方向
struct ChainGraph: View {
    let chain: OrgChain
    let name: (String) -> String
    let agent: (String) -> Agent?

    static let nodeW: CGFloat = 160, nodeH: CGFloat = 48, gapX: CGFloat = 36, gapY: CGFloat = 92, pad: CGFloat = 16

    /// 每個節點的中心位置與整張圖的大小
    static func layout(_ chain: OrgChain) -> (pos: [String: CGPoint], size: CGSize) {
        let layers = chain.layers()
        let widest = CGFloat(layers.map(\.count).max() ?? 0)
        let width = max(1, widest) * nodeW + max(0, widest - 1) * gapX + pad * 2
        var pos: [String: CGPoint] = [:]
        for (li, row) in layers.enumerated() {
            let rowW = CGFloat(row.count) * nodeW + CGFloat(max(0, row.count - 1)) * gapX
            let x0 = (width - rowW) / 2
            for (i, n) in row.enumerated() {
                pos[n] = CGPoint(x: x0 + CGFloat(i) * (nodeW + gapX) + nodeW / 2, y: pad + nodeH / 2 + CGFloat(li) * (nodeH + gapY))
            }
        }
        let height = CGFloat(layers.count) * nodeH + CGFloat(max(0, layers.count - 1)) * gapY + pad * 2
        return (pos, CGSize(width: width, height: max(height, 60)))
    }

    var body: some View {
        if chain.edges.isEmpty {
            Text("還沒有組織鏈。加一條邊,例如「CEO → 研發長(目標)」。").foregroundStyle(.secondary)
        } else {
            let (pos, size) = Self.layout(chain)
            ScrollView(.horizontal) {
                ZStack(alignment: .topLeading) {
                    Canvas { ctx, _ in
                        for e in chain.edges {
                            guard let a = pos[e.from], let b = pos[e.to] else { continue }
                            let start = CGPoint(x: a.x - 6, y: a.y + Self.nodeH / 2)
                            let end = CGPoint(x: b.x - 6, y: b.y - Self.nodeH / 2)
                            arrow(&ctx, from: start, to: end, color: .accentColor, dashed: false)
                            // 回報:從下層回到上層,稍微錯開
                            arrow(&ctx, from: CGPoint(x: b.x + 6, y: b.y - Self.nodeH / 2), to: CGPoint(x: a.x + 6, y: a.y + Self.nodeH / 2),
                                  color: .green, dashed: true)
                        }
                    }
                    .frame(width: size.width, height: size.height)
                    ForEach(chain.edges) { e in
                        if let a = pos[e.from], let b = pos[e.to] {
                            Text(e.labelText)
                                .font(.caption2.weight(.medium))
                                .padding(.horizontal, 5).padding(.vertical, 1)
                                .background(Color(nsColor: .controlBackgroundColor), in: Capsule())
                                .overlay(Capsule().strokeBorder(Color.accentColor.opacity(0.4)))
                                .position(x: (a.x + b.x) / 2 - 6, y: (a.y + b.y) / 2)
                        }
                    }
                    ForEach(Array(pos.keys), id: \.self) { n in
                        node(n).position(pos[n]!)
                    }
                }
                .frame(width: size.width, height: size.height)
            }
            HStack(spacing: 14) {
                Label("派工作", systemImage: "arrow.down").foregroundStyle(Color.accentColor)
                Label("回報(虛線)", systemImage: "arrow.up").foregroundStyle(.green)
            }
            .font(.caption)
        }
    }

    private func node(_ n: String) -> some View {
        HStack(spacing: 6) {
            if let g = agent(n) {
                AgentAvatar(agent: g, size: 26)
            } else if ChainNode.isAgent(n) {
                Image(systemName: "person.crop.circle.badge.questionmark").font(.title3).foregroundStyle(.secondary)
            } else {
                Image(systemName: "person.crop.circle").font(.title3).foregroundStyle(.secondary)
            }
            VStack(alignment: .leading, spacing: 0) {
                Text(name(n)).font(.callout.weight(.medium)).lineLimit(1)
                Text(ChainNode.isAgent(n) ? (agent(n)?.role ?? "角色") : "成員").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 8)
        .frame(width: Self.nodeW, height: Self.nodeH)
        .background(Color(nsColor: .windowBackgroundColor), in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.15)))
        .help(n)
    }

    private func arrow(_ ctx: inout GraphicsContext, from p: CGPoint, to q: CGPoint, color: Color, dashed: Bool) {
        var path = Path()
        path.move(to: p)
        path.addLine(to: q)
        ctx.stroke(path, with: .color(color.opacity(dashed ? 0.7 : 0.9)),
                   style: StrokeStyle(lineWidth: dashed ? 1.2 : 1.6, dash: dashed ? [4, 3] : []))
        let ang = atan2(q.y - p.y, q.x - p.x)
        let len: CGFloat = 8
        var head = Path()
        head.move(to: q)
        head.addLine(to: CGPoint(x: q.x - len * cos(ang - .pi / 7), y: q.y - len * sin(ang - .pi / 7)))
        head.addLine(to: CGPoint(x: q.x - len * cos(ang + .pi / 7), y: q.y - len * sin(ang + .pi / 7)))
        head.closeSubpath()
        ctx.fill(head, with: .color(color))
    }
}
