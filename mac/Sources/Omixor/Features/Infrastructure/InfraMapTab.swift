import SwiftUI

// 服務地圖:依相依深度分層排列,上層呼叫下層。節點顏色代表健康狀態。

/// 計算好的分層佈局(只跟服務清單有關,跟畫面大小無關)
struct InfraGraphModel {
    let layers: [[Service]]          // 第 0 層在最上面(入口服務)
    let edges: [(from: String, to: String)]

    init(services: [Service]) {
        let byId = Dictionary(services.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        var level: [String: Int] = [:]
        var visiting: Set<String> = []

        // level = 到最底層(不依賴任何人)的最長距離;遇到循環就當作 0
        func depth(_ id: String) -> Int {
            if let l = level[id] { return l }
            guard let s = byId[id], !visiting.contains(id) else { return 0 }
            visiting.insert(id)
            let deps = s.dependencies.filter { byId[$0] != nil && $0 != id }
            let l = deps.isEmpty ? 0 : (deps.map(depth).max() ?? 0) + 1
            visiting.remove(id)
            level[id] = l
            return l
        }
        for s in services { _ = depth(s.id) }

        let maxLevel = level.values.max() ?? 0
        var rows: [[Service]] = Array(repeating: [], count: maxLevel + 1)
        for s in services {
            rows[maxLevel - (level[s.id] ?? 0)].append(s)
        }
        // 第一層依關鍵等級與名稱;下面各層依上層呼叫者的平均位置排,減少交叉
        var order: [String: Double] = [:]
        for r in rows.indices {
            if r == 0 {
                rows[r].sort { ($0.tier, $0.name) < ($1.tier, $1.name) }
            } else {
                rows[r].sort { a, b in
                    let pa = Self.barycenter(a.id, services: services, order: order)
                    let pb = Self.barycenter(b.id, services: services, order: order)
                    if pa != pb { return pa < pb }
                    return (a.tier, a.name) < (b.tier, b.name)
                }
            }
            let n = Double(max(1, rows[r].count))
            for (i, s) in rows[r].enumerated() { order[s.id] = (Double(i) + 0.5) / n }
        }
        layers = rows.filter { !$0.isEmpty }
        edges = services.flatMap { s in
            s.dependencies.filter { byId[$0] != nil && $0 != s.id }.map { (from: s.id, to: $0) }
        }
    }

    private static func barycenter(_ id: String, services: [Service], order: [String: Double]) -> Double {
        let callers = services.filter { $0.dependencies.contains(id) }.compactMap { order[$0.id] }
        guard !callers.isEmpty else { return 2 } // 沒人呼叫的放右邊
        return callers.reduce(0, +) / Double(callers.count)
    }
}

struct InfraMapTab: View {
    @EnvironmentObject var app: AppState
    @Binding var selection: String?
    var onOpenService: (String) -> Void

    @State private var hovered: String?

    private let nodeHeight: CGFloat = 56
    private let rowSpacing: CGFloat = 112

    var body: some View {
        if app.services.isEmpty {
            EmptyStateView(title: "沒有可以畫的服務", icon: "point.3.connected.trianglepath.dotted", message: "服務目錄有資料後,這裡會畫出服務之間的相依關係。")
        } else {
            let model = InfraGraphModel(services: app.services)
            VStack(spacing: 0) {
                legendBar
                Divider()
                GeometryReader { geo in
                    let maxCount = model.layers.map(\.count).max() ?? 1
                    let width = max(geo.size.width, CGFloat(maxCount) * 150 + 40)
                    let height = max(geo.size.height, CGFloat(model.layers.count) * rowSpacing + 24)
                    ScrollView([.horizontal, .vertical]) {
                        graph(model, size: CGSize(width: width, height: height))
                            .frame(width: width, height: height)
                    }
                }
                .background(Color(nsColor: .textBackgroundColor).opacity(0.35))
                if let id = selection, let s = app.service(id) {
                    Divider()
                    infoBar(s)
                }
            }
        }
    }

