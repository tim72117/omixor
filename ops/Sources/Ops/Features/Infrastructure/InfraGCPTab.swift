import AppKit
import Charts
import SwiftUI

/// 伺服器與服務 › GCP:真實的 Cloud Run 服務、Job、錯誤日誌與指標。只讀。
struct InfraGCPTab: View {
    @EnvironmentObject var app: AppState
    @State private var selection: String?   // "svc:<名稱>" 或 "job:<名稱>"
    @State private var editing = false

    /// 不在畫面計算時啟動(會在更新畫面途中改 @Published);app 啟動時已經建好
    private var store: GCPStore { app.gcpStore ?? GCPStore(app: app) }
    private var state: ConnectionState? { app.connectionStates[app.gcpSettings.connection] }

    var body: some View {
        Group {
            if store.connection == nil || !store.isLoggedIn || !store.isConfigured {
                ScrollView { GCPSetupView().padding(24).frame(maxWidth: 760) }.frame(maxWidth: .infinity)
            } else {
                main
            }
        }
        .onAppear { if store.isLoggedIn, store.isConfigured, app.gcp.fetchedAt == nil { Task { await store.refresh() } } }
        .onChange(of: state?.account) { _, acc in
            if acc != nil, store.isLoggedIn, store.isConfigured, app.gcp.fetchedAt == nil { Task { await store.refresh() } }
        }
        .onChange(of: app.gcpSettings) { selection = nil }
        .onChange(of: app.currentOrgId) { selection = nil }
        .sheet(isPresented: $editing) { GCPSettingsSheet() }
    }

    private var main: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "person.crop.circle.badge.checkmark").foregroundStyle(.green)
                Text(state?.account ?? "已登入").font(.callout)
                Divider().frame(height: 16)
                Label(app.gcpSettings.project, systemImage: "cloud").font(.callout)
                Text(app.gcpSettings.region).font(.caption.monospaced()).foregroundStyle(.secondary)
                Button("設定…") { editing = true }.buttonStyle(.borderless)
                if let u = GCP.consoleURL(project: app.gcpSettings.project, region: app.gcpSettings.region) {
                    Button("Console") { NSWorkspace.shared.open(u) }.buttonStyle(.borderless)
                }
                Spacer()
                if let t = app.gcp.fetchedAt { Text("更新於 " + Fmt.relative(t)).font(.caption).foregroundStyle(.secondary) }
                if app.gcp.busy { ProgressView().controlSize(.small) }
                Button { Task { await store.refresh() } } label: { Image(systemName: "arrow.clockwise") }
                    .help("重新整理").disabled(app.gcp.busy)
            }
            .padding(.horizontal, 16).padding(.vertical, 8)
            if let e = app.gcp.error {
                Label(e, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16).padding(.vertical, 6).background(.orange.opacity(0.08))
            }
            Divider()
            HStack(spacing: 0) {
                List(selection: $selection) {
                    Section("Cloud Run 服務") {
                        ForEach(app.gcp.services) { s in serviceRow(s).tag("svc:" + s.id) }
                        if app.gcp.services.isEmpty, app.gcp.fetchedAt != nil { Text("沒有服務").foregroundStyle(.secondary) }
                    }
                    Section("Cloud Run Job") {
                        ForEach(app.gcp.jobs) { j in jobRow(j).tag("job:" + j.id) }
                        if app.gcp.jobs.isEmpty, app.gcp.fetchedAt != nil { Text("沒有 Job").foregroundStyle(.secondary) }
                    }
                }
                .listStyle(.sidebar)
                .frame(width: 300)
                Divider()
                Group {
                    if let sel = selection, sel.hasPrefix("svc:"), let s = app.gcp.services.first(where: { "svc:" + $0.id == sel }) {
                        GCPServiceDetail(service: s).id(s.id)
                    } else if let sel = selection, sel.hasPrefix("job:"), let j = app.gcp.jobs.first(where: { "job:" + $0.id == sel }) {
                        GCPJobDetail(job: j).id(j.id)
                    } else {
                        EmptyStateView(title: app.gcp.fetchedAt == nil ? "讀取中…" : "選一個服務或 Job", icon: "cloud",
                                       message: "狀態每 5 分鐘自動更新;錯誤日誌與指標在打開時讀。")
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .onChange(of: app.gcp.fetchedAt) {
            if selection == nil, let first = app.gcp.services.first { selection = "svc:" + first.id }
        }
        .onAppear { if selection == nil, let first = app.gcp.services.first { selection = "svc:" + first.id } }
    }

    private func serviceRow(_ s: GCPService) -> some View {
        HStack(spacing: 8) {
            Circle().fill(GCPStyle.serviceColor(s)).frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 2) {
                Text(s.id).lineLimit(1)
                Text(s.latestReady.isEmpty ? s.stateLabel : s.latestReady).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1)
            }
        }
    }

    private func jobRow(_ j: GCPJob) -> some View {
        HStack(spacing: 8) {
            Circle().fill(GCPStyle.executionColor(j.latest)).frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 2) {
                Text(j.id).lineLimit(1)
                Text(j.latest.map { "\($0.statusLabel) · " + ($0.createdAt.map(Fmt.relative) ?? "") } ?? "還沒執行過")
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
    }
}

