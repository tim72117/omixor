import AppKit
import SwiftUI

@main
struct DeskApp: App {
    @StateObject private var model = AppModel()

    init() {
        // 用 swift run 直接執行時沒有 .app 包裝,要手動設成一般 app 才會出現在 Dock 並取得焦點
        NSApplication.shared.setActivationPolicy(.regular)
        DispatchQueue.main.async { NSApplication.shared.activate(ignoringOtherApps: true) }
    }

    var body: some Scene {
        WindowGroup("Desk") {
            ContentView()
                .environmentObject(model)
                .frame(minWidth: 900, minHeight: 560)
                .onAppear { model.reloadProjects() }
        }
        .windowToolbarStyle(.unified(showsTitle: false))
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Session…") { model.newChat() }
                    .keyboardShortcut("n")
            }
            CommandGroup(after: .sidebar) {
                Button("Reload Sessions") { model.reloadProjects() }
                    .keyboardShortcut("r")
                Button(model.showInspector ? "Hide Inspector" : "Show Inspector") { model.showInspector.toggle() }
                    .keyboardShortcut("i", modifiers: [.command, .option])
            }
        }
    }
}

struct ContentView: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        NavigationSplitView {
            SidebarView()
                .navigationSplitViewColumnWidth(min: 220, ideal: 260, max: 360)
        } detail: {
            ChatView()
                .inspector(isPresented: $model.showInspector) {
                    InspectorView()
                        .inspectorColumnWidth(min: 240, ideal: 290, max: 380)
                }
        }
        .navigationTitle(model.title)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    model.showInspector.toggle()
                } label: {
                    Label("Inspector", systemImage: "sidebar.right")
                }
                .help("顯示或隱藏右側資訊")
            }
        }
    }
}
