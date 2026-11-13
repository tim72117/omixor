import AppKit
import SwiftUI

@main
struct OpsApp: App {
    @StateObject private var app = AppState()

    init() {
        // 用 swift run 執行時沒有 .app 包裝,要手動設成一般 app 才會出現在 Dock
        NSApplication.shared.setActivationPolicy(.regular)
        DispatchQueue.main.async { NSApplication.shared.activate(ignoringOtherApps: true) }
    }

    var body: some Scene {
        WindowGroup("omixor") {
            LoginGate { RootView() }
                .environmentObject(app)
                .frame(minWidth: 1180, minHeight: 720)
                .onAppear {
                    // 本機模式:先在背景啟動本機守護服務(Issue、角色、頻道、文件櫃等都在它那裡);App 結束時停掉
                    app.startLocalCore()
                    NotificationCenter.default.addObserver(forName: NSApplication.willTerminateNotification, object: nil, queue: .main) { [app] _ in
                        LocalCore.shared.stop()
                        // 外部 MCP 閘道啟動的子程序一併停掉
                        MainActor.assumeIsolated { app.mcpGateway.stopAll() }
                    }
                    app.startLiveUpdates()
                    app.startMCPServer()
                    app.startConnections()
                    app.startVoice()
                    // Issue、角色、頻道、事件、文件櫃、系統設定、GitHub:先建立,資料在背景讀(不擋主執行緒)
                    app.startCoreStores()
                    app.agentRunner.start()
                    app.startGCP()
                    app.startLine()
                    if app.gcpStore?.isLoggedIn == true, app.gcpStore?.isConfigured == true { Task { await app.gcpStore?.refresh() } }
                }
        }
        .defaultSize(width: 1480, height: 920)
        .windowToolbarStyle(.unified(showsTitle: false))
        .commands {
            CommandGroup(after: .sidebar) {
                // 快速鍵只有 ⌘1…⌘9:第 10 個以後的頁面沒有快速鍵
                // 號碼固定(關掉的頁面不顯示、也沒有快速鍵,其他頁的號碼不變)
                ForEach(Array(AppSection.allCases.prefix(9).enumerated()), id: \.element) { i, s in
                    if app.isVisible(s) {
                        Button(s.title) { app.go(s) }
                            .keyboardShortcut(KeyEquivalent(Character("\(i + 1)")), modifiers: .command)
                    }
                }
                ForEach(Array(AppSection.allCases.dropFirst(9)).filter(app.isVisible), id: \.self) { s in
                    Button(s.title) { app.go(s) }
                }
                Divider()
                Button(app.showAssistant ? "隱藏 AI 助理" : "顯示 AI 助理") { app.showAssistant.toggle() }
                    .keyboardShortcut("j", modifiers: .command)
                Button("指令面板…") { app.showCommandPalette = true }
                    .keyboardShortcut("k", modifiers: .command)
            }
        }
    }
}
