import SwiftUI

/// 右側事件詳情
struct IncDetailView: View {
    @EnvironmentObject var app: AppState
    let incidentId: String
    @State private var tab: DetailTab = .overview
    @State private var width: CGFloat = 900

    enum DetailTab: Hashable { case overview, postmortem }

    var body: some View {
        if let inc = app.incident(incidentId) {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    IncDetailHeader(incident: inc)
                    IncStatusStepper(incident: inc)
                    if inc.status == .resolved {
                        Picker("內容", selection: $tab) {
                            Text("事件詳情").tag(DetailTab.overview)
                            Text("檢討報告").tag(DetailTab.postmortem)
                        }
                        .pickerStyle(.segmented)
                        .labelsHidden()
                        .frame(maxWidth: 280)
                    }
                    if inc.status == .resolved && tab == .postmortem {
                        IncPostmortemView(incident: inc)
                    } else {
                        overview(inc)
                    }
                }
                .padding(20)
                .frame(maxWidth: 1100, alignment: .leading)
                .background(GeometryReader { g in
                    Color.clear
                        .onAppear { width = g.size.width }
                        .onChange(of: g.size.width) { _, w in width = w }
                })
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .id(incidentId)
            .onChange(of: incidentId) { _, _ in tab = .overview }
        } else {
            EmptyStateView(title: "找不到事件", icon: "questionmark.circle")
        }
    }

    @ViewBuilder private func overview(_ inc: Incident) -> some View {
        if width >= 780 {
            HStack(alignment: .top, spacing: 16) {
                VStack(alignment: .leading, spacing: 16) {
                    impactCard(inc)
                    IncAISummaryCard(incident: inc)
                    IncTimelineCard(incident: inc)
                }
                .frame(maxWidth: .infinity)
                VStack(alignment: .leading, spacing: 16) {
                    servicesCard(inc)
                    IncRelatedAlertsCard(incident: inc)
                    IncFollowUpsCard(incident: inc)
                }
                .frame(width: 290)
            }
        } else {
            VStack(alignment: .leading, spacing: 16) {
                impactCard(inc)
                IncAISummaryCard(incident: inc)
                servicesCard(inc)
                IncTimelineCard(incident: inc)
                IncRelatedAlertsCard(incident: inc)
                IncFollowUpsCard(incident: inc)
            }
        }
    }

    private func impactCard(_ inc: Incident) -> some View {
        Card("影響範圍") {
            Text(inc.impact.isEmpty ? "尚未描述影響範圍。" : inc.impact)
                .foregroundStyle(inc.impact.isEmpty ? .secondary : .primary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func servicesCard(_ inc: Incident) -> some View {
        Card("受影響的服務", subtitle: inc.services.isEmpty ? nil : "點選查看服務狀態") {
            if inc.services.isEmpty {
                Text("未指定").font(.callout).foregroundStyle(.secondary)
            } else {
                IncFlow(spacing: 6) {
                    ForEach(inc.services, id: \.self) { IncServiceChip(serviceId: $0) }
                }
            }
        }
    }
}

// MARK: - 頁首

struct IncDetailHeader: View {
    @EnvironmentObject var app: AppState
    let incident: Incident

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                IncSevBadge(sev: incident.sev, large: true)
                Text(incident.id).font(.callout.monospaced()).foregroundStyle(.secondary).textSelection(.enabled)
                IncStatusPill(status: incident.status)
                Spacer()
                Button {
                    IncFmt.copy("\(incident.id) \(IncSev.label(incident.sev)) \(incident.title)(\(incident.status.label))")
                } label: { Image(systemName: "doc.on.doc") }
                .buttonStyle(.borderless)
                .help("複製事件標題")
            }
            Text(incident.title)
                .font(.title2.weight(.semibold))
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            IncFlow(spacing: 18) {
                meta("開始", value: IncFmt.dateTime(incident.startedAt), icon: "clock")
                TimelineView(.periodic(from: .now, by: 30)) { ctx in
                    meta(incident.incIsActive ? "已持續" : "總時長",
                         value: IncFmt.duration(incident.incDuration(now: ctx.date)), icon: "timer")
                }
                if let r = incident.resolvedAt {
                    meta("解決", value: IncFmt.dateTime(r), icon: "checkmark.circle")
                }
                HStack(spacing: 6) {
                    Text("指揮官").font(.caption).foregroundStyle(.secondary)
                    ActorLabel(actor: .person(incident.commander)).font(.callout.weight(.medium))
                }
                if let ack = incident.incAckDelay {
                    meta("首次回應", value: IncFmt.duration(ack), icon: "hand.raised")
                }
            }
        }
    }

    private func meta(_ label: String, value: String, icon: String) -> some View {
        HStack(spacing: 5) {
            Image(systemName: icon).foregroundStyle(.secondary).font(.caption)
            Text(label).font(.caption).foregroundStyle(.secondary)
            Text(value).font(.callout.weight(.medium)).monospacedDigit()
        }
    }
}