enum GCPStyle {
    static func serviceColor(_ s: GCPService) -> Color {
        s.stateLabel == "正常" ? .green : s.stateLabel == "部署中" ? .orange : .red
    }
    static func executionColor(_ e: GCPExecution?) -> Color {
        switch e?.status {
        case "succeeded": return .green
        case "failed": return .red
        case "running", "pending": return .orange
        case nil: return .gray.opacity(0.4)
        default: return .secondary
        }
    }
    static func severityColor(_ s: String) -> Color {
        switch s {
        case "CRITICAL", "ALERT", "EMERGENCY", "ERROR": return .red
        case "WARNING": return .orange
        default: return .secondary
        }
    }
    static func seriesColor(_ s: String) -> Color {
        switch s { case "2xx": return .green; case "3xx": return .blue; case "4xx": return .orange; case "5xx": return .red; default: return .gray }
    }
}

// MARK: - 服務詳情

private struct GCPServiceDetail: View {
    @EnvironmentObject var app: AppState
    let service: GCPService
    @State private var hours = 24
    @State private var metrics: GCPMetrics?
    @State private var metricsError: String?
    @State private var revisions: [GCPRevision]?
    @State private var severity = "ERROR"
    @State private var logs: [GCPLogEntry]?
    @State private var logsError: String?

