import SwiftUI

/// 待核准收件匣:AI 準備好的動作,等人決定。
struct AutoInboxView: View {
    @EnvironmentObject var app: AppState
    @Binding var focusId: String?
    @Binding var editedActions: [String: String]
    var openAutomation: (String) -> Void
    var openRun: (String) -> Void

    @State private var riskFilter: RiskLevel?
    @State private var highlighted: String?

    private var pending: [ApprovalRequest] {
        app.pendingApprovals
            .filter { riskFilter == nil || $0.risk == riskFilter }
            .sorted { a, b in
                if a.risk != b.risk { return rank(a.risk) > rank(b.risk) }
                return a.expiresAt < b.expiresAt
            }
    }

    private var decided: [ApprovalRequest] {
        app.approvals.filter { $0.state != .pending }.sorted { $0.createdAt > $1.createdAt }
    }

    private func rank(_ r: RiskLevel) -> Int { r == .high ? 2 : r == .medium ? 1 : 0 }

    var body: some View {
        ScrollViewReader { proxy in
            PageScroll {
                toolbar
                if app.pendingApprovals.isEmpty {
                    Card {
                        EmptyStateView(title: "沒有待核准的請求", icon: "checkmark.seal",
                                       message: "AI 需要人把關的動作會出現在這裡。")
                            .frame(maxWidth: .infinity, minHeight: 200)
                    }
                } else if pending.isEmpty {
                    Card {
                        EmptyStateView(title: "沒有符合篩選的請求", icon: "line.3.horizontal.decrease.circle")
                            .frame(maxWidth: .infinity, minHeight: 160)
                    }
                } else {
                    ForEach(pending) { req in
                        AutoApprovalCard(request: req, highlighted: highlighted == req.id,
                                         editedActions: $editedActions,
                                         openAutomation: openAutomation, openRun: openRun)
                            .id(req.id)
                    }
                }
                if !decided.isEmpty {
                    Text("最近已處理").font(.headline).padding(.top, 8)
                    Card(padding: 0) {
                        VStack(spacing: 0) {
                            ForEach(Array(decided.prefix(12).enumerated()), id: \.element.id) { i, req in
                                if i > 0 { Divider().padding(.leading, 44) }
                                AutoDecidedRow(request: req, edited: editedActions[req.id])
                                    .id(req.id)
                                    .background(highlighted == req.id ? Color.purple.opacity(0.08) : .clear)
                            }
                        }
                    }
                }
            }
            .onAppear { scrollToFocus(proxy) }
            .onChange(of: focusId) { scrollToFocus(proxy) }
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Image(systemName: "hand.raised.fill").foregroundStyle(.purple)
            Text(app.pendingApprovals.isEmpty ? "目前沒有需要你決定的事" : "\(app.pendingApprovals.count) 件請求等你決定")
                .font(.headline)
            Spacer()
            Picker("風險", selection: $riskFilter) {
                Text("全部風險").tag(RiskLevel?.none)
                ForEach(RiskLevel.allCases.reversed()) { Text($0.label).tag(RiskLevel?.some($0)) }
            }
            .labelsHidden()
            .frame(width: 120)
        }
    }

    private func scrollToFocus(_ proxy: ScrollViewProxy) {
        guard let id = focusId else { return }
        highlighted = id
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) {
            withAnimation { proxy.scrollTo(id, anchor: .top) }
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.5) {
            withAnimation { if highlighted == id { highlighted = nil } }
        }
        focusId = nil
    }
}

// MARK: - 單張請求卡片

struct AutoApprovalCard: View {
    @EnvironmentObject var app: AppState
    let request: ApprovalRequest
    var highlighted: Bool
    @Binding var editedActions: [String: String]
    var openAutomation: (String) -> Void
    var openRun: (String) -> Void

    @State private var confirmApprove = false
    @State private var confirmReject = false
    @State private var showEdit = false

