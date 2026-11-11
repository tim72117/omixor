import SwiftUI

// 服務分頁:左邊清單、右邊詳細資料。

enum InfraServiceSort: String, CaseIterable, Identifiable {
    case attention, tier, name, traffic
    var id: String { rawValue }
    var label: String {
        switch self {
        case .attention: return "需要注意的優先"
        case .tier: return "關鍵等級"
        case .name: return "名稱"
        case .traffic: return "流量"
        }
    }
}

private func infraHealthRank(_ h: Health) -> Int {
    switch h {
    case .down: return 0
    case .degraded: return 1
    case .maintenance: return 2
    case .unknown: return 3
    case .healthy: return 4
    }
}

struct InfraServicesTab: View {
    @EnvironmentObject var app: AppState
    @Binding var selection: String?
    var onOpenServer: (String) -> Void

    @State private var search = ""
    @State private var healthFilter: Health?
    @State private var sort: InfraServiceSort = .attention

    private var filtered: [Service] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        let list = app.services.filter { s in
            (healthFilter == nil || s.health == healthFilter) &&
            (q.isEmpty || s.name.lowercased().contains(q) || s.team.lowercased().contains(q)
                || s.summary.lowercased().contains(q) || s.id.lowercased().contains(q))
        }
        switch sort {
        case .attention:
            return list.sorted {
                let a = infraHealthRank($0.health), b = infraHealthRank($1.health)
                if a != b { return a < b }
                if $0.errorBudgetRemaining != $1.errorBudgetRemaining { return $0.errorBudgetRemaining < $1.errorBudgetRemaining }
                return $0.name < $1.name
            }
        case .tier: return list.sorted { ($0.tier, $0.name) < ($1.tier, $1.name) }
        case .name: return list.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        case .traffic: return list.sorted { $0.rps > $1.rps }
        }
    }

    var body: some View {
        if app.services.isEmpty {
            EmptyStateView(title: "還沒有服務", icon: "square.stack.3d.up.slash", message: "接上服務目錄後,這裡會列出所有服務的健康狀態與 SLO。")
        } else {
            GeometryReader { g in
                HStack(spacing: 0) {
                    listPane
                        .frame(width: min(400, max(230, g.size.width * 0.32)))
                    Divider()
                    detailPane
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .onAppear(perform: ensureSelection)
            .onChange(of: app.services.count) { _, _ in ensureSelection() }
        }
    }

    private func ensureSelection() {
        if let s = selection, app.service(s) != nil { return }
        selection = filtered.first?.id ?? app.services.first?.id
    }

    // MARK: 清單

    private var listPane: some View {
        let items = filtered
        return VStack(spacing: 0) {
            HStack(spacing: 6) {
                HStack(spacing: 5) {
                    Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                    TextField("搜尋服務、團隊", text: $search).textFieldStyle(.plain)
                    if !search.isEmpty {
                        Button { search = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary) }
                            .buttonStyle(.plain)
                            .help("清除搜尋")
                    }
                }
                .padding(.horizontal, 7).padding(.vertical, 5)
                .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 7))

                Menu {
                    Picker("健康狀態", selection: $healthFilter) {
                        Text("全部狀態").tag(Health?.none)
                        Divider()
                        ForEach(Health.allCases) { h in
                            Label(h.label, systemImage: h.icon).tag(Health?.some(h))
                        }
                    }
                    Picker("排序", selection: $sort) {
                        ForEach(InfraServiceSort.allCases) { Text($0.label).tag($0) }
                    }
                } label: {
                    Image(systemName: healthFilter == nil ? "line.3.horizontal.decrease.circle" : "line.3.horizontal.decrease.circle.fill")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("篩選與排序")
            }
            .padding(10)

            Divider()

            if items.isEmpty {
                EmptyStateView(title: "沒有符合的服務", icon: "magnifyingglass", message: "換個關鍵字或清除篩選。")
                    .frame(maxHeight: .infinity)
            } else {
                List(selection: $selection) {
                    ForEach(items) { s in
                        InfraServiceRow(service: s).tag(s.id)
                    }
                }
                .listStyle(.inset)
            }

            Divider()
            HStack {
                Text("\(items.count) / \(app.services.count) 個服務")
                Spacer()
                if let h = healthFilter {
                    Button("清除篩選:\(h.label)") { healthFilter = nil }
                        .buttonStyle(.link)
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 10).padding(.vertical, 6)
        }
    }

    // MARK: 詳細

    @ViewBuilder private var detailPane: some View {
        if let id = selection, let svc = app.service(id) {
            InfraServiceDetail(service: svc,
                               onSelectService: { selection = $0 },
                               onOpenServer: onOpenServer)
                .id(svc.id)
        } else {
            EmptyStateView(title: "選擇一個服務", icon: "square.stack.3d.up", message: "從左邊清單選一個服務,查看 SLO、延遲、相依關係與最近部署。")
        }
    }
}

// MARK: - 清單列

struct InfraServiceRow: View {
    let service: Service

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: service.health.icon)
                .foregroundStyle(service.health.color)
                .font(.body)
                .help(service.health.label)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(service.name).font(.body.weight(.medium)).lineLimit(1)
                    InfraTierPill(tier: service.tier)
                }
                HStack(spacing: 8) {
                    Text("p95 \(Int(service.latencyP95))ms")
                    Text("錯誤 \(Fmt.percent(service.errorRate, digits: 2))")
                        .foregroundStyle(service.errorRate >= 1 ? AnyShapeStyle(.red) : AnyShapeStyle(.secondary))
                    Text("\(Fmt.number(service.rps)) rps")
                }
                .font(.caption.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
            }
            Spacer(minLength: 4)
            VStack(alignment: .trailing, spacing: 3) {
                Text("\(Int(service.errorBudgetRemaining * 100))%")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(infraBudgetColor(service.errorBudgetRemaining))
                InfraBudgetBar(remaining: service.errorBudgetRemaining, height: 4)
                    .frame(width: 40)
            }
            .help("錯誤預算剩餘 \(Int(service.errorBudgetRemaining * 100))%")
        }
        .padding(.vertical, 4)
    }
}
