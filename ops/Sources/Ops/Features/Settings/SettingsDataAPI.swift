import AppKit
import SwiftUI

// 設定 › 資料 API:唯讀資料 API 的開關、埠、Tailscale 白名單、token、狀態與設定說明。
// 獨立一個分頁(不放在「一般」):有白名單、token、被拒紀錄與 tailscale 說明,內容多,而且是全部組織共用的設定。

struct SettingsDataAPIPane: View {
    @EnvironmentObject var app: AppState
    @State private var portText = ""
    @State private var newLogin = ""
    @State private var shownToken: String?
    @State private var confirmRegenerate = false

    private var store: DataAPIStore { app.startDataAPI() }
    private var s: DataAPISettings { app.dataAPISettings }
    private var local: String { "http://127.0.0.1:\(s.port)\(DataAPI.prefix)" }
    private let remote = "https://<這台機器>.<tailnet>.ts.net:8443\(DataAPI.prefix)"

    private var stateText: (String, Color) {
        switch app.dataAPI.server {
        case .stopped: return (s.enabled ? "未啟動" : "已關閉", .gray)
        case .starting: return ("啟動中", .orange)
        case .listening: return ("執行中", .green)
        case .failed(let m): return ("無法啟動:\(m)", .red)
        }
    }

    var body: some View {
        Form {
            Section {
                HStack {
                    Circle().fill(stateText.1).frame(width: 8, height: 8)
                    Text(stateText.0).font(.headline)
                    Spacer()
                    Toggle("啟用", isOn: Binding(get: { s.enabled }, set: { v in store.update { $0.enabled = v } }))
                        .toggleStyle(.switch)
                }
                LabeledContent("聽的位址") { mono(local) }
                LabeledContent("連接埠") {
                    HStack {
                        TextField("3768", text: $portText).textFieldStyle(.roundedBorder).frame(width: 80)
                        Button("套用") {
                            if let p = Int(portText), (1024..<65536).contains(p) { store.update { $0.port = p } } else { portText = String(s.port) }
                        }
                        .disabled(portText == String(s.port))
                    }
                }
                LabeledContent("請求") {
                    Text("共 \(app.dataAPI.requests) 次" + (app.dataAPI.unauthorized > 0 ? ",\(app.dataAPI.unauthorized) 次未通過驗證" : ""))
                        .foregroundStyle(.secondary)
                }
                if let at = app.dataAPI.lastRequestAt {
                    LabeledContent("最近一次") {
                        Text("\(at.formatted(date: .omitted, time: .standard)) · \(app.dataAPI.lastCaller ?? "?") · \(app.dataAPI.lastPath ?? "")")
                            .foregroundStyle(.secondary).textSelection(.enabled).lineLimit(1)
                    }
                }
            } header: {
                Text("唯讀資料 API")
            } footer: {
                Text("讓別台電腦(例如辦公室電腦,透過 Tailscale)讀 Ops 目前組織的資料。只對應到 MCP 裡唯讀的工具,不能改任何東西;只綁 127.0.0.1。設定存在 data-api.json,全部組織共用。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                ForEach(s.allowedLogins, id: \.self) { l in
                    HStack {
                        Image(systemName: "person.badge.shield.checkmark")
                        Text(l).textSelection(.enabled)
                        Spacer()
                        Button(role: .destructive) { store.update { $0.allowedLogins.removeAll { $0 == l } } } label: {
                            Image(systemName: "minus.circle")
                        }.buttonStyle(.borderless)
                    }
                }
                HStack {
                    TextField("Tailscale 登入,例如 tim@example.com 或 tim72117@github", text: $newLogin).textFieldStyle(.roundedBorder)
                    Button("加入") { store.allow(newLogin); newLogin = "" }.disabled(newLogin.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            } header: {
                Text("Tailscale 白名單")
            } footer: {
                Text("tailscale serve 轉來的請求帶 Tailscale-User-Login 標頭;在白名單裡就放行。不在白名單的會記在下面「最近被拒的連線」。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            if !app.dataAPI.rejected.isEmpty {
                Section("最近被拒的連線") {
                    ForEach(app.dataAPI.rejected) { r in
                        HStack {
                            VStack(alignment: .leading) {
                                Text(r.login).textSelection(.enabled)
                                Text("\(r.count) 次 · \(Fmt.relative(r.lastAt)) · \(r.lastPath)").font(.caption).foregroundStyle(.secondary).lineLimit(1)
                            }
                            Spacer()
                            if !r.login.hasPrefix("(") { Button("加入白名單") { store.allow(r.login) } }
                        }
                    }
                }
            }

            Section {
                HStack {
                    Label(app.dataAPI.hasToken ? "已設定" : "未設定", systemImage: app.dataAPI.hasToken ? "checkmark.circle.fill" : "circle.dashed")
                        .foregroundStyle(app.dataAPI.hasToken ? .green : .secondary)
                    Spacer()
                    Button(app.dataAPI.hasToken ? "重新產生…" : "產生 token") {
                        if app.dataAPI.hasToken { confirmRegenerate = true } else { shownToken = store.regenerateToken() }
                    }
                    if app.dataAPI.hasToken { Button("撤銷", role: .destructive) { store.revokeToken(); shownToken = nil } }
                }
                if let t = shownToken {
                    HStack {
                        mono(t)
                        Button("複製") { copy(t) }
                    }
                    Text("只會顯示這一次,請現在複製。之後只能重新產生。").font(.caption).foregroundStyle(.orange)
                }
            } header: {
                Text("Bearer token(沒有 Tailscale 身分時用)")
            } footer: {
                Text("請求帶 Authorization: Bearer <token> 也能通過。token 存在鑰匙圈,重新產生後舊的立即失效。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("設定步驟") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("1. 打開上面的「啟用」,把辦公室要用的 Tailscale 登入加進白名單(或產生 token)。")
                    Text("2. 在這台 Mac 的終端機執行(只限 tailnet;不要用 funnel,funnel 會公開到網路上):")
                    command("tailscale serve --bg --https=8443 http://127.0.0.1:\(s.port)")
                    Text("443 已經被 Funnel 拿來公開別的服務,所以這裡用 8443。查看:tailscale serve status;關掉:tailscale serve --https=8443 off。")
                        .foregroundStyle(.secondary)
                    Text("3. 辦公室電腦(要登入同一個 tailnet):")
                    command("curl \(remote)/overview")
                    command("curl -H \"Authorization: Bearer <token>\" \"\(remote)/issues?status=all&limit=5\"")
                    command("Invoke-RestMethod \(remote)/overview")
                    command("Invoke-RestMethod -Uri \"\(remote)/list/alerts?limit=5\" -Headers @{ Authorization = \"Bearer <token>\" }")
                    Text("GET \(DataAPI.prefix) 會列出所有端點與唯讀工具;GET \(DataAPI.prefix)/tools/<工具>?參數=值 或 POST JSON 參數呼叫任一唯讀工具。")
                        .foregroundStyle(.secondary)
                }
                .font(.caption)
            }
        }
        .formStyle(.grouped)
        .onAppear { _ = store; portText = String(app.dataAPISettings.port) }
        .confirmationDialog("重新產生 token?", isPresented: $confirmRegenerate) {
            Button("重新產生", role: .destructive) { shownToken = store.regenerateToken() }
            Button("取消", role: .cancel) {}
        } message: { Text("舊的 token 會立即失效,用舊 token 的電腦要換成新的。") }
    }

    private func mono(_ s: String) -> some View {
        Text(s).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
    }

    private func command(_ cmd: String) -> some View {
        HStack {
            mono(cmd)
            Button("複製") { copy(cmd) }.controlSize(.small)
        }
        .padding(.leading, 12)
    }

    private func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }
}
