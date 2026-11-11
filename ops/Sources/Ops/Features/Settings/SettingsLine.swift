import AppKit
import SwiftUI

// 設定 › LINE:官方帳號(Messaging API)的 token 與 secret、接收者、通知開關、webhook、白名單、測試與紀錄。
// token 與 secret 存在鑰匙圈(依組織分開),其他設定存在組織資料夾的 line.json。

struct SettingsLinePane: View {
    @EnvironmentObject var app: AppState
    @State private var token = ""
    @State private var secret = ""
    @State private var newRecipient = ""
    @State private var newRecipientLabel = ""
    @State private var newUser = ""
    @State private var newUserName = ""
    @State private var newUserPerson = ""
    @State private var portText = ""
    @State private var testing = false
    @State private var testResult: (Bool, String)?

    private var store: LineStore { app.startLine() }
    private var s: LineSettings { app.lineSettings }

    private func bind<T>(_ key: WritableKeyPath<LineSettings, T>) -> Binding<T> {
        Binding(get: { app.lineSettings[keyPath: key] }, set: { v in store.update { $0[keyPath: key] = v } })
    }

    var body: some View {
        Form {
            accountSection
            recipientsSection
            notifySection
            webhookSection
            allowedSection
            if !app.line.unknownSenders.isEmpty { unknownSection }
            logSection
        }
        .formStyle(.grouped)
        .onAppear { _ = store; portText = String(app.lineSettings.port) }
        .onChange(of: app.currentOrgId) { _, _ in portText = String(app.lineSettings.port) }
    }

    // MARK: 官方帳號