// MARK: - 狀態步驟

struct IncStatusStepper: View {
    @EnvironmentObject var app: AppState
    let incident: Incident
    @State private var pending: IncidentStatus?
    @State private var note = ""

    var body: some View {
        // 寬度不夠時,把「推進到…」按鈕換到步驟下方
        ViewThatFits(in: .horizontal) {
            HStack(spacing: 14) { steps; advanceButton }
            VStack(alignment: .leading, spacing: 10) { steps; advanceButton }
        }
        .padding(12)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.07)))
        .alert(pending.map { "把狀態改為「\($0.label)」?" } ?? "",
               isPresented: Binding(get: { pending != nil }, set: { if !$0 { pending = nil } })) {
            TextField("補充說明(選填)", text: $note)
            Button(pending == .resolved ? "標記為已解決" : "更新狀態") {
                if let p = pending {
                    let t = note.trimmingCharacters(in: .whitespacesAndNewlines)
                    app.updateIncident(id: incident.id, status: p, note: t.isEmpty ? nil : "狀態改為「\(p.label)」:\(t)")
                }
                pending = nil
            }
            Button("取消", role: .cancel) { pending = nil }
        } message: {
            if pending == .resolved {
                Text("解決後會開始計算 MTTR,並可產生檢討報告。")
            } else {
                Text("變更會記錄在時間軸上。")
            }
        }
    }

    private var steps: some View {
        let current = incident.status.incStep
        return HStack(spacing: 0) {
            ForEach(IncidentStatus.allCases) { s in
                let i = s.incStep
                if i > 0 {
                    Rectangle()
                        .fill(i <= current ? s.color.opacity(0.8) : Color.primary.opacity(0.12))
                        .frame(height: 2)
                        .frame(minWidth: 12, maxWidth: .infinity)
                }
                Button { if s != incident.status { pending = s; note = "" } } label: {
                    HStack(spacing: 6) {
                        ZStack {
                            Circle()
                                .fill(i <= current ? s.color : Color.clear)
                                .overlay(Circle().strokeBorder(i <= current ? s.color : Color.primary.opacity(0.25), lineWidth: 1.5))
                                .frame(width: 22, height: 22)
                            if i < current {
                                Image(systemName: "checkmark").font(.system(size: 10, weight: .bold)).foregroundStyle(.white)
                            } else if i == current {
                                Image(systemName: s.incIcon).font(.system(size: 10, weight: .bold)).foregroundStyle(.white)
                            } else {
                                Text("\(i + 1)").font(.system(size: 10, weight: .semibold)).foregroundStyle(.secondary)
                            }
                        }
                        Text(s.label)
                            .font(.callout.weight(i == current ? .semibold : .regular))
                            .foregroundStyle(i == current ? s.color : (i < current ? .primary : .secondary))
                            .lineLimit(1)
                            .fixedSize()
                    }
                    .padding(.horizontal, 4)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(s == incident.status ? "目前狀態" : "把狀態改為「\(s.label)」")
            }
        }
    }

    @ViewBuilder private var advanceButton: some View {
        if let next = incident.status.incNext {
            Button { pending = next; note = "" } label: {
                Label("推進到\(next.label)", systemImage: "arrow.right.circle.fill")
            }
            .buttonStyle(.borderedProminent)
            .tint(next.color)
            .controlSize(.small)
            .fixedSize()
        }
    }
}

// MARK: - AI 摘要

struct IncAISummaryCard: View {
    @EnvironmentObject var app: AppState
    let incident: Incident

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "sparkles").foregroundStyle(.purple)
                Text("AI 摘要").font(.headline)
                Spacer()
                Text("由 AI 依時間軸整理").font(.caption).foregroundStyle(.purple.opacity(0.8))
                Button {
                    guard let cur = app.incident(incident.id) else { return }
                    let summary = IncAI.summary(cur, app: app)
                    do { _ = try withAnimation { try app.startIncidents().update(id: cur.id, aiSummary: summary, by: .person(app.me)) } } catch {
                        app.errorNotice = "AI 摘要沒有存起來:\(error.localizedDescription)"
                    }
                } label: { Image(systemName: "arrow.clockwise") }
                .buttonStyle(.borderless)
                .help("依最新時間軸重新整理")
            }
            Text(incident.aiSummary ?? IncAI.summary(incident, app: app))
                .font(.callout)
                .lineSpacing(3)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            if incident.aiSummary == nil {
                Text("尚未儲存摘要,以上為即時草稿。").font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            LinearGradient(colors: [Color.purple.opacity(0.12), Color.indigo.opacity(0.06)],
                           startPoint: .topLeading, endPoint: .bottomTrailing),
            in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.purple.opacity(0.25)))
    }
}

