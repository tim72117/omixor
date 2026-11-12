import AppKit
import SwiftUI

// 設定 › 整合:依類別列出外部服務,可連線/中斷連線。

struct SettingsIntegrationsPane: View {
    @EnvironmentObject var app: AppState
    @State private var query = ""
    @State private var onlyConnected = false
    @State private var connecting: Set<String> = []
    @State private var pendingDisconnect: Integration?

    private var filtered: [Integration] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        return app.integrations.filter { i in
            (!onlyConnected || i.connected) &&
            (q.isEmpty || i.name.lowercased().contains(q) || i.category.lowercased().contains(q) || i.detail.lowercased().contains(q))
        }
    }

    private var categories: [String] {
        Array(Set(filtered.map(\.category))).sorted()
    }

    var body: some View {
        let connectedCount = app.integrations.filter(\.connected).count
        Form {
            Section {
                HStack(spacing: 12) {
                    Image(systemName: "puzzlepiece.extension.fill")
                        .font(.title2)
                        .foregroundStyle(.tint)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("已連線 \(connectedCount) / \(app.integrations.count) 個整合").font(.headline)
                        Text("omixor 透過這些整合讀取原始碼、CI/CD、雲端資源與監控資料,並送出通知。")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                HStack {
                    TextField("搜尋整合", text: $query)
                        .textFieldStyle(.roundedBorder)
                    Toggle("只顯示已連線", isOn: $onlyConnected)
                        .toggleStyle(.checkbox)
                }
            }

            MCPServerSection()

            if app.integrations.isEmpty {
                Section {
                    EmptyStateView(title: "尚無可用的整合", icon: "puzzlepiece.extension",
                                   message: "整合清單載入後會顯示在這裡。")
                        .frame(minHeight: 180)
                }
            } else if filtered.isEmpty {
                Section {
                    Text("找不到符合「\(query)」的整合").foregroundStyle(.secondary)
                }
            } else {
                ForEach(categories, id: \.self) { cat in
                    Section(cat) {
                        ForEach(filtered.filter { $0.category == cat }) { integration in
                            row(integration)
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .confirmationDialog(
            "中斷與 \(pendingDisconnect?.name ?? "") 的連線?",
            isPresented: Binding(get: { pendingDisconnect != nil }, set: { if !$0 { pendingDisconnect = nil } }),
            presenting: pendingDisconnect
        ) { item in
            Button("中斷連線", role: .destructive) {
                app.setIntegration(id: item.id, connected: false)
                app.log(.person(app.me), "puzzlepiece.extension", "中斷整合「\(item.name)」", .settings)
            }
            Button("取消", role: .cancel) {}
        } message: { item in
            Text("omixor 將停止從 \(item.name) 讀取資料,相關的自動化與通知也會暫停。")
        }
    }

    private func row(_ i: Integration) -> some View {
        HStack(spacing: 12) {
            Image(systemName: i.icon)
                .font(.system(size: 15, weight: .medium))
                .foregroundStyle(i.connected ? Color.accentColor : Color.secondary)
                .frame(width: 30, height: 30)
                .background((i.connected ? Color.accentColor : Color.secondary).opacity(0.12),
                            in: RoundedRectangle(cornerRadius: 7))
            VStack(alignment: .leading, spacing: 2) {
                Text(i.name).font(.body.weight(.medium))
                Text(i.detail).font(.caption).foregroundStyle(.secondary).lineLimit(2)
            }
            Spacer(minLength: 12)
            if connecting.contains(i.id) {
                ProgressView().controlSize(.small)
                Text("連線中…").font(.caption).foregroundStyle(.secondary)
            } else if i.connected {
                Label("已連線", systemImage: "checkmark.circle.fill")
                    .font(.caption)
                    .foregroundStyle(.green)
                Button("中斷連線") { pendingDisconnect = i }
                    .controlSize(.small)
            } else {
                Button("連線") { connect(i) }
                    .controlSize(.small)
                    .buttonStyle(.borderedProminent)
            }
        }
        .padding(.vertical, 2)
    }

    /// 模擬 OAuth/驗證往返後完成連線
    private func connect(_ i: Integration) {
        connecting.insert(i.id)
        Task { @MainActor in
            try? await Task.sleep(nanoseconds: 900_000_000)
            connecting.remove(i.id)
            app.setIntegration(id: i.id, connected: true)
            app.log(.person(app.me), "puzzlepiece.extension.fill", "連線整合「\(i.name)」", .settings)
        }
    }
}

/// 內建 MCP 伺服器:狀態、位址、加到 Claude Code 的指令
private struct MCPServerSection: View {
    @EnvironmentObject var app: AppState

    private var command: String { "claude mcp add --transport http ops \(app.mcp.url)" }

    private var stateText: (String, Color) {
        switch app.mcp.state {
        case .stopped: return ("未啟動", .gray)
        case .starting: return ("啟動中", .orange)
        case .listening: return ("執行中", .green)
        case .failed(let msg): return ("無法啟動:\(msg)", .red)
        }
    }

    var body: some View {
        Section("MCP 伺服器") {
            HStack(spacing: 12) {
                Image(systemName: "point.3.connected.trianglepath.dotted")
                    .font(.title2)
                    .foregroundStyle(.tint)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Circle().fill(stateText.1).frame(width: 8, height: 8)
                        Text(stateText.0).font(.headline)
                    }
                    Text("MCP 用戶端可以讀取這裡的資料,並處理告警、事件、工作項目與送出待核准請求。只接受本機連線。")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            LabeledContent("位址") {
                Text(app.mcp.url).font(.system(.body, design: .monospaced)).textSelection(.enabled)
            }
            LabeledContent("加到 Claude Code") {
                HStack {
                    Text(command).font(.system(.caption, design: .monospaced)).textSelection(.enabled).lineLimit(1)
                    Button("複製") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(command, forType: .string)
                    }
                }
            }
            if app.mcp.requests > 0 {
                LabeledContent("最近使用") {
                    Text([app.mcp.lastClient, app.mcp.lastTool, app.mcp.lastRequestAt.map { $0.formatted(date: .omitted, time: .standard) }]
                        .compactMap { $0 }.joined(separator: " · ") + " · 共 \(app.mcp.requests) 次請求")
                        .foregroundStyle(.secondary)
                }
            }
        }
    }
}