    // MARK: 圖例

    private var legendBar: some View {
        HStack(spacing: 14) {
            ForEach([Health.healthy, .degraded, .down, .maintenance], id: \.self) { h in
                HStack(spacing: 5) {
                    RoundedRectangle(cornerRadius: 3).fill(h.color).frame(width: 10, height: 10)
                    Text(h.label)
                }
            }
            Spacer()
            Label("箭頭由呼叫方指向被依賴的服務", systemImage: "arrow.down")
                .lineLimit(1)
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 16).padding(.vertical, 8)
    }

    // MARK: 圖

    private func positions(_ model: InfraGraphModel, size: CGSize) -> (pos: [String: CGPoint], nodeWidth: CGFloat) {
        var pos: [String: CGPoint] = [:]
        let rows = CGFloat(max(1, model.layers.count))
        let maxCount = CGFloat(model.layers.map(\.count).max() ?? 1)
        let nodeWidth = min(176, size.width / maxCount - 18)
        let rowH = (size.height - 24) / rows
        for (r, layer) in model.layers.enumerated() {
            let y = 12 + rowH * (CGFloat(r) + 0.5)
            let colW = size.width / CGFloat(layer.count)
            for (i, s) in layer.enumerated() {
                pos[s.id] = CGPoint(x: colW * (CGFloat(i) + 0.5), y: y)
            }
        }
        return (pos, nodeWidth)
    }

    private func graph(_ model: InfraGraphModel, size: CGSize) -> some View {
        let (pos, nodeWidth) = positions(model, size: size)
        let focus = hovered ?? selection
        let related: Set<String> = {
            guard let f = focus, let s = app.service(f) else { return [] }
            var set: Set<String> = [f]
            s.dependencies.forEach { set.insert($0) }
            app.services.filter { $0.dependencies.contains(f) }.forEach { set.insert($0.id) }
            return set
        }()
        let health = Dictionary(app.services.map { ($0.id, $0.health) }, uniquingKeysWith: { a, _ in a })
        let half = nodeHeight / 2

        return ZStack {
            // 點空白處取消選取
            Color.clear.contentShape(Rectangle())
                .onTapGesture { selection = nil }

            Canvas { ctx, _ in
                for e in model.edges {
                    guard let a = pos[e.from], let b = pos[e.to] else { continue }
                    let active = focus != nil && (e.from == focus || e.to == focus)
                    let dim = focus != nil && !active
                    let target = health[e.to] ?? .unknown
                    let color: Color = active ? .accentColor
                        : (target == .healthy ? Color.secondary : target.color)
                    let start = CGPoint(x: a.x, y: a.y + half)
                    let end = CGPoint(x: b.x, y: b.y - half - 2)
                    var path = Path()
                    path.move(to: start)
                    if abs(end.y - start.y) < 4 || end.y < start.y {
                        // 同層或往上(循環):繞一個弧
                        let lift = max(40, abs(b.x - a.x) * 0.25)
                        path = Path()
                        path.move(to: CGPoint(x: a.x, y: a.y - half))
                        path.addCurve(to: CGPoint(x: b.x, y: b.y - half - 2),
                                      control1: CGPoint(x: a.x, y: a.y - half - lift),
                                      control2: CGPoint(x: b.x, y: b.y - half - lift))
                    } else {
                        let dy = (end.y - start.y) * 0.5
                        path.addCurve(to: end, control1: CGPoint(x: start.x, y: start.y + dy),
                                      control2: CGPoint(x: end.x, y: end.y - dy))
                    }
                    let opacity: Double = dim ? 0.12 : (active ? 0.95 : (target == .healthy ? 0.4 : 0.8))
                    ctx.stroke(path, with: .color(color.opacity(opacity)),
                               style: StrokeStyle(lineWidth: active ? 2 : 1.3, lineCap: .round,
                                                  dash: target == .down && !active ? [5, 4] : []))
                    // 箭頭(終點朝下)
                    var head = Path()
                    head.move(to: end)
                    head.addLine(to: CGPoint(x: end.x - 4.5, y: end.y - 8))
                    head.addLine(to: CGPoint(x: end.x + 4.5, y: end.y - 8))
                    head.closeSubpath()
                    ctx.fill(head, with: .color(color.opacity(opacity)))
                }
            }
            .allowsHitTesting(false)

            ForEach(model.layers.flatMap { $0 }) { s in
                if let p = pos[s.id] {
                    InfraMapNode(service: s,
                                 selected: selection == s.id,
                                 dimmed: focus != nil && !related.contains(s.id),
                                 width: nodeWidth, height: nodeHeight)
                        .position(p)
                        .onTapGesture(count: 2) { onOpenService(s.id) }
                        .onTapGesture { selection = selection == s.id ? nil : s.id }
                        .onHover { inside in
                            if inside { hovered = s.id } else if hovered == s.id { hovered = nil }
                        }
                        .help("\(s.name):\(s.health.label)。點兩下查看詳情")
                }
            }
        }
        .animation(.easeOut(duration: 0.15), value: focus)
    }

