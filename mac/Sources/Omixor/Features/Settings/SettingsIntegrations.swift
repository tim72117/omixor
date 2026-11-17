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
            MCPGatewaySection()

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

/// 內建 MCP 伺服器:狀態、位址、存取 token、加到 Claude Code 的指令
private struct MCPServerSection: View {
    @EnvironmentObject var app: AppState
    @State private var confirmRegenerate = false
    @State private var tokenNote: String?
    @State private var showToken = false

    /// 畫面上顯示的指令(token 遮起來);複製的是完整的
    private var maskedCommand: String { MCPLocalToken.claudeCommand(url: app.mcp.url, token: "omxl_••••••••") }

    private func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }

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
            LabeledContent("存取 token") {
                HStack {
                    Text(showToken ? (MCPLocalToken.current() ?? "(產生失敗)") : "omxl_••••••••")
                        .font(.system(.caption, design: .monospaced)).textSelection(.enabled).lineLimit(1)
                    Button(showToken ? "隱藏" : "顯示") { showToken.toggle() }
                    Button("重新產生…") { confirmRegenerate = true }
                }
            }
            LabeledContent("加到 Claude Code") {
                HStack {
                    Text(maskedCommand).font(.system(.caption, design: .monospaced)).lineLimit(2)
                    Button("複製 Claude Code 設定指令") {
                        guard let t = MCPLocalToken.current() else { tokenNote = "產生 token 失敗(系統亂數來源錯誤),請稍後再試"; return }
                        copy(MCPLocalToken.claudeCommand(url: app.mcp.url, token: t))
                        tokenNote = "已複製(含 token)。在終端機貼上執行即可"
                    }
                }
            }
            Text("""
            /mcp 一定要帶 Authorization: Bearer <token>,沒帶或不對會被拒絕(401);token 代表「這台 Mac 的使用者」,只存在這台的鑰匙圈。
            指令加的名稱是 omixor;已經加過的先在終端機執行 claude mcp remove omixor(改名前加的叫 ops,改執行 claude mcp remove ops;當初用 -s user 加的要加 -s user),再貼上複製的指令(要全域可用可在 add 後加 -s user)。
            只支援 stdio 的用戶端(omixor-mcp 轉接程式)在環境變數 OMIXOR_MCP_TOKEN 放這把 token(舊名稱 OPS_MCP_TOKEN 暫時仍可用)。重新產生後舊的立刻失效,用到的地方都要重新設定。
            token 會存在 Claude Code 的設定檔(~/.claude.json),不要把那個檔案分享出去。
            """)
                .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if let tokenNote { Text(tokenNote).font(.caption).foregroundStyle(.orange) }
            if app.mcp.requests > 0 {
                LabeledContent("最近使用") {
                    Text([app.mcp.lastClient, app.mcp.lastTool, app.mcp.lastRequestAt.map { $0.formatted(date: .omitted, time: .standard) }]
                        .compactMap { $0 }.joined(separator: " · ") + " · 共 \(app.mcp.requests) 次請求")
                        .foregroundStyle(.secondary)
                }
            }
        }
        .confirmationDialog("重新產生 MCP 存取 token?", isPresented: $confirmRegenerate) {
            Button("重新產生", role: .destructive) {
                if MCPLocalToken.regenerate() != nil {
                    tokenNote = "已重新產生:舊的 token 立刻失效。請重新複製 Claude Code 設定指令(先 claude mcp remove omixor 再加;改名前加的叫 ops)"
                } else {
                    tokenNote = "重新產生失敗(系統亂數來源錯誤),舊的 token 照舊可用"
                }
            }
        } message: { Text("舊的 token 會立刻失效,Claude Code 等用戶端要用新的指令重新設定。") }
    }
}
