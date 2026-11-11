import SwiftUI

// 設定 › 團隊與值班:成員、權限、輪值表與升級規則。

enum SettingsAccessRole: String, CaseIterable, Identifiable {
    case admin, ops, developer, viewer
    var id: String { rawValue }
    var label: String {
        switch self {
        case .admin: return "管理員"
        case .ops: return "維運"
        case .developer: return "開發者"
        case .viewer: return "唯讀"
        }
    }
    var detail: String {
        switch self {
        case .admin: return "可管理整合、成員與 AI 治理"
        case .ops: return "可部署、回滾、處理告警與事件"
        case .developer: return "可觸發流程、部署到非正式環境"
        case .viewer: return "只能檢視"
        }
    }
}

struct SettingsTeamPane: View {
    @EnvironmentObject var app: AppState
    @State private var access: [String: SettingsAccessRole] = [:]
    @State private var rotation = "weekly"
    @State private var handoff = Calendar.current.date(bySettingHour: 10, minute: 0, second: 0, of: .now) ?? .now
    @State private var escalateMinutes = 10
    @State private var autoEscalate = true
    @State private var followSun = false

    private var shifts: [OnCallShift] {
        app.onCall.filter { $0.end > .now }.sorted { ($0.start, $0.role) < ($1.start, $1.role) }
    }

    private func isOnCall(_ personId: String) -> OnCallShift? {
        app.onCall.first { $0.person == personId && $0.start <= .now && $0.end > .now }
    }

    private func defaultRole(_ p: Person) -> SettingsAccessRole {
        if p.id == app.me { return .admin }
        let r = p.role.lowercased()
        if r.contains("sre") || r.contains("維運") || r.contains("ops") || r.contains("infra") { return .ops }
        if r.contains("pm") || r.contains("產品") || r.contains("設計") { return .viewer }
        return .developer
    }

    var body: some View {
        Form {
            Section {
                if app.people.isEmpty {
                    Text("尚未加入任何成員").foregroundStyle(.secondary)
                }
                ForEach(app.people) { p in
                    HStack(spacing: 10) {
                        ActorAvatar(actor: .person(p.id), size: 28)
                        VStack(alignment: .leading, spacing: 1) {
                            HStack(spacing: 6) {
                                Text(p.name).font(.body.weight(.medium))
                                if p.id == app.me { Pill(text: "你", color: .accentColor) }
                                if let s = isOnCall(p.id) { Pill(text: "值班中 · \(s.role)", color: .green, icon: "phone.fill") }
                            }
                            Text(p.role).font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Picker("權限", selection: Binding(
                            get: { access[p.id] ?? defaultRole(p) },
                            set: { access[p.id] = $0 }
                        )) {
                            ForEach(SettingsAccessRole.allCases) { Text($0.label).tag($0) }
                        }
                        .labelsHidden()
                        .frame(width: 110)
                        .disabled(p.id == app.me)
                        .help((access[p.id] ?? defaultRole(p)).detail)
                    }
                    .padding(.vertical, 2)
                }
            } header: {
                Text("成員(\(app.people.count))")
            } footer: {
                Text("權限決定成員能在 Ops 裡執行哪些動作;AI 自動化的權限另在「AI 治理」設定。")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("值班輪值表") {
                if shifts.isEmpty {
                    Text("目前沒有排定的值班").foregroundStyle(.secondary)
                }
                ForEach(shifts) { s in
                    let active = s.start <= .now && s.end > .now
                    HStack(spacing: 10) {
                        ActorAvatar(actor: .person(s.person), size: 22)
                        Text(app.person(s.person)?.name ?? s.person).frame(minWidth: 80, alignment: .leading)
                        Pill(text: s.role, color: s.role == "Primary" ? .accentColor : .secondary)
                        if active { Pill(text: "進行中", color: .green) }
                        Spacer()
                        Text("\(shiftDate(s.start)) – \(shiftDate(s.end))")
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.secondary)
                        Menu {
                            ForEach(app.people.filter { $0.id != s.person }) { p in
                                Button("與 \(p.name) 換班") {
                                    app.log(.person(app.me), "arrow.left.arrow.right",
                                            "提出換班:\(app.person(s.person)?.name ?? s.person) 與 \(p.name)(\(shiftDate(s.start)))",
                                            .settings)
                                }
                            }
                        } label: {
                            Image(systemName: "ellipsis.circle")
                        }
                        .menuStyle(.borderlessButton)
                        .menuIndicator(.hidden)
                        .fixedSize()
                        .help("換班")
                    }
                }
            }

            Section {
                Picker("輪值週期", selection: $rotation) {
                    Text("每天").tag("daily")
                    Text("每週").tag("weekly")
                    Text("每兩週").tag("biweekly")
                }
                DatePicker("交接時間", selection: $handoff, displayedComponents: .hourAndMinute)
                Toggle("Primary 未回應時自動升級給 Secondary", isOn: $autoEscalate)
                Stepper(value: $escalateMinutes, in: 5...60, step: 5) {
                    LabeledContent("升級等待時間", value: "\(escalateMinutes) 分鐘")
                }
                .disabled(!autoEscalate)
                Toggle("跨時區接力(Follow the sun)", isOn: $followSun)
            } header: {
                Text("輪值與升級規則")
            } footer: {
                Text("Critical 告警會先通知 Primary;超過等待時間沒有確認,就通知 Secondary 與事件指揮官。")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
    }

    private func shiftDate(_ d: Date) -> String {
        d.formatted(.dateTime.month(.defaultDigits).day().hour(.twoDigits(amPM: .omitted)).minute(.twoDigits))
    }
}
