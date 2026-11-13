import SwiftUI

@main
struct OmixorApp: App {
    @StateObject private var session = Session(keychain: OmixorApp.keychain)

    /// 跑單元測試時(App 是測試的宿主)不碰 App 真的鑰匙圈項目
    static var keychain: Keychain {
        ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil
            ? Keychain(service: "com.tim72117.omixor.ios.tests.host")
            : Keychain()
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(session)
                .onOpenURL { session.open($0) }
                #if DEBUG
                // 開發用:`xcrun simctl launch booted com.tim72117.omixor.ios -OmixorPairURL 'omixor://pair?…'`
                // 直接走配對連結(simctl openurl 會先跳「要在 omixor 中打開嗎?」,自動化測試按不到)
                .task {
                    if let s = UserDefaults.standard.string(forKey: "OmixorPairURL"), let u = URL(string: s) { session.open(u) }
                }
                #endif
        }
    }
}
