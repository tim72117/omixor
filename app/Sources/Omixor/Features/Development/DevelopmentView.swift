import SwiftUI

/// 開發進度:概覽 / Sprint 看板 / Pull Requests / Repositories
struct DevelopmentView: View {
    @EnvironmentObject var app: AppState
    @StateObject private var model = DevModel()

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Picker("分頁", selection: Binding(get: { app.shownTab(model.tab) }, set: { model.tab = $0 })) {
                    ForEach(app.visibleTabs(DevTab.self)) { tab in
                        Label(tab.label, systemImage: tab.icon).tag(tab)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(maxWidth: 760)
                Spacer()
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 10)
            Divider()

            Group {
                switch app.shownTab(model.tab) {
                case .overview: DevOverviewTab()
                case .board: DevBoardTab()
                case .issues: DevIssuesTab()
                case .pulls: DevPullRequestsTab()
                case .repos: DevReposTab()
                case .github: DevGitHubTab()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .environmentObject(model)
        .onAppear { applyFocus(app.focusId) }
        .onChange(of: app.focusId) { _, new in applyFocus(new) }
        // 系統設定關掉的分頁:切到第一個開著的並提示(例如從別頁跳過來)
        .onChange(of: model.tab) { _, t in if let r = app.redirectTab(t) { model.tab = r } }
    }

    /// 其他頁跳過來時:focusId 可能是 PR 編號(可帶 #)、工作項目 id 或 repo id
    private func applyFocus(_ focus: String?) {
        guard let focus, !focus.isEmpty else { return }
        let trimmed = focus.hasPrefix("#") ? String(focus.dropFirst()) : focus
        if let n = Int(trimmed), app.pullRequests.contains(where: { $0.id == n }) {
            model.prRepoFilter = nil
            model.showPR(n)
        } else if let issue = app.issues.first(where: { $0.id.caseInsensitiveCompare(focus) == .orderedSame }) {
            model.showIssue(issue.id)
        } else if focus == "issues" {
            model.tab = .issues
        } else if focus == "github" {
            model.tab = .github
        } else if app.workItems.contains(where: { $0.id == focus }) {
            model.showItem(focus)
        } else if app.repos.contains(where: { $0.id == focus }) {
            model.tab = .repos
        } else {
            return
        }
        app.focusId = nil
    }
}
