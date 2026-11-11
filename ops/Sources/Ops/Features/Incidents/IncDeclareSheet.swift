import SwiftUI

/// 宣告新事件的表單
struct IncDeclareSheet: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    var onCreated: (String) -> Void

    @State private var title = ""
    @State private var sev = 2
    @State private var services: Set<String> = []
    @State private var commander = ""
    @State private var impact = ""
    @State private var serviceQuery = ""

    private var canSubmit: Bool { !title.trimmingCharacters(in: .whitespaces).isEmpty && !commander.isEmpty }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "flame.fill").font(.title2).foregroundStyle(.red)
                VStack(alignment: .leading, spacing: 2) {
                    Text("宣告事件").font(.title3.weight(.semibold))
                    Text("建立後會通知值班人員,並開始記錄時間軸。").font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
            }
            .padding(18)
            Divider()

            Form {
                Section {
                    TextField("標題", text: $title, prompt: Text("例如:結帳 API 回應變慢"))
                    Picker("嚴重程度", selection: $sev) {
                        ForEach(IncSev.all, id: \.self) { s in
                            Text("\(IncSev.label(s)) — \(IncSev.meaning(s))").tag(s)
                        }
                    }
                    Picker("指揮官", selection: $commander) {
                        Text("請選擇").tag("")
                        ForEach(app.people) { p in
                            Text("\(p.name)(\(p.role))").tag(p.id)
                        }
                    }
                    TextField("影響範圍", text: $impact, prompt: Text("哪些使用者、什麼功能受影響"), axis: .vertical)
                        .lineLimit(2...4)
                }
                Section("受影響的服務") {
                    if app.services.isEmpty {
                        Text("沒有可選的服務").foregroundStyle(.secondary)
                    } else {
                        if app.services.count > 8 {
                            TextField("搜尋服務", text: $serviceQuery)
                        }
                        ScrollView {
                            VStack(alignment: .leading, spacing: 4) {
                                ForEach(filteredServices) { s in
                                    Toggle(isOn: Binding(get: { services.contains(s.id) },
                                                         set: { on in if on { services.insert(s.id) } else { services.remove(s.id) } })) {
                                        HStack(spacing: 6) {
                                            Circle().fill(s.health.color).frame(width: 7, height: 7)
                                            Text(s.name)
                                            Text("Tier \(s.tier)").font(.caption).foregroundStyle(.secondary)
                                        }
                                    }
                                    .toggleStyle(.checkbox)
                                }
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .frame(maxHeight: 150)
                    }
                }
            }
            .formStyle(.grouped)

            Divider()
            HStack {
                if sev <= 2 {
                    Label("SEV\(sev) 會立即呼叫主要與次要值班", systemImage: "bell.and.waves.left.and.right")
                        .font(.caption).foregroundStyle(.orange)
                }
                Spacer()
                Button("取消", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("宣告事件", action: submit)
                    .buttonStyle(.borderedProminent)
                    .tint(.red)
                    .keyboardShortcut(.defaultAction)
                    .disabled(!canSubmit)
            }
            .padding(14)
        }
        .frame(width: 520, height: 560)
        .onAppear {
            if commander.isEmpty {
                commander = app.currentOnCall?.person ?? (app.person(app.me) != nil ? app.me : app.people.first?.id ?? "")
            }
        }
    }

    private var filteredServices: [Service] {
        let q = serviceQuery.trimmingCharacters(in: .whitespaces)
        return app.services.filter { q.isEmpty || $0.name.localizedCaseInsensitiveContains(q) || services.contains($0.id) }
            .sorted { ($0.tier, $0.name) < ($1.tier, $1.name) }
    }

    private func submit() {
        guard canSubmit else { return }
        let num = (app.incidents.map(\.incNumber).max() ?? 2000) + 1
        let id = "INC-\(num)"
        let now = Date()
        let svc = app.services.map(\.id).filter { services.contains($0) }
        var timeline = [IncidentEvent(id: UUID(), date: now, actor: .person(app.me), kind: .status,
                                      text: "宣告 \(IncSev.label(sev)) 事件,指揮官:\(app.person(commander)?.name ?? commander)")]
        if sev <= 2 {
            let names = app.onCall.filter { $0.start <= now && $0.end > now }.compactMap { app.person($0.person)?.name }
            timeline.append(IncidentEvent(id: UUID(), date: now, actor: .system, kind: .action,
                                          text: names.isEmpty ? "已呼叫值班人員" : "已呼叫值班:\(names.joined(separator: "、"))"))
        }
        let inc = Incident(id: id, title: title.trimmingCharacters(in: .whitespaces), sev: sev, status: .investigating,
                           services: svc, commander: commander, startedAt: now, resolvedAt: nil,
                           impact: impact.trimmingCharacters(in: .whitespacesAndNewlines),
                           timeline: timeline, aiSummary: nil, followUps: [])
        app.incidents.insert(inc, at: 0)
        app.log(.person(app.me), "flame.fill", "宣告 \(IncSev.label(sev)) 事件 \(id)「\(inc.title)」", .incidents)
        onCreated(id)
        dismiss()
    }
}