    private var store: GCPStore { app.startGCP() }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .firstTextBaseline) {
                    Text(service.id).font(.title2.weight(.semibold))
                    Pill(text: service.stateLabel, color: GCPStyle.serviceColor(service))
                    Spacer()
                    Picker("", selection: $hours) {
                        Text("6 小時").tag(6); Text("24 小時").tag(24); Text("7 天").tag(168)
                    }
                    .pickerStyle(.segmented).labelsHidden().frame(width: 200)
                    if let u = GCP.consoleURL(project: app.gcpSettings.project, region: app.gcpSettings.region, service: service.id) {
                        Button("在 Console 打開") { NSWorkspace.shared.open(u) }
                    }
                }
                if !service.message.isEmpty, !service.ready { Label(service.message, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
                info
                metricsCard
                logsCard
                revisionsCard
            }
            .padding(20)
            .frame(maxWidth: 980, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: hours) { await loadMetrics() }
        .task(id: "\(severity)-\(hours)") { await loadLogs() }
        .task { revisions = try? await store.revisions(service: service.id) }
    }

    private var info: some View {
        Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 6) {
            GridRow {
                Text("網址").foregroundStyle(.secondary)
                if let u = URL(string: service.url) { Link(service.url, destination: u).font(.callout.monospaced()) } else { Text("—") }
            }
            GridRow {
                Text("目前版本").foregroundStyle(.secondary)
                Text(service.latestReady + (service.rolloutPending ? "(最新建立:\(service.latestCreated),還沒就緒)" : ""))
                    .font(.callout.monospaced())
            }
            GridRow {
                Text("流量").foregroundStyle(.secondary)
                Text(service.traffic.map { "\($0.revision) \($0.percent)%" + ($0.tag.isEmpty ? "" : "(\($0.tag))") }.joined(separator: "、"))
                    .font(.callout.monospaced())
            }
            GridRow {
                Text("映像").foregroundStyle(.secondary)
                Text(service.image).font(.caption.monospaced()).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
            }
            GridRow {
                Text("更新").foregroundStyle(.secondary)
                Text((service.updatedAt.map(Fmt.relative) ?? "—") + (service.lastModifier.isEmpty ? "" : " · \(service.lastModifier)")
                     + (service.maxInstances > 0 ? " · 最多 \(service.maxInstances) 個執行個體" : ""))
                    .font(.callout)
            }
        }
    }

    private var metricsCard: some View {
        Card("請求與延遲", subtitle: "Cloud Monitoring,最近 \(hours >= 168 ? "7 天" : "\(hours) 小時")") {
            if let metricsError { Text(metricsError).font(.caption).foregroundStyle(.orange) }
            if let m = metrics {
                HStack(spacing: 24) {
                    stat("請求", "\(m.totalRequests)")
                    stat("5xx", "\(m.total("5xx"))", color: m.total("5xx") > 0 ? .red : .primary)
                    stat("4xx", "\(m.total("4xx"))")
                    stat("5xx 比例", String(format: "%.2f%%", m.errorRate * 100), color: m.errorRate > 0.003 ? .red : .primary)
                    stat("最高 p95", m.maxP95 > 0 ? String(format: "%.0f ms", m.maxP95) : "—")
                    stat("最多執行個體", "\(Int(m.instances.map(\.value).max() ?? 0))")
                }
                if m.requests.isEmpty {
                    Text("這段時間沒有請求").font(.caption).foregroundStyle(.secondary)
                } else {
                    Chart(m.requests, id: \.self) { p in
                        BarMark(x: .value("時間", p.time), y: .value("請求", p.value))
                            .foregroundStyle(by: .value("狀態", p.series))
                    }
                    .chartForegroundStyleScale(domain: ["2xx", "3xx", "4xx", "5xx", "其他"],
                                               range: ["2xx", "3xx", "4xx", "5xx", "其他"].map(GCPStyle.seriesColor))
                    .frame(height: 140)
                }
                if !m.latencyP95.isEmpty {
                    Chart(m.latencyP95, id: \.self) { p in
                        LineMark(x: .value("時間", p.time), y: .value("p95 (ms)", p.value))
                    }
                    .chartYAxisLabel("p95 延遲 (ms)")
                    .frame(height: 100)
                }
            } else if metricsError == nil {
                ProgressView().frame(maxWidth: .infinity)
            }
        }
    }

    private func stat(_ label: String, _ value: String, color: Color = .primary) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label).font(.caption).foregroundStyle(.secondary)
            Text(value).font(.title3.monospacedDigit().weight(.semibold)).foregroundStyle(color)
        }
    }

    private var logsCard: some View {
        Card {
            HStack {
                Text("日誌").font(.headline)
                Spacer()
                Picker("", selection: $severity) {
                    Text("錯誤以上").tag("ERROR"); Text("警告以上").tag("WARNING"); Text("全部").tag("DEFAULT")
                }
                .labelsHidden().frame(width: 120)
            }
            GCPLogList(logs: logs, error: logsError)
        }
    }

    private var revisionsCard: some View {
        Card("最近的版本") {
            if let revisions {
                ForEach(revisions) { r in
                    HStack {
                        Image(systemName: r.ready ? "checkmark.circle" : "xmark.circle").foregroundStyle(r.ready ? .green : .red)
                        Text(r.id).font(.callout.monospaced())
                        if r.id == service.latestReady { Pill(text: "目前", color: .green) }
                        if !r.message.isEmpty, !r.ready { Text(r.message).font(.caption).foregroundStyle(.red).lineLimit(1) }
                        Spacer()
                        Text(GCPImage.tag(r.image)).font(.caption.monospaced()).foregroundStyle(.secondary)
                        if let c = r.createdAt { Text(Fmt.relative(c)).font(.caption).foregroundStyle(.secondary) }
                    }
                }
            } else {
                ProgressView().frame(maxWidth: .infinity)
            }
        }
    }

    private func loadMetrics() async {
        metrics = nil; metricsError = nil
        do { metrics = try await store.metrics(service: service.id, hours: hours) }
        catch { metricsError = "讀不到指標:" + GCPStore.explain(error) }
    }

    private func loadLogs() async {
        logs = nil; logsError = nil
        do { logs = try await store.logs(service: service.id, severity: severity, hours: hours, limit: 100) }
        catch { logsError = "讀不到日誌:" + GCPStore.explain(error) }
    }
}