    // MARK: 資訊列

    private func infoBar(_ s: Service) -> some View {
        let dependents = app.services.filter { $0.dependencies.contains(s.id) }.count
        return HStack(spacing: 12) {
            HealthBadge(health: s.health)
            Text(s.name).font(.headline).lineLimit(1)
            InfraTierPill(tier: s.tier)
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 12) {
                    Text("依賴 \(s.dependencies.count) · 被依賴 \(dependents)")
                    Text("p95 \(Int(s.latencyP95))ms")
                    Text("錯誤 \(Fmt.percent(s.errorRate, digits: 2))")
                    Text("\(Fmt.number(s.rps)) rps")
                }
                Text("依賴 \(s.dependencies.count) · 被依賴 \(dependents)")
                EmptyView()
            }
            .font(.callout.monospacedDigit())
            .foregroundStyle(.secondary)
            .lineLimit(1)
            Spacer(minLength: 8)
            Button("取消選取") { selection = nil }
                .buttonStyle(.borderless)
            Button {
                onOpenService(s.id)
            } label: { Label("查看詳情", systemImage: "arrow.right.circle") }
            .buttonStyle(.borderedProminent)
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
        .background(.bar)
    }
}

// MARK: - 節點

struct InfraMapNode: View {
    let service: Service
    var selected: Bool
    var dimmed: Bool
    var width: CGFloat
    var height: CGFloat

    var body: some View {
        let c = service.health.color
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 5) {
                Image(systemName: service.health.icon).foregroundStyle(c).font(.caption)
                Text(service.name).font(.callout.weight(.semibold)).lineLimit(1)
                Spacer(minLength: 0)
                Text("T\(service.tier)").font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
            }
            Text("p95 \(Int(service.latencyP95))ms · \(Fmt.percent(service.errorRate, digits: 2))")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(.horizontal, 9)
        .frame(width: width, height: height)
        .background(
            RoundedRectangle(cornerRadius: 10)
                .fill(Color(nsColor: .controlBackgroundColor))
        )
        .background(
            RoundedRectangle(cornerRadius: 10)
                .fill(c.opacity(0.14))
                .padding(-0.5)
        )
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .fill(c.opacity(service.health == .healthy ? 0.04 : 0.12))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .strokeBorder(selected ? Color.accentColor : c.opacity(0.7), lineWidth: selected ? 2.5 : 1.2)
        )
        .shadow(color: .black.opacity(selected ? 0.18 : 0.06), radius: selected ? 6 : 2, y: 1)
        .opacity(dimmed ? 0.4 : 1)
        .contentShape(RoundedRectangle(cornerRadius: 10))
    }
}
