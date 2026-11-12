import SwiftUI

// 伺服器分頁:篩選列、主機卡片格線、右側詳細面板。

struct InfraServersTab: View {
    @EnvironmentObject var app: AppState
    @Binding var selection: String?
    var onOpenService: (String) -> Void

    @State private var search = ""
    @State private var envFilter: DeployEnv?
    @State private var regionFilter: String?
    @State private var healthFilter: Health?
    @State private var didInitEnv = false
    @State private var drained: Set<String> = []

    private var regions: [String] { Array(Set(app.servers.map(\.region))).sorted() }

    private var filtered: [Server] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        return app.servers.filter { s in
            (envFilter == nil || s.env == envFilter) &&
            (regionFilter == nil || s.region == regionFilter) &&
            (healthFilter == nil || s.health == healthFilter) &&
            (q.isEmpty || s.name.lowercased().contains(q) || s.role.lowercased().contains(q)
                || s.ip.contains(q) || s.tags.contains { $0.lowercased().contains(q) })
        }
        .sorted { ($0.env.rawValue, $0.name) < ($1.env.rawValue, $1.name) }
    }

    private var hasFilters: Bool { regionFilter != nil || healthFilter != nil || !search.isEmpty || envFilter != app.env }

    var body: some View {
        if app.servers.isEmpty {
            EmptyStateView(title: "還沒有主機", icon: "server.rack", message: "接上雲端供應商後,這裡會列出所有主機與容器。")
        } else {
            VStack(spacing: 0) {
                filterBar
                Divider()
                GeometryReader { geo in
                    HStack(spacing: 0) {
                        grid
                        if let id = selection, let server = app.server(id) {
                            Divider()
                            InfraServerDetail(server: server, drained: $drained,
                                              onClose: { selection = nil },
                                              onOpenService: onOpenService)
                                .id(server.id)
                                .frame(width: min(480, max(330, geo.size.width * 0.45)))
                                .transition(.move(edge: .trailing))
                        }
                    }
                }
            }
            .onAppear {
                if !didInitEnv { envFilter = app.env; didInitEnv = true }
                revealSelection()
            }
            .onChange(of: app.env) { _, new in envFilter = new }
            .onChange(of: selection) { _, _ in revealSelection() }
        }
    }

    /// 被選中的主機若被篩選藏起來,就放寬篩選讓它出現
    private func revealSelection() {
        guard let id = selection, let s = app.server(id), !filtered.contains(where: { $0.id == id }) else { return }
        envFilter = s.env
        regionFilter = nil
        healthFilter = nil
        search = ""
    }

    // MARK: 篩選列

    private var filterBar: some View {
        let items = filtered
        let down = items.filter { $0.health == .down }.count
        let degraded = items.filter { $0.health == .degraded }.count
        let avgCPU = items.isEmpty ? 0 : items.map(\.cpu).reduce(0, +) / Double(items.count)
        return HStack(spacing: 10) {
            HStack(spacing: 5) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("搜尋名稱、角色、IP、標籤", text: $search).textFieldStyle(.plain)
                if !search.isEmpty {
                    Button { search = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary) }
                        .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 7).padding(.vertical, 5)
            .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 7))
            .frame(minWidth: 140, maxWidth: 240)

            Picker("環境", selection: $envFilter) {
                Text("全部環境").tag(DeployEnv?.none)
                Divider()
                ForEach(DeployEnv.allCases) { Text($0.label).tag(DeployEnv?.some($0)) }
            }
            .labelsHidden()
            .fixedSize()
            .help("環境")

            Picker("區域", selection: $regionFilter) {
                Text("全部區域").tag(String?.none)
                Divider()
                ForEach(regions, id: \.self) { Text($0).tag(String?.some($0)) }
            }
            .labelsHidden()
            .fixedSize()
            .help("區域")

            Picker("狀態", selection: $healthFilter) {
                Text("全部狀態").tag(Health?.none)
                Divider()
                ForEach(Health.allCases) { Text($0.label).tag(Health?.some($0)) }
            }
            .labelsHidden()
            .fixedSize()
            .help("健康狀態")

            if hasFilters {
                Button("重設") {
                    search = ""; regionFilter = nil; healthFilter = nil; envFilter = app.env
                }
                .buttonStyle(.link)
            }

            Spacer(minLength: 8)

            ViewThatFits(in: .horizontal) {
                HStack(spacing: 12) {
                    Text("\(items.count) 台")
                    if down > 0 { Label("\(down) 中斷", systemImage: "xmark.octagon.fill").foregroundStyle(.red) }
                    if degraded > 0 { Label("\(degraded) 效能下降", systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange) }
                    Text("平均 CPU \(Int(avgCPU))%")
                }
                Text("\(items.count) 台")
            }
            .font(.callout.monospacedDigit())
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        .padding(.horizontal, 16).padding(.vertical, 10)
    }

    // MARK: 卡片格線

    @ViewBuilder private var grid: some View {
        let items = filtered
        if items.isEmpty {
            EmptyStateView(title: "沒有符合的主機", icon: "line.3.horizontal.decrease.circle", message: "調整環境、區域或狀態篩選。")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 230), spacing: 12)], spacing: 12) {
                    ForEach(items) { s in
                        InfraHostCard(server: s, selected: s.id == selection, drained: drained.contains(s.id))
                            .onTapGesture {
                                withAnimation(.snappy(duration: 0.25)) {
                                    selection = selection == s.id ? nil : s.id
                                }
                            }
                    }
                }
                .padding(16)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

// MARK: - 主機卡片

struct InfraHostCard: View {
    let server: Server
    var selected: Bool
    var drained: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 7) {
                StatusDot(color: server.health.color, pulsing: server.health == .down)
                Text(server.name).font(.headline).lineLimit(1)
                Spacer(minLength: 4)
                if drained { Pill(text: "已排空", color: .blue, icon: "drop.triangle") }
                EnvBadge(env: server.env)
            }
            Text("\(server.role) · \(server.provider) \(server.region)")
                .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            VStack(spacing: 4) {
                UsageBar(value: server.cpu, label: "CPU")
                UsageBar(value: server.memory, label: "RAM")
                UsageBar(value: server.disk, label: "磁碟")
            }
            if server.cpuSeries.count > 1 {
                Sparkline(values: server.cpuSeries.suffix(30).map(\.value), color: infraUsageColor(server.cpu))
                    .frame(height: 28)
                    .allowsHitTesting(false)
            }
            HStack {
                Label("\(server.containers.count) 個容器", systemImage: "shippingbox")
                let bad = server.containers.filter { $0.status != .healthy }.count
                if bad > 0 { Text("\(bad) 異常").foregroundStyle(.orange) }
                Spacer()
                Text("運作 \(server.uptimeDays) 天")
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .padding(12)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .strokeBorder(selected ? Color.accentColor : (server.health == .healthy ? Color.primary.opacity(0.07) : server.health.color.opacity(0.45)),
                              lineWidth: selected ? 2 : 1)
        )
        .contentShape(RoundedRectangle(cornerRadius: 12))
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
    }
}