enum GCPImage {
    /// 映像只顯示 tag 或 digest 的前幾碼
    static func tag(_ image: String) -> String {
        if let at = image.firstIndex(of: "@") { return String(image[image.index(after: at)...].prefix(19)) }
        if let colon = image.lastIndex(of: ":"), !image[colon...].contains("/") { return String(image[image.index(after: colon)...].prefix(12)) }
        return GCP.lastPart(image)
    }
}

private struct GCPLogList: View {
    let logs: [GCPLogEntry]?
    let error: String?
    @State private var expanded: Set<String> = []

    var body: some View {
        if let error { Text(error).font(.caption).foregroundStyle(.orange) }
        if let logs {
            if logs.isEmpty {
                Label("這段時間沒有符合的日誌", systemImage: "checkmark.seal").font(.callout).foregroundStyle(.secondary)
            } else {
                ForEach(logs) { l in
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text(l.severity).font(.caption2.weight(.bold)).foregroundStyle(GCPStyle.severityColor(l.severity))
                                .frame(width: 58, alignment: .leading)
                            if let t = l.timestamp {
                                Text(t, format: .dateTime.month().day().hour().minute().second()).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            }
                            if let st = l.httpStatus, st > 0 { Text("\(st)").font(.caption.monospaced()).foregroundStyle(st >= 500 ? .red : .secondary) }
                            Text(l.text).font(.caption.monospaced()).lineLimit(expanded.contains(l.id) ? nil : 1).textSelection(.enabled)
                        }
                        .contentShape(Rectangle())
                        .onTapGesture { if expanded.contains(l.id) { expanded.remove(l.id) } else { expanded.insert(l.id) } }
                    }
                    Divider()
                }
            }
        } else if error == nil {
            ProgressView().frame(maxWidth: .infinity)
        }
    }
}

// MARK: - Job 詳情

private struct GCPJobDetail: View {
    @EnvironmentObject var app: AppState
    let job: GCPJob
    @State private var executions: [GCPExecution]?
    @State private var execError: String?
    @State private var logs: [GCPLogEntry]?
    @State private var logsError: String?
    @State private var severity = "ERROR"

