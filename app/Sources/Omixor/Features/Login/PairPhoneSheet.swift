import AppKit
import CoreImage
import CoreImage.CIFilterBuiltins
import SwiftUI

// 配對手機:向目前連線的 omixor server 要一組一次性配對碼(POST /api/auth/pair,見 core/service/pair.go),
// 顯示成 QR code(omixor://pair?server=<網址>&code=<碼>)與文字,手機(omixor iOS)掃描或手動輸入後換到自己的 token。
// 只有遠端模式有:本機守護服務只聽 127.0.0.1、token 每次啟動換,手機連不到也不支援配對。

/// 配對連結與 QR code(純函式,方便測試)
enum PhonePairing {
    /// omixor://pair?server=<網址>&code=<碼>
    static func link(server: URL, code: String) -> URL {
        var c = URLComponents()
        c.scheme = "omixor"
        c.host = "pair"
        c.queryItems = [URLQueryItem(name: "server", value: server.absoluteString), URLQueryItem(name: "code", value: code)]
        return c.url!
    }

    /// 顯示用:XXXX-XXXX
    static func display(_ code: String) -> String {
        code.count == 8 ? "\(code.prefix(4))-\(code.suffix(4))" : code
    }

    static func qrImage(_ text: String, scale: CGFloat = 10) -> NSImage? {
        let f = CIFilter.qrCodeGenerator()
        f.message = Data(text.utf8)
        f.correctionLevel = "M"
        guard let out = f.outputImage?.transformed(by: CGAffineTransform(scaleX: scale, y: scale)),
              let cg = CIContext().createCGImage(out, from: out.extent) else { return nil }
        return NSImage(cgImage: cg, size: NSSize(width: out.extent.width, height: out.extent.height))
    }

    struct Code: Decodable {
        var code: String
        var expiresAt: Date
    }

    /// 向目前連線的 server 要配對碼(同步;在背景呼叫)
    static func request(config: IssueServerConfig = .current) throws -> Code {
        let remote = IssueRemote(config: config)
        let data = try remote.send("POST", config.url.appendingPathComponent("api/auth/pair"), try remote.json([:]))
        return try IssueRemote.decoder().decode(Code.self, from: data)
    }
}

struct PairPhoneSheet: View {
    @Environment(\.dismiss) private var dismiss
    let serverName: String
    let serverURL: URL

    @State private var code: PhonePairing.Code?
    @State private var error: String?
    @State private var loading = false
    @State private var now = Date()
    private let tick = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    var body: some View {
        VStack(spacing: 14) {
            Text("配對手機").font(.title3.weight(.semibold))
            Text("在 iPhone 的 omixor 掃描這個 QR code,或手動輸入伺服器網址與配對碼。手機會拿到一把自己的登入,可以在伺服器的 token 管理個別撤銷。")
                .font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)

            ZStack {
                RoundedRectangle(cornerRadius: 12).fill(.white)
                if let code, let img = PhonePairing.qrImage(PhonePairing.link(server: serverURL, code: code.code).absoluteString) {
                    Image(nsImage: img).interpolation(.none).resizable().scaledToFit().padding(12)
                        .opacity(expired ? 0.15 : 1)
                    if expired { Text("已過期").font(.headline).foregroundStyle(.black) }
                } else if loading {
                    ProgressView()
                } else {
                    Image(systemName: "qrcode").font(.system(size: 60)).foregroundStyle(.gray.opacity(0.4))
                }
            }
            .frame(width: 220, height: 220)

            if let code {
                VStack(spacing: 4) {
                    Text(PhonePairing.display(code.code))
                        .font(.system(size: 28, weight: .semibold, design: .monospaced))
                        .textSelection(.enabled)
                    Text(expired ? "配對碼已過期,請重新產生" : "剩下 \(remaining) · 只能用一次")
                        .font(.caption).foregroundStyle(expired ? .red : .secondary)
                }
            }
            LabeledContent("伺服器") {
                Text(serverURL.absoluteString).textSelection(.enabled).lineLimit(1).truncationMode(.middle)
            }
            .font(.caption)
            if isLoopback {
                Text("這台伺服器是 127.0.0.1:只有同一台 Mac 上的模擬器連得到,實體手機要用雲端或區網網址。")
                    .font(.caption).foregroundStyle(.orange).multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let error {
                Text(error).font(.caption).foregroundStyle(.red).multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Button("重新產生") { generate() }.disabled(loading)
                Spacer()
                Button("完成") { dismiss() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 360)
        .onAppear { generate() }
        .onReceive(tick) { now = $0 }
    }

    private var expired: Bool { code.map { $0.expiresAt <= now } ?? false }

    private var remaining: String {
        guard let code else { return "" }
        let s = max(0, Int(code.expiresAt.timeIntervalSince(now)))
        return String(format: "%d:%02d", s / 60, s % 60)
    }

    private var isLoopback: Bool {
        let h = serverURL.host ?? ""
        return h == "127.0.0.1" || h == "localhost" || h == "::1"
    }

    private func generate() {
        loading = true
        error = nil
        Task.detached {
            let r = Result { try PhonePairing.request() }
            await MainActor.run {
                loading = false
                switch r {
                case .success(let c): code = c; now = Date()
                case .failure(let e): code = nil; error = "產生配對碼失敗:\(e.localizedDescription)"
                }
            }
        }
    }
}