    private var automation: Automation? { app.automation(request.automation) }
    private var relatedRun: AutomationRun? {
        app.automationRuns.first { $0.automation == request.automation && $0.status == .waitingApproval }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            titleRow
            rationale
            if !request.evidence.isEmpty { evidence }
            VStack(alignment: .leading, spacing: 6) {
                Label("將執行的動作", systemImage: "terminal").font(.subheadline.weight(.semibold))
                AutoCodeBlock(text: request.action, maxHeight: 220)
            }
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "arrow.uturn.backward.circle").foregroundStyle(.blue)
                VStack(alignment: .leading, spacing: 2) {
                    Text("回滾計畫").font(.subheadline.weight(.semibold))
                    Text(request.rollbackPlan).foregroundStyle(.secondary).textSelection(.enabled)
                }
            }
            Divider()
            actions
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(alignment: .leading) {
            UnevenRoundedRectangle(topLeadingRadius: 12, bottomLeadingRadius: 12)
                .fill(request.risk.color)
                .frame(width: 4)
        }
        .overlay(RoundedRectangle(cornerRadius: 12)
            .strokeBorder(highlighted ? Color.purple : Color.primary.opacity(0.07), lineWidth: highlighted ? 2 : 1))
        .animation(.easeInOut, value: highlighted)
        .confirmationDialog("確定要核准高風險動作?", isPresented: $confirmApprove, titleVisibility: .visible) {
            Button("核准並執行", role: .destructive) { approve(edited: nil) }
            Button("取消", role: .cancel) {}
        } message: {
            Text("「\(request.title)」會立即執行。請確認回滾計畫可行:\(request.rollbackPlan)")
        }
        .confirmationDialog("拒絕這個請求?", isPresented: $confirmReject, titleVisibility: .visible) {
            Button("拒絕", role: .destructive) { app.decide(approvalId: request.id, approve: false) }
            Button("取消", role: .cancel) {}
        } message: {
            Text("AI 不會執行這個動作,執行紀錄會標為已拒絕。")
        }
        .sheet(isPresented: $showEdit) {
            AutoEditApprovalSheet(request: request) { cmd in approve(edited: cmd) }
        }
    }

    private var titleRow: some View {
        HStack(alignment: .top, spacing: 12) {
            AutoIconTile(icon: automation?.icon ?? "sparkles", size: 36)
            VStack(alignment: .leading, spacing: 4) {
                Text(request.title).font(.headline)
                HStack(spacing: 6) {
                    Button { openAutomation(request.automation) } label: {
                        Text(automation?.name ?? request.automation)
                    }
                    .buttonStyle(.link)
                    Text("·").foregroundStyle(.tertiary)
                    Text("提出於 \(Fmt.relative(request.createdAt))").foregroundStyle(.secondary)
                }
                .font(.caption)
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 6) {
                AutoRiskBadge(risk: request.risk)
                AutoCountdown(expiresAt: request.expiresAt)
            }
        }
    }

    private var rationale: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "sparkles").foregroundStyle(.purple)
            VStack(alignment: .leading, spacing: 2) {
                Text("AI 的判斷").font(.subheadline.weight(.semibold)).foregroundStyle(.purple)
                Text(request.rationale).textSelection(.enabled)
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.purple.opacity(0.07), in: RoundedRectangle(cornerRadius: 8))
    }

    private var evidence: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("依據").font(.subheadline.weight(.semibold))
            ForEach(Array(request.evidence.enumerated()), id: \.offset) { _, e in
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Image(systemName: "checkmark.circle").font(.caption).foregroundStyle(.secondary)
                    Text(e).font(.callout).textSelection(.enabled)
                }
            }
        }
    }

    private var actions: some View {
        HStack(spacing: 8) {
            if let run = relatedRun {
                Button { openRun(run.id) } label: { Label("查看執行過程", systemImage: "list.bullet.rectangle") }
                    .buttonStyle(.borderless)
            }
            Spacer()
            Button("拒絕") { confirmReject = true }
            Button("修改後核准…") { showEdit = true }
            Button {
                if request.risk == .high { confirmApprove = true } else { approve(edited: nil) }
            } label: {
                Label("核准並執行", systemImage: "checkmark.seal.fill")
            }
            .buttonStyle(.borderedProminent)
            .tint(request.risk == .high ? .red : .purple)
        }
        .controlSize(.regular)
    }

    private func approve(edited: String?) {
        if let edited, edited != request.action {
            editedActions[request.id] = edited
        }
        withAnimation { app.decide(approvalId: request.id, approve: true) }
        if let edited, edited != request.action {
            app.log(.person(app.me), "pencil", "修改「\(request.title)」的指令後核准", .automation)
        }
    }
}

// MARK: - 修改後核准

struct AutoEditApprovalSheet: View {
    let request: ApprovalRequest
    var onApprove: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var command = ""
    @State private var acknowledged = false

    private var changed: Bool { command != request.action }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 10) {
                AutoIconTile(icon: "pencil", size: 30)
                VStack(alignment: .leading, spacing: 2) {
                    Text("修改後核准").font(.headline)
                    Text(request.title).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                AutoRiskBadge(risk: request.risk)
            }
            Text("調整 AI 準備的指令。核准後會照你修改的版本執行,並記錄在動態牆。")
                .font(.callout).foregroundStyle(.secondary)
            TextEditor(text: $command)
                .font(.system(.body, design: .monospaced))
                .scrollContentBackground(.hidden)
                .padding(6)
                .background(AutoTheme.codeBackground, in: RoundedRectangle(cornerRadius: 8))
                .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.15)))
                .frame(minHeight: 160)
            HStack {
                if changed {
                    Pill(text: "已修改", color: .orange, icon: "pencil")
                    Button("還原 AI 版本") { command = request.action }.buttonStyle(.link)
                } else {
                    Text("尚未修改").font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
            }
            if request.risk == .high {
                Toggle("我已確認這是高風險動作,並了解回滾計畫", isOn: $acknowledged)
                    .toggleStyle(.checkbox)
            }
            HStack {
                Spacer()
                Button("取消") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("核准修改後的指令") {
                    onApprove(command)
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .tint(.purple)
                .disabled(command.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                          || (request.risk == .high && !acknowledged))
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 560)
        .onAppear { command = request.action }
    }
}

// MARK: - 已處理列

struct AutoDecidedRow: View {
    @EnvironmentObject var app: AppState
    let request: ApprovalRequest
    var edited: String?

    private var stateInfo: (String, Color, String) {
        switch request.state {
        case .approved: return (edited == nil ? "已核准" : "修改後核准", .green, "checkmark.seal.fill")
        case .rejected: return ("已拒絕", .red, "xmark.seal.fill")
        case .expired: return ("已逾時", .secondary, "hourglass.bottomhalf.filled")
        case .pending: return ("待核准", .orange, "hand.raised")
        }
    }

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: stateInfo.2).foregroundStyle(stateInfo.1).frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(request.title).lineLimit(1)
                HStack(spacing: 4) {
                    Text(app.automation(request.automation)?.name ?? request.automation)
                    if let by = request.decidedBy {
                        Text("· \(app.person(by)?.name ?? by) 決定")
                    }
                }
                .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            AutoRiskBadge(risk: request.risk)
            Pill(text: stateInfo.0, color: stateInfo.1)
        }
        .padding(.horizontal, 14).padding(.vertical, 9)
        .help(edited.map { "修改後的指令:\n\($0)" } ?? request.action)
    }
}
