import SwiftUI

/// 已解決事件的檢討報告(AI 依資料起草)
struct IncPostmortemView: View {
    @EnvironmentObject var app: AppState
    let incident: Incident
    @State private var copied = false
    @State private var createdActions: Set<String> = []

    var body: some View {
        let pm = IncAI.postmortem(incident, app: app)
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 10) {
                Image(systemName: "sparkles").foregroundStyle(.purple).font(.title3)
                VStack(alignment: .leading, spacing: 2) {
                    Text("檢討報告草稿").font(.headline)
                    Text("由 AI 依時間軸、告警與後續工作起草,請指揮官確認後再發布。")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button {
                    IncFmt.copy(pm.markdown(title: "\(incident.id) \(incident.title)"))
                    copied = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { copied = false }
                } label: {
                    Label(copied ? "已複製" : "複製為 Markdown", systemImage: copied ? "checkmark" : "doc.on.clipboard")
                }
            }
            .padding(14)
            .background(Color.purple.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
            .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.purple.opacity(0.2)))

            HStack(spacing: 12) {
                MetricTile(title: "總時長", value: IncFmt.short(incident.incDuration()), icon: "timer", tint: .blue)
                MetricTile(title: "首次回應", value: IncFmt.short(incident.incAckDelay), icon: "hand.raised", tint: .orange)
                MetricTile(title: "AI 參與", value: "\(incident.timeline.filter { $0.actor.incIsAI }.count) 則", icon: "sparkles", tint: .purple)
            }

            section("摘要", icon: "doc.text") { para(pm.summary) }
            section("影響", icon: "person.3") { para(pm.impact) }
            section("時間軸", icon: "clock") {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(Array(pm.timeline.enumerated()), id: \.offset) { _, row in
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            Text(IncFmt.dateTime(row.0)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                                .frame(width: 92, alignment: .leading)
                            Text(row.1).font(.caption.weight(.semibold)).frame(width: 90, alignment: .leading).lineLimit(1)
                            Text(row.2).font(.callout).fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    if pm.timeline.isEmpty { para("沒有時間軸紀錄。") }
                }
            }
            section("根本原因", icon: "scope") { para(pm.rootCause) }
            HStack(alignment: .top, spacing: 16) {
                section("做得好的地方", icon: "hand.thumbsup") { bullets(pm.wentWell, color: .green) }
                section("可以改進的地方", icon: "arrow.up.forward.circle") { bullets(pm.improve, color: .orange) }
            }
            section("行動項目", icon: "checklist") {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(Array(pm.actions.enumerated()), id: \.offset) { _, a in
                        HStack(spacing: 8) {
                            Image(systemName: a.2 ? "checkmark.square" : "square").foregroundStyle(.secondary)
                            Text(a.0).font(.callout)
                            Spacer()
                            if a.2 {
                                Pill(text: a.1, color: .blue)
                            } else {
                                Button("建立工作項目") { create(a.0) }
                                    .controlSize(.small)
                                    .disabled(createdActions.contains(a.0))
                                AIBadge(text: "AI 建議")
                            }
                        }
                    }
                }
            }
        }
    }

    private func create(_ title: String) {
        guard let i = app.incidents.firstIndex(where: { $0.id == incident.id }) else { return }
        let maxNum = app.workItems.compactMap { Int($0.id.filter(\.isNumber)) }.max() ?? 100
        let id = "OPS-\(maxNum + 1)"
        app.workItems.append(WorkItem(id: id, title: title, type: .incident, status: .todo, assignee: nil,
                                      points: 3, service: incident.services.first, createdByAI: true))
        _ = i
        do { _ = try app.startIncidents().update(id: incident.id, addFollowUps: [id], by: .person(app.me)) } catch {
            app.errorNotice = "\(id) 沒有關聯到 \(incident.id):\(error.localizedDescription)"
        }
        app.log(.person(app.me), "checklist", "依 \(incident.id) 檢討報告建立 \(id)", .incidents)
        createdActions.insert(title)
    }

    private func section<C: View>(_ title: String, icon: String, @ViewBuilder content: () -> C) -> some View {
        Card {
            VStack(alignment: .leading, spacing: 8) {
                Label(title, systemImage: icon).font(.headline)
                content()
            }
        }
    }

    private func para(_ s: String) -> some View {
        Text(s).font(.callout).lineSpacing(3).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
    }

    private func bullets(_ items: [String], color: Color) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            ForEach(items, id: \.self) { t in
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Circle().fill(color).frame(width: 5, height: 5).offset(y: -2)
                    Text(t).font(.callout).fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }
}
