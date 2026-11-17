import SwiftUI

// 伺服器與服務:四個分頁(服務、伺服器、服務地圖是範例資料;GCP 是真實的 Cloud Run)。
// 選取狀態放在這裡,讓分頁之間可以互相跳轉(例如從服務的主機清單跳到伺服器分頁)。

enum InfraTab: String, CaseIterable, Identifiable {
    case services, servers, map, gcp
    var id: String { rawValue }
    var label: String {
        switch self {
        case .services: return "服務"
        case .servers: return "伺服器"
        case .map: return "服務地圖"
        case .gcp: return "GCP"
        }
    }
}

struct InfrastructureView: View {
    @EnvironmentObject var app: AppState
    @State private var tab: InfraTab = .services
    @State private var selectedService: String?
    @State private var selectedServer: String?

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 20)
                .padding(.top, 16)
                .padding(.bottom, 12)
            Divider()
            Group {
                switch app.shownTab(tab) {
                case .services:
                    InfraServicesTab(selection: $selectedService, onOpenServer: openServer)
                case .servers:
                    InfraServersTab(selection: $selectedServer, onOpenService: openService)
                case .map:
                    InfraMapTab(selection: $selectedService, onOpenService: openService)
                case .gcp:
                    InfraGCPTab()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear { applyFocus(app.focusId) }
        .onChange(of: app.focusId) { _, new in applyFocus(new) }
        // 系統設定關掉的分頁:切到第一個開著的並提示(例如從別頁跳過來)
        .onChange(of: tab) { _, t in if let r = app.redirectTab(t) { tab = r } }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text("伺服器與服務").font(.title2.weight(.semibold))
                summaryLine
            }
            Spacer(minLength: 12)
            Picker("分頁", selection: Binding(get: { app.shownTab(tab) }, set: { tab = $0 })) {
                ForEach(app.visibleTabs(InfraTab.self)) { Text($0.label).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(width: 340)
        }
    }

    private var summaryLine: some View {
        let unhealthyServices = app.services.filter { $0.health != .healthy }.count
        let unhealthyServers = app.servers.filter { $0.health != .healthy }.count
        return HStack(spacing: 10) {
            Label("\(app.services.count) 個服務", systemImage: "square.stack.3d.up")
            Label("\(app.servers.count) 台主機", systemImage: "server.rack")
            if unhealthyServices + unhealthyServers > 0 {
                Label("\(unhealthyServices) 個服務、\(unhealthyServers) 台主機需要注意", systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
            } else if !app.services.isEmpty {
                Label("全部正常", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
            }
        }
        .font(.callout)
        .foregroundStyle(.secondary)
        .lineLimit(1)
    }

    private func openServer(_ id: String) {
        selectedServer = id
        tab = .servers
    }

    private func openService(_ id: String) {
        selectedService = id
        tab = .services
    }

    /// 其他頁面用 app.go(.infrastructure, focus:) 跳過來時,選中對應的服務或主機。
    private func applyFocus(_ id: String?) {
        guard let id else { return }
        if app.service(id) != nil {
            selectedService = id
            tab = .services
            app.focusId = nil
        } else if app.server(id) != nil {
            selectedServer = id
            tab = .servers
            app.focusId = nil
        } else if id == "gcp" {
            tab = .gcp
            app.focusId = nil
        }
    }
}
