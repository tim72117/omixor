import SwiftUI

// 總覽:需要人注意的卡片(服務健康、告警、AI 待核准、事件、值班)。

// MARK: - 服務健康

struct OverviewServicesCard: View {
    @EnvironmentObject var app: AppState

    private func rank(_ h: Health) -> Int {
        switch h {
        case .down: return 0
        case .degraded: return 1
        case .maintenance: return 2
        case .unknown: return 3
        case .healthy: return 4
        }
    }

    private var sorted: [Service] {
        app.services.sorted {
            (rank($0.health), $0.tier, $0.name) < (rank($1.health), $1.tier, $1.name)
        }
    }

    private var healthyCount: Int { app.services.filter { $0.health == .healthy }.count }

    var body: some View {
        Card {
            OverviewCardHeader(title: "服務健康", linkTitle: "伺服器與服務") { app.go(.infrastructure) }
            if app.services.isEmpty {
                OverviewInlineEmpty(icon: "server.rack", text: "尚未登錄任何服務")
            } else {
                HStack(spacing: 12) {
                    ForEach(Health.allCases) { h in
                        let n = app.services.filter { $0.health == h }.count
                        if n > 0 {
                            HStack(spacing: 4) {
                                Circle().fill(h.color).frame(width: 7, height: 7)
                                Text("\(h.label) \(n)")
                            }
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        }
                    }
                    Spacer()
                    Text("\(healthyCount)/\(app.services.count) 正常")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 180), spacing: 8)], spacing: 8) {
                    ForEach(sorted) { s in
                        OverviewServiceTile(service: s)
                    }
                }
            }
        }
    }
}

struct OverviewServiceTile: View {
    @EnvironmentObject var app: AppState
    let service: Service

