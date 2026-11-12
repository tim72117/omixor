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
                    app.startLiveUpdates()
                    app.startMCPServer()
                    app.startConnections()
                    app.startVoice()
                    app.startIssues()
                    app.startAgents()
                    app.startCabinet()
                    app.startGitHub()
                    if app.ghStore?.isLoggedIn == true { Task { await app.ghStore?.refresh() } }
                    app.startGCP()
                    app.startLine()
                    if app.gcpStore?.isLoggedIn == true, app.gcpStore?.isConfigured == true { Task { await app.gcpStore?.refresh() } }
                }
        }
        .defaultSize(width: 1480, height: 920)
        .windowToolbarStyle(.unified(showsTitle: false))
        .commands {
            CommandGroup(after: .sidebar) {
                ForEach(Array(AppSection.allCases.enumerated()), id: \.element) { i, s in
                    Button(s.title) { app.go(s) }
                        .keyboardShortcut(KeyEquivalent(Character("\(i + 1)")), modifiers: .command)
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
