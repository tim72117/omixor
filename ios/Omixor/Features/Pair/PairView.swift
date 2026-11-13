import SwiftUI

/// 配對:掃 Mac App 顯示的 QR code,或手動輸入伺服器網址與配對碼
struct PairView: View {
    @EnvironmentObject var session: Session
    @State private var server = ""
    @State private var code = ""
    @State private var scanning = false
    @State private var working = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    VStack(alignment: .leading, spacing: 8) {
                        Image(systemName: "iphone.and.arrow.forward").font(.largeTitle).foregroundStyle(.tint)
                        Text("和 Mac 上的 omixor 配對").font(.title2.bold())
                        Text("在 Mac App 左下角的帳號選單選「配對手機…」,用這裡掃描畫面上的 QR code,或手動輸入伺服器網址與配對碼。配對碼 5 分鐘內有效、只能用一次。")
                            .font(.callout).foregroundStyle(.secondary)
                    }
                    .padding(.vertical, 4)
                }
                if let n = session.notice {
                    Section { Label(n, systemImage: "info.circle").foregroundStyle(.orange) }
                }
                Section {
                    Button { scanning = true } label: { Label("掃描 QR code", systemImage: "qrcode.viewfinder") }
                        .disabled(working)
                }
                Section {
                    TextField("伺服器網址", text: $server, prompt: Text(verbatim: "伺服器網址,例如 https://omixor.example.com"))
                        .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    TextField("配對碼(8 碼,例如 ABCD-2345)", text: $code)
                        .textInputAutocapitalization(.characters).autocorrectionDisabled()
                        .font(.body.monospaced())
                    Button {
                        guard let link = manualLink else { return }
                        claim(link)
                    } label: {
                        HStack {
                            Text("配對")
                            if working { Spacer(); ProgressView() }
                        }
                    }
                    .disabled(manualLink == nil || working)
                    // 送出前給使用者看實際會連的完整伺服器(補了 scheme 與 port 之後)
                    if let s = PairLink.normalizeServer(server) {
                        Text("將連線到").font(.footnote).foregroundStyle(.secondary)
                        PairServerRow(server: s)
                    }
                } header: {
                    Text("手動輸入")
                } footer: {
                    if !server.isEmpty, PairLink.normalizeServer(server) == nil {
                        Text("伺服器網址不對").foregroundStyle(.red)
                    } else if !code.isEmpty, PairLink.normalizeCode(code) == nil {
                        Text("配對碼是 8 個英數字(沒有 0、O、1、I、L)").foregroundStyle(.red)
                    }
                }
                if let error {
                    Section { Text(error).foregroundStyle(.red) }
                }
                Section {
                    Text("Mac App 在「本機」模式時不能配對:本機守護服務只聽 Mac 自己(127.0.0.1),而且每次啟動都換 token。請先在 Mac App 切到遠端 omixor(雲端,或本機雲端版 http://127.0.0.1:8080;本機雲端版只有同一台 Mac 的模擬器連得到)。")
                        .font(.footnote).foregroundStyle(.secondary)
                } header: {
                    Text("說明")
                }
            }
            .navigationTitle("omixor")
            .sheet(isPresented: $scanning) {
                QRScannerSheet { text in
                    scanning = false
                    if let link = PairLink(scanned: text) {
                        claim(link)
                    } else {
                        error = "這不是 omixor 的配對 QR code"
                    }
                }
            }
        }
    }

    private var manualLink: PairLink? {
        guard let s = PairLink.normalizeServer(server), let c = PairLink.normalizeCode(code) else { return nil }
        return PairLink(server: s, code: c)
    }

    private func claim(_ link: PairLink) {
        server = link.server.absoluteString
        code = link.code
        working = true
        error = nil
        Task {
            defer { working = false }
            do {
                try await session.pair(link)
            } catch {
                self.error = "配對失敗:\(error.localizedDescription)"
            }
        }
    }
}
