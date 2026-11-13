import SwiftUI

enum CICDTab: String, CaseIterable, Identifiable {
    case runs, deployments, environments, voice
    var id: String { rawValue }
    var label: String {
        switch self {
        case .runs: return "執行紀錄"
        case .deployments: return "部署"
        case .environments: return "環境"
        case .voice: return "旁白發佈"
        }
    }
}

/// CI/CD:執行紀錄、部署、環境
struct PipelinesView: View {
    @EnvironmentObject var app: AppState
    @State private var tab: CICDTab = .runs
    @State private var selectedRunId: Int?
    @State private var highlightedDeploymentId: String?

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 20)
                .padding(.top, 16)
                .padding(.bottom, 12)
            Divider()
            Group {
                switch app.shownTab(tab) {
                case .runs: PipeRunsTab(selectedRunId: $selectedRunId)
                case .deployments: PipeDeploymentsTab(highlightId: $highlightedDeploymentId)
                case .environments: PipeEnvironmentsTab(openDeployments: { id in
                    highlightedDeploymentId = id
                    tab = .deployments
                })
                case .voice: PipeVoiceTab()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .onAppear(perform: consumeFocus)
        .onChange(of: app.focusId) { consumeFocus() }
        // 系統設定關掉的分頁:切到第一個開著的並提示(例如從別頁跳過來)
        .onChange(of: tab) { _, t in if let r = app.redirectTab(t) { tab = r } }
        .onChange(of: app.deployments) { syncServiceVersions() }
    }

    private var header: some View {
        let running = app.pipelineRuns.filter(\.cicdIsActive).count
        let waiting = app.pipelineRuns.filter { $0.status == .waitingApproval }.count
            + app.deployments.filter { $0.status == .waitingApproval }.count
        let dayAgo = Date().addingTimeInterval(-86400)
        let failed = app.pipelineRuns.filter { $0.status == .failed && $0.startedAt > dayAgo }.count
        return ViewThatFits(in: .horizontal) {
            HStack(alignment: .center, spacing: 16) {
                title
                Spacer(minLength: 12)
                counters(running: running, waiting: waiting, failed: failed)
                picker
            }
            VStack(alignment: .leading, spacing: 10) {
                HStack { title; Spacer(minLength: 8); counters(running: running, waiting: waiting, failed: failed) }
                picker
            }
        }
    }

    private var title: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("CI/CD").font(.title2.weight(.semibold))
            Text("建置、測試、部署的每一次執行").font(.callout).foregroundStyle(.secondary)
        }
        .fixedSize()
    }

    private func counters(running: Int, waiting: Int, failed: Int) -> some View {
        HStack(spacing: 6) {
            if running > 0 { Pill(text: "執行中 \(running)", color: .blue, icon: "arrow.triangle.2.circlepath") }
            if waiting > 0 { Pill(text: "待核准 \(waiting)", color: .purple, icon: "hand.raised.fill") }
            if failed > 0 { Pill(text: "24 小時內失敗 \(failed)", color: .red, icon: "xmark.circle.fill") }
        }
        .fixedSize()
    }

    private var picker: some View {
        Picker("分頁", selection: Binding(get: { app.shownTab(tab) }, set: { tab = $0 })) {
            ForEach(app.visibleTabs(CICDTab.self)) { Text($0.label).tag($0) }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .fixedSize()
    }

    /// 從其他頁跳過來:focusId 可能是 run 編號或部署 id
    private func consumeFocus() {
        guard let f = app.focusId else { return }
        if f.hasPrefix("VJ-") || f == "voice" {
            tab = .voice
            app.focusId = nil
            return
        }
        let digits = f.trimmingCharacters(in: CharacterSet(charactersIn: "#run- "))
        if let n = Int(digits), app.pipelineRuns.contains(where: { $0.id == n }) {
            tab = .runs
            selectedRunId = n
            app.focusId = nil
        } else if app.deployments.contains(where: { $0.id == f }) {
            tab = .deployments
            highlightedDeploymentId = f
            app.focusId = nil
        }
    }

    /// 本畫面發起的部署完成後,把服務的版本更新到該環境
    private func syncServiceVersions() {
        for d in app.deployments where CICDSession.trackedDeployments.contains(d.id) && d.status == .succeeded {
            if let i = app.services.firstIndex(where: { $0.id == d.service }), app.services[i].versions[d.env] != d.version {
                app.services[i].versions[d.env] = d.version
            }
            CICDSession.trackedDeployments.remove(d.id)
        }
    }
}
