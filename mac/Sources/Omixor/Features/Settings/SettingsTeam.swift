import SwiftUI

// 設定 › 團隊:成員與值班表(只讀;只有範例資料時才有內容)。
// 權限、輪值與升級規則、換班還沒有後端實作,不放只存在畫面上的設定。

struct SettingsTeamPane: View {
    @EnvironmentObject var app: AppState

    private var shifts: [OnCallShift] {
        app.onCall.filter { $0.end > .now }.sorted { ($0.start, $0.role) < ($1.start, $1.role) }
    }

    private func isOnCall(_ personId: String) -> OnCallShift? {
        app.onCall.first { $0.person == personId && $0.start <= .now && $0.end > .now }
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
                    }
                    .padding(.vertical, 2)
                }
            } header: {
                Text("成員(\(app.people.count))")
            }

            if !shifts.isEmpty {
                Section("值班輪值表") {
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
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
    }

    private func shiftDate(_ d: Date) -> String {
        d.formatted(.dateTime.month(.defaultDigits).day().hour(.twoDigits(amPM: .omitted)).minute(.twoDigits))
    }
}
