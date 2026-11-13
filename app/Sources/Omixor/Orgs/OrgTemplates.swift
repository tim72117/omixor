import SwiftUI

// 組織樣板(例如「家庭助理」):樣板只定義在 core(core/model/orgtemplates.go),App 透過目前連線的 server
// (IssueServerConfig.current;本機模式是本機守護服務)列出、預覽、套用:
//   GET  /api/orgs/<組織>/templates                 可用的樣板(功能開關關閉的不列 → 畫面不顯示入口)
//   GET  /api/orgs/<組織>/templates/<id>?scenarios=1 預覽會建立、略過什麼
//   POST /api/orgs/<組織>/templates/<id>/apply       {scenarios} 套用(組織管理員)
// 目前是框架:入口在 設定 › 組織 的工作空間區塊;之後可以再加到建立組織、虛擬角色的空狀態。

struct OrgTemplateSummary: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var description: String
    var feature: String
    var agents: Int
    var channels: Int
    var docs: Int
    var edges: Int
    var scenarios: Int?
}

struct OrgTemplatePlan: Codable {
    struct Item: Codable, Hashable {
        var kind: String
        var name: String
        var status: String
        var detail: String?
    }
    struct Template: Codable {
        var id: String
        var name: String
        var description: String
        var notes: [String]?
    }
    var template: Template
    var items: [Item]
    var applied: Bool
    var created: Int
    var skipped: Int
    var failed: Int
    var suggestAutoReply: [String]?
    var canApply: Bool
}

extension IssueRemote {
    private func decodeTemplate<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的樣板格式看不懂:\(error.localizedDescription)")
        }
    }

    /// GET /templates:可用的樣板(功能開關關閉的伺服器不會列)
    func listOrgTemplates(org: String) throws -> [OrgTemplateSummary] {
        try decodeTemplate([OrgTemplateSummary].self, try send("GET", orgURL(org, ["templates"]), nil))
    }

    /// GET /templates/<id>:預覽
    func previewOrgTemplate(org: String, id: String, scenarios: Bool) throws -> OrgTemplatePlan {
        var c = URLComponents(url: orgURL(org, ["templates", id]), resolvingAgainstBaseURL: false)!
        if scenarios { c.queryItems = [URLQueryItem(name: "scenarios", value: "1")] }
        return try decodeTemplate(OrgTemplatePlan.self, try send("GET", c.url!, nil))
    }

    /// POST /templates/<id>/apply:套用(已存在的伺服器會略過)
    func applyOrgTemplate(org: String, id: String, scenarios: Bool) throws -> OrgTemplatePlan {
        try decodeTemplate(OrgTemplatePlan.self, try send("POST", orgURL(org, ["templates", id, "apply"]), try json(["scenarios": scenarios])))
    }
}

/// 設定 › 組織 的工作空間區塊裡的樣板入口:伺服器沒有列出樣板(功能開關關閉)時什麼都不顯示
struct OrgTemplatesRows: View {
    @EnvironmentObject var app: AppState
    @State private var templates: [OrgTemplateSummary] = []
    @State private var opened: OrgTemplateSummary?

    var body: some View {
        ForEach(templates) { t in
            HStack {
                Label("套用「\(t.name)」樣板", systemImage: "square.stack.3d.down.right")
                Pill(text: "實驗", color: .orange)
                Spacer()
                Text("\(t.agents) 個角色、\(t.channels) 個頻道、\(t.docs) 份文件").font(.caption).foregroundStyle(.secondary)
                Button("預覽…") { opened = t }
            }
        }
        .task(id: app.currentOrgId) { await reload() }
        .sheet(item: $opened) { t in OrgTemplateSheet(summary: t) }
    }

    private func reload() async {
        let org = app.currentOrgId
        let list = await Task.detached { try? IssueRemote(config: IssueServerConfig.current).listOrgTemplates(org: org) }.value
        // 伺服器已經依自己的版本與功能開關過濾;App 這個版本沒有該功能的畫面時也不顯示
        templates = (list ?? []).filter { $0.feature.isEmpty || app.isFeatureEnabled($0.feature) }
    }
}

