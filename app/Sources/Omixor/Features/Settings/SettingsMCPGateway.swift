import SwiftUI

// 設定 › 整合 › 外部 MCP 閘道(功能開關 mcpGateway 沒開時不顯示)。
// 登記(伺服器、工具規則)在 core:組織管理員可以新增、修改、刪除;密鑰的值在這台輸入,存在鑰匙圈(不進 core)。

struct MCPGatewaySection: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        if app.isFeatureEnabled("mcpGateway") {
            MCPGatewaySectionBody(gateway: app.mcpGateway)
        }
    }
}

private struct MCPGatewaySectionBody: View {
    @ObservedObject var gateway: MCPGateway
    @State private var editing: CoreMCPServer?
    @State private var error: String?

    var body: some View {
        Section("外部 MCP 閘道") {
            HStack(spacing: 12) {
                Image(systemName: "arrow.triangle.branch").font(.title2).foregroundStyle(.tint)
                VStack(alignment: .leading, spacing: 2) {
                    Text("App 依組織登記連上外部 MCP 伺服器,把工具以「伺服器__工具」掛到上面的 MCP 端點").font(.headline)
                    Text("登記與角色權限存在 omixor core;密鑰只存在這台的鑰匙圈。需要確認的工具呼叫時會在 App 跳出確認。工具輸出來自外部,視為不可信。")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
            }
            if let e = error ?? gateway.loadError { Text(e).font(.caption).foregroundStyle(.orange) }
            if let e = gateway.trustError { Text(e).font(.caption).foregroundStyle(.red).fixedSize(horizontal: false, vertical: true) }
            if let v = gateway.view {
                if !v.enabled, let n = v.note { Text(n).font(.caption).foregroundStyle(.secondary) }
                if v.servers.isEmpty, v.enabled {
                    Text("還沒有登記任何外部 MCP 伺服器。").foregroundStyle(.secondary)
                }
                ForEach(v.servers) { s in
                    MCPServerRow(gateway: gateway, server: s, canEdit: v.canEdit, onEdit: { editing = s }, onError: { error = $0 })
                }
                HStack {
                    Button("重新讀取") { Task { await gateway.reload() } }
                    Button("全部連線") { Task { await gateway.connectAll() } }.disabled(!v.enabled)
                    Spacer()
                    if v.canEdit, v.enabled {
                        Button("新增伺服器…") { editing = CoreMCPServer(id: "") }
                    }
                }
            } else {
                ProgressView().controlSize(.small)
            }
            if !gateway.calls.isEmpty {
                DisclosureGroup("最近的閘道呼叫(\(gateway.calls.count))") {
                    ForEach(gateway.calls) { c in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Image(systemName: c.ok ? "checkmark.circle.fill" : "xmark.octagon.fill").foregroundStyle(c.ok ? .green : .red)
                            Text(c.at.formatted(date: .omitted, time: .standard)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            Text(c.who).font(.caption)
                            Text(c.tool).font(.caption.monospaced())
                            Text(c.detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                }
            }
        }
        .task { await gateway.reload() }
        .sheet(item: $editing) { s in
            MCPServerEditor(gateway: gateway, original: s.id.isEmpty ? nil : s, onDone: { editing = nil })
        }
    }
}

private struct MCPServerRow: View {
    @ObservedObject var gateway: MCPGateway
    let server: CoreMCPServer
    let canEdit: Bool
    let onEdit: () -> Void
    let onError: (String?) -> Void
    @State private var secretInput: [String: String] = [:]
    @State private var confirmDelete = false

    private var st: MCPConnState { gateway.state(server.id) }

    private var phaseText: (String, Color) {
        if !server.enabled { return ("已停用", .gray) }
        switch st.phase {
        case .idle: return ("未連線", .gray)
        case .connecting: return ("連線中", .orange)
        case .connected: return ("已連線 · \(st.tools.count) 個工具", .green)
        case .failed(let m): return ("連不上:\(m)", .red)
        }
    }

    var body: some View {
        DisclosureGroup {
            VStack(alignment: .leading, spacing: 8) {
                if !server.description.isEmpty { Text(server.description).font(.caption).foregroundStyle(.secondary) }
                MCPServerConfigDetail(server: server)
                if !server.secretNames.isEmpty {
                    Text(server.transport == "http" ? "這台裝置的標頭值(存在鑰匙圈)" : "這台裝置的環境變數值(存在鑰匙圈;參數的 ${名稱} 也用它)")
                        .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    ForEach(server.secretNames, id: \.name) { n in
                        let has = gateway.secret(server.id, n.kind, n.name) != nil
                        HStack {
                            Text(n.name).font(.caption.monospaced()).frame(minWidth: 120, alignment: .leading)
                            SecureField(has ? "已存(輸入新值取代)" : "輸入值", text: Binding(get: { secretInput[n.name] ?? "" }, set: { secretInput[n.name] = $0 }))
                                .textFieldStyle(.roundedBorder)
                            Button("存") {
                                gateway.setSecret(secretInput[n.name], server: server.id, kind: n.kind, name: n.name)
                                secretInput[n.name] = ""
                            }.disabled((secretInput[n.name] ?? "").isEmpty)
                            if has { Button("清除") { gateway.setSecret(nil, server: server.id, kind: n.kind, name: n.name) } }
                        }
                    }
                }
                if !gateway.isTrusted(server) {
                    Text(server.transport == "stdio"
                         ? "這台還沒信任這個指令:連線時 App 會在這台執行上面的指令。確認是你認得的程式再按「信任並連線」;登記被改過要重新信任。"
                         : "這台還沒信任這個網址:連線時會把這台存的標頭值送到上面的網址。確認網址正確再按「信任並連線」;登記被改過要重新信任。")
                        .font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                }
                HStack {
                    if case .connected = st.phase {
                        Button("中斷連線") { gateway.disconnect(server.id) }
                    } else if !gateway.isTrusted(server) {
                        // 信任的是畫面上顯示的這份(server 是這次畫面的值);登記在背景變了就拒絕,要重新檢視
                        Button("信任並連線") {
                            if gateway.trust(server.id, shown: MCPGateway.fingerprint(server)) { Task { await gateway.connect(server.id) } }
                        }.disabled(!server.enabled)
                    } else {
                        Button("連線") { Task { await gateway.connect(server.id) } }.disabled(!server.enabled)
                    }
                    if gateway.isTrusted(server) { Button("取消信任") { gateway.untrust(server.id) } }
                    if canEdit {
                        Button("同步工具清單到 core") {
                            Task { do { try await gateway.syncTools(server.id); onError(nil) } catch { onError(error.localizedDescription) } }
                        }.disabled(st.tools.isEmpty)
                        Button("修改…", action: onEdit)
                        Button("刪除…", role: .destructive) { confirmDelete = true }
                    }
                }
                toolList
            }
            .padding(.vertical, 4)
        } label: {
            HStack(spacing: 8) {
                Circle().fill(phaseText.1).frame(width: 8, height: 8)
                Text(server.name).font(.body.weight(.medium))
                Text(server.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                Text(server.transport).font(.caption2).padding(.horizontal, 5).background(.quaternary, in: Capsule())
                Text(phaseText.0).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .confirmationDialog("刪除「\(server.name)」的登記?", isPresented: $confirmDelete) {
            Button("刪除", role: .destructive) {
                Task { do { try await gateway.deleteServer(server.id); onError(nil) } catch { onError(error.localizedDescription) } }
            }
        } message: { Text("組織裡所有裝置都不會再連這台,角色權限一併拿掉;這台存的密鑰也會刪掉。") }
    }

    /// 工具與規則:core 的規則 + 這台連上讀到的(還沒同步的標示出來)
    @ViewBuilder private var toolList: some View {
        let live = Set(st.tools.map(\.name))
        let ruled = Set(server.tools.map(\.name))
        let names = server.tools.map(\.name) + st.tools.map(\.name).filter { !ruled.contains($0) }
        if !names.isEmpty {
            Text("工具(沒同步規則的照「新工具預設\(server.toolDefault ? "開" : "不開")」)").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
            ForEach(names, id: \.self) { n in
                let rule = server.tools.first { $0.name == n }
                HStack(spacing: 8) {
                    Text(n).font(.caption.monospaced())
                    if rule == nil { Text("未同步").font(.caption2).foregroundStyle(.orange) }
                    if !live.isEmpty, !live.contains(n) { Text("這台沒讀到").font(.caption2).foregroundStyle(.secondary) }
                    Spacer()
                    if canEdit {
                        Toggle("開", isOn: Binding(get: { rule?.enabled ?? server.toolDefault }, set: { v in
                            Task { do { try await gateway.setRule(server.id, tool: n, enabled: v); onError(nil) } catch { onError(error.localizedDescription) } }
                        })).toggleStyle(.checkbox).font(.caption)
                        Toggle("要確認", isOn: Binding(get: { rule?.confirm ?? false }, set: { v in
                            Task { do { try await gateway.setRule(server.id, tool: n, confirm: v); onError(nil) } catch { onError(error.localizedDescription) } }
                        })).toggleStyle(.checkbox).font(.caption)
                    } else {
                        Text((rule?.enabled ?? server.toolDefault) ? "開" : "不開").font(.caption).foregroundStyle(.secondary)
                        if rule?.confirm == true { Text("要確認").font(.caption).foregroundStyle(.orange) }
                    }
                }
            }
        }
    }
}

/// 新增或修改登記(組織管理員)。只登記密鑰的「名稱」;值在每台裝置輸入
private struct MCPServerEditor: View {
    @ObservedObject var gateway: MCPGateway
    let original: CoreMCPServer?
    let onDone: () -> Void
    @State private var id = ""
    @State private var name = ""
    @State private var desc = ""
    @State private var transport = "stdio"
    @State private var command = ""
    @State private var args = ""
    @State private var cwd = ""
    @State private var envNames = ""
    @State private var url = ""
    @State private var headerNames = ""
    @State private var enabled = true
    @State private var toolDefault = false
    @State private var error: String?
    @State private var saving = false

    private func list(_ s: String) -> [String] {
        s.split(whereSeparator: { $0 == "," || $0 == "\n" || $0 == " " }).map { String($0) }.filter { !$0.isEmpty }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(original == nil ? "新增外部 MCP 伺服器" : "修改「\(original?.name ?? "")」").font(.headline)
            Form {
                TextField("id(小寫英數與 -)", text: $id).disabled(original != nil)
                TextField("名稱", text: $name)
                TextField("說明", text: $desc)
                Picker("傳輸方式", selection: $transport) {
                    Text("stdio(App 啟動子程序)").tag("stdio")
                    Text("HTTP").tag("http")
                }
                if transport == "stdio" {
                    TextField("指令", text: $command)
                    TextField("參數(一行一個;密鑰寫 ${名稱})", text: $args, axis: .vertical).lineLimit(2...5)
                    TextField("工作目錄(可空白)", text: $cwd)
                    TextField("需要的環境變數名稱(逗號分隔)", text: $envNames)
                } else {
                    TextField("網址", text: $url)
                    TextField("需要的標頭名稱(逗號分隔,例如 Authorization)", text: $headerNames)
                }
                Toggle("啟用", isOn: $enabled)
                Toggle("新工具預設開(不建議;預設不開)", isOn: $toolDefault)
                Text("不要把密鑰寫進指令、參數或網址:這裡只登記需要的名稱,值在每台裝置輸入,存在鑰匙圈。參數需要密鑰時寫 ${名稱}(名稱要是上面登記的環境變數),連線時才用這台的值代入。注意:放在參數的密鑰在本機程序列表(ps)看得到,能用環境變數就用環境變數。")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let error { Text(error).font(.caption).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("取消", action: onDone)
                Button("儲存") { save() }.keyboardShortcut(.defaultAction).disabled(saving || id.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 520)
        .onAppear {
            guard let s = original else { return }
            id = s.id; name = s.name; desc = s.description; transport = s.transport; command = s.command ?? ""
            args = s.args.joined(separator: "\n"); cwd = s.cwd ?? ""; envNames = s.envNames.joined(separator: ", ")
            url = s.url ?? ""; headerNames = s.headerNames.joined(separator: ", "); enabled = s.enabled; toolDefault = s.toolDefault
        }
    }

    private func save() {
        saving = true
        var f: [String: Any] = ["name": name, "description": desc, "transport": transport, "enabled": enabled, "toolDefault": toolDefault]
        if transport == "stdio" {
            f["command"] = command
            f["args"] = args.split(separator: "\n").map(String.init).filter { !$0.isEmpty }
            f["cwd"] = cwd
            f["envNames"] = list(envNames)
        } else {
            f["url"] = url
            f["headerNames"] = list(headerNames)
        }
        let sid = id.trimmingCharacters(in: .whitespaces).lowercased()
        Task {
            do {
                try await gateway.saveServer(sid, fields: f)
                onDone()
            } catch {
                self.error = error.localizedDescription
            }
            saving = false
        }
    }
}

// MARK: - 連線設定的完整內容(信任前要看得到所有會影響執行的欄位)

/// 傳輸、指令、每個參數(逐一列出、標出邊界)、工作目錄、網址、需要的密鑰名稱
struct MCPServerConfigDetail: View {
    let server: CoreMCPServer

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            row("傳輸方式", server.transport == "http" ? "HTTP(Streamable HTTP)" : "stdio(App 在這台啟動子程序)")
            if server.transport == "http" {
                row("網址", server.url ?? "(沒有)", mono: true)
                row("標頭(值存在這台)", server.headerNames.isEmpty ? "(沒有)" : server.headerNames.joined(separator: "、"), mono: true)
            } else {
                row("指令", server.command ?? "(沒有)", mono: true)
                LabeledContent("參數(\(server.args.count) 個)") {
                    if server.args.isEmpty {
                        Text("(沒有)").font(.caption).foregroundStyle(.secondary)
                    } else {
                        VStack(alignment: .leading, spacing: 2) {
                            ForEach(Array(server.args.enumerated()), id: \.offset) { i, a in
                                HStack(alignment: .firstTextBaseline, spacing: 4) {
                                    Text("[\(i + 1)]").font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
                                    // 用 ⟨ ⟩ 標出每個參數的邊界(空白、空字串都看得出來)
                                    Text("⟨\(a)⟩").font(.caption.monospaced()).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                                }
                            }
                        }
                    }
                }
                row("工作目錄", (server.cwd ?? "").isEmpty ? "(App 預設)" : server.cwd!, mono: true)
                row("環境變數(值存在這台)", server.envNames.isEmpty ? "(沒有)" : server.envNames.joined(separator: "、"), mono: true)
                let ph = MCPGateway.placeholders(server.args)
                if !ph.isEmpty {
                    let bad = badPlaceholders(ph)
                    Text(placeholderNote(ph, bad))
                        .font(.caption).foregroundStyle(bad.isEmpty ? Color.secondary : Color.red).fixedSize(horizontal: false, vertical: true)
                    Text("放在參數的密鑰在本機程序列表(ps)看得到,能用環境變數就用環境變數。")
                        .font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    private func badPlaceholders(_ ph: [String]) -> [String] {
        let declared = Set(server.envNames)
        return ph.filter { !declared.contains($0) }
    }

    private func placeholderNote(_ ph: [String], _ bad: [String]) -> String {
        let all: String = ph.map { "${" + $0 + "}" }.joined(separator: "、")
        var out = "連線時參數裡的 " + all + " 會換成這台鑰匙圈裡同名環境變數的值"
        if !bad.isEmpty {
            let b: String = bad.map { "${" + $0 + "}" }.joined(separator: "、")
            out += ";" + b + " 沒有登記在環境變數名稱,不會連線"
        }
        return out
    }

    private func row(_ label: String, _ value: String, mono: Bool = false) -> some View {
        LabeledContent(label) {
            Text(value).font(mono ? .caption.monospaced() : .caption).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
        }
    }
}

// MARK: - 確認卡片(需要人確認的外部工具呼叫)

/// 疊在主視窗右下角:顯示誰、工具、完整參數(高風險欄位在前並醒目),人按允許才執行;逾時當作拒絕。
/// 沒有預設按鈕(Return 不會等於允許);Esc 是拒絕
struct MCPConfirmOverlay: View {
    @ObservedObject var gateway: MCPGateway

    var body: some View {
        VStack(alignment: .trailing, spacing: 8) {
            ForEach(gateway.pending) { r in
                VStack(alignment: .leading, spacing: 6) {
                    Label("外部工具要求執行", systemImage: "hand.raised.fill").font(.headline).foregroundStyle(.orange)
                    Text("\(r.who) 要呼叫「\(r.serverName)」的 \(r.tool)").font(.callout)
                    if let t = r.taskId { Text("任務 \(t)").font(.caption).foregroundStyle(.secondary) }
                    Text("參數(\(r.fields.count) 個,完整內容;捲動看全部)").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    ScrollView {
                        VStack(alignment: .leading, spacing: 6) {
                            if r.fields.isEmpty { Text("(沒有參數)").font(.caption).foregroundStyle(.secondary) }
                            ForEach(r.fields) { f in
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 4) {
                                        if f.risky { Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red) }
                                        Text(f.key).font(.caption.monospaced().weight(f.risky ? .bold : .regular))
                                            .foregroundStyle(f.risky ? Color.red : Color.secondary)
                                    }
                                    Text(f.value).font(.caption.monospaced()).textSelection(.enabled)
                                        .fixedSize(horizontal: false, vertical: true)
                                        .padding(4).frame(maxWidth: .infinity, alignment: .leading)
                                        .background(f.risky ? Color.red.opacity(0.08) : Color.clear, in: RoundedRectangle(cornerRadius: 4))
                                }
                            }
                        }
                        .padding(6).frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(maxHeight: 320)
                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 6))
                    HStack {
                        Text("\(Int(MCPGateway.confirmTimeout)) 秒內沒回應當作拒絕").font(.caption2).foregroundStyle(.secondary)
                        Spacer()
                        Button("拒絕", role: .cancel) { gateway.answer(r.id, allow: false) }.keyboardShortcut(.cancelAction)
                        Button("允許") { gateway.answer(r.id, allow: true) }
                    }
                }
                .padding(12)
                .frame(width: 440)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
                .shadow(radius: 6)
            }
        }
        .padding(16)
    }
}