    private var accountSection: some View {
        Section {
            LabeledContent("Channel access token") {
                HStack {
                    SecureField(app.line.hasToken ? "已存在鑰匙圈(輸入新的會取代)" : "長期 channel access token", text: $token)
                        .textFieldStyle(.roundedBorder)
                    secretBadge(app.line.hasToken)
                }
            }
            LabeledContent("Channel secret") {
                HStack {
                    SecureField(app.line.hasSecret ? "已存在鑰匙圈(輸入新的會取代)" : "Basic settings 裡的 Channel secret", text: $secret)
                        .textFieldStyle(.roundedBorder)
                    secretBadge(app.line.hasSecret)
                }
            }
            HStack {
                Button("儲存到鑰匙圈") {
                    store.saveSecrets(token: token.isEmpty ? nil : token, secret: secret.isEmpty ? nil : secret)
                    token = ""; secret = ""
                }
                .disabled(token.isEmpty && secret.isEmpty)
                Button("清除", role: .destructive) { store.clearSecrets() }
                    .disabled(!app.line.hasToken && !app.line.hasSecret)
                Spacer()
                Toggle("推播", isOn: bind(\.pushEnabled)).toggleStyle(.switch)
            }
        } header: {
            Text("LINE 官方帳號(Messaging API)")
        } footer: {
            Text("在 LINE Developers 建立 Messaging API channel:Basic settings 有 Channel secret,Messaging API 分頁最下方可發行長期 channel access token。兩者只存在這台 Mac 的鑰匙圈(依組織分開),不會寫進 line.json。LINE Notify 已停止服務,這裡用的是官方帳號。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func secretBadge(_ ok: Bool) -> some View {
        Label(ok ? "已設定" : "未設定", systemImage: ok ? "checkmark.circle.fill" : "circle.dashed")
            .labelStyle(.titleAndIcon).font(.caption)
            .foregroundStyle(ok ? .green : .secondary)
            .fixedSize()
    }

    // MARK: 接收者

    private var recipientsSection: some View {
        Section {
            ForEach(s.recipients) { r in
                HStack {
                    Image(systemName: r.id.hasPrefix("U") ? "person" : "person.3")
                    VStack(alignment: .leading) {
                        Text(r.label.isEmpty ? "(未命名)" : r.label)
                        Text(r.id).font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                    Spacer()
                    Button(role: .destructive) { store.update { $0.recipients.removeAll { $0.id == r.id } } } label: {
                        Image(systemName: "minus.circle")
                    }.buttonStyle(.borderless)
                }
            }
            HStack {
                TextField("userId(U…)或 groupId(C…)", text: $newRecipient).textFieldStyle(.roundedBorder)
                TextField("名稱", text: $newRecipientLabel).textFieldStyle(.roundedBorder).frame(width: 140)
                Button("加入") { addRecipient(newRecipient, newRecipientLabel) }
                    .disabled(!LineSettings.isValidId(newRecipient))
            }
            HStack {
                Button(testing ? "傳送中…" : "傳一則測試訊息") {
                    testing = true
                    testResult = nil
                    Task {
                        let err = await store.sendTest()
                        testResult = err.map { (false, $0) } ?? (true, "已送出,請到 LINE 查看")
                        testing = false
                    }
                }
                .disabled(testing || !app.line.hasToken || s.recipients.isEmpty)
                if let (ok, msg) = testResult {
                    Text(msg).font(.caption).foregroundStyle(ok ? .green : .red).textSelection(.enabled)
                }
            }
        } header: {
            Text("推播接收者")
        } footer: {
            Text("每則通知會用 push API 分別送給每個接收者(推播會算進官方帳號的訊息額度)。userId 不是 LINE ID:對方加官方帳號好友或在群組裡傳訊息後,會出現在下方「最近傳訊息的人」,可以直接加入。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func addRecipient(_ id: String, _ label: String) {
        let id = id.trimmingCharacters(in: .whitespaces)
        guard LineSettings.isValidId(id) else { return }
        store.update { st in
            if !st.recipients.contains(where: { $0.id == id }) { st.recipients.append(LineRecipient(id: id, label: label)) }
        }
        newRecipient = ""; newRecipientLabel = ""
    }

    // MARK: 通知

    private var notifySection: some View {
        Section("要推播的通知") {
            HStack {
                Toggle("告警觸發", isOn: bind(\.notifyAlerts))
                Spacer()
                Picker("門檻", selection: bind(\.alertMinSeverity)) {
                    Text("只有 Critical").tag(Severity.critical.rawValue)
                    Text("Warning 以上").tag(Severity.warning.rawValue)
                    Text("全部(含 Info)").tag(Severity.info.rawValue)
                }
                .fixedSize()
                .disabled(!s.notifyAlerts)
            }
            Toggle("宣告事件與事件狀態變更", isOn: bind(\.notifyIncidents))
            Toggle("新的待核准 AI 請求(附核准 / 拒絕按鈕)", isOn: bind(\.notifyApprovals))
            Toggle("旁白批次收完、待發佈", isOn: bind(\.notifyVoice))
            Toggle("虛擬角色任務需要協助(blocked)", isOn: bind(\.notifyAgentBlocked))
            Text("同一件事只推一次;app 啟動或切換組織時已經存在的狀況不會推。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    // MARK: Webhook

    private var serverText: (String, Color) {
        switch app.line.server {
        case .stopped: return (app.line.hasSecret ? (s.webhookEnabled ? "未啟動" : "已關閉") : "未啟動(要先填 channel secret)", .gray)
        case .starting: return ("啟動中", .orange)
        case .listening: return ("執行中", .green)
        case .failed(let m): return ("無法啟動:\(m)", .red)
        }
    }

    private var localURL: String { "http://127.0.0.1:\(s.port)\(LineWebhookServer.path)" }

    private var webhookSection: some View {
        Section {
            HStack {
                Circle().fill(serverText.1).frame(width: 8, height: 8)
                Text(serverText.0)
                Spacer()
                Toggle("接收 webhook", isOn: bind(\.webhookEnabled)).toggleStyle(.switch)
            }
            LabeledContent("本機位址") {
                Text(localURL).font(.system(.body, design: .monospaced)).textSelection(.enabled)
            }
            LabeledContent("連接埠") {
                HStack {
                    TextField("3766", text: $portText).textFieldStyle(.roundedBorder).frame(width: 80)
                    Button("套用") {
                        if let p = Int(portText), (1024..<65536).contains(p) { store.update { $0.port = p } }
                        else { portText = String(s.port) }
                    }
                    .disabled(portText == String(s.port))
                }
            }
            LabeledContent("公開網址") {
                TextField("https://ops-line.example.com(Tunnel / ngrok 給的網址)", text: bind(\.publicURL))
                    .textFieldStyle(.roundedBorder)
            }
            if let url = s.webhookURL {
                LabeledContent("Webhook URL") {
                    HStack {
                        Text(url).font(.system(.caption, design: .monospaced)).textSelection(.enabled).lineLimit(1)
                        Button("複製") { copy(url) }
                    }
                }
            }
            if app.line.webhookRequests > 0 {
                LabeledContent("收到") {
                    Text("\(app.line.webhookRequests) 次請求" + (app.line.rejectedSignatures > 0 ? ",\(app.line.rejectedSignatures) 次簽章不符被拒絕" : ""))
                        .foregroundStyle(.secondary)
                }
            }
            VStack(alignment: .leading, spacing: 6) {
                Text("設定步驟").font(.caption.weight(.semibold))
                Text("1. 把公開的 https 網址轉到這個埠(擇一):")
                commandRow("cloudflared tunnel --url http://127.0.0.1:\(s.port)")
                commandRow("ngrok http 127.0.0.1:\(s.port)")
                Text("2. 把工具給的網址貼到上面「公開網址」,複製 Webhook URL(結尾是 \(LineWebhookServer.path))。")
                Text("3. LINE Developers › 你的 channel › Messaging API › Webhook settings:貼上 Webhook URL、打開 Use webhook,按 Verify 應該成功。")
                Text("4. LINE Official Account Manager › 回應設定:關閉「自動回應訊息」,避免和 Ops 的回覆重複。")
                Text("每個請求都會用 channel secret 驗證 X-Line-Signature,不符就回 401 不處理。伺服器只聽 127.0.0.1;臨時網址(trycloudflare、ngrok 免費版)每次重開會變,要回 LINE Developers 更新。")
                    .foregroundStyle(.secondary)
            }
            .font(.caption)
        } header: {
            Text("Webhook(在 LINE 核准、在 LINE 問狀況)")
        }
    }

    private func commandRow(_ cmd: String) -> some View {
        HStack {
            Text(cmd).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
            Button("複製") { copy(cmd) }.controlSize(.small)
        }
        .padding(.leading, 12)
    }

    private func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }

    // MARK: 白名單

    private var allowedSection: some View {
        Section {
            ForEach(s.allowed) { u in
                HStack {
                    Image(systemName: "person.badge.shield.checkmark")
                    VStack(alignment: .leading) {
                        Text(u.name.isEmpty ? "(未命名)" : u.name)
                        Text(u.userId).font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                    Spacer()
                    Text("署名:\(app.person(u.person.isEmpty ? app.me : u.person)?.name ?? (u.person.isEmpty ? app.me : u.person))")
                        .font(.caption).foregroundStyle(.secondary)
                    Button(role: .destructive) { store.update { $0.allowed.removeAll { $0.userId == u.userId } } } label: {
                        Image(systemName: "minus.circle")
                    }.buttonStyle(.borderless)
                }
            }
            HStack {
                TextField("LINE userId(U…)", text: $newUser).textFieldStyle(.roundedBorder)
                TextField("名稱", text: $newUserName).textFieldStyle(.roundedBorder).frame(width: 110)
                Picker("署名", selection: $newUserPerson) {
                    Text("目前使用者").tag("")
                    ForEach(app.people) { Text($0.name).tag($0.id) }
                }
                .labelsHidden().fixedSize()
                Button("加入") { addAllowed(newUser, newUserName, newUserPerson) }
                    .disabled(!(LineSettings.isValidId(newUser) && newUser.hasPrefix("U")))
            }
        } header: {
            Text("允許在 LINE 核准與提問的人")
        } footer: {
            Text("只有這些 LINE userId 能按核准 / 拒絕、問狀況;其他人一律回「沒有權限」,不會看到任何資料。核准會在動態牆以「署名」的人記錄,並註明經 LINE。核准一樣是人做的決定,AI 不能透過 LINE 核准。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    private func addAllowed(_ id: String, _ name: String, _ person: String) {
        let id = id.trimmingCharacters(in: .whitespaces)
        guard LineSettings.isValidId(id), id.hasPrefix("U") else { return }
        store.update { st in
            st.allowed.removeAll { $0.userId == id }
            st.allowed.append(LineAllowedUser(userId: id, name: name, person: person))
        }
        app.line.unknownSenders.removeAll { $0.userId == id }
        newUser = ""; newUserName = ""; newUserPerson = ""
    }

    private var unknownSection: some View {
        Section {
            ForEach(app.line.unknownSenders) { u in
                HStack {
                    VStack(alignment: .leading) {
                        Text(u.userId).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                        Text("\(u.lastText) · \(Fmt.relative(u.date))").font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    if u.userId.hasPrefix("U") {
                        Button("加入白名單") { newUser = u.userId }
                    }
                    Button("加為接收者") { newRecipient = u.userId }
                }
            }
        } header: {
            Text("最近傳訊息的人(不在白名單)")
        } footer: {
            Text("按鈕會把 id 填進上面的欄位,補上名稱後按「加入」。")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    // MARK: 紀錄

    private var logSection: some View {
        Section {
            if let e = app.line.lastError, let at = app.line.lastErrorAt {
                Label("最近一次失敗(\(Fmt.relative(at))):\(e)", systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red).font(.caption).textSelection(.enabled)
            }
            LabeledContent("推播", value: "成功 \(app.line.pushed) · 失敗 \(app.line.failed)")
            if app.line.log.isEmpty {
                Text("還沒有紀錄").foregroundStyle(.secondary)
            } else {
                ForEach(app.line.log) { l in
                    HStack(alignment: .top) {
                        Image(systemName: l.ok ? "checkmark.circle" : "xmark.octagon").foregroundStyle(l.ok ? .green : .red)
                        Text(l.text).font(.caption).textSelection(.enabled)
                        Spacer()
                        Text(Fmt.time(l.date)).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        } header: {
            Text("紀錄")
        }
    }
}
