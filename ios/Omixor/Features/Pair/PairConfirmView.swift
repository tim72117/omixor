import SwiftUI

/// 從連結(omixor://pair)帶進來的配對:一律先給使用者看完整伺服器,按「確認配對」才 claim。
/// 沒登入與已登入都走這個畫面;已登入時另外說明目前的登入會被撤銷。
struct PairConfirmView: View {
    @EnvironmentObject var session: Session
    let link: PairLink

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text("有連結要求這台手機和下面的伺服器配對。只有在你剛剛自己從 Mac App 產生配對碼、而且伺服器網址正確時才確認;不認得的話請取消。")
                        .font(.callout)
                }
                Section {
                    PairServerRow(server: link.server)
                    LabeledContent("配對碼") { Text(verbatim: link.code).font(.body.monospaced()) }
                } header: {
                    Text("要配對的伺服器")
                }
                if session.phase == .signedIn, let current = session.saved?.server {
                    Section {
                        Text(verbatim: PairLink.display(current)).font(.callout.monospaced()).textSelection(.enabled)
                    } header: {
                        Text("目前已登入")
                    } footer: {
                        Text("確認後會改用新的配對重新登入,目前這台手機的登入會被撤銷。")
                    }
                }
                if let e = session.pendingError {
                    Section { Text(e).foregroundStyle(.red) }
                }
                Section {
                    Button {
                        Task { await session.confirmPending() }
                    } label: {
                        HStack {
                            Text(session.phase == .signedIn ? "確認改用新的配對" : "確認配對").bold()
                            if session.confirmingPending { Spacer(); ProgressView() }
                        }
                    }
                    .disabled(session.confirmingPending)
                    Button("取消", role: .cancel) { session.cancelPending() }
                        .disabled(session.confirmingPending)
                }
            }
            .navigationTitle("確認配對")
            .navigationBarTitleDisplayMode(.inline)
        }
        .interactiveDismissDisabled(session.confirmingPending)
    }
}

/// 顯示完整伺服器(scheme://host:port);http 明文時加警告
struct PairServerRow: View {
    let server: URL

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(verbatim: PairLink.display(server))
                .font(.body.monospaced())
                .textSelection(.enabled)
            if server.scheme?.lowercased() == "http" {
                Label("這是未加密的 http 連線:配對碼、登入 token 與之後的訊息都會以明文傳送,同一個網路上的人可能看得到或竄改。只在信任的本機或區網使用。", systemImage: "exclamationmark.triangle.fill")
                    .font(.footnote)
                    .foregroundStyle(.orange)
            }
        }
        .padding(.vertical, 2)
    }
}