/// 套用前的預覽與確認;套用後顯示結果與下一步(到自動執行打開管家的自動回覆)
struct OrgTemplateSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let summary: OrgTemplateSummary
    @State private var plan: OrgTemplatePlan?
    @State private var scenarios = false
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("「\(summary.name)」樣板").font(.title2.bold())
            Text(summary.description).foregroundStyle(.secondary)
            if (summary.scenarios ?? 0) > 0, plan?.applied != true {
                Toggle("帶入模擬情境(\(summary.scenarios ?? 0) 個示範對話與任務,標「模擬」)", isOn: $scenarios)
                Text("會在頻道寫入示範對話、建立示範任務(完成或需要協助),方便看家庭助理怎麼運作;訊息不能刪除,建議在試用的組織使用。")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let p = plan {
                List {
                    ForEach(p.items, id: \.self) { it in
                        HStack {
                            Text(kindLabel(it.kind)).font(.caption).foregroundStyle(.secondary).frame(width: 56, alignment: .leading)
                            Text(it.name)
                            Spacer()
                            if let d = it.detail, !d.isEmpty { Text(d).font(.caption).foregroundStyle(.secondary).lineLimit(2) }
                            Pill(text: statusLabel(it.status), color: statusColor(it.status))
                        }
                    }
                }
                .frame(minHeight: 260)
                if p.applied {
                    Text("建立 \(p.created) 項、略過 \(p.skipped) 項" + (p.failed > 0 ? "、失敗 \(p.failed) 項" : "")).bold()
                    ForEach(p.template.notes ?? [], id: \.self) { n in Label(n, systemImage: "lightbulb").font(.callout) }
                }
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 120)
            }
            if let e = error { Text(e).foregroundStyle(.orange) }
            HStack {
                Spacer()
                if plan?.applied == true {
                    Button("到自動執行") { dismiss(); app.go(.automation, focus: "runner") }
                    Button("完成") { dismiss() }.keyboardShortcut(.defaultAction)
                } else {
                    Button("取消") { dismiss() }
                    Button("套用") { Task { await apply() } }
                        .keyboardShortcut(.defaultAction)
                        .disabled(busy || plan?.canApply != true)
                }
            }
            if plan?.canApply == false, plan?.applied != true {
                Text("要組織管理員才能套用。").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(20)
        .frame(width: 620)
        .task(id: scenarios) { await preview() }
    }

    private func preview() async {
        let org = app.currentOrgId, id = summary.id, sc = scenarios
        do {
            plan = try await Task.detached { try IssueRemote(config: IssueServerConfig.current).previewOrgTemplate(org: org, id: id, scenarios: sc) }.value
            error = nil
        } catch { self.error = error.localizedDescription }
    }

    private func apply() async {
        busy = true
        defer { busy = false }
        let org = app.currentOrgId, id = summary.id, sc = scenarios
        do {
            plan = try await Task.detached { try IssueRemote(config: IssueServerConfig.current).applyOrgTemplate(org: org, id: id, scenarios: sc) }.value
            error = nil
            app.startAgents().load()
            app.startChannels().load()
            app.startCabinet()
        } catch { self.error = error.localizedDescription }
    }

    private func kindLabel(_ k: String) -> String {
        switch k {
        case "agent": return "角色"
        case "chain": return "組織鏈"
        case "channel": return "頻道"
        case "section": return "文件櫃"
        case "doc": return "文件"
        case "scenario": return "模擬"
        default: return k
        }
    }

    private func statusLabel(_ s: String) -> String {
        switch s {
        case "create": return "會建立"
        case "skip", "skipped": return "已存在,略過"
        case "created": return "已建立"
        case "failed": return "失敗"
        default: return s
        }
    }

    private func statusColor(_ s: String) -> Color {
        switch s {
        case "create", "created": return .green
        case "failed": return .red
        default: return .gray
        }
    }
}