// MARK: - 相關告警

struct IncRelatedAlertsCard: View {
    @EnvironmentObject var app: AppState
    let incident: Incident

    private var alerts: [Alert] {
        let window = incident.startedAt.addingTimeInterval(-3600)...(incident.resolvedAt ?? .now)
        return app.alerts.filter { a in
            a.incidentId == incident.id || (a.incidentId == nil && incident.services.contains(a.service) && window.contains(a.startedAt))
        }
        .sorted { $0.startedAt < $1.startedAt }
    }

    var body: some View {
        Card("相關告警", subtitle: alerts.isEmpty ? nil : "\(alerts.count) 則") {
            if alerts.isEmpty {
                Text("沒有關聯的告警").font(.callout).foregroundStyle(.secondary)
            } else {
                VStack(spacing: 2) {
                    ForEach(alerts) { a in
                        Button { app.go(.monitoring, focus: a.id) } label: {
                            HStack(alignment: .top, spacing: 8) {
                                Image(systemName: a.state == .firing ? "bell.badge.fill" : "bell")
                                    .foregroundStyle(a.severity.color)
                                    .frame(width: 16)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(a.title).font(.callout).lineLimit(2)
                                    HStack(spacing: 4) {
                                        Text(a.state.label)
                                        Text("·")
                                        Text(Fmt.time(a.startedAt))
                                        Text("·")
                                        Text(a.value).monospacedDigit()
                                    }
                                    .font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer(minLength: 0)
                                Image(systemName: "chevron.right").font(.caption2).foregroundStyle(.tertiary)
                            }
                            .padding(6)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .help("到「監控與告警」查看")
                    }
                }
            }
        }
    }
}

// MARK: - 後續工作

struct IncFollowUpsCard: View {
    @EnvironmentObject var app: AppState
    let incident: Incident
    @State private var adding = false
    @State private var newTitle = ""

    private var items: [WorkItem] {
        incident.followUps.compactMap { id in app.workItems.first { $0.id == id } }
    }

    var body: some View {
        Card("後續工作", subtitle: items.isEmpty ? nil : "\(items.filter { $0.status == .done }.count)/\(items.count) 完成") {
            VStack(alignment: .leading, spacing: 4) {
                if items.isEmpty && !adding {
                    Text("還沒有後續工作").font(.callout).foregroundStyle(.secondary)
                }
                ForEach(items) { w in
                    Button { app.go(.development, focus: w.id) } label: {
                        HStack(spacing: 8) {
                            Image(systemName: w.status == .done ? "checkmark.circle.fill" : "circle")
                                .foregroundStyle(w.status == .done ? .green : .secondary)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(w.title).font(.callout).lineLimit(2)
                                    .strikethrough(w.status == .done, color: .secondary)
                                HStack(spacing: 4) {
                                    Text(w.id).monospaced()
                                    Text("·")
                                    Text(w.status.label)
                                    if let a = w.assignee {
                                        Text("·")
                                        Text(app.name(of: a))
                                    }
                                }
                                .font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer(minLength: 0)
                            if w.createdByAI { AIBadge() }
                        }
                        .padding(.vertical, 4)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .help("到「開發進度」查看 \(w.id)")
                }
                if adding {
                    HStack {
                        TextField("工作標題", text: $newTitle)
                            .textFieldStyle(.roundedBorder)
                            .onSubmit(add)
                        Button("新增", action: add).disabled(newTitle.trimmingCharacters(in: .whitespaces).isEmpty)
                        Button("取消") { adding = false; newTitle = "" }
                    }
                    .controlSize(.small)
                } else {
                    Button { adding = true } label: { Label("新增後續工作", systemImage: "plus") }
                        .buttonStyle(.borderless)
                        .padding(.top, 4)
                }
            }
        }
    }

    private func add() {
        let t = newTitle.trimmingCharacters(in: .whitespaces)
        guard !t.isEmpty, let i = app.incidents.firstIndex(where: { $0.id == incident.id }) else { return }
        let maxNum = app.workItems.compactMap { Int($0.id.filter(\.isNumber)) }.max() ?? 100
        let id = "OPS-\(maxNum + 1)"
        app.workItems.append(WorkItem(id: id, title: t, type: .incident, status: .todo, assignee: .person(app.me),
                                      points: 2, service: incident.services.first, createdByAI: false))
        _ = i
        do { _ = try app.startIncidents().update(id: incident.id, addFollowUps: [id], by: .person(app.me)) } catch {
            app.errorNotice = "\(id) 沒有關聯到 \(incident.id):\(error.localizedDescription)"
        }
        app.log(.person(app.me), "plus.circle", "為 \(incident.id) 建立後續工作 \(id)", .incidents)
        newTitle = ""
        adding = false
    }
}
