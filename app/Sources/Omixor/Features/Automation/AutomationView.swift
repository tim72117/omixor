import SwiftUI

/// AI 自動化:人監督 AI 自動完成維運步驟。
/// 分頁:待核准(收件匣)/虛擬角色/派發/組織鏈/粉絲專頁/自動化流程/執行紀錄/成效。
struct AutomationView: View {
    @EnvironmentObject var app: AppState
    @State private var tab: AutoTab = .inbox
    @State private var focusApproval: String?
    @State private var detailAutomation: String?
    @State private var selectedRun: String?
    @State private var selectedAgent: String?
    /// 修改後核准時,記住人改過的指令(模型的 action 是唯讀)
    @State private var editedActions: [String: String] = [:]

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 20)
                .padding(.top, 18)
                .padding(.bottom, 12)
            Divider()
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear { consumeFocus() }
        .onChange(of: app.focusId) { consumeFocus() }
        .sheet(item: Binding(get: { detailAutomation.map(AutoSheetID.init) },
                             set: { detailAutomation = $0?.id })) { item in
            AutoFlowDetailSheet(automationId: item.id) { target in
                detailAutomation = nil
                open(target)
            }
            .environmentObject(app)
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 14) {
            PageHeader("AI 自動化", subtitle: "AI 負責分析與執行,人負責把關。調整每個流程的自主程度,並在這裡核准高風險動作。") {
                summaryPills
            }
            Picker("分頁", selection: $tab) {
                ForEach(AutoTab.allCases) { t in
                    Text(tabTitle(t)).tag(t)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(maxWidth: 760)
        }
    }

    private var summaryPills: some View {
        let active = app.automations.filter(\.enabled).count
        let running = app.automationRuns.filter { $0.status == .running }.count
        return ViewThatFits {
            HStack(spacing: 8) {
                AIBadge(text: "\(active) 個流程啟用中")
                if running > 0 { Pill(text: "\(running) 個執行中", color: .blue, icon: "arrow.triangle.2.circlepath") }
                if !app.pendingApprovals.isEmpty {
                    Pill(text: "\(app.pendingApprovals.count) 件待核准", color: .orange, icon: "hand.raised.fill")
                }
            }
            AIBadge(text: "\(active) 個啟用")
        }
    }

    private func tabTitle(_ t: AutoTab) -> String {
        if t == .agents || t == .dispatch {
            let blocked = app.agentTasks.filter { $0.status == .blocked }.count
            if blocked > 0 { return "\(t.label)(\(blocked))" }
        }
        if t == .inbox, !app.pendingApprovals.isEmpty { return "\(t.label)(\(app.pendingApprovals.count))" }
        return t.label
    }

    @ViewBuilder private var content: some View {
        switch tab {
        case .inbox:
            AutoInboxView(focusId: $focusApproval, editedActions: $editedActions,
                          openAutomation: { detailAutomation = $0 },
                          openRun: { id in selectedRun = id; tab = .runs })
        case .agents:
            AutoAgentsView(selection: $selectedAgent)
        case .dispatch:
            AutoDispatchView(openTask: { t in selectedAgent = t.agentId; tab = .agents })
        case .chain:
            AutoChainView()
        case .runner:
            AutoRunnerView(runner: app.agentRunner)
        case .facebook:
            AutoFacebookView()
        case .flows:
            AutoFlowsView(openDetail: { detailAutomation = $0 })
        case .runs:
            AutoRunsView(selection: $selectedRun,
                         openApproval: { id in focusApproval = id; tab = .inbox },
                         openAutomation: { detailAutomation = $0 })
        case .insights:
            AutoInsightsView(openAutomation: { detailAutomation = $0 })
        }
    }

    /// 跨頁跳轉:focusId 可能是核准請求、自動化或執行紀錄的 id
    private func consumeFocus() {
        guard let id = app.focusId else { return }
        if open(id) { app.focusId = nil }
    }

    @discardableResult
    private func open(_ id: String) -> Bool {
        if id == "agents" { tab = .agents; return true }
        if id == "dispatch" { tab = .dispatch; return true }
        if id == "chain" { tab = .chain; return true }
        if id == "facebook" || app.fbSettings.drafts.contains(where: { $0.id == id }) { tab = .facebook; return true }
        if let a = app.agents.first(where: { $0.id == id }) { selectedAgent = a.id; tab = .agents; return true }
        if let t = app.agentTasks.first(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) {
            selectedAgent = t.agentId; tab = .agents; return true
        }
        if app.approvals.contains(where: { $0.id == id }) {
            focusApproval = id
            tab = .inbox
            return true
        }
        if app.automations.contains(where: { $0.id == id }) {
            tab = .flows
            detailAutomation = id
            return true
        }
        if app.automationRuns.contains(where: { $0.id == id }) {
            selectedRun = id
            tab = .runs
            return true
        }
        return false
    }
}

struct AutoSheetID: Identifiable {
    let id: String
}