    var body: some View {
        Button {
            app.go(.infrastructure, focus: service.id)
        } label: {
            VStack(alignment: .leading, spacing: 5) {
                HStack(spacing: 6) {
                    Image(systemName: service.health.icon)
                        .foregroundStyle(service.health.color)
                    Text(service.name)
                        .font(.subheadline.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    if service.tier == 1 {
                        Text("T1")
                            .font(.caption2.weight(.bold))
                            .foregroundStyle(.secondary)
                            .help("第 1 級(最關鍵)服務")
                    }
                }
                HStack(spacing: 8) {
                    Text("p95 \(Int(service.latencyP95)) ms")
                    Text("錯誤 \(Fmt.percent(service.errorRate, digits: 2))")
                }
                .font(.caption.monospacedDigit())
                .foregroundStyle(.secondary)
                .lineLimit(1)
                HStack(spacing: 4) {
                    Image(systemName: "tag").font(.caption2)
                    Text(service.versions[app.env] ?? "未部署到 \(app.env.short)")
                        .lineLimit(1)
                }
                .font(.caption2.monospaced())
                .foregroundStyle(.tertiary)
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(service.health.color.opacity(service.health == .healthy ? 0.05 : 0.12),
                        in: RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8)
                .strokeBorder(service.health.color.opacity(service.health == .healthy ? 0.12 : 0.35)))
        }
        .buttonStyle(OverviewPressStyle())
        .help("\(service.name):\(service.summary)")
    }
}

// MARK: - 告警

struct OverviewAlertsCard: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        let firing = app.firingAlerts
        Card {
            OverviewCardHeader(title: "觸發中的告警", count: firing.count,
                               countColor: firing.contains { $0.severity == .critical } ? .red : .orange) {
                app.go(.monitoring)
            }
            if firing.isEmpty {
                OverviewInlineEmpty(icon: "bell.slash", text: "沒有觸發中的告警", color: .green)
            } else {
                VStack(spacing: 0) {
                    ForEach(Array(firing.prefix(5).enumerated()), id: \.element.id) { i, alert in
                        if i > 0 { Divider() }
                        OverviewAlertRow(alert: alert)
                    }
                }
                if firing.count > 5 {
                    Text("還有 \(firing.count - 5) 則告警")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
        }
    }
}

struct OverviewAlertRow: View {
    @EnvironmentObject var app: AppState
    let alert: Alert

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            StatusDot(color: alert.severity.color, pulsing: alert.severity == .critical)
                .padding(.top, 5)
            VStack(alignment: .leading, spacing: 3) {
                Button {
                    app.go(.monitoring, focus: alert.id)
                } label: {
                    Text(alert.title)
                        .font(.subheadline.weight(.medium))
                        .multilineTextAlignment(.leading)
                        .lineLimit(2)
                }
                .buttonStyle(.plain)
                HStack(spacing: 6) {
                    Text(app.service(alert.service)?.name ?? alert.service)
                    Text("·")
                    Text(alert.value).monospacedDigit()
                    Text("·")
                    Text(Fmt.relative(alert.startedAt))
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                if let note = alert.aiNote {
                    HStack(alignment: .top, spacing: 4) {
                        Image(systemName: "sparkles").foregroundStyle(.purple)
                        Text(note).lineLimit(2)
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 4)
            Button("確認") { app.acknowledge(alertId: alert.id) }
                .controlSize(.small)
                .help("確認這則告警,表示你正在處理")
        }
        .padding(.vertical, 8)
        .contextMenu {
            Button("確認") { app.acknowledge(alertId: alert.id) }
            Button("靜音") { app.silence(alertId: alert.id) }
            Button("解除") { app.resolve(alertId: alert.id) }
            Divider()
            Button("在監控頁開啟") { app.go(.monitoring, focus: alert.id) }
            if let inc = alert.incidentId {
                Button("開啟事件 \(inc)") { app.go(.incidents, focus: inc) }
            }
        }
    }
}

// MARK: - AI 待核准

struct OverviewApprovalsCard: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        let pending = app.pendingApprovals.sorted { $0.expiresAt < $1.expiresAt }
        Card {
            HStack(spacing: 6) {
                Image(systemName: "sparkles").foregroundStyle(.purple)
                OverviewCardHeader(title: "AI 需要你決定", count: pending.count, countColor: .purple) {
                    app.go(.automation)
                }
            }
            if pending.isEmpty {
                OverviewInlineEmpty(icon: "checkmark.seal", text: "沒有等待核准的 AI 動作", color: .green)
            } else {
                VStack(spacing: 10) {
                    ForEach(pending.prefix(3)) { req in
                        OverviewApprovalRow(request: req)
                    }
                }
                if pending.count > 3 {
                    Text("還有 \(pending.count - 3) 項等待核准")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
        }
    }
}

struct OverviewApprovalRow: View {
    @EnvironmentObject var app: AppState
    let request: ApprovalRequest
    @State private var confirmApprove = false
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Pill(text: request.risk.label, color: request.risk.color)
                Text(app.automation(request.automation)?.name ?? "AI 自動化")
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                Spacer()
                Text(expiryText)
                    .font(.caption)
                    .foregroundStyle(request.expiresAt.timeIntervalSinceNow < 900 ? .orange : .secondary)
            }
            Text(request.title).font(.subheadline.weight(.semibold))
            Text(request.rationale)
                .font(.caption).foregroundStyle(.secondary)
                .lineLimit(expanded ? nil : 2)
            Text(request.action)
                .font(.caption.monospaced())
                .lineLimit(expanded ? nil : 2)
                .textSelection(.enabled)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 6))
            if expanded {
                if !request.evidence.isEmpty {
                    VStack(alignment: .leading, spacing: 3) {
                        Text("依據").font(.caption.weight(.semibold))
                        ForEach(request.evidence, id: \.self) { e in
                            Label(e, systemImage: "checkmark.circle")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
                VStack(alignment: .leading, spacing: 3) {
                    Text("回滾計畫").font(.caption.weight(.semibold))
                    Text(request.rollbackPlan).font(.caption).foregroundStyle(.secondary)
                }
            }
            HStack(spacing: 8) {
                Button(expanded ? "收合" : "詳細內容") {
                    withAnimation(.easeInOut(duration: 0.15)) { expanded.toggle() }
                }
                .buttonStyle(.link)
                .font(.caption)
                Spacer()
                Button("拒絕") { app.decide(approvalId: request.id, approve: false) }
                Button("核准") {
                    if request.risk == .high { confirmApprove = true }
                    else { app.decide(approvalId: request.id, approve: true) }
                }
                .buttonStyle(.borderedProminent)
                .tint(.purple)
            }
            .controlSize(.small)
        }
        .padding(10)
        .background(Color.purple.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.purple.opacity(0.18)))
        .confirmationDialog("核准高風險動作?", isPresented: $confirmApprove) {
            Button("核准並執行") { app.decide(approvalId: request.id, approve: true) }
            Button("取消", role: .cancel) {}
        } message: {
            Text("「\(request.title)」被標為高風險。核准後 AI 會立即執行,必要時依回滾計畫復原:\(request.rollbackPlan)")
        }
    }

    private var expiryText: String {
        request.expiresAt < .now ? "已逾期" : "\(Fmt.relative(request.expiresAt))到期"
    }
}

