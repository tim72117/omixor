import SwiftUI

/// 開發進度:概覽 / 看板 / Issue / Pull Requests / Repositories(組織追蹤的 repo,真實資料)
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
                case .repos: DevRepositoriesTab()
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

    /// 其他頁跳過來時切到對應的分頁(規則見 DevModel.apply)
    private func applyFocus(_ focus: String?) {
        guard let focus, !focus.isEmpty, model.apply(focus: focus, app: app) else { return }
        app.focusId = nil
    }
}