    private var store: GCPStore { app.startGCP() }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .firstTextBaseline) {
                    Text(job.id).font(.title2.weight(.semibold))
                    if let l = job.latest { Pill(text: "最近一次:" + l.statusLabel, color: GCPStyle.executionColor(l)) }
                    Spacer()
                    if let u = GCP.consoleURL(project: app.gcpSettings.project, region: app.gcpSettings.region, job: job.id) {
                        Button("在 Console 打開") { NSWorkspace.shared.open(u) }
                    }
                }
                Card("最近的執行") {
                    if let execError { Text(execError).font(.caption).foregroundStyle(.orange) }
                    if let executions {
                        ForEach(executions) { e in
                            HStack {
                                Circle().fill(GCPStyle.executionColor(e)).frame(width: 8, height: 8)
                                Text(e.id).font(.callout.monospaced())
                                Text(e.statusLabel).font(.caption).foregroundStyle(GCPStyle.executionColor(e))
                                Text("\(e.succeeded)/\(e.tasks) 個工作成功").font(.caption).foregroundStyle(.secondary)
                                if !e.message.isEmpty { Text(e.message).font(.caption).foregroundStyle(.red).lineLimit(1) }
                                Spacer()
                                if let d = GHStyle.duration(e.duration) { Text(d).font(.caption).foregroundStyle(.secondary) }
                                if let c = e.createdAt { Text(Fmt.relative(c)).font(.caption).foregroundStyle(.secondary) }
                            }
                        }
                    } else if execError == nil {
                        ProgressView().frame(maxWidth: .infinity)
                    }
                }
                Card {
                    HStack {
                        Text("日誌(最近 7 天)").font(.headline)
                        Spacer()
                        Picker("", selection: $severity) {
                            Text("錯誤以上").tag("ERROR"); Text("警告以上").tag("WARNING"); Text("全部").tag("DEFAULT")
                        }
                        .labelsHidden().frame(width: 120)
                    }
                    GCPLogList(logs: logs, error: logsError)
                }
            }
            .padding(20)
            .frame(maxWidth: 980, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task {
            do { executions = try await store.executions(job: job.id) } catch { execError = "讀不到執行:" + GCPStore.explain(error) }
        }
        .task(id: severity) {
            logs = nil; logsError = nil
            do { logs = try await store.logs(job: job.id, severity: severity, hours: 168, limit: 100) }
            catch { logsError = "讀不到日誌:" + GCPStore.explain(error) }
        }
    }
}

// MARK: - 設定

private struct GCPSettingsSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    @State private var draft = GCPSettings()
    @State private var services = ""
    @State private var jobs = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("GCP 設定").font(.title3.weight(.semibold))
            GCPSettingsFields(draft: $draft, services: $services, jobs: $jobs)
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("儲存") {
                    app.startGCP().update {
                        $0.project = draft.project; $0.region = draft.region
                        $0.services = services.split(separator: ",").map(String.init)
                        $0.jobs = jobs.split(separator: ",").map(String.init)
                    }
                    Task { await app.gcpStore?.refresh() }
                    dismiss()
                }
                .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
                .disabled(draft.project.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20).frame(width: 520)
        .onAppear {
            draft = app.gcpSettings
            services = app.gcpSettings.services.joined(separator: ", ")
            jobs = app.gcpSettings.jobs.joined(separator: ", ")
        }
    }
}

private struct GCPSettingsFields: View {
    @Binding var draft: GCPSettings
    @Binding var services: String
    @Binding var jobs: String

    var body: some View {
        Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 8) {
            GridRow { Text("專案 id"); TextField("例如 shuttle-045094509", text: $draft.project).textFieldStyle(.roundedBorder) }
            GridRow { Text("區域"); TextField("asia-east1", text: $draft.region).textFieldStyle(.roundedBorder) }
            GridRow { Text("追蹤的服務"); TextField("用逗號分隔;空白表示全部", text: $services).textFieldStyle(.roundedBorder) }
            GridRow { Text("追蹤的 Job"); TextField("用逗號分隔;空白表示全部", text: $jobs).textFieldStyle(.roundedBorder) }
        }
    }
}

// MARK: - 登入引導

struct GCPSetupView: View {
    @EnvironmentObject var app: AppState
    @State private var clientId = ""
    @State private var secret = ""
    @State private var draft = GCPSettings()
    @State private var services = ""
    @State private var jobs = ""

    private var store: GCPStore { app.gcpStore ?? GCPStore(app: app) }
    private var conn: Connection? { store.connection }
    private var state: ConnectionState? { conn.flatMap { app.connectionStates[$0.id] } }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label("連上 Google Cloud", systemImage: "cloud").font(.title2.weight(.semibold))
            Text("連上後,這裡會顯示 Cloud Run 服務的狀態、版本與流量、Job 的執行結果、錯誤日誌,以及請求量、5xx 與延遲;虛擬角色也能透過 MCP 讀。Ops 只讀(cloud-platform.read-only),不會部署、改流量或執行 Job。")
                .foregroundStyle(.secondary)