// MARK: - 事件

struct OverviewIncidentsCard: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        let open = app.openIncidents.sorted { ($0.sev, $0.startedAt) < ($1.sev, $1.startedAt) }
        Card {
            OverviewCardHeader(title: "進行中的事件", count: open.count, countColor: open.isEmpty ? .secondary : .red) {
                app.go(.incidents)
            }
            if open.isEmpty {
                let last = app.incidents.compactMap(\.resolvedAt).max()
                OverviewInlineEmpty(icon: "flame",
                                    text: last.map { "沒有進行中的事件 · 上次結案 \(Fmt.relative($0))" } ?? "沒有進行中的事件",
                                    color: .green)
            } else {
                VStack(spacing: 8) {
                    ForEach(open.prefix(3)) { inc in
                        OverviewIncidentRow(incident: inc)
                    }
                }
            }
        }
    }
}

struct OverviewIncidentRow: View {
    @EnvironmentObject var app: AppState
    let incident: Incident

    var body: some View {
        Button {
            app.go(.incidents, focus: incident.id)
        } label: {
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    Pill(text: "SEV\(incident.sev)", color: overviewSevColor(incident.sev))
                    Text(incident.id).font(.caption.monospaced()).foregroundStyle(.secondary)
                    Spacer()
                    Pill(text: incident.status.label, color: incident.status.color)
                }
                Text(incident.title)
                    .font(.subheadline.weight(.semibold))
                    .multilineTextAlignment(.leading)
                if !incident.impact.isEmpty {
                    Text(incident.impact).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                }
                if let ai = incident.aiSummary {
                    HStack(alignment: .top, spacing: 4) {
                        Image(systemName: "sparkles").foregroundStyle(.purple)
                        Text(ai).lineLimit(3)
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
                HStack(spacing: 6) {
                    ActorAvatar(actor: .person(incident.commander), size: 16)
                    Text("指揮官 \(app.person(incident.commander)?.name ?? incident.commander)")
                    Spacer()
                    Image(systemName: "clock")
                    Text("已持續 \(Fmt.duration(Date().timeIntervalSince(incident.startedAt)))")
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(overviewSevColor(incident.sev).opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(overviewSevColor(incident.sev).opacity(0.25)))
        }
        .buttonStyle(OverviewPressStyle())
    }
}

// MARK: - 值班

struct OverviewOnCallCard: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        let now = Date()
        let current = app.onCall.filter { $0.start <= now && $0.end > now }.sorted { $0.role < $1.role }
        let next = app.onCall.filter { $0.start > now }.sorted { $0.start < $1.start }.first
        Card {
            OverviewCardHeader(title: "值班", linkTitle: "輪值設定") { app.go(.settings) }
            if current.isEmpty && next == nil {
                OverviewInlineEmpty(icon: "person.badge.clock", text: "尚未設定值班輪值")
            } else {
                ForEach(current) { shift in
                    shiftRow(shift, caption: "到 \(shift.end.formatted(.dateTime.month().day().hour().minute()))")
                }
                if let next {
                    Divider()
                    Text("下一班").font(.caption).foregroundStyle(.secondary)
                    shiftRow(next, caption: "\(Fmt.relative(next.start))開始")
                }
            }
        }
    }

    private func shiftRow(_ shift: OnCallShift, caption: String) -> some View {
        HStack(spacing: 10) {
            ActorAvatar(actor: .person(shift.person), size: 28)
            VStack(alignment: .leading, spacing: 1) {
                Text(app.person(shift.person)?.name ?? shift.person).font(.subheadline.weight(.medium))
                Text(app.person(shift.person)?.role ?? "").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 1) {
                Pill(text: shift.role, color: shift.role == "Primary" ? .accentColor : .secondary)
                Text(caption).font(.caption2).foregroundStyle(.secondary)
            }
        }
    }
}
