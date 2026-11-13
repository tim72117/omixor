import SwiftUI

/// 沒登入 → 配對;沒選組織 → 選組織;都好了 → 分頁
struct RootView: View {
    @EnvironmentObject var session: Session

    var body: some View {
        Group {
            switch session.phase {
            case .signedOut:
                PairView()
            case .signedIn:
                if let org = session.org {
                    WorkspaceView(session: session, org: org).id("\(session.saved?.server.absoluteString ?? "")|\(org.id)")
                } else {
                    OrgPickerView()
                }
            }
        }
        .task(id: session.saved?.token) {
            guard session.phase == .signedIn else { return }
            await session.refreshMe()
            await session.loadOrgs()
        }
        // 整個 App 只讓 http/https/mailto 連結打開
        .environment(\.openURL, SafeLink.action)
        // 從連結帶進來的配對(沒登入、已登入都一樣):先顯示完整伺服器,使用者確認才配對
        .sheet(item: Binding(
            get: { session.pendingLink },
            set: { if $0 == nil { session.cancelPending() } }
        )) { link in
            PairConfirmView(link: link).environmentObject(session)
        }
    }
}

/// 選好組織後的分頁(每個組織一組 store)
struct WorkspaceView: View {
    @StateObject private var channels: ChannelsStore
    @StateObject private var issues: IssuesStore
    @StateObject private var dispatch: DispatchStore
    @StateObject private var directory = Directory()
    @EnvironmentObject var session: Session
    let org: Org

    enum Tab: Hashable { case chat, issues, dispatch, settings }
    @State private var tab: Tab = WorkspaceView.debugTab ?? .chat

    /// 開發用:`-OmixorTab chat|issues|dispatch|settings` 指定一開始的分頁(截圖、自動化測試用)
    static var debugTab: Tab? {
        #if DEBUG
        switch UserDefaults.standard.string(forKey: "OmixorTab") {
        case "issues": return .issues
        case "dispatch": return .dispatch
        case "settings": return .settings
        case "chat": return .chat
        default: return nil
        }
        #else
        return nil
        #endif
    }

    init(session: Session, org: Org) {
        self.org = org
        _channels = StateObject(wrappedValue: ChannelsStore(session: session, org: org.id))
        _issues = StateObject(wrappedValue: IssuesStore(session: session, org: org.id))
        _dispatch = StateObject(wrappedValue: DispatchStore(session: session, org: org.id))
    }

    var body: some View {
        TabView(selection: $tab) {
            ChannelListView(store: channels)
                .tabItem { Label("對話", systemImage: "bubble.left.and.bubble.right") }
                .badge(channels.totalUnread)
                .tag(Tab.chat)
            IssueListView(store: issues)
                .tabItem { Label("Issue", systemImage: "exclamationmark.circle") }
                .tag(Tab.issues)
            DispatchView(store: dispatch)
                .tabItem { Label("派發", systemImage: "person.2.badge.gearshape") }
                .badge(dispatch.stuckCount())
                .tag(Tab.dispatch)
            SettingsView()
                .tabItem { Label("設定", systemImage: "gearshape") }
                .tag(Tab.settings)
        }
        .environmentObject(directory)
        .task {
            if let api = session.api { await directory.load(api: api, org: org.id) }
            // 角標要有數字:先把各分頁的資料載一次
            async let a: Void = channels.refresh()
            async let b: Void = dispatch.refresh()
            _ = await (a, b)
        }
        // 不在那一頁時,角標(未讀、卡住的任務)也要更新
        .foregroundPolling(every: 30) {
            await channels.refresh()
            await dispatch.refresh()
        }
    }
}