            if !store.isConfigured {
                Card {
                    VStack(alignment: .leading, spacing: 10) {
                        Text("專案").font(.headline)
                        GCPSettingsFields(draft: $draft, services: $services, jobs: $jobs)
                        Button("儲存") {
                            store.update {
                                $0.project = draft.project; $0.region = draft.region
                                $0.services = services.split(separator: ",").map(String.init)
                                $0.jobs = jobs.split(separator: ",").map(String.init)
                            }
                        }
                        .disabled(draft.project.trimmingCharacters(in: .whitespaces).isEmpty)
                    }
                }
            }

            if conn == nil {
                Button("開始設定登入") { store.ensureConnection() }.buttonStyle(.borderedProminent)
            } else if let c = conn {
                if c.oauth.clientId.isEmpty || !c.oauth.hasClientSecret { registerSteps(c) } else { loginStep(c) }
            }
            if let e = state?.error { Label(e, systemImage: "exclamationmark.triangle").foregroundStyle(.orange) }
        }
        .onAppear {
            draft = app.gcpSettings
            services = app.gcpSettings.services.joined(separator: ", ")
            jobs = app.gcpSettings.jobs.joined(separator: ", ")
            clientId = conn?.oauth.clientId ?? ""
        }
    }

    private func registerSteps(_ c: Connection) -> some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                Text("1. 在 Google Cloud 建一個 OAuth 用戶端(只要做一次)").font(.headline)
                VStack(alignment: .leading, spacing: 4) {
                    Text("• 按下面的按鈕打開 Google Auth Platform › 用戶端 › 建立用戶端。")
                    Text("• 應用程式類型選「電腦版應用程式」(Desktop app),名稱填 Ops,按建立。")
                    Text("• 第一次用的話會先要你設定 OAuth 同意畫面:使用者類型選「外部」,把你自己的 Google 帳號加進「測試使用者」。")
                    Text("• 同意畫面在「測試」狀態時,登入大約 7 天會過期要重登;改成「正式版」(不用送審,自己用會看到未驗證警告)就不會。")
                }
                .font(.callout).foregroundStyle(.secondary)
                Button("打開 Google Cloud 建立用戶端") { NSWorkspace.shared.open(GCP.createClientURL(project: app.gcpSettings.project)) }
                Divider()
                Text("2. 把用戶端 ID 和用戶端密鑰貼到這裡").font(.headline)
                TextField("用戶端 ID(….apps.googleusercontent.com)", text: $clientId).textFieldStyle(.roundedBorder)
                SecureField("用戶端密鑰(存在鑰匙圈)", text: $secret).textFieldStyle(.roundedBorder)
                Button("儲存並用瀏覽器登入") {
                    var n = c
                    n.oauth.clientId = clientId.trimmingCharacters(in: .whitespacesAndNewlines)
                    app.connStore?.upsert(n)
                    app.connStore?.setClientSecret(secret.trimmingCharacters(in: .whitespacesAndNewlines), for: c.id)
                    secret = ""
                    Task { await app.connStore?.login(c.id) }
                }
                .buttonStyle(.borderedProminent)
                .disabled(clientId.trimmingCharacters(in: .whitespaces).isEmpty || (secret.isEmpty && !c.oauth.hasClientSecret))
            }
        }
    }

    private func loginStep(_ c: Connection) -> some View {
        Card {
            VStack(alignment: .leading, spacing: 10) {
                Text("用瀏覽器登入 Google").font(.headline)
                Text("會打開 Google 的授權頁,選帳號、按允許後回到 Ops 就完成了。只要求唯讀權限(cloud-platform.read-only)。帳號需要這個專案的檢視權限。")
                    .font(.callout).foregroundStyle(.secondary)
                HStack {
                    Button("用瀏覽器登入") { Task { await app.connStore?.login(c.id) } }
                        .buttonStyle(.borderedProminent).disabled(state?.busy == true)
                    if state?.busy == true { ProgressView().controlSize(.small); Text("等瀏覽器授權…").font(.caption) }
                    Spacer()
                    Button("重新設定 OAuth 用戶端") {
                        var n = c; n.oauth.clientId = ""
                        app.connStore?.upsert(n)
                        app.connStore?.setClientSecret(nil, for: c.id)
                        clientId = ""
                    }
                    .buttonStyle(.borderless)
                }
            }
        }
    }
}
