import SwiftUI

/// 設定:帳號、伺服器、組織、登出
struct SettingsView: View {
    @EnvironmentObject var session: Session
    @State private var confirmLogout = false

    var body: some View {
        NavigationStack {
            Form {
                Section("帳號") {
                    LabeledContent("名稱", value: session.user?.displayName ?? "—")
                    LabeledContent("Email", value: session.user?.email ?? "—")
                    LabeledContent("伺服器權限", value: session.user?.role == "admin" ? "伺服器管理員" : "成員")
                    LabeledContent("這台裝置", value: session.device.isEmpty ? Session.deviceName : session.device)
                }
                Section("伺服器") {
                    LabeledContent("網址") { Text(session.saved?.server.absoluteString ?? "—").textSelection(.enabled) }
                    LabeledContent("Token", value: session.saved?.tokenId ?? "—")
                }
                Section("組織") {
                    LabeledContent("目前", value: session.org.map { "\($0.name)(\($0.isAdmin ? "管理員" : "成員"))" } ?? "—")
                    if session.orgs.count > 1 {
                        Button("切換組織") { session.selectOrg(nil) }
                    }
                }
                Section {
                    Button("登出", role: .destructive) { confirmLogout = true }
                } footer: {
                    Text("登出會撤銷這台手機在伺服器上的登入,並清掉鑰匙圈裡的資料;Mac 和其他裝置不受影響。推播通知還沒有,之後的版本再加。")
                }
                Section {
                    LabeledContent("版本", value: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "")
                }
            }
            .navigationTitle("設定")
            .confirmationDialog("登出這台手機?", isPresented: $confirmLogout, titleVisibility: .visible) {
                Button("登出", role: .destructive) { Task { await session.logout() } }
            }
        }
    }
}
